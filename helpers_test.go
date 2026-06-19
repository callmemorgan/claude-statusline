package main

import (
	"github.com/callmemorgan/claude-statusline/internal/config"
	"strings"
	"testing"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/palette"
)

func ctxWindowSegment(t *testing.T) segmentInfo {
	t.Helper()
	initSegments(nil)
	seg, ok := segmentByID("context-window")
	if !ok {
		t.Fatal("context-window segment not registered")
	}
	return seg
}

func TestSettingsForDefaults(t *testing.T) {
	seg := ctxWindowSegment(t)
	s := config.SettingsFor(config.Config{}, seg.id, seg.settings)
	if !s.Bool("show_bar") || !s.Bool("show_warning") {
		t.Error("expected toggles to default to true")
	}
	if s.Int("bar_width") != 20 || s.Str("iconset") != "default" || s.Int("warn_at") != 60 || s.Int("crit_at") != 80 {
		t.Errorf("unexpected defaults: width=%d iconset=%q warn=%d crit=%d",
			s.Int("bar_width"), s.Str("iconset"), s.Int("warn_at"), s.Int("crit_at"))
	}
	if _, ok := s["show_countdown"]; ok {
		t.Error("context-window should not resolve a show_countdown setting")
	}
	if _, ok := s["stress_test"]; ok {
		t.Error("ephemeral specs must not appear in resolved settings")
	}
}

func TestSettingsForOverridesAndCoercion(t *testing.T) {
	seg := ctxWindowSegment(t)
	cfg := config.Config{Settings: map[string]map[string]any{"context-window": {
		"bar_width": float64(35), // JSON numbers decode as float64
		"iconset":   "nonsense",  // invalid enum value → default
		"warn_at":   999,         // out of range → clamped
		"show_bar":  "yes",       // wrong type → default
	}}}
	s := config.SettingsFor(cfg, seg.id, seg.settings)
	if s.Int("bar_width") != 35 {
		t.Errorf("float64 not coerced: %d", s.Int("bar_width"))
	}
	if s.Str("iconset") != "default" {
		t.Errorf("invalid enum should fall back to default: %q", s.Str("iconset"))
	}
	if s.Int("warn_at") != 100 {
		t.Errorf("out-of-range int should clamp: %d", s.Int("warn_at"))
	}
	if !s.Bool("show_bar") {
		t.Error("wrong-typed bool should fall back to default true")
	}
}

func TestPruneSettings(t *testing.T) {
	seg := ctxWindowSegment(t)
	s := config.SettingsFor(config.Config{}, seg.id, seg.settings)
	if got := config.PruneSettings(seg.settings, s); got != nil {
		t.Errorf("all-default settings should prune to nil, got %v", got)
	}
	s["bar_width"] = 35
	got := config.PruneSettings(seg.settings, s)
	if len(got) != 1 || got["bar_width"] != 35 {
		t.Errorf("expected only the changed key, got %v", got)
	}
}

func TestProgressBarFractional(t *testing.T) {
	// smooth at 25% of width 10: 20 of 80 units → 2 full cells + partial 4/8.
	got := progressBarWithIconset(25, "", "", palette.Palette{}, 10, "smooth")
	if got != "██▌       " {
		t.Errorf("smooth 25%%/10 = %q", got)
	}
	if got := progressBarWithIconset(0, "", "", palette.Palette{}, 10, "smooth"); got != "          " {
		t.Errorf("smooth 0%% = %q", got)
	}
	if got := progressBarWithIconset(100, "", "", palette.Palette{}, 10, "smooth"); got != "██████████" {
		t.Errorf("smooth 100%% = %q", got)
	}
	// Whole-cell sets are unchanged by the iconset refactor.
	if got := progressBarWithIconset(50, "", "", palette.Palette{}, 10, "blocks"); got != "█████░░░░░" {
		t.Errorf("blocks 50%% = %q", got)
	}
	// Unknown name falls back to default glyphs.
	if got := progressBarWithIconset(50, "", "", palette.Palette{}, 4, "nope"); got != "##--" {
		t.Errorf("fallback = %q", got)
	}
	// Every named set renders at the declared width.
	for _, name := range iconsetNames() {
		for _, pct := range []int{0, 33, 50, 99, 100} {
			if w := ansi.VisibleWidth(progressBarWithIconset(pct, "", "", palette.Palette{}, 20, name)); w != 20 {
				t.Errorf("iconset %q at %d%% has width %d, want 20", name, pct, w)
			}
		}
	}
}

func TestFilterSegments(t *testing.T) {
	initSegments(nil)
	all := registeredSegments
	if got := filterSegments(all, ""); len(got) != len(all) {
		t.Errorf("empty query should return all, got %d/%d", len(got), len(all))
	}
	got := filterSegments(all, "rate")
	for _, s := range got {
		if !strings.Contains(s.id, "rate") && !strings.Contains(strings.ToLower(s.desc), "rate") {
			t.Errorf("unexpected match %q", s.id)
		}
	}
	if len(got) < 3 { // rate-limit-5h, rate-limit-7d, cost-rate
		t.Errorf("expected at least 3 'rate' matches, got %d", len(got))
	}
	if got := filterSegments(all, "GIT"); len(got) == 0 {
		t.Error("filter should be case-insensitive")
	}
	if got := filterSegments(all, "zzzznope"); len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}

func TestFooterRows(t *testing.T) {
	long := footerText("main")
	if got := footerRows(long, 0); got != 1 {
		t.Errorf("zero width = %d rows, want 1", got)
	}
	if got := footerRows(long, len(long)+10); got != 1 {
		t.Errorf("wide terminal = %d rows, want 1", got)
	}
	if got := footerRows(long, 100); got < 2 {
		t.Errorf("main footer at 100 cols = %d rows, want ≥2 (len %d)", got, len(long))
	}
	if got := footerRows(long, 10); got != 3 {
		t.Errorf("pathological width = %d rows, want clamp at 3", got)
	}
}
