package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestProjectRowsRespectByteAndPixelLimits(t *testing.T) {
	for _, project := range []string{"even-app", strings.Repeat("i", 100), strings.Repeat("中文项目", 20)} {
		for _, state := range []string{"working", "done"} {
			rows, keys := composeProjectRows([]agentSession{{record: agentStateRecord{Project: project, State: state}}})
			line := rows[0]
			if !utf8.ValidString(line) || len(line) > 63 || textUnits(line) > lensUnits {
				t.Fatalf("invalid row: %q (%d bytes, %d pixels)", line, len(line), textUnits(line))
			}
			if !strings.HasPrefix(line, "▶ ") || keys[0] != "project:"+project {
				t.Fatalf("project entry changed: %q, %v", line, keys)
			}
			if state == "working" {
				if !strings.HasSuffix(line, "  ●") {
					t.Fatalf("missing activity marker: %q", line)
				}
				if len(line) < 63 && textUnits(line)+characterUnits(' ') <= lensUnits {
					t.Fatalf("activity marker could move farther right: %q", line)
				}
			} else if strings.HasSuffix(line, "●") {
				t.Fatalf("inactive project has activity marker: %q", line)
			}
		}
	}
}

var viewNow = time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)

func sampleSource() statusSource {
	return statusSource{Agent: "workbuddy", Label: "WorkBuddy", Dir: "/tmp/statusbar/state.d"}
}

