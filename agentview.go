package main

import (
	"fmt"
	"strings"
	"time"
)

// The glasses list is composed here and nowhere else.
//
// A G2 native list row is one unstyled string: the firmware has no colour,
// icon, font size or alignment control, and it renders an empty row as "·".
// That leaves exactly three style decisions, and this file owns all three:
//
//  1. which glyph opens a row  -> what kind of thing it is, read before the text
//  2. how wide a row may be    -> counted in the firmware's own glyph units
//  3. how many rows fit        -> how much history survives
//
// The layout follows the Codex CLI reading order on purpose: session, prompt,
// state, live command, then recent output. Stable positions let a glance work
// without scanning the whole lens.
//
// CALIBRATION — how wide a row may be and how many rows fit are both unknown.
//
// The firmware receives each row as an opaque string and clips it at whatever
// pixel width its font dictates. Nothing pins that down: OpenEvenSdk wraps
// teleprompter prose at 25 characters, but that is a reading measure for a
// paragraph, not the width of the lens — and the SDK's own notes say CJK column
// width has no protocol guarantee at all. So the two numbers below are *our*
// cap, chosen so the rows we care about fit, and LensCalibration() is how they
// get replaced by measured ones. Everything that depends on them is these
// constants.
const (
	// asciiUnits and cjkUnits are how the row budget counts glyphs: on this lens
	// a CJK glyph is about twice as wide as an ASCII one.
	asciiUnits = 10
	cjkUnits   = 21
	// The 536px container has 10px padding and a 1px border on each side,
	// leaving 514px. Keep a small margin for firmware/font differences.
	lensUnits = 500
	// maxRows is how many rows we push. Eight rows across 288 px is about 36 px
	// per row, which is the constraint that decides this.
	maxRows = 8
	// streamReadLimit is how many settled steps are read per refresh. It is
	// deliberately larger than the row budget so the composer can drop entries
	// that would only repeat the live line and still fill the screen.
	streamReadLimit       = 12
	completionUnits       = 360
	completionInnerPixels = 364
)

// lensStyle is the entire glyph vocabulary of the display. Nothing else in the
// codebase spells a row marker, so re-skinning the glasses is a one-value
// change.
type lensStyle struct {
	Header string // shell-like session prompt
	Live   string // an in-flight tool call
	Wait   string // blocked on the human — the row you would act on
	Fail   string // a step that failed
	Step   string // a settled tool call
	Say    string // the agent explaining itself
	User   string // what you asked for
	Ledger string // the file-change roll-up
	// Ellipsis clips a row that does not fit. It lives in the vocabulary
	// because firmware glyph coverage is unverified.
	Ellipsis string
}

// lens is the active vocabulary.
//
// Markers come from the G2 design guide's supported navigation and selection
// glyphs. Their shapes carry hierarchy without repeating field labels.
var lens = lensStyle{
	Header:   ">_ ",
	Live:     "▶ ",
	Wait:     "□ ",
	Fail:     "× ",
	Step:     "✓ ",
	Say:      "● ",
	User:     "▷ ",
	Ledger:   " changes",
	Ellipsis: "…",
}

// composeAgentRows renders one compact terminal transcript for a session.
func composeAgentRows(source statusSource, record agentStateRecord, steps []agentStreamStep, now time.Time) []string {
	if record.State == "done" {
		return composeCompletionRows(source, record, now)
	}
	rows := []string{headerRow(source, record, now)}
	if goal := goalRow(steps); goal != "" {
		rows = append(rows, goal)
	}
	rows = append(rows, statusSummaryRow(record, now))
	rows = append(rows, focusRow(record, steps))
	activity := activityRows(steps, record, 3)
	if len(activity) > 0 {
		rows = append(rows, "  ────────────────────")
		rows = append(rows, activity...)
	}
	return rows
}

