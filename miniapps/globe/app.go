package globe

import (
	"context"
	"image"
	"sync"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const PackageName = "com.wangyuche.evenapp.globe"
const ContainerName = "globe-input"
const renderTimeout = 20 * time.Second

type Hooks struct {
	Display func(context.Context, *image.Gray) error
	Exit    func(context.Context) error
	Error   func(error)
}

// App renders only after a gesture so image traffic never starves input events.
type App struct {
	hooks  Hooks
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func New(hooks Hooks) *App { return &App{hooks: hooks} }
func (*App) ID() string    { return "globe" }
func (*App) MenuItem() g2.MenuItem {
	return g2.MenuItem{PackageName: PackageName, Name: "地球仪"}
}
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	return nil
}
func (a *App) HandleEvent(event protocol.EvenHubEvent) bool {
	if event.Kind == protocol.EvenHubEventMenu && event.PackageName == PackageName {
		a.launch()
		return true
	}
	a.mu.Lock()
	active := a.cancel != nil
	a.mu.Unlock()
	if !active {
		return false
	}
	if event.Kind == protocol.EvenHubEventText && event.Name == ContainerName {
		if event.Type == protocol.EvenHubEventScrollTop || event.Type == protocol.EvenHubEventScrollBottom {
			return true
		}
	}
	if event.Kind == protocol.EvenHubEventSystem && event.Type == protocol.EvenHubEventDoubleClick {
		a.cancelSession()
		if a.hooks.Exit != nil {
			go a.hooks.Exit(context.Background())
		}
		return true
	}
	if event.Kind == protocol.EvenHubEventSystem && (event.Type == protocol.EvenHubEventForegroundExit || event.Type == protocol.EvenHubEventSystemExit || event.Type == protocol.EvenHubEventAbnormalExit) {
		a.cancelSession()
		return true
	}
	return false
}
func (a *App) launch() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	a.cancel, a.done = cancel, done
	a.mu.Unlock()
	go a.run(ctx, done)
}
func (a *App) run(ctx context.Context, done chan struct{}) {
	defer func() {
		a.mu.Lock()
		if a.done == done {
			a.cancel = nil
			a.done = nil
		}
		a.mu.Unlock()
		close(done)
	}()
	if a.hooks.Display == nil {
		return
	}
	if err := a.displayFrame(ctx, Render(0)); err != nil {
		a.reportError(ctx, err)
		return
	}
	<-ctx.Done()
}

func (a *App) reportError(ctx context.Context, err error) {
	if ctx.Err() == nil && a.hooks.Error != nil {
		a.hooks.Error(err)
	}
}

func (a *App) displayFrame(ctx context.Context, frame *image.Gray) error {
	frameContext, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()
	return a.hooks.Display(frameContext, frame)
}

func (a *App) cancelSession() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
}

func (a *App) Stop() error {
	a.cancelSession()
	a.mu.Lock()
	done := a.done
	a.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}