// TestComposeAgentRowsRendersTheWorkingSession pins the whole layout, so any
// change to what the glasses show has to be deliberate.
//
//	● WorkBuddy / even-app
//	★ 把改动文件推给眼镜
//	▶ 执行中 · 2:14  □ 9步/2文件
//	▷ 运行 go build ./...
//	■ 写入 statusmonitor.go
func TestComposeAgentRowsRendersTheWorkingSession(t *testing.T) {
	record := agentStateRecord{
		State:       "tool",
		Project:     "even-app",
		SessionID:   "s1",
		StepCount:   9,
		ChangeCount: 3,
		StartedAt:   float64(viewNow.Add(-2*time.Minute - 14*time.Second).Unix()),
		Current:     "go build ./...",
		CurrentTool: "Bash",
		FileCount:   2,
		Additions:   597,
		Deletions:   38,
	}
	steps := []agentStreamStep{
		{Kind: "prompt", Text: "把改动文件推给眼镜"},
		{Kind: "tool", Tool: "Write", Status: "ok", Text: "statusmonitor.go", File: "statusmonitor.go", Action: "modify", Additions: 12, Deletions: 3},
		{Kind: "say", Text: "先看 SDK 的能力边界"},
	}

	rows := composeAgentRows(sampleSource(), record, steps, viewNow)
	want := []string{
		sessionPrompt + "WorkBuddy · even-app",
		lens.User + "把改动文件推给眼镜",
		lens.Header + "● 正在执行  ·  2:14  ·  2F +597 -38",
		lens.Live + "正在运行命令 go build ./...",
		lens.Header + "────────────────────",
		lens.Step + "statusmonitor.go                                 ● M +12 -3",
		commandPrompt + "go build ./...",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows =\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

// TestComposeAgentRowsStaysInsideTheBudget is the guarantee that matters most:
// nothing we hand the firmware is wider than the row budget, because the
// firmware clips silently rather than wrapping.
func TestComposeAgentRowsStaysInsideTheBudget(t *testing.T) {
	record := agentStateRecord{
		State:       "tool",
		Project:     "a-really-quite-long-project-name",
		StartedAt:   float64(viewNow.Add(-90 * time.Minute).Unix()),
		Current:     "go test -count=1 -race ./internal/very/deep/package/...",
		CurrentTool: "Bash",
		FileCount:   12,
		Additions:   12345,
		Deletions:   678,
	}
	steps := []agentStreamStep{
		{Kind: "say", Text: strings.Repeat("很长的说明文字", 6)},
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "even-workbuddy-status-writer.test.mjs"},
		{Kind: "tool", Tool: "Bash", Status: "fail", Text: "some-command --with --a --lot --of --flags"},
	}
	rows := composeAgentRows(sampleSource(), record, steps, viewNow)
	for _, row := range rows {
		if !fitsUnits(row, lensUnits) {
			t.Fatalf("row %q is %d units, budget is %d", row, textUnits(row), lensUnits)
		}
	}
}

// TestComposeAgentRowsBoundedByBudget makes sure a chatty session cannot push
// the list past the row budget, and that the newest step always survives.
func TestComposeAgentRowsBoundedByBudget(t *testing.T) {
	steps := make([]agentStreamStep, 0, 40)
	for i := 0; i < 40; i++ {
		steps = append(steps, agentStreamStep{Kind: "tool", Tool: "Read", Status: "ok", Text: "file" + string(rune('a'+i%26)) + ".go"})
	}
	rows := composeAgentRows(sampleSource(), agentStateRecord{State: "thinking", Project: "even-app"}, steps, viewNow)

	if len(rows) > maxRows {
		t.Fatalf("rows = %d, want at most %d", len(rows), maxRows)
	}
	newest := row(lens.Step, stepBody(steps[len(steps)-1]))
	if !strings.Contains(strings.Join(rows, "\n"), newest) {
		t.Fatalf("newest step %q missing from rows: %v", newest, rows)
	}
}

func TestComposeAgentRowsKeepsInformationOrderFixed(t *testing.T) {
	record := agentStateRecord{State: "tool", Project: "even-app", Current: "ls", CurrentTool: "Bash", FileCount: 3, Additions: 9, Deletions: 2}
	steps := []agentStreamStep{{Kind: "tool", Tool: "Read", Status: "ok", Text: "main.go"}}
	rows := composeAgentRows(sampleSource(), record, steps, viewNow)

	prefixes := []string{sessionPrompt + "WorkBuddy · even-app", lens.Header + "● ", lens.Live + "正在查看目录", lens.Header + "─"}
	for index, prefix := range prefixes {
		if !strings.HasPrefix(rows[index], prefix) {
			t.Fatalf("row %d = %q, want prefix %q", index, rows[index], prefix)
		}
	}
}

func TestHeaderKeepsIdentitySeparateFromClock(t *testing.T) {
	started := float64(viewNow.Add(-2*time.Minute - 14*time.Second).Unix())

	short := headerRow(sampleSource(), agentStateRecord{Project: "even-app", StartedAt: started}, viewNow)
	if short != sessionPrompt+"WorkBuddy · even-app" {
		t.Fatalf("short header = %q", short)
	}

	long := headerRow(sampleSource(), agentStateRecord{Project: "a-really-quite-long-project-name", StartedAt: started}, viewNow)
	if !strings.HasPrefix(long, sessionPrompt+"WorkBuddy · a-really") {
		t.Fatalf("long header lost the identity: %q", long)
	}
	if !fitsUnits(long, lensUnits) {
		t.Fatalf("long header is %d units, budget is %d", textUnits(long), lensUnits)
	}

	// With no clock to show at all the identity is still rendered whole.
	bare := headerRow(sampleSource(), agentStateRecord{Project: "even-app"}, viewNow)
	if bare != sessionPrompt+"WorkBuddy · even-app" {
		t.Fatalf("bare header = %q", bare)
	}
}

// TestLiveRowCoversEveryState keeps the live line honest: every state the
// adapter can emit has to say something, and done must not claim to be busy.
func TestLiveRowCoversEveryState(t *testing.T) {
	cases := map[string]string{
		"thinking":       lens.Live + "思考中…",
		"needs_input":    lens.Wait + "等你回答",
		"permission":     lens.Wait + "等你授权",
		"compacting":     lens.Live + "压缩上下文中…",
		"paused":         lens.Wait + "任务已暂停",
		"idle":           lens.Step + "空闲",
		"":               "",
		"somethingWeird": "",
	}
	for state, want := range cases {
		if got := liveRow(agentStateRecord{State: state}); got != want {
			t.Fatalf("liveRow(%q) = %q, want %q", state, got, want)
		}
	}
	if got := liveRow(agentStateRecord{State: "done", StepCount: 9}); got != lens.Step+"已完成 · 9 步" {
		t.Fatalf("done live row = %q", got)
	}

	// A tool state with an in-flight call shows the call, not a generic label.
	if got := liveRow(agentStateRecord{State: "tool", CurrentTool: "Edit", Current: "evenservice.go"}); got != lens.Live+"M evenservice.go" {
		t.Fatalf("live tool row = %q", got)
	}
	if got := liveRow(agentStateRecord{State: "tool"}); got != lens.Live+"执行中…" {
		t.Fatalf("bare tool row = %q", got)
	}
}

// TestStepRowsSkipTheLiveLine avoids showing the in-flight call twice.
func TestStepRowsSkipTheLiveLine(t *testing.T) {
	record := agentStateRecord{State: "tool", CurrentTool: "Bash", Current: "go build ./..."}
	steps := []agentStreamStep{
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "evenservice.go"},
		{Kind: "tool", Tool: "Bash", Status: "ok", Text: "go build ./..."},
	}
	rows := stepRows(steps, record, 6)
	for _, row := range rows {
		if strings.Contains(row, "go build") {
			t.Fatalf("live line was repeated in the step rows: %v", rows)
		}
	}
	if len(rows) != 1 || rows[0] != lens.Step+"M evenservice.go" {
		t.Fatalf("step rows = %v", rows)
	}

	// A repeat further down the history is real, so it survives.
	deeper := []agentStreamStep{
		{Kind: "tool", Tool: "Bash", Status: "ok", Text: "go build ./..."},
		{Kind: "tool", Tool: "Read", Status: "ok", Text: "go.mod"},
		{Kind: "tool", Tool: "Bash", Status: "ok", Text: "go build ./..."},
	}
	if rows := stepRows(deeper, record, 6); len(rows) != 2 {
		t.Fatalf("deeper repeat was dropped: %v", rows)
	}
}

