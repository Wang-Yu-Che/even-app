package main

import (
	"strings"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

const (
	// Every icon update is a fresh bitmap pushed over BLE, so the regular icon
	// stays compact. The 32px official source geometry is scaled into this box.
	statusIconSize = 20
	statusIconID   = 2
	statusIconName = "state-icon"
	// The icon is a compact status badge in the dashboard's top-right corner,
	// leaving the top-left prompt free for the terminal's fixed `>` marker.
	statusIconX = 516
	statusIconY = 28

	completionIconSize = 64
	completionIconX    = 256
	completionIconY    = 18
)

const (
	loadingGrid        = 4
	loadingFillFrames  = loadingGrid
	loadingDrainFrames = loadingGrid
	loadingFrames      = loadingFillFrames + loadingDrainFrames
	// loadingFrameDelay is the gap between frames, not the frame period. The
	// link sets the real pace: UpdateStatusIcon waits for an ACK, so the
	// observed period is this delay plus the round trip.
	//
	// It is deliberately slow. The sweep runs for as long as the agent works,
	// so its cost is a sustained BLE load, and a heartbeat shares the same
	// arm. The lens is glanced at rather than watched, and what it has to answer
	// is only "is this still going".
	// Bitmap frames and text updates share the same BLE link. A 350ms gap kept
	// the link almost permanently occupied on hardware, so fresh progress had
	// to wait behind animation writes. The slower sweep still reads as alive but
	// leaves most of the link available for the information that matters.
	loadingFrameDelay = 1500 * time.Millisecond
)

// loadingCellOrder is upstream's fill order, kept verbatim so the clockwise
// quadrant sweep survives. Entries are (row, col) with row 0 at the top.
var loadingCellOrder = [loadingGrid * loadingGrid][2]int{
	// bottom-left: column by column, left to right, top to bottom
	{2, 0}, {3, 0}, {2, 1}, {3, 1},
	// bottom-right: the same sweep
	{2, 2}, {3, 2}, {2, 3}, {3, 3},
	// top-right: column by column, right to left, bottom to top — the reverse
	// of the first two quadrants, which is what keeps the sweep turning
	{1, 3}, {0, 3}, {1, 2}, {0, 2},
	// top-left: the same reversed sweep
	{1, 1}, {0, 1}, {1, 0}, {0, 0},
}

// loadingCellRank maps a grid cell to its position in the sweep. Both halves of
// the cycle are expressed against it, which is what keeps them reading as one
// motion rather than as a fill followed by an unrelated emptying.
var loadingCellRank = func() [loadingGrid][loadingGrid]int {
	var ranks [loadingGrid][loadingGrid]int
	for index, cell := range loadingCellOrder {
		ranks[cell[0]][cell[1]] = index
	}
	return ranks
}()

// stillStates are the states that hold still on the lens: a question waiting on
// the human, or a finished run. They are a list of exceptions on purpose —
// anything else, including a state a future adapter invents, is treated as work
// in progress, which fails towards "something is happening" rather than towards
// an empty icon.
var stillStates = map[string]bool{
	"needs_input": true,
	"permission":  true,
	"done":        true,
	"idle":        true,
	"error":       true,
	"failed":      true,
}

func isWorkingState(state string) bool { return !stillStates[state] }

// Working states pulse the official Even AI bars. Still states cost one frame.
func statusIconFrameCount(state string) int {
	if isWorkingState(state) {
		return loadingFrames
	}
	return 1
}

// statusIcon rasterizes the official evenhub-app-ui pixel icons for the lens.
// Their 32px source geometry is preserved; only scale and 4-bit brightness are
// adapted for the G2 image container.
func statusIcon(state string, frame int) g2.StatusIcon {
	return statusIconFor(state, activityIcon(state, "", ""), frame)
}

func statusIconFor(state, icon string, frame int) g2.StatusIcon {
	size, iconX, iconY := statusIconSize, statusIconX, statusIconY
	if state == "done" {
		size, iconX, iconY = completionIconSize, completionIconX, completionIconY
	}
	bmp, _ := protocol.EncodeBMP4(size, size, func(x, y int) uint8 {
		if isWorkingState(state) {
			return activeIconPixel(icon, frame, x, y, size)
		}
		return stillPixel(state, x, y, size)
	})
	return g2.StatusIcon{ID: statusIconID, Name: statusIconName, X: iconX, Y: iconY, Width: size, Height: size, BMP: bmp}
}

func activityIcon(state, tool, current string) string {
	switch state {
	case "done":
		return "complete"
	case "needs_input", "permission":
		return "pause"
	case "error", "failed":
		return "alert"
	case "thinking":
		return "even-ai"
	}

	switch tool {
	case "Read", "Grep", "Glob", "WebFetch", "WebSearch":
		return "search"
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return "edit"
	case "Task", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "TodoWrite":
		return "checklist"
	case "Bash":
		command := strings.ToLower(current)
		if strings.Contains(command, " test") || strings.HasPrefix(command, "test ") ||
			strings.Contains(command, "lint") || strings.Contains(command, "vet") ||
			strings.Contains(command, "check") {
			return "checklist"
		}
		return "play"
	}
	if isWorkingState(state) {
		return "even-ai"
	}
	return "checkmark"
}

type iconRect struct{ x, y, width, height float64 }

func inRects(x, y float64, rects []iconRect) bool {
	for _, rect := range rects {
		if x >= rect.x && x < rect.x+rect.width && y >= rect.y && y < rect.y+rect.height {
			return true
		}
	}
	return false
}

func activeIconPixel(icon string, frame, x, y, size int) uint8 {
	lx := float64(x) * 32 / float64(size)
	ly := float64(y) * 32 / float64(size)
	on := false
	switch icon {
	case "play":
		on = inRects(lx, ly, playRects)
	case "search":
		on = inRects(lx, ly, searchRects)
	case "edit":
		on = inRects(lx, ly, editRects)
	case "checklist":
		on = inRects(lx, ly, checklistRects)
	default:
		return evenAIPixel(frame, x, y, size)
	}
	if !on {
		return 0
	}
	if frame%2 == 0 {
		return 15
	}
	return 10
}

var playRects = []iconRect{
	{9, 3, 3, 2}, {7, 5, 2, 22}, {9, 27, 3, 2}, {12, 5, 3, 2},
	{15, 7, 3, 2}, {18, 9, 3, 2}, {21, 11, 3, 2}, {24, 13, 2, 2},
	{26, 15, 2, 2}, {24, 17, 2, 2}, {21, 19, 3, 2}, {18, 21, 3, 2},
	{15, 23, 3, 2}, {12, 25, 3, 2},
}

var searchRects = []iconRect{
	{9, 5, 7, 2}, {7, 7, 2, 2}, {16, 7, 2, 2}, {5, 9, 2, 7},
	{18, 9, 2, 7}, {7, 16, 2, 2}, {16, 16, 2, 2}, {9, 18, 7, 2},
	{18, 18, 3, 3}, {21, 21, 3, 3}, {24, 24, 3, 3},
}

var checklistRects = []iconRect{
	{4, 5, 4, 2}, {2, 7, 2, 4}, {8, 7, 2, 4}, {12, 8, 18, 2},
	{4, 11, 4, 2}, {10, 19, 2, 2}, {8, 21, 2, 2}, {12, 22, 18, 2},
	{2, 23, 2, 2}, {6, 23, 2, 2}, {4, 25, 2, 2},
}

var editRects = []iconRect{
	{23, 3, 2, 2}, {21, 5, 2, 2}, {25, 5, 2, 2}, {19, 7, 2, 2}, {27, 7, 2, 2},
	{17, 9, 2, 2}, {21, 9, 2, 2}, {25, 9, 2, 2}, {15, 11, 2, 2}, {23, 11, 2, 2},
	{13, 13, 2, 2}, {21, 13, 2, 2}, {11, 15, 2, 2}, {19, 15, 2, 2},
	{9, 17, 2, 2}, {17, 17, 2, 2}, {7, 19, 2, 2}, {15, 19, 2, 2},
	{5, 21, 2, 2}, {13, 21, 2, 2}, {3, 23, 2, 6}, {7, 23, 2, 2},
	{11, 23, 2, 2}, {9, 25, 2, 2}, {5, 27, 4, 2}, {15, 27, 15, 2},
}

// evenAIPixel is the exact five-bar geometry from the official Even AI.svg.
// A restrained brightness pulse supplies motion without changing its outline.
func evenAIPixel(frame, x, y, size int) uint8 {
	lx := float64(x) * 32 / float64(size)
	ly := float64(y) * 32 / float64(size)
	bar := -1
	switch {
	case lx >= 2 && lx < 4 && ly >= 12 && ly < 20:
		bar = 0
	case lx >= 8.5 && lx < 10.5 && ly >= 8 && ly < 24:
		bar = 1
	case lx >= 15 && lx < 17 && ly >= 2 && ly < 30:
		bar = 2
	case lx >= 21.5 && lx < 23.5 && ly >= 8 && ly < 24:
		bar = 3
	case lx >= 28 && lx < 30 && ly >= 12 && ly < 20:
		bar = 4
	}
	if bar < 0 {
		return 0
	}
	pulse := ((frame % loadingFrames) + loadingFrames) % loadingFrames
	distance := bar - pulse%5
	if distance < 0 {
		distance = -distance
	}
	if distance == 0 {
		return 15
	}
	return 8
}

// loadingCellOn reports whether the cell containing (x, y) is lit at a frame.
//
// The lit set is always a contiguous run of the sweep, so a frame is one
// number: how much of the grid is showing. Filling grows that run from the
// front; draining removes it from the front too, which is what makes the block
// appear to drain back into the corner it grew out of.
func loadingCellOn(frame, x, y int) bool {
	cell := statusIconSize / loadingGrid
	row, column := y/cell, x/cell
	if row < 0 || row >= loadingGrid || column < 0 || column >= loadingGrid {
		return false
	}
	rank := loadingCellRank[row][column]
	frame = ((frame % loadingFrames) + loadingFrames) % loadingFrames
	if frame < loadingFillFrames {
		return rank < (frame+1)*loadingGrid
	}
	// Draining keeps the tail of the sweep, so the quadrants disappear in the
	// same order they appeared.
	keep := (loadingFrames - 1 - frame) * loadingGrid
	return rank >= loadingGrid*loadingGrid-keep
}

// stillPixel draws the silhouettes for the states that hold still. An unknown
// state draws nothing here; statusIcon never routes a working state to it.
func stillPixel(state string, x, y, size int) uint8 {
	// Map the transport bitmap back onto the icon set's native 32px grid.
	lx := float64(x) * 32 / float64(size)
	ly := float64(y) * 32 / float64(size)
	switch state {
	case "needs_input", "permission":
		// evenhub-app-ui Edit & Settings Icons/Pause.svg
		if (lx >= 9 && lx < 11 || lx >= 21 && lx < 23) && ly >= 4 && ly < 28 {
			return 15
		}
	case "done":
		// evenhub-app-ui Status Icons/Complete.svg
		if completePixel(lx, ly) {
			return 15
		}
	case "idle":
		if checkmarkPixel(lx, ly) {
			return 15
		}
	case "error", "failed":
		// evenhub-app-ui Status Icons/Alert.svg
		border := (lx >= 4 && lx < 28 && (ly >= 4 && ly < 6 || ly >= 26 && ly < 28)) ||
			(ly >= 4 && ly < 28 && (lx >= 4 && lx < 6 || lx >= 26 && lx < 28))
		exclamation := lx >= 15 && lx < 17 && ((ly >= 9 && ly < 19) || (ly >= 21 && ly < 23))
		if border || exclamation {
			return 15
		}
	}
	return 0
}

func completePixel(x, y float64) bool {
	return (x >= 2 && x < 4 && y >= 16 && y < 18) ||
		(x >= 4 && x < 6 && y >= 18 && y < 20) ||
		(x >= 6 && x < 8 && y >= 20 && y < 22) ||
		(x >= 8 && x < 10 && y >= 22 && y < 24) ||
		(x >= 10 && x < 12 && y >= 24 && y < 26) ||
		(x >= 12 && x < 14 && y >= 22 && y < 24) ||
		(x >= 14 && x < 16 && y >= 20 && y < 22) ||
		(x >= 16 && x < 18 && y >= 18 && y < 20) ||
		(x >= 18 && x < 20 && y >= 16 && y < 18) ||
		(x >= 20 && x < 22 && y >= 14 && y < 16) ||
		(x >= 22 && x < 24 && y >= 12 && y < 14) ||
		(x >= 24 && x < 26 && y >= 10 && y < 12) ||
		(x >= 26 && x < 28 && y >= 8 && y < 10) ||
		(x >= 28 && x < 30 && y >= 6 && y < 8)
}

func checkmarkPixel(x, y float64) bool {
	return (x >= 5 && x < 7 && y >= 16 && y < 18) ||
		(x >= 7 && x < 9 && y >= 18 && y < 20) ||
		(x >= 9 && x < 11 && y >= 20 && y < 22) ||
		(x >= 11 && x < 13 && y >= 22 && y < 24) ||
		(x >= 13 && x < 15 && y >= 20 && y < 22) ||
		(x >= 15 && x < 17 && y >= 18 && y < 20) ||
		(x >= 17 && x < 19 && y >= 16 && y < 18) ||
		(x >= 19 && x < 21 && y >= 14 && y < 16) ||
		(x >= 21 && x < 23 && y >= 12 && y < 14) ||
		(x >= 23 && x < 25 && y >= 10 && y < 12) ||
		(x >= 25 && x < 27 && y >= 8 && y < 10)
}
