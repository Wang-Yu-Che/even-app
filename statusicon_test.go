package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

// The bitmap layout, asserted by TestStatusIconGeometryIsStable: EncodeBMP4
// writes a 118-byte header followed by rows padded to a 4-byte boundary. At
// 24px each row is 12 bytes of pixels, which is already a multiple of four, so
// this size happens to need no padding at all.
const (
	bmpHeaderBytes = 118
	bmpRowStride   = 12
)

// Mirrors how even-g2-go frames an EvenHub payload: chunks of 180 bytes, each
// carried in a frame with an 8-byte header. They are restated here rather than
// imported because the SDK keeps them unexported, and the point of the test
// below is to assert the wire cost the app is budgeting for.
const (
	evenHubChunkSize  = 180
	evenHubFrameBytes = 188
)

// upstreamFrames are the even-toolkit frames this implementation keeps. The
// component runs 32 frames of 50ms; the last frame of each quadrant is index
// 4q + 3 while filling and 16 + 4q + 3 while draining.
var upstreamFrames = [loadingFrames]int{3, 7, 11, 15, 19, 23, 27, 31}

// upstreamLit transcribes even-toolkit's Loading component, which decides each
// cell independently: cell i is lit while i <= t during the fill half of the
// cycle, and while i > t during the drain half, with t counting 0..15 in both.
//
// It is written from the component rather than from statusicon.go on purpose.
// The two share no arithmetic, so agreement between them is evidence that the
// eight frames really are the upstream ones rather than a lookalike.
func upstreamLit(frame, cell int) bool {
	const cells = loadingGrid * loadingGrid
	frame = ((frame % (cells * 2)) + cells*2) % (cells * 2)
	if frame < cells {
		return cell <= frame
	}
	return cell > frame-cells
}

// bmpValue reads one pixel back out of the encoded bitmap, so the tests check
// the image the lens actually receives instead of the arithmetic behind it.
func bmpValue(t *testing.T, bmp []byte, x, y int) uint8 {
	t.Helper()
	if len(bmp) != bmpHeaderBytes+bmpRowStride*statusIconSize {
		t.Fatalf("unexpected bitmap size %d", len(bmp))
	}
	return bmpValueSized(bmp, x, y, statusIconSize, bmpRowStride)
}

func bmpValueSized(bmp []byte, x, y, height, stride int) uint8 {
	// BMP rows run bottom-up, and each byte holds two pixels, high nibble first.
	offset := bmpHeaderBytes + (height-1-y)*stride + x/2
	if x%2 == 0 {
		return bmp[offset] >> 4
	}
	return bmp[offset] & 0x0F
}

// litGrid exercises the retained Toolkit sweep renderer directly. Hardware no
// longer schedules these frames, but keeping the renderer correct lets the
// desktop preview and any future non-BLE target share the same visual language.
func litGrid(t *testing.T, state string, frame int) [loadingGrid][loadingGrid]bool {
	t.Helper()
	bmp, err := protocol.EncodeBMP4(statusIconSize, statusIconSize, func(x, y int) uint8 {
		if loadingCellOn(frame, x, y) {
			return 15
		}
		return 0
	})
	if err != nil {
		t.Fatalf("encode loading frame: %v", err)
	}
	cell := statusIconSize / loadingGrid
	var lit [loadingGrid][loadingGrid]bool
	for row := range loadingGrid {
		for column := range loadingGrid {
			lit[row][column] = bmpValue(t, bmp, column*cell+cell/2, row*cell+cell/2) != 0
		}
	}
	return lit
}

func litCount(lit [loadingGrid][loadingGrid]bool) int {
	count := 0
	for _, row := range lit {
		for _, on := range row {
			if on {
				count++
			}
		}
	}
	return count
}

// TestLoadingFramesAreUpstreamDecimation is the point of the whole exercise: the
// eight frames a lens can afford must still be even-toolkit's animation. Frame k
// has to light exactly the cells upstream lights at upstreamFrames[k].
func TestLoadingFramesAreUpstreamDecimation(t *testing.T) {
	rank := map[[2]int]int{}
	for index, cell := range loadingCellOrder {
		rank[cell] = index
	}
	for frame, upstream := range upstreamFrames {
		lit := litGrid(t, "thinking", frame)
		for row := range loadingGrid {
			for column := range loadingGrid {
				want := upstreamLit(upstream, rank[[2]int{row, column}])
				if lit[row][column] != want {
					t.Fatalf("frame %d (upstream %d) cell (%d,%d): got %v, want %v",
						frame, upstream, row, column, lit[row][column], want)
				}
			}
		}
	}
}

