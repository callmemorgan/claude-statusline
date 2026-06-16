package main

import (
	"testing"
	"time"
)

// buildStatuslineSpans must render byte-identically to buildStatusline (it is
// the same reflow with segment identity carried alongside) and its spans must
// locate each segment at the right visible column.
func TestSpansMatchBuildStatusline(t *testing.T) {
	initSegments(nil)
	now := time.Unix(1750000000, 0)
	p := samplePayload()
	st := previewState(now)

	cfg := defaultConfig()
	for _, reflow := range []string{"", "off", "cascade", "group"} {
		for _, width := range []int{0, 80, 60, 40} {
			cfg.Reflow = reflow
			in := buildInput{P: p, C: palette{}, Cfg: cfg, State: st, Width: width, Now: now}
			want := buildStatusline(in)
			got, spans := buildStatuslineSpans(in)

			if len(got) != len(want) {
				t.Fatalf("reflow=%q width=%d: %d lines, want %d", reflow, width, len(got), len(want))
			}
			if len(spans) != len(got) {
				t.Fatalf("reflow=%q width=%d: %d span-rows, want %d", reflow, width, len(spans), len(got))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("reflow=%q width=%d line %d:\n got %q\nwant %q", reflow, width, i, got[i], want[i])
				}
			}
			// Every span's text must appear at its recorded column in the
			// stripped line.
			for li, row := range spans {
				stripped := []rune(stripANSI(got[li]))
				for _, sp := range row {
					segText := []rune(stripANSI(sp.Text))
					if sp.Col < 0 || sp.Col+len(segText) > len(stripped) {
						t.Errorf("reflow=%q width=%d line %d span %s: col %d+%d out of range (line %d wide)",
							reflow, width, li, sp.ID, sp.Col, len(segText), len(stripped))
						continue
					}
					got := string(stripped[sp.Col : sp.Col+len(segText)])
					if got != string(segText) {
						t.Errorf("reflow=%q width=%d line %d span %s at col %d: got %q want %q",
							reflow, width, li, sp.ID, sp.Col, got, string(segText))
					}
				}
			}
		}
	}
}