// TestStepRowsRespectsTheBudget keeps the composer from overrunning the list.
func TestStepRowsRespectsTheBudget(t *testing.T) {
	steps := []agentStreamStep{}
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		steps = append(steps, agentStreamStep{Kind: "tool", Tool: "Read", Status: "ok", Text: name})
	}
	for budget := 0; budget <= 5; budget++ {
		if rows := stepRows(steps, agentStateRecord{}, budget); len(rows) != budget {
			t.Fatalf("budget %d produced %d rows: %v", budget, len(rows), rows)
		}
	}
}

// TestStepRowsCollapseRepeats keeps a burst of identical edits from eating the
// whole row budget: three "编辑 App.tsx" rows must become one.
func TestStepRowsCollapseRepeats(t *testing.T) {
	steps := []agentStreamStep{
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "App.tsx"},
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "App.tsx"},
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "App.tsx"},
		{Kind: "tool", Tool: "Read", Status: "ok", Text: "App.tsx"},
		{Kind: "tool", Tool: "Edit", Status: "ok", Text: "App.tsx"},
	}
	rows := stepRows(steps, agentStateRecord{}, 6)
	// Newest first: the last three entries of the slice are the run of three.
	want := []string{lens.Step + "M App.tsx", lens.Step + "R App.tsx", lens.Step + "M App.tsx ×3"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("collapsed rows = %v, want %v", rows, want)
	}
}

func TestToolActionCoversTaskTools(t *testing.T) {
	cases := map[string]string{
		"TaskUpdate": "更新任务 #2",
		"TaskCreate": "新建任务 拆分模块",
		"TaskList":   "查看任务",
		"Read":       "R go.mod",
	}
	for tool, want := range cases {
		target := map[string]string{
			"TaskUpdate": "#2",
			"TaskCreate": "拆分模块",
			"Read":       "go.mod",
		}[tool]
		if got := toolAction(tool, target); got != want {
			t.Fatalf("toolAction(%q) = %q, want %q", tool, got, want)
		}
	}
	// An unknown tool still renders as something rather than an empty row.
	if got := toolAction("BrandNewTool", ""); got != "BrandNewTool" {
		t.Fatalf("unknown tool = %q", got)
	}
}

