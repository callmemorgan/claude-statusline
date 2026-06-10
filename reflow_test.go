package main

import (
	"strings"
	"testing"
)

func seg(s string, width int) string {
	return s + strings.Repeat("x", width-len(s))
}

// TestReflowCascadeSpillsTrailingSegments: in cascade mode a too-wide line
// moves its trailing segment to the next line.
func TestReflowCascadeSpillsTrailingSegments(t *testing.T) {
	parts := map[int][]string{
		1: {seg("a", 20), seg("b", 20), seg("c", 20)},
	}
	// Line 1 budget = columns - timingSuffixReserve(15) - safetyMargin(5).
	// Width of 3 segs = 1 + 20 + 3 + 20 + 3 + 20 = 67. Columns 80 → budget 60,
	// so "c" spills to line 2; remaining 44 fits.
	out := buildStatuslineCascade(parts, 80)
	if len(out) != 2 {
		t.Fatalf("expected 2 physical lines, got %d: %q", len(out), out)
	}
	if !strings.Contains(out[0], "a") || !strings.Contains(out[0], "b") || strings.Contains(out[0], "c") {
		t.Errorf("line 1 wrong: %q", out[0])
	}
	if !strings.Contains(out[1], "c") {
		t.Errorf("line 2 should contain spilled segment: %q", out[1])
	}
}

// TestReflowCascadeBlankLineBeforeOverflowedLogicalLine: when overflow lands
// on a line that already had its own segments, a blank separator line is
// inserted before it.
func TestReflowCascadeBlankSeparator(t *testing.T) {
	parts := map[int][]string{
		1: {seg("a", 30), seg("b", 30)},
		2: {seg("m", 10)},
	}
	// Columns 60 → line-1 budget 40 → "b" spills onto logical line 2, which
	// already existed → blank line inserted between.
	out := buildStatuslineCascade(parts, 60)
	if len(out) != 3 {
		t.Fatalf("expected 3 physical lines (incl. blank), got %d: %q", len(out), out)
	}
	if out[1] != "" {
		t.Errorf("expected blank separator at index 1, got %q", out[1])
	}
}

// TestReflowCascadeNoColumns: zero columns disables reflow entirely.
func TestReflowCascadeNoColumns(t *testing.T) {
	parts := map[int][]string{
		1: {seg("a", 100), seg("b", 100)},
	}
	out := buildStatuslineCascade(parts, 0)
	if len(out) != 1 {
		t.Fatalf("expected 1 line with no reflow, got %d", len(out))
	}
}

// TestReflowGroupKeepsLogicalLineBoundaries: group mode never mixes segments
// from different logical lines onto one physical line.
func TestReflowGroupKeepsLogicalLineBoundaries(t *testing.T) {
	parts := map[int][]string{
		1: {seg("a", 10), seg("b", 10)},
		2: {seg("m", 10), seg("n", 10)},
	}
	// Wide terminal: each logical line fits on its own physical line, and
	// line 2's segments must not join line 1.
	out := buildStatuslineGroup(parts, 200)
	if len(out) != 2 {
		t.Fatalf("expected 2 physical lines, got %d: %q", len(out), out)
	}
	if strings.Contains(out[0], "m") || strings.Contains(out[1], "a") {
		t.Errorf("logical lines mixed: %q", out)
	}

	// Narrow terminal: logical line 1 wraps into two physical lines; line 2
	// still starts on its own physical line.
	out = buildStatuslineGroup(parts, 32)
	if len(out) != 3 {
		t.Fatalf("expected 3 physical lines, got %d: %q", len(out), out)
	}
	if !strings.Contains(out[2], "m") || !strings.Contains(out[2], "n") {
		t.Errorf("logical line 2 should be intact on last physical line: %q", out)
	}
}
