package main

import (
	"slices"
	"strings"
	"testing"
)

func TestCanvasMoveVertUpToEmptyLine(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"model", "duration", "cost"},
		Lines:    map[string]int{"model": 3, "duration": 3, "cost": 3},
	}

	// Move "cost" up from line 3 to line 2, which is empty.
	// It should land before the first segment on the next occupied line above
	// (line 1 has no segment, so it goes to the head).
	if !canvasMoveVert(&cfg, "cost", -1) {
		t.Fatal("expected move to succeed")
	}
	want := []string{"cost", "model", "duration"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments = %v, want %v", cfg.Segments, want)
	}
	if cfg.Lines["cost"] != 2 {
		t.Errorf("cost line = %d, want 2", cfg.Lines["cost"])
	}
}

func TestCanvasMoveVertUpToEmptyLineWithHigherOccupiedLine(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"model", "duration", "cost", "tokens"},
		Lines:    map[string]int{"model": 1, "duration": 3, "cost": 3, "tokens": 3},
	}

	// Move "tokens" up from line 3 to line 2 (empty). The next occupied line
	// above is line 1, so it should land before "model".
	if !canvasMoveVert(&cfg, "tokens", -1) {
		t.Fatal("expected move to succeed")
	}
	want := []string{"tokens", "model", "duration", "cost"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments = %v, want %v", cfg.Segments, want)
	}
}

func TestCanvasMoveVertDownToEmptyLine(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"model", "duration", "cost"},
		Lines:    map[string]int{"model": 1, "duration": 1, "cost": 1},
	}

	// Move "cost" down from line 1 to line 2, which is empty. It should land
	// at the end of the list (the existing "keep it where it was-ish" rule).
	if !canvasMoveVert(&cfg, "cost", 1) {
		t.Fatal("expected move to succeed")
	}
	want := []string{"model", "duration", "cost"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments = %v, want %v", cfg.Segments, want)
	}
	if cfg.Lines["cost"] != 2 {
		t.Errorf("cost line = %d, want 2", cfg.Lines["cost"])
	}
}

func TestCanvasMoveVertDropsOverrideAtNaturalLine(t *testing.T) {
	initSegments(nil)
	// "model" has natural line 2.
	cfg := config{
		Segments: []string{"model"},
		Lines:    map[string]int{"model": 3},
	}

	if !canvasMoveVert(&cfg, "model", -1) {
		t.Fatal("expected move to succeed")
	}
	if _, ok := cfg.Lines["model"]; ok {
		t.Errorf("override should be dropped when returned to natural line 2")
	}
}

func TestCanvasMoveVertClamps(t *testing.T) {
	initSegments(nil)
	cfg := config{Segments: []string{"model"}, Lines: map[string]int{"model": 1}}
	if canvasMoveVert(&cfg, "model", -1) {
		t.Error("expected up from line 1 to be a no-op")
	}
	cfg.Lines["model"] = 9
	if canvasMoveVert(&cfg, "model", 1) {
		t.Error("expected down from line 9 to be a no-op")
	}
}

func TestCanvasMoveVertUnknownID(t *testing.T) {
	initSegments(nil)
	cfg := config{Segments: []string{"model"}}
	if canvasMoveVert(&cfg, "not-a-segment", -1) {
		t.Error("expected unknown id to be a no-op")
	}
}

func TestCanvasReorderHoriz(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"model", "duration", "cost"},
		Lines:    map[string]int{"model": 2, "duration": 2, "cost": 2},
	}

	if !canvasReorderHoriz(&cfg, "duration", 1) {
		t.Fatal("expected reorder to succeed")
	}
	want := []string{"model", "cost", "duration"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments = %v, want %v", cfg.Segments, want)
	}

	if !canvasReorderHoriz(&cfg, "cost", -1) {
		t.Fatal("expected reorder to succeed")
	}
	want = []string{"cost", "model", "duration"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments = %v, want %v", cfg.Segments, want)
	}
}

func TestCanvasReorderHorizNoOpAtEdges(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"model", "duration", "cost"},
		Lines:    map[string]int{"model": 2, "duration": 2, "cost": 2},
	}

	if canvasReorderHoriz(&cfg, "model", -1) {
		t.Error("expected reorder left of first item to be a no-op")
	}
	if canvasReorderHoriz(&cfg, "cost", 1) {
		t.Error("expected reorder right of last item to be a no-op")
	}
	want := []string{"model", "duration", "cost"}
	if !slices.Equal(cfg.Segments, want) {
		t.Errorf("segments changed unexpectedly: %v", cfg.Segments)
	}
}

func TestCanvasReorderHorizUnknownID(t *testing.T) {
	initSegments(nil)
	cfg := config{Segments: []string{"model"}}
	if canvasReorderHoriz(&cfg, "not-a-segment", 1) {
		t.Error("expected unknown id to be a no-op")
	}
}

func TestFilterSegmentsNarrowsBySubstring(t *testing.T) {
	initSegments(nil)
	all := registeredSegments

	got := filterSegments(all, "git")
	for _, s := range got {
		if !strings.Contains(s.id, "git") && !strings.Contains(strings.ToLower(s.desc), "git") {
			t.Errorf("unexpected match %q for 'git'", s.id)
		}
	}
	if len(got) < 2 {
		t.Errorf("expected at least 2 'git' matches, got %d", len(got))
	}

	if got := filterSegments(all, "zzzznope"); len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}
