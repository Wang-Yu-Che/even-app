package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/ble"
	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

type DeviceCapabilities struct {
	Text           bool `json:"text"`
	NativeText     bool `json:"nativeText"`
	NativeList     bool `json:"nativeList"`
	Image          bool `json:"image"`
	MicrophoneLC3  bool `json:"microphoneLC3"`
	InputEvents    bool `json:"inputEvents"`
	DeviceSettings bool `json:"deviceSettings"`
	Brightness     bool `json:"brightness"`
	HeadUp         bool `json:"headUp"`
	ScreenPosition bool `json:"screenPosition"`
	Dashboard      bool `json:"dashboard"`
}

type DeviceCandidate struct {
	Arm     string `json:"arm"`
	Address string `json:"address"`
	Name    string `json:"name"`
	RSSI    int16  `json:"rssi"`
}

type DeviceSettings struct {
	BatteryPercent       int    `json:"batteryPercent"`
	Charging             bool   `json:"charging"`
	LeftFirmwareVersion  string `json:"leftFirmwareVersion"`
	RightFirmwareVersion string `json:"rightFirmwareVersion"`
	Brightness           int    `json:"brightness"`
	AutoBrightness       bool   `json:"autoBrightness"`
	HeadUpEnabled        bool   `json:"headUpEnabled"`
	HeadUpAngle          int    `json:"headUpAngle"`
	WearDetection        bool   `json:"wearDetection"`
	SilentMode           bool   `json:"silentMode"`
	ScreenDepth          int    `json:"screenDepth"`
	ScreenHeight         int    `json:"screenHeight"`
}

type DeviceEvent struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ItemName  string `json:"itemName"`
	Type      string `json:"type"`
	ItemIndex int    `json:"itemIndex"`
}

type DashboardScheduleItem struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Location     string `json:"location"`
	Time         string `json:"time"`
	EndTimestamp int    `json:"endTimestamp"`
}

// DeviceStatus is the connection state displayed by the desktop client.
type DeviceStatus struct {
	Connected              bool               `json:"connected"`
	Connecting             bool               `json:"connecting"`
	LeftState              string             `json:"leftState"`
	RightState             string             `json:"rightState"`
	LastError              string             `json:"lastError"`
	LastErrorAt            string             `json:"lastErrorAt"`
	ReconnectCount         int                `json:"reconnectCount"`
	DisplayDurationSeconds int                `json:"displayDurationSeconds"`
	DeviceID               string             `json:"deviceId"`
	DeviceName             string             `json:"deviceName"`
	AudioAvailable         bool               `json:"audioAvailable"`
	SettingsKnown          bool               `json:"settingsKnown"`
	Capabilities           DeviceCapabilities `json:"capabilities"`
	Settings               DeviceSettings     `json:"settings"`
	AudioActive            bool               `json:"audioActive"`
	AudioFrames            uint64             `json:"audioFrames"`
	AudioBytes             uint64             `json:"audioBytes"`
	LastEvent              DeviceEvent        `json:"lastEvent"`
}

// EvenService exposes the supported G2 operations to the Wails frontend.
type EvenService struct {
	mu                 sync.Mutex
	client             *g2.Client
	clientStatus       g2.Status
	monitor            *statusMonitor
	displayGeneration  uint64
	connectCancel      context.CancelFunc
	connectGeneration  uint64
	connecting         bool
	lastConnectError   string
	lastConnectErrorAt time.Time
	reconnectCount     int
	wasReconnecting    bool
	scanCandidates     map[string]ble.ScanResult
	deviceCachePath    string
	cachedDevice       *g2.Device
	subscriptionCancel context.CancelFunc
	audioCancel        context.CancelFunc
	audioActive        bool
	audioFrames        atomic.Uint64
	audioBytes         atomic.Uint64
	lastEvent          DeviceEvent
	displayDuration    time.Duration
	displayTimer       *time.Timer
	agentUpdateMu      sync.Mutex
	pendingAgentUpdate *agentDisplayUpdate
	agentDisplayWake   chan struct{}
	agentPageActive    bool
	agentIconState     string
	// iconMu serialises icon writes between the loading sweep and a status
	// push, so a sweep that has already been superseded cannot land a frame on
	// top of the icon pushed after it.
	iconMu sync.Mutex
	// loadingToken is the liveness token of the running sweep. It is atomic
	// because the sweep checks it without taking s.mu: the sweep must never
	// wait on the service lock, which is held across BLE writes by a push.
	loadingToken   atomic.Uint64
	loadingActive  bool
	loadingCancel  context.CancelFunc
	serviceContext context.Context
	serviceCancel  context.CancelFunc
}