func composeCompletionRows(source statusSource, record agentStateRecord, now time.Time) []string {
	name := record.Project
	if name == "" {
		name = record.ThreadName
	}
	if name == "" {
		name = source.Label
	}
	elapsed := strings.TrimPrefix(elapsedRow(record, now), "时间  ")
	rows := []string{
		centerCompletionRow("任务已完成"),
		centerCompletionRow("────────"),
		centerCompletionRow(source.Label + " · " + name),
	}
	summary := "用时 " + elapsed
	if record.FileCount > 0 {
		summary += fmt.Sprintf(" · %dF +%d -%d", record.FileCount, record.Additions, record.Deletions)
	} else if record.StepCount > 0 {
		summary += fmt.Sprintf(" · %d 步", record.StepCount)
	}
	return append(rows, centerCompletionRow(summary))
}

func centerCompletionRow(text string) string {
	text = truncateUnits(text, completionUnits)
	padding := max(0, (completionInnerPixels-completionTextPixels(text))/(2*5))
	return strings.Repeat(" ", padding) + text
}

// completionTextPixels mirrors the useful advances from @evenrealities/pretext
// for the small completion-page vocabulary. The firmware font uses 5px spaces,
// 20px CJK glyphs and proportional Latin glyphs; treating them as terminal
// columns is what made the old result visibly lean left.
func completionTextPixels(text string) int {
	width := 0
	for _, character := range text {
		switch {
		case character == ' ' || strings.ContainsRune(".,:;'`·", character):
			width += 5
		case strings.ContainsRune("ilI|!", character):
			width += 4
		case strings.ContainsRune("mwMW@", character):
			width += 16
		case character <= 0x7F:
			width += 11
		default:
			width += 20
		}
	}
	return width
}

// headerRow is the always-present session identity.
func headerRow(source statusSource, record agentStateRecord, now time.Time) string {
	name := record.Project
	if name == "" {
		name = record.ThreadName
	}
	if name == "" {
		name = source.Label
	}

	// Keep the final 60 units clear for the top-right toolkit status badge.
	return truncateUnits(lens.Header+source.Label+" · "+name, lensUnits-60)
}

func statusSummaryRow(record agentStateRecord, now time.Time) string {
	status := map[string]string{
		"thinking":    "THINKING",
		"tool":        "RUNNING",
		"needs_input": "INPUT",
		"permission":  "PERMISSION",
		"compacting":  "COMPACTING",
		"idle":        "IDLE",
		"done":        "DONE",
	}[record.State]
	if status == "" {
		status = "UNKNOWN"
	}
	elapsed := strings.TrimPrefix(elapsedRow(record, now), "时间  ")
	prefix := "○"
	if isWorkingState(record.State) {
		prefix = "●"
	} else if record.State == "done" {
		prefix = "✓"
	} else if record.State == "needs_input" || record.State == "permission" {
		prefix = "□"
	}
	summary := prefix + " " + status + "  ·  " + elapsed
	if record.FileCount > 0 {
		summary += fmt.Sprintf("  ·  %dF +%d -%d", record.FileCount, record.Additions, record.Deletions)
	} else if record.ChangeCount > 0 {
		summary += fmt.Sprintf("  ·  %02d%s", record.ChangeCount, lens.Ledger)
	}
	return row("  ", summary)
}

func focusRow(record agentStateRecord, steps []agentStreamStep) string {
	if record.State == "needs_input" || record.State == "permission" {
		for index := len(steps) - 1; index >= 0; index-- {
			if steps[index].Kind == "ask" {
				return row(lens.Wait, stepBody(steps[index]))
			}
		}
	}
	return currentRow(record)
}

func goalRow(steps []agentStreamStep) string {
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].Kind == "prompt" {
			return row(lens.User, stepBody(steps[index]))
		}
	}
	return ""
}

func elapsedRow(record agentStateRecord, now time.Time) string {
	if record.StartedAt <= 0 {
		return row("时间  ", "--:--")
	}
	started := time.Unix(0, int64(record.StartedAt*float64(time.Second)))
	end := now
	if (record.State == "done" || record.State == "idle") && record.Timestamp > 0 {
		end = time.Unix(0, int64(record.Timestamp*float64(time.Second)))
	}
	elapsed := end.Sub(started)
	if elapsed < 0 {
		elapsed = 0
	}
	return row("时间  ", formatElapsed(elapsed))
}

