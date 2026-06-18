package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// stripAllCSI removes every CSI escape sequence (ESC [ ... final byte) so
// tests can reason about visible glyphs independently of color/cursor
// sequences.
var reAllCSI = regexp.MustCompile(`\x1b\[[0-9:;<=>?]*[@A-Za-z]`)

func stripAllCSI(s string) string {
	return reAllCSI.ReplaceAllString(s, "")
}

// moveCursorSegmentHoriz must reorder segments within the cursor's line.
func TestMoveCursorSegmentHoriz(t *testing.T) {
	cfg := config{Segments: []string{"a", "b", "c"}}
	curSpans := [][]segSpan{
		{{ID: "a", Col: 0, Width: 1}, {ID: "b", Col: 2, Width: 1}, {ID: "c", Col: 4, Width: 1}},
	}

	// Move b left.
	if id, moved := moveCursorSegmentHoriz(&cfg, curSpans, 0, 1, -1); !moved || id != "b" {
		t.Fatalf("move left: moved=%v id=%q", moved, id)
	}
	if got := cfg.Segments; !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("after left: got %v", got)
	}

	// Update the synthetic span layout to match the new cfg order and move b
	// back to the right.
	curSpans = [][]segSpan{
		{{ID: "b", Col: 0, Width: 1}, {ID: "a", Col: 2, Width: 1}, {ID: "c", Col: 4, Width: 1}},
	}
	if id, moved := moveCursorSegmentHoriz(&cfg, curSpans, 0, 0, 1); !moved || id != "b" {
		t.Fatalf("move right: moved=%v id=%q", moved, id)
	}
	if got := cfg.Segments; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("after right: got %v", got)
	}

	// Edge clamps.
	curSpans = [][]segSpan{
		{{ID: "a", Col: 0, Width: 1}, {ID: "b", Col: 2, Width: 1}, {ID: "c", Col: 4, Width: 1}},
	}
	if _, moved := moveCursorSegmentHoriz(&cfg, curSpans, 0, 0, -1); moved {
		t.Fatal("left from first should not move")
	}
	if _, moved := moveCursorSegmentHoriz(&cfg, curSpans, 0, 2, 1); moved {
		t.Fatal("right from last should not move")
	}
}

// moveCursorSegmentVert must relocate a segment to the adjacent line,
// preserving column-nearest ordering.
func TestMoveCursorSegmentVert(t *testing.T) {
	cfg := config{
		Segments: []string{"a", "b"},
		Lines:    map[string]int{"a": 1, "b": 2},
	}
	curSpans := [][]segSpan{
		{{ID: "a", Col: 0, Width: 1}},
		{{ID: "b", Col: 0, Width: 1}},
	}

	if id, moved := moveCursorSegmentVert(&cfg, curSpans, 0, 0, 1); !moved || id != "a" {
		t.Fatalf("move down: moved=%v id=%q", moved, id)
	}
	if cfg.Lines["a"] != 2 {
		t.Fatalf("a should be on line 2, got %d", cfg.Lines["a"])
	}
	if got := cfg.Segments; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("segments after move down: got %v", got)
	}

	// Move a back up.
	if id, moved := moveCursorSegmentVert(&cfg, curSpans, 0, 0, -1); !moved || id != "a" {
		t.Fatalf("move up: moved=%v id=%q", moved, id)
	}
	if cfg.Lines["a"] != 1 {
		t.Fatalf("a should be back on line 1, got %d", cfg.Lines["a"])
	}

	// Clamps.
	if _, moved := moveCursorSegmentVert(&cfg, curSpans, 0, 0, -1); moved {
		t.Fatal("move up from line 1 should not move")
	}
	cfg.Lines["a"] = 9
	if _, moved := moveCursorSegmentVert(&cfg, curSpans, 0, 0, 1); moved {
		t.Fatal("move down from line 9 should not move")
	}
}

// insertSegmentAtCursor inserts after the cursor and adopts the anchor's line.
func TestInsertSegmentAtCursor(t *testing.T) {
	cfg := config{Segments: []string{"a", "b"}}
	curSpans := [][]segSpan{
		{{ID: "a", Col: 0, Width: 1}, {ID: "b", Col: 2, Width: 1}},
	}

	if id, ok := insertSegmentAtCursor(&cfg, curSpans, 0, 0, "a", "c"); !ok || id != "c" {
		t.Fatalf("insert at a: ok=%v id=%q", ok, id)
	}
	want := []string{"a", "c", "b"}
	if got := cfg.Segments; !reflect.DeepEqual(got, want) {
		t.Fatalf("segments: got %v, want %v", got, want)
	}

	// Insert with no cursor appends.
	cfg2 := config{Segments: []string{"a"}}
	if id, ok := insertSegmentAtCursor(&cfg2, [][]segSpan{}, 0, 0, "", "z"); !ok || id != "z" {
		t.Fatalf("insert append: ok=%v id=%q", ok, id)
	}
	if got := cfg2.Segments; !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("append: got %v", got)
	}
}

