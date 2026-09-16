package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// staleAfter bounds how long a hook state file may go untouched before the
// session is treated as idle. It guards against agents that die without
// emitting their Stop / SessionEnd event.
const staleAfter = 12 * time.Hour

// statusPollInterval keeps hook-to-lens latency below a perceptible beat. Hook
// adapters write tiny local JSON files atomically enough for the reader below,
// so polling four times a second is cheap and avoids waiting a full second
// before a real state or tool transition enters the BLE queue.
const statusPollInterval = 250 * time.Millisecond

// clockPushFloor is the shortest gap allowed between two pushes whose only
// difference is the elapsed clock.
//
// The composed rows carry an m:ss timer. Publish it once per second so elapsed
// time advances continuously instead of appearing frozen and then jumping.
// The latest-value display queue still collapses updates if BLE falls behind.
const clockPushFloor = time.Second

// agentStateRecord is the on-disk hook state written by every agent adapter
// (scripts/even-codex-status-writer.mjs, scripts/even-workbuddy-status-writer.mjs).
//
// The first block is the original state snapshot. Everything after it is the
// roll-up the WorkBuddy adapter added so the glasses header can describe the
// session without reading the step stream ("3 步 · 已改 2 文件").
type agentStateRecord struct {
	State      string  `json:"state"`
	ThreadName string  `json:"threadName"`
	Project    string  `json:"project"`
	SessionID  string  `json:"sessionId"`
	Source     string  `json:"source"`
	Timestamp  float64 `json:"ts"`

	StepCount   int     `json:"stepCount"`
	ChangeCount int     `json:"changeCount"`
	StartedAt   float64 `json:"startedAt"`
	Title       string  `json:"title"`
	Current     string  `json:"current"`
	CurrentTool string  `json:"currentTool"`
	FileCount   int     `json:"fileCount"`
	Additions   int     `json:"additions"`
	Deletions   int     `json:"deletions"`
}

// agentStreamStep is one line of stream.d/<sessionId>.jsonl: a settled unit of
// work, in the same order the desktop renders it.
//
// Kind is one of prompt / say / tool / ask / note / done. Only tool steps carry
// Tool and the file fields.
type agentStreamStep struct {
	Timestamp float64 `json:"ts"`
	Kind      string  `json:"kind"`
	Text      string  `json:"text"`
	Tool      string  `json:"tool"`
	Status    string  `json:"status"`
	Action    string  `json:"action"`
	File      string  `json:"file"`
	Additions int     `json:"add"`
	Deletions int     `json:"del"`
}

// statusSource is one agent whose hook state directory is being watched.
type statusSource struct {
	Agent string // machine key, e.g. "codex" / "workbuddy"
	Label string // display label, e.g. "Codex" / "WorkBuddy"
	Dir   string // absolute path of the state.d directory
}

// streamDir resolves the sibling stream.d directory. The layout is fixed by the
// adapter contract: <statusbar>/{state.d,stream.d,files.d}.
func (s statusSource) streamDir() string {
	return filepath.Join(filepath.Dir(s.Dir), "stream.d")
}

// CodexStatus describes the hook-backed desktop status bridge.
//
// The name is kept for backwards compatibility with the generated Wails
// bindings: it now aggregates every configured agent source.
type CodexStatus struct {
	Available bool   `json:"available"`
	Message   string `json:"message"`
	// State is the raw hook state of the session that currently owns the
	// glasses ("thinking", "needs_input", ...). The desktop uses it to show the
	// same loading sweep the lens is showing, so the preview carries the motion
	// and not only the text.
	State string `json:"state"`
	// Rows is exactly what the glasses are showing: the composed list for the
	// most recently active session. The desktop panel renders it verbatim so
	// the content can be reviewed without wearing the device.
	Rows []string `json:"rows"`
}