// TestStepMarkerCarriesTheMeaning is the point of the gutter: "failed" and
// "waiting on you" are now conveyed by the glyph, not by words, which buys the
// width back on every row.
func TestStepMarkerCarriesTheMeaning(t *testing.T) {
	cases := []struct {
		step agentStreamStep
		want string
	}{
		{agentStreamStep{Kind: "tool", Tool: "Bash", Status: "fail"}, lens.Fail},
		{agentStreamStep{Kind: "tool", Tool: "Bash", Status: "ok"}, lens.Step},
		{agentStreamStep{Kind: "say"}, lens.Say},
		{agentStreamStep{Kind: "ask"}, lens.Wait},
		{agentStreamStep{Kind: "prompt"}, lens.User},
		{agentStreamStep{Kind: "note"}, lens.Step},
		{agentStreamStep{Kind: "done"}, lens.Step},
	}
	for _, item := range cases {
		if got := stepMarker(item.step); got != item.want {
			t.Fatalf("stepMarker(%s) = %q, want %q", item.step.Kind, got, item.want)
		}
	}
}

func TestStepBodyLeavesTheMarkerToSayIt(t *testing.T) {
	cases := []struct {
		step agentStreamStep
		want string
	}{
		{agentStreamStep{Kind: "tool", Tool: "Bash", Status: "fail", Text: "go test ./..."}, "go test ./..."},
		{agentStreamStep{Kind: "tool", Tool: "Edit", Status: "ok", Text: "agentview.go", File: "agentview.go", Action: "modify", Additions: 8, Deletions: 2}, "M agentview.go  +8 -2"},
		{agentStreamStep{Kind: "say", Text: "  先看 SDK  "}, "先看 SDK"},
		{agentStreamStep{Kind: "ask", Text: "运行 rm -rf build"}, "运行 rm -rf build"},
		{agentStreamStep{Kind: "ask", Tool: "Bash", Text: "rm -rf build"}, "rm -rf build"},
		{agentStreamStep{Kind: "ask", Text: "已空闲，等你回来"}, "已空闲，等你回来"},
		{agentStreamStep{Kind: "prompt", Text: "把改动推给眼镜"}, "把改动推给眼镜"},
		{agentStreamStep{Kind: "done", Text: "本轮结束"}, "本轮结束"},
	}
	for _, item := range cases {
		if got := stepBody(item.step); got != item.want {
			t.Fatalf("stepBody(%s) = %q, want %q", item.step.Kind, got, item.want)
		}
	}
}

// TestRowRefusesAnEmptyBody: an empty row would be rendered by the firmware as
// a lone "·", which reads as content that is not there.
func TestRowRefusesAnEmptyBody(t *testing.T) {
	if got := row(lens.Step, "   "); got != "" {
		t.Fatalf("empty body produced %q", got)
	}
	if got := row(lens.Step, "读取 main.go"); got != lens.Step+"读取 main.go" {
		t.Fatalf("row = %q", got)
	}
}

// Proportional Latin glyphs must not be measured as terminal columns.
func TestTextUnitsUsesFirmwareAdvances(t *testing.T) {
	if got := textUnits("abcd"); got != 45 {
		t.Fatalf("ascii units = %d", got)
	}
	if got := textUnits("中文"); got != 2*cjkUnits {
		t.Fatalf("cjk units = %d", got)
	}
	if got := textUnits("a中"); got != 12+cjkUnits {
		t.Fatalf("mixed units = %d", got)
	}
}

// TestTruncateUnitsStaysInsideBudget guards the row budget itself: no clipping
// path may produce a row wider than the budget, whatever the script mix.
func TestTruncateUnitsStaysInsideBudget(t *testing.T) {
	cases := []string{
		strings.Repeat("编", 60),
		strings.Repeat("a", 200),
		strings.Repeat("混合 mixed 文本 ", 20),
		"  padded  ",
		"ok",
	}
	for _, input := range cases {
		got := truncateUnits(input, lensUnits)
		if !fitsUnits(got, lensUnits) {
			t.Fatalf("truncateUnits(%q) = %q, %d units > %d", input, got, textUnits(got), lensUnits)
		}
		if strings.Contains(input, "padded") {
			continue
		}
		if textUnits(input) > lensUnits && !strings.HasSuffix(got, lens.Ellipsis) {
			t.Fatalf("clipped row %q is missing the ellipsis", got)
		}
	}
	if got := truncateUnits("  short  ", lensUnits); got != "short" {
		t.Fatalf("trim = %q", got)
	}
	// A budget that cannot even hold the ellipsis degrades to just the ellipsis
	// rather than to a row that overflows.
	if got := truncateUnits("界", asciiUnits); got != lens.Ellipsis {
		t.Fatalf("degenerate budget = %q", got)
	}
}