type agentDisplayUpdate struct {
	rows []string
	// animate marks a real transition rather than a push that only carries new
	// rows. A working state uses it to restart the loading sweep, so the block
	// visibly grows again the moment the agent does something.
	animate bool
	state   string
	icon    string
}

const completionDisplayDuration = 15 * time.Second

// hookSources lists every agent whose hook state directory is mirrored to the
// glasses. Adapters live in scripts/even-<agent>-status-writer.mjs.
func hookSources() []statusSource {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []statusSource{
		{Agent: "codex", Label: "Codex", Dir: filepath.Join(home, ".codex", "statusbar", "state.d")},
		{Agent: "workbuddy", Label: "WorkBuddy", Dir: filepath.Join(home, ".workbuddy", "statusbar", "state.d")},
	}
}

func NewEvenService() *EvenService {
	serviceContext, serviceCancel := context.WithCancel(context.Background())
	service := &EvenService{
		displayDuration:  5 * time.Second,
		agentDisplayWake: make(chan struct{}, 1),
		serviceContext:   serviceContext,
		serviceCancel:    serviceCancel,
	}
	if path, err := deviceCachePath(); err == nil {
		service.deviceCachePath = path
		if device, loadErr := loadCachedDevice(path); loadErr == nil {
			service.cachedDevice = &device
		}
	}
	service.monitor = newStatusMonitor(hookSources(), nil)
	service.monitor.onView = service.pushAgentView
	service.monitor.Start()
	go service.runAgentDisplayQueue()
	service.startConnection()
	return service
}

func (s *EvenService) Connect() (DeviceStatus, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client != nil {
		if client.Status().Ready {
			return s.Status(), nil
		}
		device := client.Status().Device
		_ = client.Close()
		reconnected, err := g2.ConnectDevice(context.Background(), device, g2.ConnectOptions{
			AutoReconnect: true, ReconnectDelay: time.Second,
		})
		if err != nil {
			s.mu.Lock()
			s.client = nil
			s.recordConnectionErrorLocked(err)
			s.mu.Unlock()
			return s.Status(), err
		}
		s.mu.Lock()
		s.client = reconnected
		s.lastConnectError = ""
		s.startEventSubscriptionLocked(reconnected)
		s.mu.Unlock()
		s.startDeviceSettingsSync(reconnected)
		return s.Status(), nil
	}
	s.startConnection()
	return s.Status(), nil
}

// ScanCandidates discovers every nearby arm so the frontend can explicitly
// pair one left and one right device when multiple glasses are present.
func (s *EvenService) ScanCandidates() ([]DeviceCandidate, error) {
	s.mu.Lock()
	if s.client != nil && s.client.Status().Ready {
		s.mu.Unlock()
		return nil, errors.New("请先断开当前设备再扫描")
	}
	if s.connectCancel != nil {
		s.connectCancel()
		s.connectCancel = nil
	}
	s.connectGeneration++
	s.connecting = true
	s.lastConnectError = ""
	s.mu.Unlock()

	arms, err := g2.ScanArms(context.Background(), g2.ScanOptions{Timeout: 10 * time.Second})
	s.mu.Lock()
	s.connecting = false
	defer s.mu.Unlock()
	if err != nil {
		s.lastConnectError = err.Error()
		return nil, err
	}

	s.scanCandidates = make(map[string]ble.ScanResult)
	result := make([]DeviceCandidate, 0, len(arms[ble.Left])+len(arms[ble.Right]))
	for _, arm := range []ble.Arm{ble.Left, ble.Right} {
		for _, candidate := range arms[arm] {
			s.scanCandidates[candidate.Address] = candidate
			result = append(result, DeviceCandidate{
				Arm: arm.String(), Address: candidate.Address, Name: candidate.Name, RSSI: candidate.RSSI,
			})
		}
	}
	return result, nil
}

func (s *EvenService) ConnectDevice(leftAddress, rightAddress string) (DeviceStatus, error) {
	s.mu.Lock()
	left, leftOK := s.scanCandidates[leftAddress]
	right, rightOK := s.scanCandidates[rightAddress]
	previous := s.client
	s.connecting = true
	s.lastConnectError = ""
	s.mu.Unlock()
	if !leftOK || !rightOK {
		s.mu.Lock()
		s.connecting = false
		s.mu.Unlock()
		return s.Status(), errors.New("所选镜腿已失效，请重新扫描")
	}
	device, err := g2.NewDevice(left, right)
	if err != nil {
		s.mu.Lock()
		s.connecting = false
		s.mu.Unlock()
		return s.Status(), err
	}
	if previous != nil {
		_ = previous.Close()
	}
	client, err := g2.ConnectDevice(context.Background(), device, g2.ConnectOptions{
		AutoReconnect: true, ReconnectDelay: time.Second,
	})
	s.mu.Lock()
	s.connecting = false
	if err != nil {
		s.client = nil
		s.lastConnectError = err.Error()
		status := s.statusLocked()
		s.mu.Unlock()
		return status, err
	}
	s.client = client
	s.rememberDeviceLocked(device)
	s.scanCandidates = nil
	s.startEventSubscriptionLocked(client)
	status := s.statusLocked()
	s.mu.Unlock()
	s.startDeviceSettingsSync(client)
	return status, nil
}

