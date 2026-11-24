package main

import (
	"fmt"
	"strings"

	"even-glasses/miniapps/media"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
	"github.com/Wang-Yu-Che/even-g2-go/protocol"
)

var mediaPageStyle = g2.TextStyle{
	X: 8, Y: 14, Width: 560, Height: 260,
	BorderWidth: 1, BorderColor: 4, BorderRadius: 10, PaddingLength: 13, EventCaptureName: media.InputContainerName,
}

// The cover is drawn at mediaCoverSize; the placeholder art is designed on a
// 64px grid and scaled up to it.
const (
	mediaCoverX    = 23
	mediaCoverY    = 58
	mediaCoverSize = 96
)

var mediaPageCover = mediaBitmap(2, "media-cover", mediaCoverX, mediaCoverY, mediaCoverSize, mediaCoverSize, func(px, py int) uint8 {
	x, y := px*64/mediaCoverSize, py*64/mediaCoverSize
	switch {
	case px == 0 || py == 0 || px == mediaCoverSize-1 || py == mediaCoverSize-1:
		return 7
	case (x-46)*(x-46)+(y-17)*(y-17) <= 64:
		return 13
	case y >= min(32+absMedia(x-22), 41+absMedia(x-47)):
		return 9
	default:
		return 2
	}
})

var mediaPageDivider = mediaBitmap(3, "media-divider", 212, mediaCoverY, 20, mediaCoverSize, func(x, _ int) uint8 {
	if x == 9 {
		return 5
	}
	return 0
})

func mediaBitmap(id int, name string, x, y, width, height int, pixel func(int, int) uint8) g2.StatusIcon {
	bmp, err := protocol.EncodeBMP4(width, height, pixel)
	if err != nil {
		panic(err)
	}
	return g2.StatusIcon{ID: id, Name: name, X: x, Y: y, Width: width, Height: height, BMP: bmp}
}

func absMedia(n int) int { return max(n, -n) }

func mediaPageText(state media.State, message string) string {
	if message == "" && !state.HasMedia() {
		message = "当前没有媒体"
	}
	if message != "" {
		return "\n" + mediaSpacing(mediaInfoX) + "正在播放\n" + mediaSpacing(210) + message
	}

	title := strings.Join(strings.Fields(state.Title), " ")
	if title == "" {
		title = "媒体播放中"
	}
	metadata := strings.Trim(strings.Join([]string{state.Artist, state.Album}, " · "), " ·")
	rows := make([]string, 6)
	// The source app is left out for now so the cover can be larger. Title and
	// artist sit on rows 2–3, level with the middle of the cover; only the
	// title scrolls, the artist is clipped with an ellipsis.
	// Playback time drives the marquee at one rune per 0.75s (three 0.25s
	// redraw ticks, so steps stay evenly spaced); it pauses with the track.
	step := max(0, int(state.ElapsedTime/0.75))
	rows[2] = mediaSpacing(mediaInfoX) + marqueeUnits(title, mediaInfoUnits, step)
	rows[3] = mediaSpacing(mediaInfoX) + truncateUnits(strings.Join(strings.Fields(metadata), " "), mediaInfoUnits)
	for i, lyric := range mediaLyricRows(state.Lyrics, state.LyricProgress, mediaLyricFallback(state)) {
		if lyric == "" {
			continue
		}
		row := &rows[i+1]
		*row += mediaSpacing(mediaLyricX-textUnits(*row)+(mediaLyricUnits-textUnits(lyric))/2) + lyric
	}

	const width = 530
	elapsed, duration := "—", "—"
	filled := 0
	const segments = 26
	if state.ElapsedTime >= 0 {
		elapsed = mediaTime(state.ElapsedTime)
	}
	if state.Duration > 0 {
		duration = mediaTime(state.Duration)
		if state.ElapsedTime >= 0 {
			filled = int(min(state.ElapsedTime/state.Duration, 1) * segments)
		}
	}
	track := strings.Repeat("─", segments)
	if state.Duration > 0 && state.ElapsedTime >= 0 {
		marker := min(filled, segments-1)
		track = strings.Repeat("━", marker) + "●" + strings.Repeat("─", segments-marker-1)
	}
	// Times get their own row (the blank one above the bar) so the ticking
	// elapsed time can't push the controls around: digits have different
	// advances and spaces only come in 5-unit steps.
	rows[5] = elapsed + mediaSpacing(width-textUnits(elapsed)-textUnits(duration)) + duration
	rows = append(rows, track)

	center := "▶"
	if state.Playing {
		// A single solid square stays centered and matches ▶ in size.
		center = "■"
	}
	// Use the same run of spaces on both sides of the center control. Placing
	// each symbol at an absolute column relied on our estimated glyph widths,
	// and when ◀◀ renders narrower than estimated its gap came out wider.
	gap := mediaSpacing(44)
	controls := "◀◀" + gap + center + gap + "▶▶"
	footer := mediaSpacing((width-textUnits(controls))/2) + controls
	return strings.Join(append(rows, footer), "\n")
}

