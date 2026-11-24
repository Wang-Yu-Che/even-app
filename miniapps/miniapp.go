// Package miniapps defines the compile-time plug-in boundary used by the
// desktop host. A miniapp owns its menu entry and event handling while the host
// continues to own the single BLE connection.
package miniapps

import (
	"context"
	"fmt"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

// MiniApp is the lifecycle shared by every glasses application.
type MiniApp interface {
	ID() string
	MenuItem() g2.MenuItem
	Start(context.Context) error
	HandleEvent(protocol.EvenHubEvent) bool
	Stop() error
}

// Registry owns the installed miniapps in deterministic menu order.
type Registry struct {
	apps []MiniApp
}

func New(apps ...MiniApp) (*Registry, error) {
	seen := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		if app == nil {
			return nil, fmt.Errorf("miniapp cannot be nil")
		}
		if app.ID() == "" {
			return nil, fmt.Errorf("miniapp ID cannot be empty")
		}
		if _, exists := seen[app.ID()]; exists {
			return nil, fmt.Errorf("duplicate miniapp ID %q", app.ID())
		}
		seen[app.ID()] = struct{}{}
	}
	return &Registry{apps: append([]MiniApp(nil), apps...)}, nil
}

func MustNew(apps ...MiniApp) *Registry {
	registry, err := New(apps...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) MenuItems() []g2.MenuItem {
	items := make([]g2.MenuItem, 0, len(r.apps))
	for _, app := range r.apps {
		items = append(items, app.MenuItem())
	}
	return items
}

func (r *Registry) Start(ctx context.Context) error {
	for _, app := range r.apps {
		if err := app.Start(ctx); err != nil {
			return fmt.Errorf("start miniapp %q: %w", app.ID(), err)
		}
	}
	return nil
}

// Dispatch stops at the first app that claims an event. This keeps one active
// glasses page from accidentally reacting to another app's input.
func (r *Registry) Dispatch(event protocol.EvenHubEvent) bool {
	for _, app := range r.apps {
		if app.HandleEvent(event) {
			return true
		}
	}
	return false
}

func (r *Registry) Stop() error {
	for index := len(r.apps) - 1; index >= 0; index-- {
		if err := r.apps[index].Stop(); err != nil {
			return fmt.Errorf("stop miniapp %q: %w", r.apps[index].ID(), err)
		}
	}
	return nil
}
