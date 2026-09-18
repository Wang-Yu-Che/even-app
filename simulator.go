package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

// simulatorPage is the page model shared by the glasses writer and the
// embedded preview. It contains the exact text, geometry and BMP bytes sent to
// G2, so the desktop does not need a second native simulator process.
type simulatorPage struct {
	List  *simulatorList `json:"list,omitempty"`
	Text  string         `json:"text"`
	Style g2.TextStyle   `json:"style"`
	Icon  *g2.StatusIcon `json:"icon"`
}

type officialSimulator struct {
	mu        sync.Mutex
	page      simulatorPage
	nativeURL string
	nativeErr error
}

func nativeSimulatorExecutable() (string, error) {
	if path := os.Getenv("EVEN_SIMULATOR_PATH"); path != "" {
		return path, nil
	}
	name := "evenhub-simulator"
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "windows" {
		platform, name = "win32", name+".exe"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	var candidates []string
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	candidates = append(candidates, filepath.Join("frontend", "node_modules", "@evenrealities", "sim-"+platform+"-"+arch, "bin", name))
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return filepath.Abs(path)
		}
	}
	return "", errors.New("未找到官方模拟器，请在 frontend 运行 npm install")
}

func (s *officialSimulator) nativeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /frame", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.page)
	})
	files, _ := fs.Sub(assets, "frontend/dist")
	mux.Handle("/", http.FileServer(http.FS(files)))
	return mux
}

func (s *officialSimulator) startNative(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nativeURL != "" {
		return nil
	}
	path, err := nativeSimulatorExecutable()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: s.nativeHandler(), ReadHeaderTimeout: 5 * time.Second}
	url := "http://" + listener.Addr().String()
	cmd := exec.CommandContext(ctx, path, url+"/simulator.html")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		listener.Close()
		return err
	}
	s.nativeURL, s.nativeErr = url, nil
	go func() { _ = server.Serve(listener) }()
	go func() {
		err := cmd.Wait()
		_ = server.Close()
		s.mu.Lock()
		if s.nativeURL == url {
			s.nativeURL = ""
			s.nativeErr = err
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *officialSimulator) setPage(text string, style g2.TextStyle, icon *g2.StatusIcon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page = simulatorPage{Text: text, Style: style, Icon: icon}
}

func (s *officialSimulator) setText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page.Text = text
}

func (s *officialSimulator) setIcon(icon g2.StatusIcon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page.Icon = &icon
}

// PreviewPage returns the actual page model for rendering inside the Wails
// window. JSON keeps the external g2 structs out of generated TypeScript types.
func (s *EvenService) PreviewPage() (string, error) {
	s.simulator.mu.Lock()
	page := s.simulator.page
	s.simulator.mu.Unlock()
	if page.Style.Height == 0 {
		page = simulatorPage{Text: " ", Style: blankAgentPageStyle()}
	}
	data, err := json.Marshal(page)
	return string(data), err
}

// StartNativeSimulator opens the official desktop simulator only after the
// user explicitly requests it from the embedded preview toolbar.
func (s *EvenService) StartNativeSimulator() error {
	return s.simulator.startNative(s.serviceContext)
}

type simulatorList struct {
	Items        []string `json:"items"`
	ItemWidth    int      `json:"itemWidth"`
	SelectBorder bool     `json:"selectBorder"`
}

func (s *officialSimulator) setList(rows []string, style g2.TextStyle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	itemWidth := style.Width - 2*(style.BorderWidth+style.PaddingLength)
	if style.ListItemWidth > 0 {
		itemWidth = style.ListItemWidth
	}
	s.page = simulatorPage{
		Style: style,
		List:  &simulatorList{Items: append([]string(nil), rows...), ItemWidth: itemWidth, SelectBorder: true},
	}
}
