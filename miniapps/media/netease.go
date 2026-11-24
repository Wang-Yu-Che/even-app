package media

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const neteaseBundleID = "com.netease.163music"
const neteaseAPI = "https://music.163.com"

type neteaseSong struct {
	ID       int64
	Name     string
	Duration float64
	Artists  []struct{ Name string }
}

func loadNeteaseLyrics(ctx context.Context, baseURL string, track State) []lyricLine {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	query := url.Values{
		"s":     {strings.TrimSpace(track.Title + " " + track.Artist)},
		"type":  {"1"},
		"limit": {"20"},
	}
	var search struct {
		Result struct{ Songs []neteaseSong }
	}
	if !readNeteaseJSON(ctx, baseURL+"/api/search/get?"+query.Encode(), &search) {
		return nil
	}
	id := matchNeteaseSong(track, search.Result.Songs)
	if id == 0 {
		return nil
	}

	var lyrics struct {
		LRC struct{ Lyric string }
	}
	if !readNeteaseJSON(ctx, baseURL+"/api/song/lyric?id="+strconv.FormatInt(id, 10)+"&lv=1", &lyrics) {
		return nil
	}
	return parseNeteaseLRC(lyrics.LRC.Lyric)
}

func matchNeteaseSong(track State, songs []neteaseSong) int64 {
	var id int64
	bestDelta := 2.0
	for _, song := range songs {
		if !strings.EqualFold(strings.TrimSpace(song.Name), strings.TrimSpace(track.Title)) {
			continue
		}
		artists := make([]string, len(song.Artists))
		for i, artist := range song.Artists {
			artists[i] = strings.TrimSpace(artist.Name)
		}
		normalize := func(value string) string {
			return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
				return strings.ContainsRune(" /、,，&", r)
			}), "/")
		}
		if track.Artist != "" && normalize(track.Artist) != normalize(strings.Join(artists, "/")) {
			continue
		}
		delta := 0.0
		if track.Duration > 0 {
			delta = math.Abs(track.Duration - song.Duration/1000)
		}
		if delta < bestDelta {
			id, bestDelta = song.ID, delta
		} else if delta == bestDelta && song.ID != id {
			id = 0
		}
	}
	return id
}

func readNeteaseJSON(ctx context.Context, endpoint string, result any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(result) == nil
}

var lrcTimestamp = regexp.MustCompile(`^\[(\d+):(\d+(?:\.\d+)?)\]`)

func parseNeteaseLRC(lrc string) []lyricLine {
	var lines []lyricLine
	for _, text := range strings.Split(lrc, "\n") {
		var times []float64
		for match := lrcTimestamp.FindStringSubmatch(text); match != nil; match = lrcTimestamp.FindStringSubmatch(text) {
			minutes, _ := strconv.ParseFloat(match[1], 64)
			seconds, _ := strconv.ParseFloat(match[2], 64)
			times = append(times, minutes*60+seconds)
			text = text[len(match[0]):]
		}
		for _, at := range times {
			lines = append(lines, lyricLine{Time: at, Text: strings.TrimSpace(text)})
		}
	}
	slices.SortStableFunc(lines, func(a, b lyricLine) int { return cmp.Compare(a.Time, b.Time) })
	return lines
}