// TestLoadingSweepLightsQuadrantsInOrder pins the motion itself, without going
// through the fill order at all: the block grows from the bottom-left corner and
// then drains back into that same corner.
func TestLoadingSweepLightsQuadrantsInOrder(t *testing.T) {
	all := func(int, int) bool { return true }
	none := func(int, int) bool { return false }
	top := func(row, _ int) bool { return row < 2 }
	bottom := func(row, _ int) bool { return row >= 2 }
	left := func(_, column int) bool { return column < 2 }
	right := func(_, column int) bool { return column >= 2 }
	union := func(a, b func(int, int) bool) func(int, int) bool {
		return func(row, column int) bool { return a(row, column) || b(row, column) }
	}
	intersect := func(a, b func(int, int) bool) func(int, int) bool {
		return func(row, column int) bool { return a(row, column) && b(row, column) }
	}

	cases := []struct {
		frame int
		note  string
		want  func(row, column int) bool
	}{
		{0, "bottom-left only", intersect(bottom, left)},
		{1, "the whole bottom half", bottom},
		{2, "everything but the top-left", union(bottom, right)},
		{3, "the full grid", all},
		{4, "bottom-left drained", union(top, right)},
		{5, "the whole top half", top},
		{6, "top-left only", intersect(top, left)},
		{7, "empty, ready to grow again", none},
	}
	for _, item := range cases {
		lit := litGrid(t, "tool", item.frame)
		for row := range loadingGrid {
			for column := range loadingGrid {
				if want := item.want(row, column); lit[row][column] != want {
					t.Fatalf("frame %d (%s) cell (%d,%d): got %v, want %v",
						item.frame, item.note, row, column, lit[row][column], want)
				}
			}
		}
	}
}

// TestLoadingSweepIsOneCycle checks the shape of the cycle: it grows a quadrant
// at a time, peaks, drains a quadrant at a time, and wraps.
func TestLoadingSweepIsOneCycle(t *testing.T) {
	for frame, want := range []int{4, 8, 12, 16, 12, 8, 4, 0} {
		if got := litCount(litGrid(t, "thinking", frame)); got != want {
			t.Fatalf("frame %d lit %d cells, want %d", frame, got, want)
		}
	}
	if first, next := litGrid(t, "thinking", 0), litGrid(t, "thinking", loadingFrames); first != next {
		t.Fatal("frame 0 and frame loadingFrames differ, so the sweep is not a cycle")
	}
	if got, want := litGrid(t, "thinking", -1), litGrid(t, "thinking", loadingFrames-1); got != want {
		t.Fatal("a negative frame did not wrap to the last frame")
	}
}

// TestStillStatesRenderOneFrame guards the other half of the design: a state the
// user reads at a glance costs exactly one bitmap, and it does not move.
func TestStillStatesRenderOneFrame(t *testing.T) {
	for _, state := range []string{"done", "idle", "needs_input", "permission", "paused", "error", "failed"} {
		if isWorkingState(state) {
			t.Fatalf("%s should hold still", state)
		}
		if got := statusIconFrameCount(state); got != 1 {
			t.Fatalf("%s animates with %d frames, want 1", state, got)
		}
		base := statusIcon(state, 0).BMP
		for _, frame := range []int{1, 3, 7, 11} {
			if !bytes.Equal(statusIcon(state, frame).BMP, base) {
				t.Fatalf("%s changed between frame 0 and frame %d", state, frame)
			}
		}
	}
}

// TestWorkingStatesShareTheToolkitLoadingSweep checks that active states use
// the same conservative hardware animation.
func TestWorkingStatesShareTheToolkitLoadingSweep(t *testing.T) {
	reference := statusIcon("thinking", 0).BMP
	for _, state := range []string{"thinking", "tool", "compacting", "some-new-state"} {
		if !isWorkingState(state) {
			t.Fatalf("%s should be treated as working", state)
		}
		if got := statusIconFrameCount(state); got != loadingFrames {
			t.Fatalf("%s has %d hardware frames, want %d", state, got, loadingFrames)
		}
		if !bytes.Equal(statusIcon(state, 0).BMP, reference) {
			t.Fatalf("%s does not share the loading sweep", state)
		}
		if bytes.Equal(statusIcon(state, 1).BMP, reference) {
			t.Fatalf("%s loading frame did not advance", state)
		}
	}
}

func TestActivityIconUsesOfficialActionMetaphors(t *testing.T) {
	cases := []struct {
		state, tool, current, want string
	}{
		{"thinking", "", "", "even-ai"},
		{"tool", "Read", "agentview.go", "search"},
		{"tool", "Grep", "statusIcon", "search"},
		{"tool", "Edit", "statusicon.go", "edit"},
		{"tool", "Bash", "go test ./...", "checklist"},
		{"tool", "Bash", "go build ./...", "play"},
		{"permission", "Bash", "deploy", "pause"},
		{"paused", "", "", "pause"},
		{"done", "", "", "complete"},
		{"failed", "Bash", "go test ./...", "alert"},
	}
	for _, item := range cases {
		if got := activityIcon(item.state, item.tool, item.current); got != item.want {
			t.Errorf("activityIcon(%q, %q, %q) = %q, want %q", item.state, item.tool, item.current, got, item.want)
		}
	}
}