// TestCalibrationRowsAreSelfDescribing keeps the calibration list honest: it is
// the only instrument for the two unknowns, so a row that shifts position or
// loses its label would invalidate the reading.
func TestCalibrationRowsAreSelfDescribing(t *testing.T) {
	rows := calibrationRows()
	if len(rows) != calibrationRowCount {
		t.Fatalf("calibration has %d rows, want %d", len(rows), calibrationRowCount)
	}
	if !strings.HasPrefix(rows[0], "1 ") {
		t.Fatalf("row 0 is not the instruction row: %q", rows[0])
	}
	// The rulers start at column 0, so the last visible glyph is the reading.
	if rows[1] != "abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("ascii ruler = %q", rows[1])
	}
	if runes := []rune(rows[2]); len(runes) < 20 {
		t.Fatalf("cjk ruler is too short to measure with: %q", rows[2])
	}
	if !strings.Contains(rows[3], "▍") || !strings.Contains(rows[3], lens.Say) {
		t.Fatalf("glyph row does not offer both candidates and defaults: %q", rows[3])
	}
	// Numbered rows ascend so the highest visible number reads out as maxRows.
	for index := 4; index < len(rows); index++ {
		want := fmt.Sprintf("第%d行", index+1)
		if rows[index] != want {
			t.Fatalf("row %d = %q, want %q", index, rows[index], want)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		14 * time.Second:                "0:14",
		2*time.Minute + 14*time.Second:  "2:14",
		59*time.Minute + 59*time.Second: "59:59",
		3*time.Hour + 7*time.Minute:     "3:07",
		100*time.Hour + 30*time.Minute:  "100:30",
	}
	for input, want := range cases {
		if got := formatElapsed(input); got != want {
			t.Fatalf("formatElapsed(%s) = %q, want %q", input, got, want)
		}
	}
}

func TestElapsedRowContinuesWhileRunningAndFreezesWhenDone(t *testing.T) {
	started := float64(viewNow.Add(-2 * time.Minute).Unix())
	running := agentStateRecord{State: "tool", StartedAt: started}
	if got := elapsedRow(running, viewNow); got != "时间  2:00" {
		t.Fatalf("running time = %q", got)
	}
	if got := elapsedRow(running, viewNow.Add(7*time.Second)); got != "时间  2:07" {
		t.Fatalf("continued time = %q", got)
	}
	done := agentStateRecord{State: "done", StartedAt: started, Timestamp: float64(viewNow.Unix())}
	if got := elapsedRow(done, viewNow.Add(time.Hour)); got != "时间  2:00" {
		t.Fatalf("done time did not freeze: %q", got)
	}
}

func TestNotificationFrameAnimatesOnlyTheTitle(t *testing.T) {
	rows := []string{lens.Header + "Codex / even-app", "▶ 执行中"}
	frame := notificationFrame(rows, 2)
	if frame[0] != ".. Codex / even-app" || frame[1] != rows[1] {
		t.Fatalf("animation frame = %v", frame)
	}
	if rows[0] != lens.Header+"Codex / even-app" {
		t.Fatalf("animation mutated source rows: %v", rows)
	}
}

func TestQuestionUsesTwoFullWidthRowsBeforeTruncating(t *testing.T) {
	prompt := strings.Repeat("这是一个需要保留在眼镜上的较长问题", 8)
	rows := questionRows([]agentStreamStep{{Kind: "prompt", Text: prompt}}, 4)
	if len(rows) != 4 {
		t.Fatalf("question rows = %v, want four", rows)
	}
	if !strings.HasPrefix(rows[0], lens.User) || !strings.HasPrefix(rows[1], "  ") {
		t.Fatalf("question continuation is unclear: %v", rows)
	}
	for _, row := range rows {
		if !fitsUnits(row, lensUnits) {
			t.Fatalf("question row exceeds lens width: %q", row)
		}
	}
	if !strings.HasSuffix(rows[3], lens.Ellipsis) {
		t.Fatalf("truncated question has no ellipsis: %q", rows[3])
	}
}

