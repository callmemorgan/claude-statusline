package main

import "testing"

func TestAssignSegmentLine(t *testing.T) {
	initSegments(nil)
	// "model" has a known natural line; pick whatever it is.
	seg, ok := segmentByID("model")
	if !ok {
		t.Fatal("no model segment")
	}
	natural := seg.line

	cfg := config{}
	// Assigning a non-natural line records an override.
	assignSegmentLine(&cfg, "model", natural+1)
	if got := cfg.Lines["model"]; got != natural+1 {
		t.Fatalf("override = %d, want %d", got, natural+1)
	}
	// Assigning back to the natural line deletes the override (minimal config).
	assignSegmentLine(&cfg, "model", natural)
	if _, present := cfg.Lines["model"]; present {
		t.Fatalf("natural-line assignment should delete the override, got %v", cfg.Lines)
	}
}