// startConnection starts the client-owned initial connection loop. The SDK's
// AutoReconnect takes over after the first successful connection; this loop is
// what keeps scanning when the glasses were not available at app startup.
func (s *EvenService) startConnection() {
	s.mu.Lock()
	if s.client != nil || s.connecting {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.connectGeneration++
	generation := s.connectGeneration
	s.connectCancel = cancel
	s.connecting = true
	s.lastConnectError = ""
	s.mu.Unlock()

	go s.connectUntilReady(ctx, generation)
}

func (s *EvenService) connectUntilReady(ctx context.Context, generation uint64) {
	s.mu.Lock()
	cached := s.cachedDevice
	s.mu.Unlock()
	if cached != nil {
		connectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		client, err := g2.ConnectDevice(connectCtx, *cached, g2.ConnectOptions{
			AutoReconnect: true, ReconnectDelay: time.Second,
		})
		cancel()
		if err == nil {
			s.mu.Lock()
			if generation != s.connectGeneration || ctx.Err() != nil {
				s.mu.Unlock()
				_ = client.Close()
				return
			}
			s.client = client
			s.startEventSubscriptionLocked(client)
			s.connecting = false
			s.lastConnectError = ""
			s.mu.Unlock()
			s.startDeviceSettingsSync(client)
			return
		}
	}

	for {
		client, err := g2.Connect(ctx, g2.ConnectOptions{
			AutoReconnect:  true,
			ReconnectDelay: time.Second,
		})
		if err == nil {
			s.mu.Lock()
			if generation != s.connectGeneration || ctx.Err() != nil {
				s.mu.Unlock()
				_ = client.Close()
				return
			}
			s.client = client
			s.rememberDeviceLocked(client.Status().Device)
			s.startEventSubscriptionLocked(client)
			s.connecting = false
			s.lastConnectError = ""
			s.mu.Unlock()
			s.startDeviceSettingsSync(client)
			return
		}

		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if generation == s.connectGeneration {
			if errors.Is(err, g2.ErrMultipleDevices) {
				s.lastConnectError = "检测到多副 Even G2，请仅保留要连接的一副设备后重试"
			} else {
				s.lastConnectError = fmt.Sprintf("连接失败，正在重试：%v", err)
			}
		}
		s.mu.Unlock()

		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *EvenService) rememberDeviceLocked(device g2.Device) {
	if device.Left.Address == "" || device.Right.Address == "" {
		return
	}
	s.cachedDevice = &device
	if s.deviceCachePath != "" {
		_ = saveCachedDevice(s.deviceCachePath, device)
	}
}

func (s *EvenService) Disconnect() error {
	s.mu.Lock()
	s.connectGeneration++
	cancel := s.connectCancel
	s.connectCancel = nil
	s.connecting = false
	s.lastConnectError = ""
	client := s.client
	s.stopSubscriptionsLocked()
	s.agentPageActive = false
	s.agentIconState = ""
	s.stopLoadingLocked()
	s.cancelDisplayTimerLocked()
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if client != nil {
		err := client.Disconnect()
		s.mu.Lock()
		s.clientStatus = client.Status()
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *EvenService) ServiceShutdown() error {
	s.serviceCancel()
	s.mu.Lock()
	s.connectGeneration++
	if s.connectCancel != nil {
		s.connectCancel()
		s.connectCancel = nil
	}
	client := s.client
	s.client = nil
	s.stopSubscriptionsLocked()
	s.connecting = false
	s.stopLoadingLocked()
	s.cancelDisplayTimerLocked()
	s.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}

func (s *EvenService) Status() DeviceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *EvenService) CodexStatus() CodexStatus { return s.monitor.Status() }

func deviceSettings(settings g2.DeviceSettings) DeviceSettings {
	return DeviceSettings{
		BatteryPercent: settings.BatteryPercent, Charging: settings.Charging,
		LeftFirmwareVersion: settings.LeftFirmwareVersion, RightFirmwareVersion: settings.RightFirmwareVersion,
		Brightness: settings.Brightness, AutoBrightness: settings.AutoBrightness,
		HeadUpEnabled: settings.HeadUpEnabled, HeadUpAngle: settings.HeadUpAngle,
		WearDetection: settings.WearDetection, SilentMode: settings.SilentMode,
		ScreenDepth: settings.ScreenDepth, ScreenHeight: settings.ScreenHeight,
	}
}

func (s *EvenService) RequestDeviceSettings() (DeviceSettings, error) {
	var result DeviceSettings
	err := s.withClient(func(client *g2.Client) error {
		settings, err := client.RequestDeviceSettings(context.Background())
		if err == nil {
			result = deviceSettings(settings)
		}
		return err
	})
	return result, err
}

// startDeviceSettingsSync reads persisted device settings as soon as both arms
// are ready. A short retry covers the brief interval in which BLE is connected
// but the settings service has not started answering yet.
func (s *EvenService) startDeviceSettingsSync(client *g2.Client) {
	go func() {
		delays := [...]time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond}
		var lastErr error
		for _, delay := range delays {
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-s.serviceContext.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}

			s.mu.Lock()
			current := s.client == client
			s.mu.Unlock()
			if !current || !client.Status().Ready {
				return
			}

			ctx, cancel := context.WithTimeout(s.serviceContext, 5*time.Second)
			_, lastErr = client.RequestDeviceSettings(ctx)
			cancel()
			if lastErr == nil {
				s.mu.Lock()
				if s.client == client {
					s.clientStatus = client.Status()
				}
				s.mu.Unlock()
				return
			}
		}

		s.mu.Lock()
		if s.client == client {
			s.recordConnectionErrorLocked(fmt.Errorf("自动读取设备设置失败: %w", lastErr))
		}
		s.mu.Unlock()
	}()
}