func currentRow(record agentStateRecord) string {
	current := ""
	if record.Current != "" {
		current = toolAction(record.CurrentTool, record.Current)
	} else {
		current = map[string]string{
			"thinking":    "分析任务",
			"tool":        "执行任务",
			"needs_input": "等待你的回答",
			"permission":  "等待你的授权",
			"compacting":  "整理上下文",
			"idle":        "暂无操作",
			"done":        "任务结束",
		}[record.State]
	}
	if current == "" {
		current = "暂无信息"
	}
	return row(lens.Live, current)
}

func questionRows(steps []agentStreamStep, limit int) []string {
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].Kind == "prompt" {
			return wrapRows(lens.User, stepBody(steps[index]), limit)
		}
	}
	return nil
}

func activityRows(steps []agentStreamStep, record agentStateRecord, limit int) []string {
	live := ""
	if record.Current != "" {
		live = toolAction(record.CurrentTool, record.Current)
	}
	rows := []string{}
	for index := len(steps) - 1; index >= 0; index-- {
		if len(rows) >= limit {
			break
		}
		if steps[index].Kind == "prompt" || steps[index].Kind == "say" || steps[index].Kind == "ask" {
			continue
		}
		body := stepBody(steps[index])
		if body != "" && body != live {
			rows = append(rows, activityStepRow(steps[index], body))
		}
	}
	// Codex prints its transcript from old to new, with the freshest completed
	// action nearest the bottom. We select from the tail above, then restore
	// that same reading order so the two displays can be compared line-for-line.
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	return rows
}

func activityStepRow(step agentStreamStep, body string) string {
	if step.Kind != "tool" || step.File == "" {
		return row(stepMarker(step), body)
	}
	operation := map[string]string{"create": "A", "modify": "M", "delete": "D"}[step.Action]
	if operation == "" {
		operation = "M"
	}
	if step.Additions > 0 || step.Deletions > 0 {
		operation = fmt.Sprintf("%s +%d -%d", operation, step.Additions, step.Deletions)
	}
	return fileChangeRow(stepMarker(step), step.File, operation)
}

func wrapRows(marker, body string, limit int) []string {
	body = strings.TrimSpace(body)
	if body == "" || limit < 1 {
		return nil
	}
	rows := []string{}
	remaining := []rune(body)
	for len(remaining) > 0 && len(rows) < limit {
		prefix := marker
		if len(rows) > 0 {
			prefix = "  "
		}
		if len(rows) == limit-1 {
			rows = append(rows, prefix+truncateUnits(string(remaining), lensUnits-textUnits(prefix)))
			break
		}
		budget := lensUnits - textUnits(prefix)
		used := 0
		end := 0
		for end < len(remaining) {
			width := characterUnits(remaining[end])
			if used+width > budget {
				break
			}
			used += width
			end++
		}
		rows = append(rows, strings.TrimRight(prefix+string(remaining[:end]), " "))
		remaining = remaining[end:]
	}
	return rows
}

func notificationFrame(rows []string, dots int) []string {
	frame := append([]string(nil), rows...)
	if len(frame) == 0 || dots < 1 {
		return frame
	}
	title := strings.TrimPrefix(frame[0], lens.Header)
	frame[0] = truncateUnits(strings.Repeat(".", dots)+" "+title, lensUnits)
	return frame
}

// liveRow answers "what is it doing right now". Settled work lives in the step
// rows below it, so this row only carries the current state — but it is always
// row 1, which is what makes it findable without searching.
func liveRow(record agentStateRecord) string {
	switch record.State {
	case "tool", "thinking":
		body := "思考中…"
		if record.Current != "" {
			body = toolAction(record.CurrentTool, record.Current)
		} else if record.State == "tool" {
			body = "执行中…"
		}
		return row(lens.Live, body)
	case "needs_input":
		return row(lens.Wait, "等你回答")
	case "permission":
		return row(lens.Wait, "等你授权")
	case "compacting":
		return row(lens.Live, "压缩上下文中…")
	case "idle":
		return row(lens.Step, "空闲")
	case "done":
		if record.StepCount > 0 {
			return row(lens.Step, fmt.Sprintf("已完成 · %d 步", record.StepCount))
		}
		return row(lens.Step, "已完成")
	default:
		return ""
	}
}

