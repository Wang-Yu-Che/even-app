// Package codex contains the registration boundary for the Codex sessions
// miniapp. Its presentation engine is supplied by the desktop host so it can
// share the host's serialized BLE writer.
package codex

import (
	"context"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const (
	ID            = "codex"
	PackageName   = "com.wangyuche.evenapp.codex"
	ContainerName = "agent-stream"
)

type Hooks struct {
	Launch func()
	Event  func(protocol.EvenHubEvent) bool
}

type App struct {
	hooks Hooks
}

func New(hooks Hooks) *App {
	return &App{hooks: hooks}
}

func (*App) ID() string { return ID }

func (*App) MenuItem() g2.MenuItem {
	return g2.MenuItem{PackageName: PackageName, Name: "Codex 会话"}
}

func (*App) Start(context.Context) error { return nil }

func (a *App) HandleEvent(event protocol.EvenHubEvent) bool {
	if event.Kind == protocol.EvenHubEventMenu && event.PackageName == PackageName {
		if a.hooks.Launch != nil {
			a.hooks.Launch()
		}
		return true
	}
	if a.hooks.Event != nil {
		return a.hooks.Event(event)
	}
	return false
}

func (*App) Stop() error { return nil }
