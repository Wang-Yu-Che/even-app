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
	monitor.selected = "workbuddy:s1"
	monitor.refresh()

	rows := monitor.Rows()
	if len(rows) < 4 {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0] != sessionPrompt+"WorkBuddy · even-app" {
		t.Fatalf("header = %q", rows[0])
	}
	if rows[1] != lens.User+"把改动文件推给眼镜" {
		t.Fatalf("status row = %q", rows[1])
	}
	if !strings.HasPrefix(rows[2], lens.Header+"●  正在执行  ·  --:--") {
		t.Fatalf("status row = %q", rows[2])
	}
	if rows[3] != lens.Live+"正在运行命令 go test ./..." {
		t.Fatalf("activity summary = %q", rows[3])
	}
	if rows[4] != dividerRow {
		t.Fatalf("activity divider = %q", rows[3])
	}
	if !strings.HasPrefix(rows[5], lens.Step+"statusmonitor.go") || !strings.HasSuffix(rows[5], "● M") {
		t.Fatalf("recent row = %q", rows[4])
	}
	if rows[len(rows)-1] != commandPrompt+"go test ./..." {
		t.Fatalf("current command is not the last row: %v", rows)
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
	monitor.selected = "codex:continuous"
	monitor.now = func() time.Time { return now }
	monitor.onView = func(rows []string, animate bool, _, _ string, _ []string) {
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

func TestSessionListNavigation(t *testing.T) {
	dir := t.TempDir()
	source := statusSource{Agent: "codex", Label: "Codex", Dir: dir}
	now := time.Now()
	for i := 0; i < 7; i++ {
		writeRecord(t, dir, fmt.Sprintf("s%d", i), "thinking", now)
	}
	monitor := newStatusMonitor([]statusSource{source}, nil)
	monitor.refresh()
	monitor.selectListItem("project:even-app")
	if monitor.Status().State != "sessions" || len(monitor.Rows()) != 7 {
		t.Fatalf("list: %+v", monitor.Status())
	}
	monitor.selectListItem("codex:s5")
	if monitor.selected != "codex:s5" || monitor.Status().State != "thinking" {
		t.Fatalf("selection: %+v", monitor.Status())
	}
	writeRecord(t, dir, "s0", "permission", now.Add(time.Second))
	monitor.refresh()
	if monitor.selected != "codex:s5" || monitor.Status().State != "thinking" {
		t.Fatal("other session stole detail")
	}
	monitor.handleInput(3)
	if monitor.Status().State != "sessions" {
		t.Fatal("back lost list focus")
	}
	monitor.selectListItem("codex:s5")
	if err := os.Remove(filepath.Join(dir, "s5.json")); err != nil {
		t.Fatal(err)
	}
	monitor.refresh()
	if monitor.selected != "" || monitor.Status().State != "sessions" {
		t.Fatal("missing selection did not return to list")
	}
}

func TestClosedMenuRetainsSessionsAndReportsCompletion(t *testing.T) {
	dir := t.TempDir()
	source := statusSource{Agent: "codex", Label: "Codex", Dir: dir}
	events := make(chan agentStateRecord, 8)
	monitor := newStatusMonitor([]statusSource{source}, func(_ statusSource, record agentStateRecord) { events <- record })
	monitor.dismissed = true
	pushes := 0
	monitor.onView = func(_ []string, _ bool, _, _ string, _ []string) { pushes++ }
	writeRecord(t, dir, "old", "done", time.Now().Add(-2*staleAfter))
	writeRecord(t, dir, "current", "thinking", time.Now())
	monitor.refresh()
	if len(monitor.sessions) != 2 || pushes != 0 || len(events) != 0 {
		t.Fatal("startup must retain records without displaying or notifying")
	}
	writeRecord(t, dir, "current", "done", time.Now())
	monitor.refresh()
	if got := waitEvent(t, events); got.SessionID != "current" || got.State != "done" {
		t.Fatalf("completion: %+v", got)
	}
	monitor.refresh()
	if pushes != 0 || len(events) != 0 {
		t.Fatal("closed menu pushed a page or repeated completion")
	}
	monitor.openSessionList()
	monitor.selectListItem("project:even-app")
	if pushes == 0 || len(monitor.Rows()) != 2 {
		t.Fatal("menu did not expose retained sessions")
	}
}

func TestMenuOpensSessionList(t *testing.T) {
	dir := t.TempDir()
	writeRecord(t, dir, "s1", "thinking", time.Now())
	monitor := newStatusMonitor([]statusSource{{Agent: "codex", Label: "Codex", Dir: dir}}, nil)
	var pushedState string
	monitor.onView = func(_ []string, _ bool, state, _ string, _ []string) {
		pushedState = state
	}
	monitor.refresh()
	monitor.selectListItem("project:even-app")
	monitor.selectListItem("codex:s1")
	monitor.dismissDisplay()
	monitor.listOffset = nativeListPageSize
	monitor.openSessionList()
	if monitor.displayDismissed() || monitor.selected != "" || monitor.project != "" || monitor.listOffset != 0 {
		t.Fatal("menu did not reset navigation")
	}
	if monitor.Status().State != "projects" || len(monitor.Rows()) == 0 || pushedState != "projects" {
		t.Fatalf("menu did not publish project list: %+v", monitor.Status())
	}
	pushedState = ""
	monitor.openSessionList()
	if pushedState != "projects" {
		t.Fatal("reopening menu did not republish list")
	}
}

func TestDisplayingSessionRequiresVisibleMatchingDetail(t *testing.T) {
	source := statusSource{Agent: "codex"}
	record := agentStateRecord{SessionID: "s1"}
	monitor := newStatusMonitor(nil, nil)
	monitor.selected = "codex:s1"
	if !monitor.displayingSession(source, record) {
		t.Fatal("visible selected session was not recognized")
	}
	monitor.dismissed = true
	if monitor.displayingSession(source, record) {
		t.Fatal("dismissed session was treated as visible")
	}
	monitor.dismissed = false
	if monitor.displayingSession(source, agentStateRecord{SessionID: "s2"}) {
		t.Fatal("different session was treated as visible")
	}
}

func TestNativeSessionListPagingAndDismiss(t *testing.T) {
	dir := t.TempDir()
	source := statusSource{Agent: "codex", Label: "Codex", Dir: dir}
	for i := 0; i < 25; i++ {
		writeRecord(t, dir, fmt.Sprintf("s%02d", i), "thinking", time.Now())
	}
	monitor := newStatusMonitor([]statusSource{source}, nil)
	var keys []string
	monitor.onView = func(_ []string, _ bool, _, _ string, listKeys []string) { keys = listKeys }
	monitor.refresh()
	monitor.selectListItem("project:even-app")
	if len(keys) != nativeListPageSize+1 || keys[nativeListPageSize] != "next" {
		t.Fatalf("first page: %v", keys)
	}
	monitor.selectListItem(keys[nativeListPageSize])
	if len(keys) != nativeListPageSize+2 || keys[0] != "previous" || keys[1] != "codex:s18" {
		t.Fatalf("second page: %v", keys)
	}
	monitor.selectListItem(keys[1])
	if monitor.selected != "codex:s18" {
		t.Fatalf("selected %s", monitor.selected)
	}
	monitor.handleInput(3)
	monitor.dismissDisplay()
	writeRecord(t, dir, "new", "tool", time.Now())
	monitor.refresh()
	if len(monitor.Rows()) != 0 || !monitor.displayDismissed() {
		t.Fatal("dismissed page reappeared")
	}
	monitor.resumeDisplay()
	if len(monitor.Rows()) == 0 || monitor.displayDismissed() {
		t.Fatal("resume failed")
	}
}

func TestProjectFoldersAndSessionTitles(t *testing.T) {
	dir := t.TempDir()
	source := statusSource{Agent: "codex", Label: "Codex", Dir: dir}
	now := time.Now()
	for _, record := range []agentStateRecord{
		{SessionID: "a", Project: "even-app", Title: "优化眼镜列表", ThreadName: "old-id", State: "thinking"},
		{SessionID: "b", Project: "even-app", State: "done"},
		{SessionID: "c", Project: "other", Title: "更新文档", State: "done"},
	} {
		record.Timestamp = float64(now.Unix())
		writeRecordValue(t, dir, record.SessionID, record)
	}
	writeStream(t, dir, "b", []string{`{"kind":"prompt","text":"修复蓝牙连接问题"}`})
	monitor := newStatusMonitor([]statusSource{source}, nil)
	var keys []string
	monitor.onView = func(_ []string, _ bool, _, _ string, next []string) { keys = next }
	monitor.refresh()
	if monitor.Status().State != "projects" || len(keys) != 2 || keys[0] != "project:even-app" {
		t.Fatalf("projects: %v %v", monitor.Rows(), keys)
	}
	if !strings.HasPrefix(monitor.Rows()[0], "▶ even-app") || !strings.HasSuffix(monitor.Rows()[0], "●") || monitor.Rows()[1] != "▶ other" {
		t.Fatalf("activity dots: %v", monitor.Rows())
	}
	monitor.selectListItem(keys[0])
	if len(keys) != 2 || keys[0] != "codex:a" || keys[1] != "codex:b" {
		t.Fatalf("project sessions: %v", keys)
	}
	if !strings.HasPrefix(monitor.Rows()[0], "优化眼镜列表") || !strings.HasPrefix(monitor.Rows()[1], "修复蓝牙连") {
		t.Fatalf("titles: %v", monitor.Rows())
	}
	monitor.selectListItem(keys[0])
	monitor.handleInput(3)
	if monitor.Status().State != "sessions" || monitor.project != "even-app" {
		t.Fatal("detail back did not keep project")
	}
	monitor.handleInput(3)
	if monitor.Status().State != "projects" || monitor.project != "" {
		t.Fatal("folder back did not reach projects")
	}
	record := readStateRecordForTest(t, dir, "a")
	record.State = "done"
	writeRecordValue(t, dir, "a", record)
	monitor.refresh()
	if strings.HasSuffix(monitor.Rows()[0], "●") {
		t.Fatal("finished project still marked active")
	}
	record.State = "permission"
	writeRecordValue(t, dir, "a", record)
	monitor.refresh()
	if !strings.HasSuffix(monitor.Rows()[0], "●") {
		t.Fatal("waiting project not marked active")
	}
}