func TestActivityRowsStreamNewestCompletedActions(t *testing.T) {
	steps := []agentStreamStep{
		{Kind: "prompt", Text: "优化显示"},
		{Kind: "tool", Tool: "Read", Text: "agentview.go"},
		{Kind: "say", Text: "准备修改"},
		{Kind: "tool", Tool: "Edit", Text: "agentview.go"},
		{Kind: "tool", Tool: "Bash", Text: "go test ./..."},
	}
	rows := activityRows(steps, agentStateRecord{}, 2)
	want := []string{lens.Step + "M agentview.go", commandPrompt + "go test ./..."}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("stream rows = %v, want %v", rows, want)
	}
}

func TestActivityRowsPinsCurrentCommandAtTheBottomWithoutLiveMarker(t *testing.T) {
	steps := []agentStreamStep{
		{Kind: "tool", Tool: "Read", Text: "go.mod"},
		{Kind: "tool", Tool: "Edit", Text: "agentview.go"},
	}
	record := agentStateRecord{State: "tool", CurrentTool: "Bash", Current: "go test ./..."}
	rows := activityRows(steps, record, 3)
	want := []string{lens.Step + "R go.mod", lens.Step + "M agentview.go", commandPrompt + "go test ./..."}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("activity rows = %v, want %v", rows, want)
	}
	if strings.Contains(rows[len(rows)-1], lens.Live) {
		t.Fatalf("current command still carries the live marker: %q", rows[len(rows)-1])
	}
}

func TestLiveCommandPromptAcceptsCodexToolNames(t *testing.T) {
	for _, tool := range []string{"Bash", "exec_command", "functions.exec_command"} {
		if got := liveToolAction(tool, "go test ./..."); got != commandPrompt+"go test ./..." {
			t.Fatalf("liveToolAction(%q) = %q", tool, got)
		}
	}
}

func TestThinkingRowNeverRendersCommandPrompt(t *testing.T) {
	rows := composeAgentRows(sampleSource(), agentStateRecord{State: "thinking", Project: "even-app"}, nil, viewNow)
	for _, rendered := range rows[1:] {
		if strings.HasPrefix(rendered, commandPrompt) {
			t.Fatalf("thinking view contains command prompt: %v", rows)
		}
	}
}

func TestThinkingViewShowsLatestAssistantOutput(t *testing.T) {
	steps := []agentStreamStep{
		{Kind: "say", Text: "先检查页面状态"},
		{Kind: "tool", Tool: "Read", Text: "agentview.go"},
		{Kind: "say", Text: strings.Repeat("正在核对终端布局", 20)},
	}
	rows := composeAgentRows(sampleSource(), agentStateRecord{State: "thinking", Project: "even-app"}, steps, viewNow)
	if len(rows) < 3 || !strings.HasPrefix(rows[2], lens.Say+"正在核对终端布局") {
		t.Fatalf("latest analysis output missing: %v", rows)
	}
	if !strings.HasSuffix(rows[2], lens.Ellipsis) || !fitsUnits(rows[2], lensUnits) {
		t.Fatalf("analysis output was not safely truncated: %q", rows[2])
	}
}

// TestLedgerRowHiddenWithoutChanges keeps a quiet session from reserving a row
// for nothing.
func TestLedgerRowHiddenWithoutChanges(t *testing.T) {
	if got := ledgerRow(agentStateRecord{}); got != "" {
		t.Fatalf("empty ledger row = %q", got)
	}
	rows := composeAgentRows(sampleSource(), agentStateRecord{State: "idle", Project: "even-app"}, nil, viewNow)
	if len(rows) != 3 {
		t.Fatalf("idle session without changes = %v", rows)
	}
}

func TestComposeAgentRowsPromotesHumanAction(t *testing.T) {
	record := agentStateRecord{State: "permission", Project: "even-app"}
	steps := []agentStreamStep{
		{Kind: "prompt", Text: "部署应用"},
		{Kind: "ask", Tool: "Bash", Text: "wails3 package"},
	}
	rows := composeAgentRows(sampleSource(), record, steps, viewNow)
	if rows[3] != lens.Wait+"wails3 package" {
		t.Fatalf("human action was not promoted: %v", rows)
	}
}

