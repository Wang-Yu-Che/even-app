package media

import "slices"

type lyricLine struct {
	Time float64
	Text string
}

func lyricsAt(lines []lyricLine, position float64) [3]string {
	var rows [3]string
	if position < 0 {
		return rows
	}
	next, _ := slices.BinarySearchFunc(lines, position, func(line lyricLine, at float64) int {
		if line.Time <= at {
			return -1
		}
		return 1
	})
	for row := range rows {
		index := next - 2 + row
		if index >= 0 && index < len(lines) {
			rows[row] = lines[index].Text
		}
	}
	return rows
}

// lyricProgress reports how far position is through the current line. The
// last line runs until the track ends, or for five seconds if that is unknown.
func lyricProgress(lines []lyricLine, position, duration float64) float64 {
	next, _ := slices.BinarySearchFunc(lines, position, func(line lyricLine, at float64) int {
		if line.Time <= at {
			return -1
		}
		return 1
	})
	if next == 0 || position < 0 {
		return 0
	}
	start, end := lines[next-1].Time, lines[next-1].Time+5
	if next < len(lines) {
		end = lines[next].Time
	} else if duration > start {
		end = duration
	}
	if end <= start {
		return 1
	}
	return min(1, (position-start)/(end-start))
}