func (s *EvenService) SetBrightness(level int, automatic bool) error {
	return s.withClient(func(client *g2.Client) error {
		return client.SetBrightness(context.Background(), g2.BrightnessOptions{Level: level, Auto: automatic})
	})
}

func (s *EvenService) SetHeadUp(enabled bool, angle int) error {
	return s.withClient(func(client *g2.Client) error {
		return client.SetHeadUp(context.Background(), enabled, angle)
	})
}

func (s *EvenService) SetScreenPosition(height, depth int) error {
	return s.withClient(func(client *g2.Client) error {
		return client.SetScreenPosition(context.Background(), g2.ScreenPosition{Height: height, Depth: depth})
	})
}

func (s *EvenService) startEventSubscriptionLocked(client *g2.Client) {
	if s.subscriptionCancel != nil {
		s.subscriptionCancel()
	}
	ctx, cancel := context.WithCancel(s.serviceContext)
	s.subscriptionCancel = cancel
	s.clientStatus = client.Status()
	events := client.SubscribeEvents(ctx)
	statuses := client.SubscribeStatus(ctx)
	errors := client.Errors()
	go func() {
		for event := range events {
			s.mu.Lock()
			if s.client == client {
				s.lastEvent = DeviceEvent{
					Kind: event.Kind.String(), Name: event.Name, ItemName: event.ItemName,
					ItemIndex: event.ItemIndex, Type: protocol.EvenHubEventTypeName(event.Type),
				}
			}
			s.mu.Unlock()
		}
	}()
	go func() {
		for status := range statuses {
			s.mu.Lock()
			if s.client == client {
				becameReady := status.Ready && !s.clientStatus.Ready
				s.clientStatus = status
				reconnecting := status.Left == g2.Reconnecting || status.Right == g2.Reconnecting
				if reconnecting && !s.wasReconnecting {
					s.reconnectCount++
				}
				s.wasReconnecting = reconnecting
				if becameReady {
					s.startDeviceSettingsSync(client)
				}
			}
			s.mu.Unlock()
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-errors:
				if !ok {
					return
				}
				s.mu.Lock()
				if s.client == client {
					s.recordConnectionErrorLocked(err)
				}
				s.mu.Unlock()
			}
		}
	}()
}

func (s *EvenService) recordConnectionErrorLocked(err error) {
	s.lastConnectError = err.Error()
	s.lastConnectErrorAt = time.Now()
}

