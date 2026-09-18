package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestCompletionNotificationIncludesSession(t *testing.T) {
	record := agentStateRecord{
		SessionID: "session-1", State: "done", Title: "  修复菜单\n并整理样式  ", Project: "even-app",
		StartedAt: 1699999958, Timestamp: 1700000000, StepCount: 5, FileCount: 2, Additions: 22, Deletions: 4,
	}
	notification := sessionCompletionNotification(statusSource{Agent: "codex", Label: "Codex"}, record)
	if notification.ID != "codex:session-1" || notification.PackageName != codexMenuPackage || notification.Title != "修复菜单 并整理样式" || notification.Subtitle != "even-app · 已完成" || notification.Message != "用时 0:42 · 5 步\n2 个文件 · +22 / -4" || notification.Timestamp.Unix() != 1700000000 {
		t.Fatalf("notification: %+v", notification)
	}
}

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

func TestBlankAgentPageCoversTheLensWithoutDecoration(t *testing.T) {
	style := blankAgentPageStyle()
	if style.X != 0 || style.Y != 0 || style.Width != 576 || style.Height != 288 {
		t.Fatalf("blank page geometry = %+v, want full 576x288 lens", style)
	}
	if style.BorderWidth != 0 || style.BorderColor != 0 || style.BorderRadius != 0 || style.PaddingLength != 0 {
		t.Fatalf("blank page has visible decoration: %+v", style)
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

func TestAgentLayoutsFitTextAndIcons(t *testing.T) {
	for _, state := range []string{"tool", "done"} {
		style := agentPageStyle(state)
		rows := composeAgentRows(sampleSource(), agentStateRecord{
			State: state, Project: strings.Repeat("宽W", 60), Current: strings.Repeat("W", 100),
			FileCount: 123, Additions: 123456, Deletions: 123456,
		}, []agentStreamStep{{Kind: "prompt", Text: strings.Repeat("中文W", 60)}}, viewNow)
		innerHeight := style.Height - 2*(style.PaddingLength+style.BorderWidth)
		if len(rows)*27 > innerHeight || style.X+style.Width > 576 || style.Y+style.Height > 288 {
			t.Fatalf("%s layout overflows: %+v, %d rows", state, style, len(rows))
		}
		for _, row := range rows {
			innerWidth := style.Width - 2*(style.PaddingLength+style.BorderWidth)
			if textUnits(row) > innerWidth {
				t.Fatalf("%s row exceeds text width: %q", state, row)
			}
		}
		icon := statusIcon(state, 0)
		if state == "done" && (icon.X <= style.X || icon.X+icon.Width >= style.X+style.Width) {
			t.Fatalf("completion icon is outside its frame: %+v", icon)
		}
	}
}

func TestTerminalLayoutKeepsOuterFrame(t *testing.T) {
	style := agentPageStyle("tool")
	if style.BorderWidth != 1 || style.BorderColor != 7 || style.BorderRadius != 6 || style.PaddingLength != 12 {
		t.Fatalf("terminal frame style = %+v", style)
	}
}

func TestCompletionLayoutKeepsOuterFrame(t *testing.T) {
	style := agentPageStyle("done")
	if style.BorderWidth != 2 || style.BorderColor != 15 || style.BorderRadius != 10 || style.PaddingLength != 16 {
		t.Fatalf("completion frame style = %+v", style)
	}
}