func TestOfficialActionIconsRenderDifferentBitmaps(t *testing.T) {
	icons := []string{"even-ai", "play", "search", "edit", "checklist"}
	seen := map[string]bool{}
	for _, icon := range icons {
		bmp := string(statusIconFor("tool", icon, 0).BMP)
		if seen[bmp] {
			t.Fatalf("official action icon %q duplicated another bitmap", icon)
		}
		seen[bmp] = true
	}
}

// TestStatusIconGeometryIsStable pins the numbers both the layout and the frame
// cost are derived from: a 24px icon is four cells of 6px, while still fitting
// bytes, which is what decides how many frames the link can carry.
func TestStatusIconGeometryIsStable(t *testing.T) {
	if cell := statusIconSize / loadingGrid; cell != 6 {
		t.Fatalf("cell is %dpx, want 6", cell)
	}
	icon := statusIcon("thinking", 0)
	if icon.ID != statusIconID || icon.Name != statusIconName ||
		icon.Width != statusIconSize || icon.Height != statusIconSize {
		t.Fatalf("icon geometry drifted: %+v", icon)
	}
	if want := bmpHeaderBytes + bmpRowStride*statusIconSize; len(icon.BMP) != want {
		t.Fatalf("frame is %d bytes, want %d", len(icon.BMP), want)
	}
}

func TestCompletionIconIsInsideCardAndVerticallyCentered(t *testing.T) {
	icon := statusIcon("done", 0)
	if icon.Width != completionIconSize || icon.Height != completionIconSize {
		t.Fatalf("completion icon size = %dx%d, want %dx%d", icon.Width, icon.Height, completionIconSize, completionIconSize)
	}
	if icon.X != completionIconX || icon.Y != completionIconY {
		t.Fatalf("completion icon position = (%d,%d), want (%d,%d)", icon.X, icon.Y, completionIconX, completionIconY)
	}
	if icon.X <= completionCardX || icon.X+icon.Width >= completionCardX+completionCardWidth {
		t.Fatalf("completion icon is outside card horizontally: %+v", icon)
	}
	if icon.Y <= completionCardY || icon.Y+icon.Height >= completionCardY+completionCardHeight {
		t.Fatalf("completion icon is outside card vertically: %+v", icon)
	}
	if icon.Y*2+icon.Height != completionCardY*2+completionCardHeight {
		t.Fatalf("completion icon is not vertically centered in card: %+v", icon)
	}
}

func TestCompletionIconIsAPlainCheckmark(t *testing.T) {
	icon := statusIcon("done", 0)
	stride := ((icon.Width+1)/2 + 3) &^ 3
	for _, point := range [][2]int{{9, 25}, {18, 34}, {39, 12}} {
		if pixel := bmpValueSized(icon.BMP, point[0], point[1], icon.Height, stride); pixel == 0 {
			t.Fatalf("completion icon pixel (%d,%d) is off", point[0], point[1])
		}
	}
	for _, point := range [][2]int{{4, 8}, {42, 8}, {4, 39}, {42, 39}} {
		if pixel := bmpValueSized(icon.BMP, point[0], point[1], icon.Height, stride); pixel != 0 {
			t.Fatalf("completion icon retained a frame pixel at (%d,%d)", point[0], point[1])
		}
	}
}

// TestStatusIconFitsInThreeBLEFrames is the load-bearing half of the geometry.
//
// Every icon update is a fresh bitmap pushed over BLE, so the sweep's cost is
// this frame count multiplied by how often it ticks. The sweep runs for as long
// as the agent works and shares the arm with the link's heartbeat, which is what
// made the glasses freeze and drop the connection; the icon size is therefore
// chosen for the frame count, not for looks. Deliberately raising this number
// means deliberately raising the sustained BLE load.
func TestStatusIconFitsInThreeBLEFrames(t *testing.T) {
	const want = 3
	icon := statusIcon("thinking", 0)
	fragment, err := protocol.BuildEvenHubImageFragment(
		icon.ID, icon.Name, 1, len(icon.BMP), 0, icon.BMP, 7)
	if err != nil {
		t.Fatalf("BuildEvenHubImageFragment() error = %v", err)
	}
	frames, err := protocol.FrameEvenHub(1, protocol.EvenHubServiceID, protocol.EvenHubRequest, fragment, evenHubChunkSize)
	if err != nil {
		t.Fatalf("FrameEvenHub() error = %v", err)
	}
	if len(frames) != want {
		t.Fatalf("one icon update is %d BLE frames, want %d; the link would carry %d B per update",
			len(frames), want, len(frames)*evenHubFrameBytes)
	}
}

// TestLoadingCyclePace pins the other half of the load: how long one sweep
// cycle takes. Frames per update times updates per cycle is the sustained cost,
// so a change to either number is a change to what the link has to carry.
func TestLoadingCyclePace(t *testing.T) {
	cycle := time.Duration(loadingFrames) * loadingFrameDelay
	if cycle < 8*time.Second {
		t.Fatalf("a full cycle is %s; below eight seconds spends too much BLE capacity", cycle)
	}
	if cycle > 15*time.Second {
		t.Fatalf("a full cycle is %s; past fifteen seconds it stops reading as motion", cycle)
	}
}