func TestThinkingShowsSettledWorkSummary(t *testing.T) {
	steps := []agentStreamStep{
		{Kind: "prompt", Text: "检查布局"},
		{Kind: "tool", Tool: "Read", Status: "ok", Text: "agentview.go"},
		{Kind: "tool", Tool: "Grep", Status: "ok", Text: "sessionPrompt"},
		{Kind: "tool", Tool: "Edit", Status: "ok", File: "agentview.go", Action: "modify"},
		{Kind: "tool", Tool: "Bash", Status: "ok", Text: "go test ./..."},
	}
	got := focusRow(agentStateRecord{State: "thinking"}, steps)
	want := lens.Live + "已运行命令 go test ./..."
	if got != want {
		t.Fatalf("thinking summary = %q, want %q", got, want)
	}
}

func TestFileChangeBadgeUsesSupportedCircle(t *testing.T) {
	got := fileChangeRow(lens.Step, "agentview.go", "M +1 -1")
	if !strings.Contains(got, "● M +1 -1") || strings.Contains(got, "■") {
		t.Fatalf("file change badge = %q", got)
	}
}

func TestComposeAgentRowsMakesCompletionProminent(t *testing.T) {
	record := agentStateRecord{
		State: "done", Project: "even-app", StepCount: 4, FileCount: 2,
		Additions: 28, Deletions: 6, StartedAt: float64(viewNow.Add(-3 * time.Minute).Unix()), Timestamp: float64(viewNow.Unix()),
	}
	steps := []agentStreamStep{
		{Kind: "prompt", Text: "完善完成提示"},
		{Kind: "tool", Tool: "Edit", File: "agentview.go", Action: "modify", Additions: 20, Deletions: 4},
		{Kind: "tool", Tool: "Bash", Text: "go test ./..."},
	}
	rows := composeAgentRows(sampleSource(), record, steps, viewNow)
	if len(rows) > maxRows {
		t.Fatalf("completion rows = %d, want at most %d: %v", len(rows), maxRows, rows)
	}
	if strings.TrimSpace(rows[0]) != "任务已完成" {
		t.Fatalf("completion title = %q", rows[0])
	}
	if rows[1] != "" {
		t.Fatalf("completion spacing = %q", rows[1])
	}
	if strings.TrimSpace(rows[2]) != "WorkBuddy · even-app" {
		t.Fatalf("completion identity = %q", rows[2])
	}
	if strings.TrimSpace(rows[3]) != "用时 3:00" || strings.TrimSpace(rows[4]) != "2 个文件 · +28 / -6" {
		t.Fatalf("completion summary = %q", rows[3])
	}
	if strings.Contains(strings.Join(rows, "\n"), "任务结束") {
		t.Fatalf("completion view retained the running-style final action: %v", rows)
	}
}

func TestCompletionRowsAreCenteredInTextColumn(t *testing.T) {
	for _, text := range []string{"任务已完成", "WorkBuddy · even-app", "2 个文件 · +28 / -6"} {
		got := completionRow(text)
		content := strings.TrimLeft(got, " ")
		left := textUnits(got) - textUnits(content) - textUnits(completionIndent)
		right := completionUnits - textUnits(content) - left
		if difference := left - right; difference < -characterUnits(' ') || difference > characterUnits(' ') {
			t.Fatalf("completionRow(%q) is not centered: left=%d right=%d row=%q", text, left, right, got)
		}
	}
}

func TestToolActivityDescriptions(t *testing.T) {
	for _, tc := range []struct {
		tool, target string
		completed    bool
		want         string
	}{
		{"Read", "agentview.go", true, "已读取 agentview.go"},
		{"Grep", "sessionPrompt", false, "正在搜索 sessionPrompt"},
		{"Bash", "rg sessionPrompt agentview.go", false, "正在搜索 sessionPrompt agentview.go"},
		{"Bash", "cat AGENTS.md", true, "已读取文件 AGENTS.md"},
		{"ToolSearch", "google drive", true, "已加载工具 google drive"},
	} {
		if got := describeToolActivity(tc.tool, tc.target, tc.completed); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestLatestToolActivityReplacesEarlierCommentary(t *testing.T) {
	steps := []agentStreamStep{{Kind: "say", Text: "我先查看文件"}, {Kind: "tool", Tool: "Read", Text: "agentview.go", Status: "ok"}}
	if got := focusRow(agentStateRecord{State: "thinking"}, steps); got != lens.Live+"已读取 agentview.go" {
		t.Fatalf("activity = %q", got)
	}
}
