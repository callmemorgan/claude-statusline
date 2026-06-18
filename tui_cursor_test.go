package main

import (
	"regexp"
	"testing"
	"time"
)

// reTviewTag strips tview color/style region tags ("[fg:bg:flags]") so a
// painted preview line can be reduced back to its plain visible glyphs.
var reTviewTag = regexp.MustCompile(`\[[a-zA-Z0-9#,:_\- ]*\]`)

// dropTviewTags removes tview color/style region tags but leaves tview's
// literal-bracket escaping ("[[" and "]]") intact, so two tview strings that
// escape the same underlying text compare equal. Used to compare the
// cursor-painted line against the same line rendered through the normal
// (non-cursor) ansiToTview path — the bracket-escaping then cancels out on
// both sides.
func dropTviewTags(s string) string {
	return reTviewTag.ReplaceAllString(s, "")
}

// paintCursorLine must preserve the exact visible glyphs of the real rendered
// line — it only re-colors the cursor span, never changes the text.
func TestPaintCursorLinePreservesGlyphs(t *testing.T) {
	initSegments(nil)
	now := time.Unix(1750000000, 0)
	p := samplePayload()
	st := previewState(now)
	cfg := defaultConfig()

	in := buildInput{P: p, C: currentPalette(cfg), Cfg: cfg, State: st, Width: 80, Now: now}
	lines, spans := buildStatuslineSpans(in)

	for li, row := range spans {
		if len(row) == 0 {
			continue
		}
		// The non-cursor render path is ansiToTview(line); the cursor painter
		// must produce the same visible glyphs once both have their tview tags
		// removed (bracket-escaping is identical on both sides).
		want := dropTviewTags(ansiToTview(lines[li]))
		for ci := range row {
			painted := paintCursorLine(lines[li], row, ci, false)
			if got := dropTviewTags(painted); got != want {
				t.Errorf("line %d cursor %d:\n got %q\nwant %q", li, ci, got, want)
			}
			// And again in grabbing mode (different highlight, same glyphs).
			painted = paintCursorLine(lines[li], row, ci, true)
			if got := dropTviewTags(painted); got != want {
				t.Errorf("line %d cursor %d (grab):\n got %q\nwant %q", li, ci, got, want)
			}
		}
	}
}

// sliceVisible must cut on visible columns: the stripped slice equals the
// corresponding window of the stripped source.
func TestSliceVisibleColumns(t *testing.T) {
	src := "\x1b[31mab\x1b[0m\x1b[32mcd\x1b[0mef" // visible "abcdef"
	cases := []struct {
		start, end int
		want       string
	}{
		{0, 2, "ab"},
		{2, 4, "cd"},
		{0, 6, "abcdef"},
		{4, -1, "ef"},
		{1, 5, "bcde"},
	}
	for _, c := range cases {
		got := stripANSI(sliceVisible(src, c.start, c.end))
		if got != c.want {
			t.Errorf("sliceVisible(%d,%d) = %q, want %q", c.start, c.end, got, c.want)
		}
	}
}

func TestNearestSpanCol(t *testing.T) {
	row := []segSpan{{Col: 1}, {Col: 10}, {Col: 25}}
	for _, c := range []struct {
		target, want int
	}{
		{0, 0}, {3, 0}, {8, 1}, {12, 1}, {20, 2}, {100, 2},
	} {
		if got := nearestSpanCol(row, c.target); got != c.want {
			t.Errorf("nearestSpanCol(target=%d) = %d, want %d", c.target, got, c.want)
		}
	}
}
