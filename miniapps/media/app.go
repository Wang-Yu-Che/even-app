package media

import (
	"context"
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const PackageName = "com.wangyuche.evenapp.media"
const ContainerName = "now-playing"
const InputContainerName = "media-input"

// ErrDisplayDeferred asks the player to redraw its latest state on a later tick.
var ErrDisplayDeferred = errors.New("media display update deferred")

type State struct {
	BundleIdentifier  string
	ProcessIdentifier int
	Title             string
	Artist            string
	Album             string
	Playing           bool
	Duration          float64
	ElapsedTime       float64
	Timestamp         time.Time
	PlaybackRate      float64
	Lyrics            [3]string
	// LyricProgress is how far playback is through the current lyric line,
	// from 0 to 1, so a long line can scroll across exactly that span.
	LyricProgress float64
	LyricsStatus  string
	// ArtworkData is the base64 cover image from MediaRemote, empty if none.
	// A string keeps State comparable; it is shared, not copied, on assignment.
	ArtworkData string
}

type Control int

const (
	Previous Control = iota
	TogglePlayback
	Next
)

func (s State) Position(now time.Time) float64 {
	position := s.ElapsedTime
	if position < 0 {
		return position
	}
	if s.Playing && !s.Timestamp.IsZero() {
		rate := s.PlaybackRate
		if rate == 0 {
			rate = 1
		}
		position = max(0, position+max(0, now.Sub(s.Timestamp).Seconds())*rate)
	}
	if s.Duration > 0 {
		position = min(position, s.Duration)
	}
	return position
}

func (s State) HasMedia() bool {
	return s.Title != "" || s.BundleIdentifier != "" || s.ProcessIdentifier != 0
}

type Hooks struct {
	Display func(context.Context, State, string, bool) error
	Exit    func() error
	Error   func(error)
}

type App struct {
	hooks     Hooks
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	input     chan int
	suspended bool
	runs      sync.WaitGroup
	stream    func(context.Context, func(State) error) error
	send      func(context.Context, Control) error
}

func Supported() bool      { return runtime.GOOS == "darwin" }
func New(hooks Hooks) *App { return &App{hooks: hooks, stream: stream, send: sendControl} }
func (*App) ID() string    { return "media" }

func (*App) MenuItem() g2.MenuItem {
	return g2.MenuItem{PackageName: PackageName, Name: "正在播放"}
}

func (a *App) Start(ctx context.Context) error {
	a.ctx = ctx
	return nil
}

func (a *App) HandleEvent(event protocol.EvenHubEvent) bool {
	if event.Kind == protocol.EvenHubEventMenu && event.PackageName == PackageName {
		a.Cancel()
		a.launch()
		return true
	}
	if event.Kind != protocol.EvenHubEventSystem &&
		(event.Kind != protocol.EvenHubEventText || event.Name != InputContainerName) {
		return false
	}
	switch event.Type {
	case protocol.EvenHubEventScrollTop, protocol.EvenHubEventScrollBottom, protocol.EvenHubEventClick:
		return a.HandleControlEvent(event)
	case protocol.EvenHubEventDoubleClick:
		if !a.Cancel() {
			return false
		}
		if err := a.hooks.Exit(); err != nil {
			a.hooks.Error(err)
		}
		return true
	case protocol.EvenHubEventForegroundExit:
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.cancel == nil {
			return a.suspended
		}
		a.cancel()
		a.cancel, a.input = nil, nil
		a.suspended = true
		return true
	case protocol.EvenHubEventForegroundEnter:
		a.mu.Lock()
		resume := a.suspended
		a.suspended = false
		a.mu.Unlock()
		if resume {
			a.launch()
		}
		return resume
	case protocol.EvenHubEventSystemExit, protocol.EvenHubEventAbnormalExit:
		return a.Cancel()
	}
	return false
}

// HandleControlEvent queues media commands independently of page updates.
func (a *App) HandleControlEvent(event protocol.EvenHubEvent) bool {
	if event.Kind != protocol.EvenHubEventSystem &&
		(event.Kind != protocol.EvenHubEventText || event.Name != InputContainerName) {
		return false
	}
	switch event.Type {
	case protocol.EvenHubEventScrollTop, protocol.EvenHubEventScrollBottom, protocol.EvenHubEventClick:
	default:
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.input == nil {
		return false
	}
	lastInput.Store(time.Now().UnixNano())
	select {
	case a.input <- event.Type:
	default:
		log.Printf("[media-control] input queue full gesture=%s", protocol.EvenHubEventTypeName(event.Type))
	}
	return true
}

// lastInput is when the last glasses gesture arrived on the media page.
var lastInput atomic.Int64

// LastInput reports when the last gesture arrived, so the display can hold
// large writes until the gesture's own BLE traffic has drained.
func LastInput() time.Time { return time.Unix(0, lastInput.Load()) }

func (a *App) launch() {
	a.mu.Lock()
	if a.ctx.Err() != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	input := make(chan int, 8)
	a.cancel, a.input = cancel, input
	a.runs.Add(1)
	a.mu.Unlock()

	go func() {
		defer a.runs.Done()
		defer func() {
			cancel()
			a.mu.Lock()
			if a.input == input {
				a.cancel, a.input = nil, nil
			}
			a.mu.Unlock()
		}()
		a.run(ctx, input)
	}()
}

func (a *App) run(ctx context.Context, input <-chan int) {
	message := "正在读取媒体状态…"
	var state State
	report := func(err error) {
		if ctx.Err() == nil {
			a.hooks.Error(err)
		}
	}

	var hasMedia atomic.Bool
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case gesture := <-input:
				if ctx.Err() != nil || !hasMedia.Load() {
					continue
				}
				control := TogglePlayback
				switch gesture {
				case protocol.EvenHubEventScrollTop:
					control = Previous
				case protocol.EvenHubEventScrollBottom:
					control = Next
				}
				commandCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := a.send(commandCtx, control)
				cancel()
				log.Printf("[media-control] gesture=%s command=%d err=%v", protocol.EvenHubEventTypeName(gesture), control.commandID(), err)
				if err != nil {
					report(err)
				}
			}
		}
	}()

	states := make(chan State)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- a.stream(ctx, func(state State) error {
			select {
			case states <- state:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	initial := true
	firstFrame := time.NewTimer(300 * time.Millisecond)
	defer firstFrame.Stop()
	firstFrameDeadline := firstFrame.C
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	redrawPending := false
	var view State
	var lines []lyricLine
	var lyricsStatus string
	var pendingLyrics <-chan []lyricLine
	cancelLyrics := func() {}
	defer func() { cancelLyrics() }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-firstFrameDeadline:
			firstFrameDeadline = nil
		case err := <-streamDone:
			report(err)
			streamDone = nil
			state = State{}
			hasMedia.Store(false)
			message = "媒体状态读取失败"
		case lines = <-pendingLyrics:
			pendingLyrics = nil
			lyricsStatus = ""
			if len(lines) == 0 {
				lyricsStatus = "暂无同步歌词"
			}
		case next := <-states:
			if next.Title != state.Title || next.Artist != state.Artist || next.ProcessIdentifier != state.ProcessIdentifier ||
				next.BundleIdentifier != state.BundleIdentifier || next.Album != state.Album {
				cancelLyrics()
				pendingLyrics, lines = nil, nil
				lyricsStatus = "暂无同步歌词"
				if next.Title != "" && next.BundleIdentifier == neteaseBundleID {
					lyricsCtx, cancel := context.WithCancel(ctx)
					cancelLyrics = cancel
					result := make(chan []lyricLine, 1)
					pendingLyrics = result
					lyricsStatus = "正在查找歌词…"
					go func(track State) {
						result <- loadNeteaseLyrics(lyricsCtx, neteaseAPI, track)
					}(next)
				}
			}
			state = next
			hasMedia.Store(state.HasMedia())
			if state.Timestamp.IsZero() && state.ElapsedTime >= 0 {
				state.Timestamp = time.Now()
			}
			message = ""
		case <-ticker.C:
			// While playing every tick redraws so scrolling text moves smoothly;
			// the display skips the BLE write when the text is unchanged.
			if !redrawPending && (!state.Playing || state.Timestamp.IsZero()) {
				continue
			}

		}
		view = state
		view.ElapsedTime = state.Position(time.Now())
		view.Lyrics = lyricsAt(lines, view.ElapsedTime)
		view.LyricProgress = lyricProgress(lines, view.ElapsedTime, view.Duration)
		view.LyricsStatus = lyricsStatus
		if len(lines) > 0 && view.ElapsedTime < 0 {
			view.LyricsStatus = "等待播放进度"
		}
		err := a.hooks.Display(ctx, view, message, initial)
		if initial {
			firstFrame.Stop()
			firstFrameDeadline = nil
			initial = false
		}
		redrawPending = errors.Is(err, ErrDisplayDeferred)
		if err != nil && !redrawPending {
			report(err)
			return
		}
	}
}

func (a *App) Cancel() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	wasSuspended := a.suspended
	a.suspended = false
	if a.cancel == nil {
		return wasSuspended
	}
	a.cancel()
	a.cancel, a.input = nil, nil
	return true
}

func (a *App) Stop() error {
	a.Cancel()
	a.runs.Wait()
	return nil
}