func (s *EvenService) StartMicrophone() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return errors.New("请先连接 Even G2")
	}
	if s.audioActive {
		return nil
	}
	if !s.client.Capabilities().MicrophoneLC3 {
		return errors.New("当前设备或连接不支持 LC3 麦克风")
	}
	if err := s.client.StartMicrophone(context.Background()); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.serviceContext)
	s.audioCancel = cancel
	s.audioActive = true
	s.audioFrames.Store(0)
	s.audioBytes.Store(0)
	frames := s.client.SubscribeAudio(ctx)
	go func() {
		for frame := range frames {
			s.audioFrames.Add(1)
			s.audioBytes.Add(uint64(len(frame.Data)))
		}
	}()
	return nil
}

func (s *EvenService) StopMicrophone() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audioCancel != nil {
		s.audioCancel()
		s.audioCancel = nil
	}
	s.audioActive = false
	if s.client == nil || !s.client.Status().Ready {
		return nil
	}
	return s.client.StopMicrophone(context.Background())
}

func (s *EvenService) ShowDashboard() error {
	return s.withClient(func(client *g2.Client) error {
		return client.ShowDashboard(context.Background())
	})
}

func (s *EvenService) ConfigureDashboard(widgetOrder []int, halfDay, celsius, experimental bool) error {
	if !experimental {
		return errors.New("请先启用 Dashboard 实验功能")
	}
	order := make([]g2.DashboardWidget, len(widgetOrder))
	for index, widget := range widgetOrder {
		order[index] = g2.DashboardWidget(widget)
	}
	return s.withClient(func(client *g2.Client) error {
		return client.ConfigureDashboard(context.Background(), g2.DashboardConfig{
			WidgetOrder: order, HalfDay: halfDay, Celsius: celsius,
		})
	})
}

func (s *EvenService) PushDashboardSchedule(items []DashboardScheduleItem, experimental bool) error {
	if !experimental {
		return errors.New("请先启用 Dashboard 实验功能")
	}
	schedule := make([]g2.DashboardScheduleItem, len(items))
	for index, item := range items {
		schedule[index] = g2.DashboardScheduleItem{
			ID: item.ID, Title: item.Title, Location: item.Location,
			Time: item.Time, EndTimestamp: item.EndTimestamp,
		}
	}
	return s.withClient(func(client *g2.Client) error {
		return client.PushDashboardSchedule(context.Background(), schedule)
	})
}

func (s *EvenService) ClearDashboardSchedule(experimental bool) error {
	if !experimental {
		return errors.New("请先启用 Dashboard 实验功能")
	}
	return s.withClient(func(client *g2.Client) error {
		return client.ClearDashboardSchedule(context.Background())
	})
}

func (s *EvenService) stopSubscriptionsLocked() {
	if s.subscriptionCancel != nil {
		s.subscriptionCancel()
		s.subscriptionCancel = nil
	}
	if s.audioCancel != nil {
		s.audioCancel()
		s.audioCancel = nil
	}
	s.audioActive = false
}

