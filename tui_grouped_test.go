package main

import (
	"strings"
	"testing"
)

// initSegments is required so segmentByID / registeredSegments are populated.
func ensureRegistry(t *testing.T) {
	t.Helper()
	initSegments(nil)
}

// segRowIDs returns the segment ids in grouped order (headers dropped), so a
// test can assert the list's vertical order == render order.
func segRowIDs(rows []listRow) []string {
	var out []string
	for _, r := range rows {
		if !r.header {
			out = append(out, r.seg.id)
		}
	}
	return out
}

// headerLines returns the header rows' line numbers in order.
func headerLines(rows []listRow) []int {
	var out []int
	for _, r := range rows {
		if r.header {
			out = append(out, r.line)
		}
	}
	return out
}

func infosFor(ids ...string) []segmentInfo {
	var out []segmentInfo
	for _, id := range ids {
		if s, ok := segmentByID(id); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestGroupRowsOrdersByLineThenOff(t *testing.T) {
	ensureRegistry(t)
	// directory(line1) model(line2) context-window(line3); enable directory and
	// model, leave context-window off. Order in cfg.Segments deliberately
	// model-before-directory to prove grouping is by line, not slice order.
	cfg := config{Segments: []string{"model", "directory"}}
	visible := infosFor("directory", "model", "context-window")

	rows, rowToSeg := groupRows(visible, cfg)
	if got, want := headerLines(rows), []int{1, 2, offLine}; !eqInts(got, want) {
		t.Fatalf("header lines = %v, want %v", got, want)
	}
	if got, want := segRowIDs(rows), []string{"directory", "model", "context-window"}; !eqStrs(got, want) {
		t.Fatalf("segment order = %v, want %v", got, want)
	}
	// rowToSeg must point header rows at -1 and segment rows at the visible idx.
	for i, r := range rows {
		if r.header && rowToSeg[i] != -1 {
			t.Fatalf("row %d is a header but rowToSeg = %d", i, rowToSeg[i])
		}
		if !r.header {
			if rowToSeg[i] < 0 || visible[rowToSeg[i]].id != r.seg.id {
				t.Fatalf("row %d rowToSeg mismatch", i)
			}
		}
	}
}

func TestGroupRowsRespectsRenderOrderWithinLine(t *testing.T) {
	ensureRegistry(t)
	// Two line-1 segments; cfg order is the render order within the line.
	cfg := config{Segments: []string{"git-branch", "directory"}}
	visible := infosFor("directory", "git-branch")
	rows, _ := groupRows(visible, cfg)
	if got, want := segRowIDs(rows), []string{"git-branch", "directory"}; !eqStrs(got, want) {
		t.Fatalf("within-line order = %v, want %v", got, want)
	}
}

func TestMoveSegmentDownReordersWithinLine(t *testing.T) {
	ensureRegistry(t)
	cfg := config{Segments: []string{"git-branch", "directory"}}
	if !moveSegmentInGroup(&cfg, "git-branch", +1) {
		t.Fatal("expected move to report a change")
	}
	if got, want := cfg.Segments, []string{"directory", "git-branch"}; !eqStrs(got, want) {
		t.Fatalf("after down-move cfg.Segments = %v, want %v", got, want)
	}
	// Both still on their natural line (1) — no line override written.
	if len(cfg.Lines) != 0 {
		t.Fatalf("expected no line overrides, got %v", cfg.Lines)
	}
}

func TestMoveSegmentDownCrossesIntoNextLine(t *testing.T) {
	ensureRegistry(t)
	// directory is the only line-1 segment; model is on line 2. Moving directory
	// down crosses the line-2 header → it reassigns to line 2, at that group's top.
	cfg := config{Segments: []string{"directory", "model"}}
	if !moveSegmentInGroup(&cfg, "directory", +1) {
		t.Fatal("expected change")
	}
	if cfg.Lines["directory"] != 2 {
		t.Fatalf("expected directory on line 2, got %v", cfg.Lines)
	}
	// directory should now precede model in line 2's group.
	rows, _ := groupRows(infosFor("directory", "model"), cfg)
	if got, want := segRowIDs(rows), []string{"directory", "model"}; !eqStrs(got, want) {
		t.Fatalf("grouped order = %v, want %v", got, want)
	}
}

func TestMoveSegmentUpCrossesIntoPrevLine(t *testing.T) {
	ensureRegistry(t)
	// directory(line1) and model(line2) enabled. Moving model up crosses the
	// line-1 header → model reassigns to line 1, at the bottom of that group.
	cfg := config{Segments: []string{"directory", "model"}}
	if !moveSegmentInGroup(&cfg, "model", -1) {
		t.Fatal("expected change")
	}
	if cfg.Lines["model"] != 1 {
		t.Fatalf("expected model on line 1, got %v", cfg.Lines)
	}
	rows, _ := groupRows(infosFor("directory", "model"), cfg)
	if got, want := segRowIDs(rows), []string{"directory", "model"}; !eqStrs(got, want) {
		t.Fatalf("grouped order = %v, want %v", got, want)
	}
	// Only one occupied line now → exactly one line header.
	if got, want := headerLines(rows), []int{1}; !eqInts(got, want) {
		t.Fatalf("header lines = %v, want %v", got, want)
	}
}

func TestMoveSegmentDownOffLastLineDisables(t *testing.T) {
	ensureRegistry(t)
	// Single enabled segment forced onto line 9 (the last line): moving it down
	// crosses the off header → disable.
	cfg := config{Segments: []string{"directory"}, Lines: map[string]int{"directory": 9}}
	if !moveSegmentInGroup(&cfg, "directory", +1) {
		t.Fatal("expected change")
	}
	for _, id := range cfg.Segments {
		if id == "directory" {
			t.Fatalf("directory should be disabled, cfg.Segments = %v", cfg.Segments)
		}
	}
}

func TestMoveSegmentUpFromOffEnables(t *testing.T) {
	ensureRegistry(t)
	// model enabled on line 2; directory is in the off group. Moving directory
	// up re-enables it onto the last occupied line (2).
	cfg := config{Segments: []string{"model"}}
	if !moveSegmentInGroup(&cfg, "directory", -1) {
		t.Fatal("expected change")
	}
	found := false
	for _, id := range cfg.Segments {
		if id == "directory" {
			found = true
		}
	}
	if !found {
		t.Fatalf("directory should be enabled, cfg.Segments = %v", cfg.Segments)
	}
	if cfg.Lines["directory"] != 2 {
		t.Fatalf("expected directory on line 2, got %v", cfg.Lines)
	}
}

func TestMoveSegmentDownFromOffIsNoop(t *testing.T) {
	ensureRegistry(t)
	cfg := config{Segments: []string{"model"}}
	if moveSegmentInGroup(&cfg, "directory", +1) {
		t.Fatal("moving a disabled segment down should be a no-op")
	}
}

func TestHeaderLabel(t *testing.T) {
	if !strings.Contains(headerLabel(3), "line 3") {
		t.Fatalf("headerLabel(3) = %q", headerLabel(3))
	}
	if !strings.Contains(headerLabel(offLine), "off") {
		t.Fatalf("headerLabel(off) = %q", headerLabel(offLine))
	}
}

func TestMoveToNewEmptyLineBelow(t *testing.T) {
	ensureRegistry(t)
	// directory is alone on line 1. Moving down creates a new empty line 2.
	cfg := config{Segments: []string{"directory"}}
	if !moveSegmentInGroup(&cfg, "directory", +1) {
		t.Fatal("expected change")
	}
	if cfg.Lines["directory"] != 2 {
		t.Fatalf("expected directory on line 2, got %v", cfg.Lines)
	}
	rows, _ := groupRows(infosFor("directory"), cfg)
	if got, want := headerLines(rows), []int{2}; !eqInts(got, want) {
		t.Fatalf("header lines = %v, want %v", got, want)
	}
}

func TestMoveToNewEmptyLineAbove(t *testing.T) {
	ensureRegistry(t)
	// model is alone on line 2. Moving up creates a new empty line 1 above it.
	cfg := config{Segments: []string{"model"}}
	if !moveSegmentInGroup(&cfg, "model", -1) {
		t.Fatal("expected change")
	}
	if cfg.Lines["model"] != 1 {
		t.Fatalf("expected model on line 1, got %v", cfg.Lines)
	}
	rows, _ := groupRows(infosFor("model"), cfg)
	if got, want := headerLines(rows), []int{1}; !eqInts(got, want) {
		t.Fatalf("header lines = %v, want %v", got, want)
	}
}

func TestMoveUpFromLineOneIsNoop(t *testing.T) {
	ensureRegistry(t)
	cfg := config{Segments: []string{"directory"}}
	if moveSegmentInGroup(&cfg, "directory", -1) {
		t.Fatal("moving up from line 1 should be a no-op")
	}
	if got, want := cfg.Segments, []string{"directory"}; !eqStrs(got, want) {
		t.Fatalf("cfg.Segments = %v, want %v", got, want)
	}
	if len(cfg.Lines) != 0 {
		t.Fatalf("expected no line overrides, got %v", cfg.Lines)
	}
}

func TestWithinLineReorderThreeSegments(t *testing.T) {
	ensureRegistry(t)
	// Three line-1 segments in cfg order: git-branch, directory, added-dirs.
	cfg := config{Segments: []string{"git-branch", "directory", "added-dirs"}}

	// Move directory down: it should swap with added-dirs.
	if !moveSegmentInGroup(&cfg, "directory", +1) {
		t.Fatal("expected directory down-move to change")
	}
	if got, want := cfg.Segments, []string{"git-branch", "added-dirs", "directory"}; !eqStrs(got, want) {
		t.Fatalf("after directory down: cfg.Segments = %v, want %v", got, want)
	}

	// Move directory up twice: back to the top of the line.
	if !moveSegmentInGroup(&cfg, "directory", -1) {
		t.Fatal("expected directory up-move to change")
	}
	if !moveSegmentInGroup(&cfg, "directory", -1) {
		t.Fatal("expected second directory up-move to change")
	}
	if got, want := cfg.Segments, []string{"directory", "git-branch", "added-dirs"}; !eqStrs(got, want) {
		t.Fatalf("after directory up twice: cfg.Segments = %v, want %v", got, want)
	}
	if len(cfg.Lines) != 0 {
		t.Fatalf("expected no line overrides, got %v", cfg.Lines)
	}
}

func TestFilteredViewKeepsRowToSegConsistent(t *testing.T) {
	ensureRegistry(t)
	// directory(line1) and model(line2) enabled; context-window(line3) off.
	cfg := config{Segments: []string{"directory", "model"}}
	// Filter visible to only directory and context-window.
	visible := infosFor("directory", "context-window")
	rows, rowToSeg := groupRows(visible, cfg)

	// Headers: line 1 and off. No line-2 header because model is filtered out.
	if got, want := headerLines(rows), []int{1, offLine}; !eqInts(got, want) {
		t.Fatalf("header lines = %v, want %v", got, want)
	}
	if got, want := segRowIDs(rows), []string{"directory", "context-window"}; !eqStrs(got, want) {
		t.Fatalf("segment order = %v, want %v", got, want)
	}
	// rowToSeg must point each segment row at the correct visible index.
	for i, r := range rows {
		if r.header && rowToSeg[i] != -1 {
			t.Fatalf("row %d is a header but rowToSeg = %d", i, rowToSeg[i])
		}
		if !r.header {
			if rowToSeg[i] < 0 || visible[rowToSeg[i]].id != r.seg.id {
				t.Fatalf("row %d rowToSeg mismatch: got %d for seg %s", i, rowToSeg[i], r.seg.id)
			}
		}
	}
}

func TestAssignLineCfgRemovesOverrideWhenNatural(t *testing.T) {
	ensureRegistry(t)
	// model's natural line is 2; an override equal to that should be deleted.
	cfg := config{Lines: map[string]int{"model": 2}}
	assignLineCfg(&cfg, "model", 2)
	if _, ok := cfg.Lines["model"]; ok {
		t.Fatalf("expected line override for model to be removed, got %v", cfg.Lines)
	}

	// An override to a different line should be stored.
	assignLineCfg(&cfg, "model", 1)
	if cfg.Lines["model"] != 1 {
		t.Fatalf("expected model on line 1, got %v", cfg.Lines)
	}
}

func TestEffectiveLineClampsOutOfRangeOverride(t *testing.T) {
	ensureRegistry(t)
	cfg := config{Lines: map[string]int{"directory": 100}}
	if got := effectiveLine("directory", cfg); got != maxRenderLine {
		t.Fatalf("effectiveLine(directory) = %d, want %d", got, maxRenderLine)
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
