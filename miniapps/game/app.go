package game

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const PackageName = "com.wangyuche.evenapp.game"
const ContainerName = "game-input"
const Width, Height = 20, 8
const CellWidth, LineHeight = 20, 27
const PageInset = 7
const PageWidth = Width*CellWidth + PageInset*2
const PageHeight = (Height+1)*LineHeight + PageInset*2

type point struct{ x, y int }

type Hooks struct {
	Display func(context.Context, string, bool) error
	Exit    func() error
	Error   func(error)
}

type App struct {
	mu                     sync.Mutex
	hooks                  Hooks
	snake                  []point
	direction, next        point
	food                   point
	over                   bool
	lastMove               time.Time
	active, dirty, initial bool
	cancel                 context.CancelFunc
	done                   chan struct{}
}

func New(hooks Hooks) *App { return &App{hooks: hooks} }
func (*App) ID() string    { return "game" }
func (*App) MenuItem() g2.MenuItem {
	return g2.MenuItem{PackageName: PackageName, Name: "贪吃蛇"}
}

func (a *App) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	a.cancel, a.done = cancel, make(chan struct{})
	go func() {
		defer close(a.done)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.mu.Lock()
				if a.active && !a.over && time.Since(a.lastMove) >= 350*time.Millisecond {
					a.step()
					a.lastMove = time.Now()
					a.dirty = true
				}
				if a.active && a.dirty {
					text, initial := a.render(), a.initial
					a.dirty, a.initial = false, false
					a.mu.Unlock()
					err := a.hooks.Display(ctx, text, initial)
					a.mu.Lock()
					if err != nil {
						a.active = false
						a.hooks.Error(err)
					}
				}
				a.mu.Unlock()
			}
		}
	}()
	return nil
}

func (a *App) Launch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snake = []point{{5, 4}, {4, 4}, {3, 4}}
	a.direction, a.next = point{1, 0}, point{1, 0}
	a.over = false
	a.lastMove = time.Now()
	a.placeFood()
	a.active, a.dirty, a.initial = true, true, true
}

func (a *App) Input(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || a.over {
		return
	}
	var direction point
	switch key {
	case "w":
		direction = point{0, -1}
	case "s":
		direction = point{0, 1}
	case "a":
		direction = point{-1, 0}
	case "d":
		direction = point{1, 0}
	default:
		return
	}
	if direction.x != -a.direction.x || direction.y != -a.direction.y {
		a.next = direction
	}
}

func (a *App) HandleEvent(event protocol.EvenHubEvent) bool {
	if event.Kind == protocol.EvenHubEventMenu {
		if event.PackageName == PackageName {
			a.Launch()
			return true
		}
		a.mu.Lock()
		a.active = false
		a.mu.Unlock()
		return false
	}
	if event.Kind != protocol.EvenHubEventSystem {
		return false
	}
	a.mu.Lock()
	active := a.active
	switch event.Type {
	case protocol.EvenHubEventDoubleClick, protocol.EvenHubEventForegroundExit, protocol.EvenHubEventSystemExit, protocol.EvenHubEventAbnormalExit:
		a.active = false
	}
	a.mu.Unlock()
	if active && event.Type == protocol.EvenHubEventDoubleClick {
		go a.hooks.Exit()
		return true
	}
	return false
}

func (a *App) Stop() error {
	if a.cancel != nil {
		a.cancel()
		<-a.done
	}
	return nil
}

func (a *App) step() {
	a.direction = a.next
	head := point{a.snake[0].x + a.direction.x, a.snake[0].y + a.direction.y}
	eating := head == a.food
	body := a.snake
	if !eating {
		body = body[:len(body)-1]
	}
	if head.x < 0 || head.x >= Width || head.y < 0 || head.y >= Height || slices.Contains(body, head) {
		a.over = true
		return
	}
	a.snake = append([]point{head}, body...)
	if eating {
		a.placeFood()
	}
}

func (a *App) placeFood() {
	empty := make([]point, 0, Width*Height-len(a.snake))
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			cell := point{x, y}
			if !slices.Contains(a.snake, cell) {
				empty = append(empty, cell)
			}
		}
	}
	if len(empty) == 0 {
		a.over = true
		return
	}
	a.food = empty[rand.IntN(len(empty))]
}

func (a *App) render() string {
	var text strings.Builder
	fmt.Fprintf(&text, "贪吃蛇  得分 %d", len(a.snake)-3)
	if a.over {
		if len(a.snake) == Width*Height {
			text.WriteString("  通关！")
		} else {
			text.WriteString("  游戏结束")
		}
	}
	for y := 0; y < Height; y++ {
		text.WriteByte('\n')
		for x := 0; x < Width; x++ {
			cell := point{x, y}
			switch {
			case slices.Contains(a.snake, cell):
				text.WriteString("▦")
			case cell == a.food && len(a.snake) < Width*Height:
				text.WriteString("◆")
			default:
				text.WriteString("　")
			}
		}
	}
	return text.String()
}