type statusMonitor struct {
	mu            sync.Mutex
	sources       []statusSource
	states        map[string]string
	started       bool
	status        CodexStatus
	onState       func(statusSource, agentStateRecord)
	onView        func([]string, bool, string, string)
	composedKey   string
	composedSteps []agentStreamStep
	hiddenKey     string
	viewKey       string
	viewState     string
	viewAction    string
	// lastPush is when rows were last handed to onView. It is what the elapsed
	// clock is throttled against; see clockPushFloor.
	lastPush time.Time
	// now is the clock refresh reads. Making it a field lets a test advance
	// time in steps instead of sleeping through them, which keeps the elapsed
	// clock's behaviour testable without a test that blocks.
	now func() time.Time
}

// clock returns the monitor's time source, falling back to the wall clock so a
// monitor built without one still refreshes.
func (m *statusMonitor) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

func newStatusMonitor(sources []statusSource, onState func(statusSource, agentStateRecord)) *statusMonitor {
	return &statusMonitor{sources: sources, states: map[string]string{}, onState: onState, now: time.Now}
}

func (m *statusMonitor) Start() {
	go func() {
		ticker := time.NewTicker(statusPollInterval)
		defer ticker.Stop()
		for {
			m.refresh()
			<-ticker.C
		}
	}()
}

func (m *statusMonitor) Status() CodexStatus { m.mu.Lock(); defer m.mu.Unlock(); return m.status }

// Rows returns the composed glasses list for the most recently active session.
func (m *statusMonitor) Rows() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status.Rows
}

// ClearRows keeps the desktop preview aligned with an expired glasses display.
func (m *statusMonitor) ClearRows() {
	m.mu.Lock()
	m.status.Rows = nil
	m.hiddenKey = m.composedKey
	m.mu.Unlock()
}