// SetDisplayDuration controls how long every successful display write remains
// visible. Changing it also restarts the timer for content already on screen.
func (s *EvenService) SetDisplayDuration(seconds int) error {
	if seconds < 1 || seconds > 300 {
		return errors.New("显示时长必须在 1 到 300 秒之间")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.displayDuration = time.Duration(seconds) * time.Second
	if s.displayTimer != nil && s.client != nil {
		s.scheduleClearLocked(s.client, s.agentIconState)
	}
	return nil
}

func (s *EvenService) ShowTeleprompter(text string) error {
	return s.withDisplayClient(func(client *g2.Client) error {
		return client.DisplayText(context.Background(), strings.TrimSpace(text))
	})
}

func (s *EvenService) ShowNativeText(text string) error {
	return s.withDisplayClient(func(client *g2.Client) error {
		return client.ShowText(context.Background(), "desktop", strings.TrimSpace(text))
	})
}

// ShowImage decodes a browser data URL or plain base64 image and streams it
// through the SDK's full-lens 4-bit image renderer.
func (s *EvenService) ShowImage(encoded string) error {
	encoded = strings.TrimSpace(encoded)
	if _, payload, found := strings.Cut(encoded, ","); found {
		encoded = payload
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("图片数据无效: %w", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("仅支持 PNG 或 JPEG 图片: %w", err)
	}
	return s.withDisplayClient(func(client *g2.Client) error {
		return client.DisplayImage(context.Background(), decoded)
	})
}

// ClearDisplay closes the active native page without switching to teleprompter mode.
func (s *EvenService) ClearDisplay() error {
	return s.withClient(func(client *g2.Client) error {
		s.displayGeneration++
		s.cancelDisplayTimerLocked()
		s.stopLoadingLocked()
		if err := client.ShutdownNative(context.Background()); err != nil {
			return err
		}
		s.agentPageActive = false
		s.agentIconState = ""
		s.monitor.ClearRows()
		return nil
	})
}

func (s *EvenService) ShowList(rows []string) error {
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		if item := strings.TrimSpace(row); item != "" {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return errors.New("请至少输入一条列表内容")
	}
	return s.withDisplayClient(func(client *g2.Client) error {
		return client.DisplayList(context.Background(), "desktop-list", items)
	})
}

func (s *EvenService) withDisplayClient(operation func(*g2.Client) error) error {
	return s.withClient(func(client *g2.Client) error {
		if err := operation(client); err != nil {
			s.recordConnectionErrorLocked(fmt.Errorf("眼镜显示失败: %w", err))
			return err
		}
		s.agentPageActive = false
		s.agentIconState = ""
		s.scheduleClearLocked(client, "")
		return nil
	})
}

func (s *EvenService) withClient(operation func(*g2.Client) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return errors.New("请先连接 Even G2")
	}
	return operation(s.client)
}

func (s *EvenService) statusLocked() DeviceStatus {
	duration := s.displayDuration
	if duration <= 0 {
		duration = 5 * time.Second
	}
	if s.client == nil {
		state := g2.Disconnected.String()
		if s.connecting {
			state = g2.Scanning.String()
		}
		return DeviceStatus{
			Connecting: s.connecting, LeftState: state, RightState: state,
			LastError: s.lastConnectError, LastErrorAt: formatDiagnosticTime(s.lastConnectErrorAt),
			ReconnectCount: s.reconnectCount, DisplayDurationSeconds: int(duration / time.Second),
		}
	}
	status := s.clientStatus
	capabilities := status.Capabilities
	return DeviceStatus{
		Connected:              status.Ready,
		Connecting:             !status.Ready,
		LeftState:              status.Left.String(),
		RightState:             status.Right.String(),
		LastError:              s.lastConnectError,
		LastErrorAt:            formatDiagnosticTime(s.lastConnectErrorAt),
		ReconnectCount:         s.reconnectCount,
		DisplayDurationSeconds: int(duration / time.Second),
		DeviceID:               status.Device.ID,
		DeviceName:             status.Device.Name,
		AudioAvailable:         status.AudioAvailable,
		SettingsKnown:          status.SettingsKnown,
		Settings:               deviceSettings(status.Settings),
		AudioActive:            s.audioActive,
		AudioFrames:            s.audioFrames.Load(),
		AudioBytes:             s.audioBytes.Load(),
		LastEvent:              s.lastEvent,
		Capabilities: DeviceCapabilities{
			Text: capabilities.Text, NativeText: capabilities.NativeText,
			NativeList: capabilities.NativeList, Image: capabilities.Image,
			MicrophoneLC3: capabilities.MicrophoneLC3, InputEvents: capabilities.InputEvents,
			DeviceSettings: capabilities.DeviceSettings, Brightness: capabilities.Brightness,
			HeadUp: capabilities.HeadUp, ScreenPosition: capabilities.ScreenPosition,
			Dashboard: capabilities.Dashboard,
		},
	}
}

func formatDiagnosticTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}

func displayClearDelay(configured time.Duration, state string) time.Duration {
	if configured <= 0 {
		configured = 5 * time.Second
	}
	if state == "done" && configured < completionDisplayDuration {
		return completionDisplayDuration
	}
	return configured
}

func (s *EvenService) scheduleClearLocked(client *g2.Client, state string) {
	s.cancelDisplayTimerLocked()
	s.displayGeneration++
	generation := s.displayGeneration
	duration := displayClearDelay(s.displayDuration, state)
	s.displayTimer = time.AfterFunc(duration, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.client != client || s.displayGeneration != generation {
			return
		}
		s.displayTimer = nil
		s.displayGeneration++
		s.stopLoadingLocked()
		if err := client.ShutdownNative(context.Background()); err == nil {
			s.agentPageActive = false
			s.agentIconState = ""
			s.monitor.ClearRows()
		} else {
			s.recordConnectionErrorLocked(fmt.Errorf("定时关闭眼镜页面失败: %w", err))
		}
	})
}

func (s *EvenService) cancelDisplayTimerLocked() {
	if s.displayTimer != nil {
		s.displayTimer.Stop()
		s.displayTimer = nil
	}
}

// streamContainerName is the native list container mirrored to the glasses.
// Every push rebuilds the same container, so the row count can change between
// pushes without any create/destroy churn on the device.
const streamContainerName = "agent-stream"

