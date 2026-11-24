package main

import (
	"strings"
	"testing"

	"even-glasses/miniapps/media"
)

func TestMediaClockKeepsDurationFixed(t *testing.T) {
	state := media.State{Title: "Track", ElapsedTime: 9, Duration: 3600}
	first := mediaClock(state, "")
	state.ElapsedTime = 10
	second := mediaClock(state, "")
	if first[0].Content != "0:09" || second[0].Content != "0:10" ||
		first[0].Geometry != second[0].Geometry || first[1] != second[1] || second[1].Content != "1:00:00" {
		t.Fatalf("unexpected clock update: %v → %v", first, second)
	}
}

func TestMediaWithoutLyricsShowsTitleAndSource(t *testing.T) {
	state := media.State{Title: "一个不需要在狭窄歌词分栏里滚动的标题", BundleIdentifier: "com.google.Chrome", LyricsStatus: "暂无同步歌词"}
	if mediaShowsLyrics(state, "") {
		t.Fatal("divider should be hidden without lyrics")
	}
	rows := strings.Split(mediaPageText(state, "", false), "\n")
	if strings.TrimSpace(rows[2]) != state.Title || strings.TrimSpace(rows[3]) != "Chrome" {
		t.Fatalf("unexpected title and source: %q, %q", rows[2], rows[3])
	}
	state.Lyrics[1] = "当前歌词"
	if !mediaShowsLyrics(state, "") || !strings.Contains(mediaPageText(state, "", true), "「当前歌词」") {
		t.Fatal("synced lyrics should use the lyric layout")
	}
}