// sliceVisible must not hang on non-SGR CSI sequences and should still slice
// visible columns correctly around them.
func TestSliceVisibleNonSGR(t *testing.T) {
	// \x1b[2K is an erase-in-line CSI that does not end in 'm'.
	src := "\x1b[2K\x1b[31mab\x1b[0m"
	got := stripAllCSI(sliceVisible(src, 0, 2))
	if got != "ab" {
		t.Fatalf("sliceVisible with non-SGR CSI = %q, want ab", got)
	}

	// A cursor-positioning sequence should also be skipped safely.
	src2 := "\x1b[10C\x1b[32mcd\x1b[0m"
	if got := stripAllCSI(sliceVisible(src2, 0, 2)); got != "cd" {
		t.Fatalf("sliceVisible with CUP-like CSI = %q, want cd", got)
	}
}

func TestAssignSegmentLineUnknownPreservesOverride(t *testing.T) {
	cfg := config{}
	assignSegmentLine(&cfg, "unknown-segment", 1)
	if _, ok := cfg.Lines["unknown-segment"]; !ok {
		t.Fatal("unknown segment override on line 1 should be preserved")
	}
	assignSegmentLine(&cfg, "unknown-segment", 2)
	if cfg.Lines["unknown-segment"] != 2 {
		t.Fatalf("unknown segment override should be 2, got %d", cfg.Lines["unknown-segment"])
	}
}

// buildStatuslineSpans must honour custom Lines/Colors and synthetic plugin
// segments.
func TestSpansCustomLinesColorsAndPlugin(t *testing.T) {
	initSegments(nil)
	// Make sure an ambient NO_COLOR doesn't disable the color override test.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	now := time.Unix(1750000000, 0)
	p := samplePayload()
	st := previewState(now)

	// Synthetic plugin: a tiny shell script that prints a fixed value.
	tmp := t.TempDir()
	script := filepath.Join(tmp, "plugin.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'demo-plugin'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	initSegments([]pluginDef{{ID: "demo", Command: script}})

	cfg := defaultConfig()
	cfg.Segments = []string{"model", "directory", "demo"}
	cfg.ColorDepth = "truecolor"
	cfg.Lines = map[string]int{"directory": 3}
	cfg.Colors = map[string]string{"model": "red"}

	in := buildInput{P: p, C: currentPalette(cfg), Cfg: cfg, State: st, Width: 80, Now: now}
	lines, spans := buildStatuslineSpans(in)

	// Custom line: directory must appear on line 3.
	foundDir := false
	for li, row := range spans {
		for _, sp := range row {
			if sp.ID == "directory" {
				foundDir = true
				if li != 2 {
					t.Fatalf("directory should be on physical line 2 (line 3), got %d", li)
				}
			}
		}
	}
	if !foundDir {
		t.Fatal("directory span not found")
	}

	// Custom color: model text must contain the red ANSI code.
	foundModel := false
	for _, row := range spans {
		for _, sp := range row {
			if sp.ID == "model" {
				foundModel = true
				if !strings.Contains(sp.Text, "\x1b[31m") {
					t.Fatalf("model span should contain red ANSI, got %q", sp.Text)
				}
			}
		}
	}
	if !foundModel {
		t.Fatal("model span not found")
	}

	// Plugin segment must render with the expected text.
	foundPlugin := false
	for li, row := range spans {
		for _, sp := range row {
			if sp.ID == "demo" {
				foundPlugin = true
				if stripANSI(sp.Text) != "demo-plugin" {
					t.Fatalf("plugin span text = %q, want demo-plugin", stripANSI(sp.Text))
				}
				// Verify it also appears in the matching rendered line.
				if !strings.Contains(stripANSI(lines[li]), "demo-plugin") {
					t.Fatalf("plugin text not in rendered line %d: %q", li, lines[li])
				}
			}
		}
	}
	if !foundPlugin {
		t.Fatal("demo plugin span not found")
	}
}