// The left column's text starts just right of the cover and ends before the
// divider; mediaInfoX is in text units from the container's text origin.
const (
	mediaInfoX     = 107
	mediaInfoUnits = 85 // four CJK characters plus a little slack
)

// marqueeUnits returns text unchanged when it fits; otherwise it scrolls one
// rune per step through "text   text", holding the start for a few steps so
// the beginning stays readable.
func marqueeUnits(text string, budget, step int) string {
	if fitsUnits(text, budget) {
		return text
	}
	const hold = 3 // steps, about two seconds at the start of each pass
	loop := []rune(text + "   ")
	offset := max(0, step%(len(loop)+hold)-hold)
	window := append(loop[offset:], loop[:offset]...)
	window = append(window, loop...)
	used := 0
	for i, r := range window {
		used += characterUnits(r)
		if used > budget {
			return string(window[:i])
		}
	}
	return string(window)
}

// The lyric column sits right of the divider and lines up with the three
// info rows on the left: previous line, current line, next line.
const (
	mediaLyricX     = 218
	mediaLyricUnits = 310
)

// mediaLyricFallback is what the lyric column shows without lyrics: the
// lookup status while searching, otherwise the source app's name.
func mediaLyricFallback(state media.State) string {
	if state.LyricsStatus == "正在查找歌词…" {
		return state.LyricsStatus
	}
	switch state.BundleIdentifier {
	case "com.apple.Music":
		return "Apple Music"
	case "com.netease.163music":
		return "网易云音乐"
	case "com.google.Chrome":
		return "Chrome"
	case "":
		return state.LyricsStatus
	}
	return state.BundleIdentifier
}

func mediaLyricRows(lines [3]string, progress float64, status string) []string {
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	if lines[1] == "" {
		// Nothing synced yet: a single status line, level with the title.
		return []string{"", truncateUnits(status, mediaLyricUnits)}
	}
	// Always three rows, no scrolling: 「」 marks the current line. A long
	// current line wraps and takes over the neighbours' rows; past three rows
	// it pages, three rows at a time, as the line is sung.
	wrapped := wrapUnits("「"+lines[1]+"」", mediaLyricUnits)
	switch len(wrapped) {
	case 1:
		return []string{truncateUnits(lines[0], mediaLyricUnits), wrapped[0], truncateUnits(lines[2], mediaLyricUnits)}
	case 2:
		return append(wrapped, truncateUnits(lines[2], mediaLyricUnits))
	}
	pages := (len(wrapped) + 2) / 3
	page := min(pages-1, int(progress*float64(pages)))
	rows := wrapped[page*3 : min(len(wrapped), page*3+3)]
	for len(rows) < 3 {
		rows = append(rows, "")
	}
	return rows
}

// wrapUnits breaks text into rows that each fit the budget.
func wrapUnits(text string, budget int) []string {
	var rows []string
	start, used := 0, 0
	for offset, r := range text {
		if used+characterUnits(r) > budget {
			rows = append(rows, text[start:offset])
			start, used = offset, 0
		}
		used += characterUnits(r)
	}
	return append(rows, text[start:])
}

func mediaSpacing(width int) string {
	return strings.Repeat(" ", max(0, width/characterUnits(' ')))
}

func mediaTime(seconds float64) string {
	total := int(seconds)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total/60%60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
