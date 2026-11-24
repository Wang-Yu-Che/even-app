package game

import (
	"slices"
	"strings"
	"testing"

	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

func TestMovementAndExit(t *testing.T) {
	app := New(Hooks{})
	if !app.HandleEvent(protocol.EvenHubEvent{Kind: protocol.EvenHubEventMenu, PackageName: PackageName}) {
		t.Fatal("game menu was not handled")
	}
	app.Input("a")
	if app.next != (point{1, 0}) {
		t.Fatal("reverse turn accepted")
	}
	app.food = point{6, 4}
	app.step()
	if len(app.snake) != 4 || app.snake[0] != (point{6, 4}) {
		t.Fatal("food did not grow snake")
	}
	if slices.Contains(app.snake, app.food) {
		t.Fatal("food inside snake")
	}
	app.Input("w")
	app.step()
	if app.snake[0] != (point{6, 3}) || !strings.Contains(app.render(), "得分 1") {
		t.Fatal("turn or score failed")
	}
	app.HandleEvent(protocol.EvenHubEvent{Kind: protocol.EvenHubEventSystem, Type: protocol.EvenHubEventForegroundExit})
	app.Input("a")
	if app.active || app.next != (point{0, -1}) {
		t.Fatal("exited game still accepts input")
	}
	app.Launch()
	if app.over || len(app.snake) != 3 {
		t.Fatal("restart failed")
	}
}

func TestCollision(t *testing.T) {
	for _, test := range []struct {
		name      string
		snake     []point
		direction point
		over      bool
	}{
		{"wall", []point{{19, 2}, {18, 2}, {17, 2}}, point{1, 0}, true},
		{"body", []point{{2, 2}, {3, 2}, {3, 3}, {2, 3}, {1, 3}}, point{0, 1}, true},
		{"vacating tail", []point{{2, 2}, {3, 2}, {3, 3}, {2, 3}}, point{0, 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := New(Hooks{})
			app.snake, app.next, app.food = test.snake, test.direction, point{0, 0}
			app.step()
			if app.over != test.over {
				t.Fatalf("over = %v", app.over)
			}
		})
	}
}

func TestBoardLayout(t *testing.T) {
	app := New(Hooks{})
	app.Launch()
	rows := strings.Split(app.render(), "\n")
	if len(rows) != Height+1 {
		t.Fatal("incorrect board height")
	}
	for _, row := range rows[1:] {
		cells := []rune(row)
		if len(cells) != Width {
			t.Fatal("incorrect board width")
		}
		for _, cell := range cells {
			if !strings.ContainsRune("▦◆　", cell) {
				t.Fatalf("unexpected board glyph %q", cell)
			}
		}
	}
	if PageWidth-PageInset*2 != Width*CellWidth || PageHeight-PageInset*2 != len(rows)*LineHeight {
		t.Fatal("container does not fit board")
	}
}