// stepRows renders the newest settled steps first, so a glance always lands on
// the most recent activity without scrolling.
//
// Two things are folded away, because on a lens a wasted row costs real
// information: the step the live line is already showing (only the first row is
// checked — a deeper repeat is real history), and a run of identical steps,
// which becomes "· 编辑 App.tsx ×3" instead of three rows saying the same thing.
func stepRows(steps []agentStreamStep, record agentStateRecord, budget int) []string {
	live := ""
	if record.Current != "" {
		live = toolAction(record.CurrentTool, record.Current)
	}

	type entry struct {
		marker string
		body   string
		count  int
		fileOp string
	}
	entries := []entry{}
	for i := len(steps) - 1; i >= 0 && len(entries) < budget; i-- {
		body := stepBody(steps[i])
		if body == "" {
			continue
		}
		if len(entries) == 0 && body == live {
			continue
		}
		if n := len(entries); n > 0 && entries[n-1].body == body {
			entries[n-1].count++
			continue
		}
		fileOp := ""
		if steps[i].Kind == "tool" && steps[i].File != "" {
			fileOp = map[string]string{"create": "A", "modify": "M", "delete": "D"}[steps[i].Action]
			if fileOp == "" {
				fileOp = "M"
			}
			if steps[i].Additions > 0 || steps[i].Deletions > 0 {
				fileOp = fmt.Sprintf("%s +%d -%d", fileOp, steps[i].Additions, steps[i].Deletions)
			}
			body = steps[i].File
		}
		entries = append(entries, entry{marker: stepMarker(steps[i]), body: body, count: 1, fileOp: fileOp})
	}

	rows := make([]string, 0, len(entries))
	for _, item := range entries {
		body := item.body
		if item.count > 1 {
			body = fmt.Sprintf("%s ×%d", body, item.count)
		}
		if item.fileOp != "" {
			rows = append(rows, fileChangeRow(item.marker, body, item.fileOp))
		} else {
			rows = append(rows, row(item.marker, body))
		}
	}
	return rows
}

func fileChangeRow(marker, file, operation string) string {
	badge := "■ " + operation
	leftBudget := lensUnits - textUnits(marker) - textUnits(badge) - 80
	file = truncateUnits(file, max(1, leftBudget))
	gap := max(1, (lensUnits-textUnits(marker)-textUnits(file)-textUnits(badge))/asciiUnits)
	return marker + file + strings.Repeat(" ", gap) + badge
}

// stepMarker is the gutter glyph for one settled step: it says what kind of
// thing happened before the text is read.
func stepMarker(step agentStreamStep) string {
	switch step.Kind {
	case "prompt":
		return lens.User
	case "say":
		return lens.Say
	case "ask":
		return lens.Wait
	case "tool":
		if step.Status == "fail" {
			return lens.Fail
		}
		return lens.Step
	default:
		return lens.Step
	}
}

// stepBody is the unclipped text of one settled step, without its gutter. The
// meaning that used to be baked into the wording ("失败 ·", "等待 ·") now lives
// in the marker, which buys back that width on every row.
func stepBody(step agentStreamStep) string {
	switch step.Kind {
	case "tool":
		if step.File != "" {
			operation := map[string]string{"create": "A", "modify": "M", "delete": "D"}[step.Action]
			if operation == "" {
				operation = "M"
			}
			changes := ""
			if step.Additions > 0 || step.Deletions > 0 {
				changes = fmt.Sprintf("  +%d -%d", step.Additions, step.Deletions)
			}
			return operation + " " + step.File + changes
		}
		return toolAction(step.Tool, step.Text)
	case "ask":
		// A permission request names the tool it is asking about; the "?" gutter
		// supplies the rest of the sentence.
		if step.Tool != "" {
			return toolAction(step.Tool, step.Text)
		}
		return strings.TrimSpace(step.Text)
	default:
		return strings.TrimSpace(step.Text)
	}
}

// ledgerRow summarises what the session changed on disk. It is pinned to the
// last row so its position never moves.
func ledgerRow(record agentStateRecord) string {
	if record.FileCount == 0 {
		return ""
	}
	return row(lens.Ledger, fmt.Sprintf("%d 文件 +%d -%d", record.FileCount, record.Additions, record.Deletions))
}