// LensCalibration pushes the self-describing calibration list and returns it, so
// the desktop can show the same rows.
//
// It measures the two numbers the whole layout is built on — how wide a row may
// be and how many rows fit — plus which candidate glyphs the firmware actually
// renders; see calibrationRows for how to read the result back. It reuses the
// status container on purpose, so the next status push simply replaces it.
func (s *EvenService) LensCalibration() ([]string, error) {
	rows := calibrationRows()
	err := s.withDisplayClient(func(client *g2.Client) error {
		return client.DisplayList(context.Background(), streamContainerName, rows)
	})
	return rows, err
}

// pushAgentView publishes into a latest-value mailbox. Hook callbacks never
// write BLE themselves: they replace the desired frame and wake the one
// persistent renderer. A slow write therefore collapses any burst that arrives
// behind it into one current frame instead of a queue of stale ones.
func (s *EvenService) pushAgentView(rows []string, animate bool, state, icon string) {
	update := agentDisplayUpdate{rows: append([]string(nil), rows...), animate: animate, state: state, icon: icon}
	s.agentUpdateMu.Lock()
	s.pendingAgentUpdate = &update
	s.agentUpdateMu.Unlock()
	select {
	case s.agentDisplayWake <- struct{}{}:
	case <-s.serviceContext.Done():
	default:
	}
}

func (s *EvenService) runAgentDisplayQueue() {
	for {
		select {
		case <-s.serviceContext.Done():
			return
		case <-s.agentDisplayWake:
			for {
				s.agentUpdateMu.Lock()
				update := s.pendingAgentUpdate
				s.pendingAgentUpdate = nil
				s.agentUpdateMu.Unlock()
				if update == nil {
					break
				}
				s.showQueuedAgentUpdate(*update)
			}
		}
	}
}

// shouldRestartSweep reports whether a status push should restart the loading
// sweep from its first frame.
//
// A sweep that is already running and has nothing new to say is left alone. The
// elapsed clock ticks every second while an agent works, so restarting on every
// push would snap the block back to frame 0 long before a cycle could finish —
// the animation would read as stuck rather than as alive. Only a push that was
// not sweeping at all, or that carries a real transition, starts a new sweep.
//
// wasLoading has to be sampled before the sweep is retired for the write that
// follows; see showQueuedAgentUpdate.
func shouldRestartSweep(wasLoading bool, update agentDisplayUpdate, previousState string) bool {
	return !wasLoading || update.animate || update.state != previousState
}

func (s *EvenService) showQueuedAgentUpdate(update agentDisplayUpdate) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return false
	}
	client := s.client
	previousState := s.agentIconState
	style := g2.TextStyle{
		X: 20, Y: 14, Width: 536, Height: 260,
		BorderWidth: 1, BorderColor: 7, BorderRadius: 6, PaddingLength: 12,
	}
	if update.state == "done" {
		style = g2.TextStyle{
			X: 88, Y: 104, Width: 400, Height: 164,
			BorderWidth: 2, BorderColor: 15, BorderRadius: 10, PaddingLength: 16,
		}
	}

	// The sweep writes the icon from its own goroutine. Stopping it before the
	// page is touched is what makes the write below the last one to land, and
	// the call reports back whether it was running at all.
	wasLoading := s.stopLoadingLocked()
	layoutChanged := (previousState == "done") != (update.state == "done")
	if s.agentPageActive && layoutChanged {
		if err := client.ShutdownNative(context.Background()); err != nil {
			s.failAgentPageLocked()
			return false
		}
		s.agentPageActive = false
	}

	if s.agentPageActive {
		if err := client.UpdateText(context.Background(), streamContainerName, strings.Join(update.rows, "\n")); err != nil {
			s.failAgentPageLocked()
			return false
		}
	} else if err := s.createAgentPage(client, update.rows, style, update.state, update.icon); err != nil {
		s.failAgentPageLocked()
		return false
	} else {
		s.agentPageActive = true
	}

	if isWorkingState(update.state) {
		// A busy agent owns the lens until it stops. The clear timer is
		// deliberately left unarmed here: a sweep that vanished after
		// displayDuration seconds would be gone long before the agent finished
		// thinking, which is exactly the moment it is worth having.
		s.cancelDisplayTimerLocked()
		if shouldRestartSweep(wasLoading, update, previousState) {
			s.startLoadingLocked(update.state, update.icon)
		}
		s.agentIconState = update.state
		return true
	}

	if update.state != previousState {
		if err := s.setAgentIcon(client, update.state, update.icon, 0); err != nil {
			s.failAgentPageLocked()
			return false
		}
	}
	s.agentIconState = update.state
	s.scheduleClearLocked(client, update.state)
	return true
}

