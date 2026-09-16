package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeRecord(t *testing.T, dir string, session string, state string, ts time.Time) {
	t.Helper()
	record := agentStateRecord{
		State:     state,
		SessionID: session,
		Project:   "even-app",
		Source:    "workbuddy",
		Timestamp: float64(ts.UnixNano()) / float64(time.Second),
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// waitEvent blocks until the monitor emits a state change, or fails.
// onState callbacks are dispatched from their own goroutine on purpose so a
// slow glasses write can never stall the refresh loop.
func waitEvent(t *testing.T, events <-chan agentStateRecord) agentStateRecord {
	t.Helper()
	select {
	case record := <-events:
		return record
	case <-time.After(time.Second):
		t.Fatal("no state transition emitted within 1s")
		return agentStateRecord{}
	}
}

// TestStatusMonitorMirrorsStateChanges covers the transition, aggregation and
// staleness rules that decide what reaches the glasses.
func TestStatusMonitorMirrorsStateChanges(t *testing.T) {
	dir := t.TempDir()
	source := statusSource{Agent: "workbuddy", Label: "WorkBuddy", Dir: dir}

	events := make(chan agentStateRecord, 8)
	monitor := newStatusMonitor([]statusSource{source}, func(_ statusSource, record agentStateRecord) {
		events <- record
	})

	// Missing directory: nothing available.
	missing := newStatusMonitor([]statusSource{{Agent: "codex", Label: "Codex", Dir: filepath.Join(dir, "nope")}}, nil)
	missing.refresh()
	if status := missing.Status(); status.Available || status.Message != "未安装状态 hooks" {
		t.Fatalf("missing dir status = %+v", status)
	}

	// First pass only primes the cache, it must not emit.
	writeRecord(t, dir, "s1", "thinking", time.Now())
	monitor.refresh()
	if len(events) != 0 {
		t.Fatalf("first refresh emitted %d events, want 0", len(events))
	}
	if status := monitor.Status(); !status.Available || !strings.Contains(status.Message, "WorkBuddy 1 个活跃任务") {
		t.Fatalf("active status = %+v", status)
	}

	// Same state again: still silent.
	monitor.refresh()
	if len(events) != 0 {
		t.Fatalf("unchanged state emitted %d events, want 0", len(events))
	}

	// Real transition: tool -> permission -> done.
	writeRecord(t, dir, "s1", "permission", time.Now())
	monitor.refresh()
	if record := waitEvent(t, events); record.State != "permission" {
		t.Fatalf("emitted state = %q, want permission", record.State)
	}

	writeRecord(t, dir, "s1", "done", time.Now())
	monitor.refresh()
	if record := waitEvent(t, events); record.State != "done" {
		t.Fatalf("emitted state = %q, want done", record.State)
	}
	if status := monitor.Status(); !strings.Contains(status.Message, "当前无活跃任务") {
		t.Fatalf("idle status = %+v", status)
	}

	// Stale files must be ignored entirely.
	writeRecord(t, dir, "s2", "thinking", time.Now().Add(-2*staleAfter))
	monitor.refresh()
	select {
	case record := <-events:
		t.Fatalf("stale record emitted %+v", record)
	default:
	}
	if status := monitor.Status(); !strings.Contains(status.Message, "当前无活跃任务") {
		t.Fatalf("stale status = %+v", status)
	}
}

// writeStream appends settled steps to the sibling stream.d directory that the
// adapter contract puts next to state.d.
func writeStream(t *testing.T, dir string, session string, lines []string) {
	t.Helper()
	streamDir := filepath.Join(filepath.Dir(dir), "stream.d")
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(streamDir, session+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write stream: %v", err)
	}
}

func TestReadStreamTail(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state.d")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := statusSource{Agent: "workbuddy", Label: "WorkBuddy", Dir: stateDir}

	if steps := readStreamTail(source.streamDir(), "missing", 5); steps != nil {
		t.Fatalf("missing stream returned %v, want nil", steps)
	}

	writeStream(t, stateDir, "s1", []string{
		`{"ts":1,"kind":"prompt","text":"one"}`,
		`not json at all`,
		`{"ts":2,"kind":"tool","tool":"Bash","status":"ok","text":"two"}`,
		``,
		`{"ts":3,"kind":"say","text":"three"}`,
	})
	tail := readStreamTail(source.streamDir(), "s1", 2)
	if len(tail) != 2 {
		t.Fatalf("tail = %v, want 2 entries", tail)
	}
	if tail[0].Text != "two" || tail[1].Text != "three" {
		t.Fatalf("tail kept the wrong end: %+v", tail)
	}
	if tail[1].Kind != "say" {
		t.Fatalf("kind not decoded: %+v", tail[1])
	}
}

func TestReadStreamViewKeepsPromptBeyondActionTail(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state.d")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := []string{`{"ts":1,"kind":"prompt","text":"请保留这个完整问题"}`}
	for index := 0; index < 20; index++ {
		lines = append(lines, fmt.Sprintf(`{"ts":%d,"kind":"tool","tool":"Read","text":"file-%d.go"}`, index+2, index))
	}
	writeStream(t, stateDir, "long", lines)

	view := readStreamView(filepath.Join(dir, "stream.d"), "long", 3)
	if len(view) != 4 || view[0].Kind != "prompt" || view[0].Text != "请保留这个完整问题" {
		t.Fatalf("stream view lost prompt: %+v", view)
	}
	if view[len(view)-1].Text != "file-19.go" {
		t.Fatalf("stream view lost latest action: %+v", view)
	}
}

// TestStatusMonitorPublishesGlassesRows covers the whole read path the device
// depends on: state.d + stream.d on disk become the exact list that is pushed.
func TestStatusMonitorPublishesGlassesRows(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state.d")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := statusSource{Agent: "workbuddy", Label: "WorkBuddy", Dir: stateDir}

	writeRecord(t, stateDir, "s1", "tool", time.Now())
	writeStream(t, stateDir, "s1", []string{
		`{"ts":1,"kind":"prompt","text":"把改动文件推给眼镜"}`,
		`{"ts":2,"kind":"tool","tool":"Write","status":"ok","text":"statusmonitor.go","file":"statusmonitor.go"}`,
	})

	// The live indicator and the ledger come from the state record, so patch
	// them in the way the adapter would.
	record := readStateRecordForTest(t, stateDir, "s1")
	record.Current = "go test ./..."
	record.CurrentTool = "Bash"
	record.FileCount = 2
	record.Additions = 597
	record.Deletions = 38
	writeRecordValue(t, stateDir, "s1", record)

	monitor := newStatusMonitor([]statusSource{source}, nil)
	monitor.refresh()

	rows := monitor.Rows()
	if len(rows) < 4 {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0] != ">_ WorkBuddy · even-app" {
		t.Fatalf("header = %q", rows[0])
	}
	if rows[1] != "▷ 把改动文件推给眼镜" {
		t.Fatalf("status row = %q", rows[1])
	}
	if !strings.HasPrefix(rows[2], "● RUNNING  ·  --:--") {
		t.Fatalf("status row = %q", rows[2])
	}
	if rows[3] != lens.Live+"> go test ./..." {
		t.Fatalf("current row = %q", rows[3])
	}
	if !strings.HasPrefix(rows[5], lens.Step+"statusmonitor.go") || !strings.HasSuffix(rows[5], "■ M") {
		t.Fatalf("recent row = %q", rows[5])
	}
	if status := monitor.Status(); len(status.Rows) != len(rows) {
		t.Fatalf("Status().Rows = %v, want the same list", status.Rows)
	}
}

// TestStatusMonitorAdvancesTheElapsedClockEverySecond keeps the timer visibly
// moving while work is active.
//
// The composed rows carry an m:ss timer, so while an agent works they differ
// every second. The clock is injected rather than slept through, so the
// assertions are exact and the test does not block.
func TestStatusMonitorAdvancesTheElapsedClockEverySecond(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state.d")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := statusSource{Agent: "codex", Label: "Codex", Dir: stateDir}

	now := time.Unix(1700000000, 0)
	record := agentStateRecord{
		State:     "thinking",
		SessionID: "continuous",
		Project:   "even-app",
		StartedAt: float64(now.Add(-time.Minute).UnixNano()) / float64(time.Second),
		Timestamp: float64(now.UnixNano()) / float64(time.Second),
	}
	writeRecordValue(t, stateDir, "continuous", record)

	type view struct {
		rows    []string
		animate bool
	}
	views := []view{}
	monitor := newStatusMonitor([]statusSource{source}, nil)
	monitor.now = func() time.Time { return now }
	monitor.onView = func(rows []string, animate bool, _, _ string) {
		views = append(views, view{rows: append([]string(nil), rows...), animate: animate})
	}

	monitor.refresh()
	if len(views) != 1 {
		t.Fatalf("published %d views on the first pass, want 1", len(views))
	}
	firstStatus := monitor.Status()

	// A clock-only tick is published so the lens never looks frozen.
	now = now.Add(time.Second)
	monitor.refresh()
	if len(views) != 2 {
		t.Fatalf("published %d views after a clock-only tick, want 2", len(views))
	}
	if status := monitor.Status(); slices.Equal(status.Rows, firstStatus.Rows) {
		t.Fatalf("elapsed rows did not advance: %v", status.Rows)
	}
	if views[0].rows[1] == views[1].rows[1] {
		t.Fatalf("elapsed status did not advance: %q", views[0].rows[1])
	}
	if views[0].animate || views[1].animate {
		t.Fatalf("clock-only refresh animated: %+v", views)
	}

	// Real work ignores the floor and goes out immediately.
	record.CurrentTool = "Bash"
	record.Current = "go test ./..."
	now = now.Add(time.Second)
	record.Timestamp = float64(now.UnixNano()) / float64(time.Second)
	writeRecordValue(t, stateDir, "continuous", record)
	monitor.refresh()
	if len(views) != 3 || !views[2].animate {
		t.Fatalf("new action did not animate: %+v", views)
	}
}

func readStateRecordForTest(t *testing.T, dir, session string) agentStateRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, session+".json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var record agentStateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return record
}

func writeRecordValue(t *testing.T, dir, session string, record agentStateRecord) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLiveSessionPreview is opt-in and exists so the composed list can be
// eyeballed against real artifacts without a device:
//
//	EVEN_STATUS_DIR=~/.workbuddy/statusbar go test -run TestLiveSessionPreview -v .
func TestLiveSessionPreview(t *testing.T) {
	root := os.Getenv("EVEN_STATUS_DIR")
	if root == "" {
		t.Skip("set EVEN_STATUS_DIR to preview a live session")
	}

	source := statusSource{Agent: "workbuddy", Label: "WorkBuddy", Dir: filepath.Join(root, "state.d")}
	records, ok := readStatusDir(source.Dir)
	if !ok {
		t.Fatalf("no state.d under %s", root)
	}
	// The monitor picks the session that moved last; mirror that here.
	var newest agentStateRecord
	for _, record := range records {
		if record.Timestamp > newest.Timestamp {
			newest = record
		}
	}
	if newest.SessionID == "" {
		t.Fatal("no session records found")
	}

	steps := readStreamView(source.streamDir(), newest.SessionID, streamReadLimit)
	rows := composeAgentRows(source, newest, steps, time.Now())
	t.Logf("session %s · state=%s · %d settled steps on disk", newest.SessionID, newest.State, len(steps))
	for i, row := range rows {
		t.Logf("  [%d] %s", i, row)
	}
}