// toolAction turns a tool invocation into a verb + target pair.
func toolAction(tool, target string) string {
	if tool == "Bash" {
		if target == "" {
			return "> shell"
		}
		// ASCII `>` is the p10k-style prompt fallback with reliable G2 font
		// coverage. It remains separate from lens.Live (the execution marker).
		return "> " + target
	}
	verb := map[string]string{
		"Read":         "R",
		"Write":        "W",
		"Edit":         "M",
		"MultiEdit":    "M",
		"NotebookEdit": "M",
		"Grep":         "/",
		"Glob":         "*",
		"WebFetch":     "联网",
		"WebSearch":    "联网",
		"Task":         "子任务",
		"TaskCreate":   "新建任务",
		"TaskUpdate":   "更新任务",
		"TaskList":     "查看任务",
		"TaskGet":      "查看任务",
		"TaskStop":     "停止任务",
		"TodoWrite":    "任务清单",
	}[tool]
	if verb == "" {
		verb = tool
	}
	if target == "" {
		return verb
	}
	return verb + " " + target
}

// formatElapsed renders a duration the way a glanceable header wants it:
// m:ss under an hour, h:mm above.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d", hours, minutes)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// --- width ----------------------------------------------------------------------

// row assembles a marked row and clips it to the budget. Clipping happens here
// rather than in each builder so the ellipsis always lands in the same place.
func row(marker, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return truncateUnits(marker+body, lensUnits)
}

// characterUnits is how many of the firmware's units one glyph occupies. The
// metric is not guessed: even-g2-go formats teleprompter text with exactly this
// rule, so the same arithmetic that keeps a teleprompter line from clipping
// keeps a list row from clipping.
func characterUnits(character rune) int {
	if character <= 0x7F {
		return asciiUnits
	}
	return cjkUnits
}

// textUnits measures a string in glyph units, which is what the firmware lays
// out by. Counting runes instead would let a row of Chinese overflow while a
// row of ASCII wastes half the line.
func textUnits(text string) int {
	units := 0
	for _, character := range text {
		units += characterUnits(character)
	}
	return units
}

func fitsUnits(text string, budget int) bool { return textUnits(text) <= budget }

// truncateUnits clips to the budget, charging the ellipsis against it so the
// result is always inside the row.
func truncateUnits(text string, budget int) string {
	text = strings.TrimSpace(text)
	if textUnits(text) <= budget {
		return text
	}
	limit := budget - textUnits(lens.Ellipsis)
	var clipped strings.Builder
	used := 0
	for _, character := range text {
		width := characterUnits(character)
		if used+width > limit {
			break
		}
		clipped.WriteRune(character)
		used += width
	}
	return strings.TrimRight(clipped.String(), " ") + lens.Ellipsis
}

// --- calibration ----------------------------------------------------------------

// calibrationRowCount is how many rows the calibration list pushes. It is
// deliberately larger than maxRows: the point is to find where the list stops
// being visible, which is information the row budget cannot provide.
const calibrationRowCount = 12

// calibrationRows is a self-describing test list. Pushing it answers the two
// guesses the layout is built on — how wide a row really is, and how many rows
// really fit — plus which candidate glyphs the firmware renders at all.
//
// Read it back like this:
//
//	row 1  instructions
//	row 2  a..z from column 0        -> the last visible letter is the ASCII width
//	row 3  甲乙丙… from column 0      -> the last visible glyph, doubled, is the
//	                                    CJK width (compare with row 2 to learn
//	                                    whether CJK really is double-width)
//	row 4  candidate glyphs          -> anything shown as a box or a blank is
//	                                    not supported and must not be used
//	row 5+ 第N行                     -> the highest N still visible is maxRows
//
// If the list scrolls instead of stopping, that is worth knowing too: it means
// the row budget is soft and older rows stay reachable.
func calibrationRows() []string {
	rows := []string{
		"1 镜片校准：读最大行号",
		"abcdefghijklmnopqrstuvwxyz",
		"甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉戌亥",
		"> ? ! " + lens.Step + "▍ ✓ » × ─ ● " + lens.Say + lens.User + lens.Ledger,
	}
	for index := 5; index <= calibrationRowCount; index++ {
		rows = append(rows, fmt.Sprintf("第%d行", index))
	}
	return rows
}