// createAgentPage builds the text and icon page. The bitmap rides along with
// the create, so it is written under iconMu: a sweep that had not yet noticed
// it was superseded must not land a frame on top of it.
func (s *EvenService) createAgentPage(client *g2.Client, rows []string, style g2.TextStyle, state, icon string) error {
	s.iconMu.Lock()
	defer s.iconMu.Unlock()
	return client.ShowTextWithIcon(context.Background(), streamContainerName, strings.Join(rows, "\n"), style, statusIconFor(state, icon, 0))
}

// setAgentIcon swaps the bitmap for a state that holds still.
func (s *EvenService) setAgentIcon(client *g2.Client, state, icon string, frame int) error {
	s.iconMu.Lock()
	defer s.iconMu.Unlock()
	return client.UpdateStatusIcon(context.Background(), statusIconFor(state, icon, frame))
}

// failAgentPageLocked marks the page as gone, so the next push rebuilds it
// instead of writing into a container that is no longer there.
func (s *EvenService) failAgentPageLocked() {
	s.stopLoadingLocked()
	s.agentPageActive = false
	s.agentIconState = ""
}

// stopLoadingLocked cancels the sweep and retires its token, so any frame it
// still has in flight is rejected. Callers hold s.mu; the sweep never does.
//
// It reports whether a sweep was actually running. Handing that back rather
// than letting a caller sample loadingActive for itself is deliberate: the
// field is cleared here, so a caller that read it afterwards would always see
// false and restart the sweep on every push — the exact mistake that made the
// icon look frozen. The only correct way to get the answer is this return value.
func (s *EvenService) stopLoadingLocked() bool {
	wasActive := s.loadingActive
	s.loadingToken.Add(1)
	s.loadingActive = false
	if s.loadingCancel != nil {
		s.loadingCancel()
		s.loadingCancel = nil
	}
	return wasActive
}

// startLoadingLocked restarts the sweep from its first frame.
func (s *EvenService) startLoadingLocked(state, icon string) {
	s.stopLoadingLocked()
	if s.client == nil || !s.agentPageActive || statusIconFrameCount(state) < 2 {
		return
	}
	ctx, cancel := context.WithCancel(s.serviceContext)
	s.loadingCancel = cancel
	s.loadingActive = true
	go s.runLoadingSweep(ctx, s.loadingToken.Load(), s.client, state, icon)
}

// runLoadingSweep advances the loading icon for as long as the agent works.
//
// This is the only writer of the icon that outlives a status push, and it is
// shaped by two constraints. It must never hold s.mu: a push holds that lock
// across BLE writes, so a sweep running for minutes would block Status, Connect
// and SetDisplayDuration for just as long. And it must never land a frame after
// the state has moved on, or the lens would keep sweeping through a state that
// already finished. It therefore carries its own liveness token and re-checks it
// while holding iconMu, which is what orders it against the icon a push may be
// writing at that same instant.
func (s *EvenService) runLoadingSweep(ctx context.Context, token uint64, client *g2.Client, state, icon string) {
	frames := statusIconFrameCount(state)
	failed := false
	defer func() {
		if failed {
			s.repairAgentPage(token)
		}
	}()
	for frame := 0; ; frame++ {
		superseded, err := s.writeLoadingFrame(token, client, state, icon, frame%frames)
		if err != nil {
			failed = true
			return
		}
		if superseded {
			return
		}
		timer := time.NewTimer(loadingFrameDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// writeLoadingFrame draws one frame unless the sweep has been superseded. The
// token is checked again after iconMu is held, which is where the ordering
// against a concurrent status push comes from.
func (s *EvenService) writeLoadingFrame(token uint64, client *g2.Client, state, icon string, frame int) (bool, error) {
	// A progress update is more valuable than an animation frame. Leave the BLE
	// link idle so the persistent renderer can publish the newest text first.
	s.agentUpdateMu.Lock()
	hasPendingUpdate := s.pendingAgentUpdate != nil
	s.agentUpdateMu.Unlock()
	if hasPendingUpdate {
		return false, nil
	}
	s.iconMu.Lock()
	defer s.iconMu.Unlock()
	if s.loadingToken.Load() != token {
		return true, nil
	}
	return false, client.UpdateStatusIcon(context.Background(), statusIconFor(state, icon, frame))
}

// repairAgentPage forces the next push to rebuild the page, which is the only
// repair available once a write has failed. A superseded sweep is ignored, so a
// late failure cannot tear down the page a newer push already owns.
func (s *EvenService) repairAgentPage(token uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadingToken.Load() != token {
		return
	}
	s.loadingActive = false
	s.agentPageActive = false
	s.agentIconState = ""
	s.cancelDisplayTimerLocked()
}
