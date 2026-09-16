package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
	"time"
)

func TestDisplayDurationDefaultsToFiveSeconds(t *testing.T) {
	service := &EvenService{}
	status := service.Status()
	if status.DisplayDurationSeconds != 5 {
		t.Fatalf("DisplayDurationSeconds = %d, want 5", status.DisplayDurationSeconds)
	}
}

func TestShowImageRejectsInvalidData(t *testing.T) {
	service := &EvenService{}
	if err := service.ShowImage("not-base64"); err == nil {
		t.Fatal("ShowImage() accepted invalid base64")
	}
}

func TestShowImageDecodesDataURLBeforeCheckingConnection(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	service := &EvenService{}
	err := service.ShowImage("data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes()))
	if err == nil || err.Error() != "请先连接 Even G2" {
		t.Fatalf("ShowImage() error = %v, want connection error after successful decode", err)
	}
}

func TestSetDisplayDuration(t *testing.T) {
	service := &EvenService{}
	if err := service.SetDisplayDuration(12); err != nil {
		t.Fatalf("SetDisplayDuration() error = %v", err)
	}
	if got := service.Status().DisplayDurationSeconds; got != 12 {
		t.Fatalf("DisplayDurationSeconds = %d, want 12", got)
	}
}

func TestSetDisplayDurationRejectsOutOfRangeValues(t *testing.T) {
	service := &EvenService{}
	for _, seconds := range []int{0, 301} {
		if err := service.SetDisplayDuration(seconds); err == nil {
			t.Errorf("SetDisplayDuration(%d) returned nil error", seconds)
		}
	}
}

func TestCompletionDisplayStaysVisibleLongEnough(t *testing.T) {
	if got := displayClearDelay(5*time.Second, "done"); got != completionDisplayDuration {
		t.Fatalf("done delay = %s, want %s", got, completionDisplayDuration)
	}
	if got := displayClearDelay(30*time.Second, "done"); got != 30*time.Second {
		t.Fatalf("configured longer delay = %s, want 30s", got)
	}
	if got := displayClearDelay(5*time.Second, "idle"); got != 5*time.Second {
		t.Fatalf("idle delay = %s, want 5s", got)
	}
}

// stopLoadingLocked is the only place the sweep's liveness is sampled, because
// it clears the field on the way out. A caller that read loadingActive for
// itself after calling it would always get false and restart the sweep on every
// push, resetting the animation before it could complete a cycle.
func TestStopLoadingReportsWhetherASweepWasRunning(t *testing.T) {
	service := &EvenService{}

	if got := service.stopLoadingLocked(); got {
		t.Fatal("stopLoadingLocked() reported a sweep with none started")
	}

	service.loadingActive = true
	tokenBefore := service.loadingToken.Load()
	if got := service.stopLoadingLocked(); !got {
		t.Fatal("stopLoadingLocked() reported no sweep while one was running")
	}
	if service.loadingActive {
		t.Fatal("stopLoadingLocked() left the sweep marked active")
	}
	if service.loadingToken.Load() == tokenBefore {
		t.Fatal("stopLoadingLocked() did not retire the sweep's token")
	}
	if got := service.stopLoadingLocked(); got {
		t.Fatal("stopLoadingLocked() reported a sweep after retiring it")
	}
}

// The sweep has to survive the pushes that arrive while it runs. An agent
// working on a task publishes its elapsed clock once a second, and each of
// those pushes retires the sweep before writing; restarting on all of them
// would reset the block to frame 0 before it could finish a cycle, which is
// what makes the icon look frozen instead of moving.
func TestShouldRestartSweepKeepsARunningSweepAlone(t *testing.T) {
	clockTick := agentDisplayUpdate{rows: []string{"a", "b"}, animate: false, state: "thinking"}

	cases := []struct {
		name          string
		wasLoading    bool
		update        agentDisplayUpdate
		previousState string
		want          bool
	}{
		{
			name:          "clock tick during a run leaves the sweep alone",
			wasLoading:    true,
			update:        clockTick,
			previousState: "thinking",
			want:          false,
		},
		{
			name:          "no sweep running starts one",
			wasLoading:    false,
			update:        clockTick,
			previousState: "thinking",
			want:          true,
		},
		{
			name:          "a real transition restarts so the block grows again",
			wasLoading:    true,
			update:        agentDisplayUpdate{rows: []string{"a", "b"}, animate: true, state: "thinking"},
			previousState: "thinking",
			want:          true,
		},
		{
			name:          "a new working state restarts even without the animate flag",
			wasLoading:    true,
			update:        agentDisplayUpdate{rows: []string{"a", "b"}, animate: false, state: "tool"},
			previousState: "thinking",
			want:          true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldRestartSweep(testCase.wasLoading, testCase.update, testCase.previousState); got != testCase.want {
				t.Fatalf("shouldRestartSweep() = %v, want %v", got, testCase.want)
			}
		})
	}
}