func (m *statusMonitor) refresh() {
	type transition struct {
		source statusSource
		record agentStateRecord
	}

	now := m.clock()
	transitions := []transition{}
	summaries := []string{}
	present := []string{}
	available := false

	// The session that moved most recently owns the glasses. With several
	// agents running, "what did I just touch" is the only sensible single view.
	var newestSource statusSource
	var newestRecord agentStateRecord
	hasNewest := false

	m.mu.Lock()
	first := !m.started
	m.started = true

	for _, source := range m.sources {
		records, ok := readStatusDir(source.Dir)
		if !ok {
			continue
		}
		available = true
		present = append(present, source.Label)

		active := 0
		for _, record := range records {
			if record.Timestamp > 0 && now.Sub(time.Unix(0, int64(record.Timestamp*float64(time.Second)))) > staleAfter {
				continue
			}
			key := source.Agent + ":" + record.SessionID
			previous := m.states[key]
			m.states[key] = record.State
			if record.State != "done" && record.State != "idle" {
				active++
			}
			if !hasNewest || record.Timestamp > newestRecord.Timestamp {
				newestSource, newestRecord, hasNewest = source, record, true
			}
			if !first && previous != "" && previous != record.State && m.onState != nil {
				transitions = append(transitions, transition{source, record})
			}
		}
		if active > 0 {
			summaries = append(summaries, fmt.Sprintf("%s %d 个活跃任务", source.Label, active))
		}
	}

	var rows []string
	viewKey := ""
	viewState := ""
	viewAction := ""
	viewIcon := ""
	animate := false
	streamChanged := false
	if hasNewest {
		key := fmt.Sprintf("%s:%s:%s:%d:%d:%f", newestSource.Agent, newestRecord.SessionID, newestRecord.State, newestRecord.StepCount, newestRecord.ChangeCount, newestRecord.StartedAt)
		if key != m.composedKey {
			m.composedSteps = readStreamView(newestSource.streamDir(), newestRecord.SessionID, streamReadLimit)
			m.composedKey = key
			streamChanged = true
		}
		viewKey = newestSource.Agent + ":" + newestRecord.SessionID
		viewState = newestRecord.State
		viewAction = fmt.Sprintf("%s:%s:%d", newestRecord.CurrentTool, newestRecord.Current, newestRecord.StepCount)
		viewIcon = activityIcon(newestRecord.State, newestRecord.CurrentTool, newestRecord.Current)
		animate = !first && (viewKey != m.viewKey || viewState != m.viewState || viewAction != m.viewAction)
		if key != m.hiddenKey {
			rows = composeAgentRows(newestSource, newestRecord, m.composedSteps, now)
		}
	} else {
		m.composedKey = ""
		m.composedSteps = nil
	}

	// Hand the rows over only when something moved that is worth moving for.
	// Rows are compared against the last published set rather than the last
	// composed one, so a throttle that skips a push does not also swallow the
	// difference — the skipped tick simply stays pending for the next one.
	// Emptying always publishes, which is how a session going away clears the
	// desktop; an empty view never reaches the glasses, which are cleared by
	// their own timer instead.
	meaningful := animate || streamChanged
	due := m.lastPush.IsZero() || now.Sub(m.lastPush) >= clockPushFloor
	publish := !slices.Equal(rows, m.status.Rows) && (len(rows) == 0 || meaningful || due)
	previousRows := m.status.Rows

	switch {
	case !available:
		m.status = CodexStatus{Message: "未安装状态 hooks"}
	case len(summaries) == 0:
		m.status = CodexStatus{Available: true, Message: "正在监测 " + strings.Join(present, " / ") + "：当前无活跃任务"}
	default:
		m.status = CodexStatus{Available: true, Message: "正在监测 " + strings.Join(summaries, " · ")}
	}
	m.status.State = viewState
	if publish {
		m.status.Rows = rows
		m.lastPush = now
	} else {
		// The switch above rebuilds the status from scratch, so a held-back
		// push has to carry the rows the lens is still showing back across — and
		// they have to be the last *published* ones, or the desktop preview
		// would drift ahead of a lens that has not been told anything yet.
		m.status.Rows = previousRows
	}
	m.viewKey = viewKey
	m.viewState = viewState
	m.viewAction = viewAction
	onView := m.onView
	m.mu.Unlock()
	if publish && onView != nil && len(rows) > 0 {
		onView(rows, animate, viewState, viewIcon)
	}

	for _, item := range transitions {
		go m.onState(item.source, item.record)
	}
}

func readStatusDir(dir string) ([]agentStateRecord, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	records := []agentStateRecord{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var record agentStateRecord
		if json.Unmarshal(data, &record) == nil && record.SessionID != "" {
			records = append(records, record)
		}
	}
	return records, true
}

// readStreamTail returns the newest `limit` settled steps for one session.
//
// The adapter already caps the file (STREAM_LIMIT), so reading it whole keeps
// this simple; a missing file is not an error, it just means the session has
// not settled a step yet. Blank and unparsable lines are dropped *before* the
// tail is taken, so a stray blank line cannot silently shorten the list.
func readStreamTail(dir, sessionID string, limit int) []agentStreamStep {
	data, err := os.ReadFile(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		return nil
	}
	steps := []agentStreamStep{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var step agentStreamStep
		if json.Unmarshal([]byte(line), &step) == nil && step.Kind != "" {
			steps = append(steps, step)
		}
	}
	if len(steps) > limit {
		steps = steps[len(steps)-limit:]
	}
	return steps
}

// readStreamView keeps the latest prompt even after a long run pushes it out
// of the action tail. The hook bounds each stream file to 600 lines.
func readStreamView(dir, sessionID string, actionLimit int) []agentStreamStep {
	steps := readStreamTail(dir, sessionID, 600)
	if len(steps) == 0 {
		return nil
	}
	start := max(0, len(steps)-actionLimit)
	view := append([]agentStreamStep(nil), steps[start:]...)
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].Kind != "prompt" {
			continue
		}
		if index < start {
			view = append([]agentStreamStep{steps[index]}, view...)
		}
		break
	}
	return view
}
