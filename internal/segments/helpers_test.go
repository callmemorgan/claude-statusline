package segments

import (
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
)

func ctxWindowSegment(t *testing.T) Info {
	t.Helper()
	Init()
	seg, ok := ByID("context-window")
	if !ok {
		t.Fatal("context-window segment not registered")
	}
	return seg
}

func TestSettingsForDefaults(t *testing.T) {
	seg := ctxWindowSegment(t)
	s := config.SettingsFor(config.Config{}, seg.ID, seg.Settings)
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
	s := config.SettingsFor(cfg, seg.ID, seg.Settings)
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
	s := config.SettingsFor(config.Config{}, seg.ID, seg.Settings)
	if got := config.PruneSettings(seg.Settings, s); got != nil {
		t.Errorf("all-default settings should prune to nil, got %v", got)
	}
	s["bar_width"] = 35
	got := config.PruneSettings(seg.Settings, s)
	if len(got) != 1 || got["bar_width"] != 35 {
		t.Errorf("expected only the changed key, got %v", got)
	}
}

func TestProgressBarFractional(t *testing.T) {
	// smooth at 25% of width 10: 20 of 80 units → 2 full cells + partial 4/8.
	got := ProgressBarWithIconset(25, "", "", palette.Palette{}, 10, "smooth")
	if got != "██▌       " {
		t.Errorf("smooth 25%%/10 = %q", got)
	}
	if got := ProgressBarWithIconset(0, "", "", palette.Palette{}, 10, "smooth"); got != "          " {
		t.Errorf("smooth 0%% = %q", got)
	}
	if got := ProgressBarWithIconset(100, "", "", palette.Palette{}, 10, "smooth"); got != "██████████" {
		t.Errorf("smooth 100%% = %q", got)
	}
	// Whole-cell sets are unchanged by the iconset refactor.
	if got := ProgressBarWithIconset(50, "", "", palette.Palette{}, 10, "blocks"); got != "█████░░░░░" {
		t.Errorf("blocks 50%% = %q", got)
	}
	// Unknown name falls back to default glyphs.
	if got := ProgressBarWithIconset(50, "", "", palette.Palette{}, 4, "nope"); got != "##--" {
		t.Errorf("fallback = %q", got)
	}
	// Every named set renders at the declared width.
	for _, name := range IconsetNames() {
		for _, pct := range []int{0, 33, 50, 99, 100} {
			if w := ansi.VisibleWidth(ProgressBarWithIconset(pct, "", "", palette.Palette{}, 20, name)); w != 20 {
				t.Errorf("iconset %q at %d%% has width %d, want 20", name, pct, w)
			}
		}
	}
}

var testNow = time.Unix(1750000000, 0)

func TestNewPayloadSegments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	Init()
	render := func(p payload.Payload, id string) (string, bool) {
		seg, ok := ByID(id)
		if !ok {
			t.Fatalf("segment %q not registered", id)
		}
		return seg.Render(RenderCtx{P: p, Now: testNow})
	}

	var p payload.Payload
	if _, show := render(p, "output-style"); show {
		t.Error("output-style should hide with no payload data")
	}
	p.OutputStyle.Name = "default"
	if _, show := render(p, "output-style"); show {
		t.Error("output-style should hide when style is default")
	}
	p.OutputStyle.Name = "Explanatory"
	if got, show := render(p, "output-style"); !show || got != "✎ Explanatory" {
		t.Errorf("output-style = %q, %v", got, show)
	}

	if _, show := render(p, "added-dirs"); show {
		t.Error("added-dirs should hide when empty")
	}
	p.Workspace.AddedDirs = []string{"/a"}
	if got, _ := render(p, "added-dirs"); got != "+1 dir" {
		t.Errorf("added-dirs singular = %q", got)
	}
	p.Workspace.AddedDirs = []string{"/a", "/b"}
	if got, _ := render(p, "added-dirs"); got != "+2 dirs" {
		t.Errorf("added-dirs plural = %q", got)
	}

	if _, show := render(p, "email"); show {
		t.Error("email should hide when empty")
	}
	p.Email = "morgan@skyslope.com"
	if got, _ := render(p, "email"); got != "morgan@…" {
		t.Errorf("email = %q", got)
	}
}

func TestFilterSegments(t *testing.T) {
	Init()
	all := All()
	if got := Filter(all, ""); len(got) != len(all) {
		t.Errorf("empty query should return all, got %d/%d", len(got), len(all))
	}
	got := Filter(all, "rate")
	for _, s := range got {
		if !strings.Contains(s.ID, "rate") && !strings.Contains(strings.ToLower(s.Desc), "rate") {
			t.Errorf("unexpected match %q", s.ID)
		}
	}
	if len(got) < 6 { // rate-limit-5h/7d/fable/sonnet/opus + cost-rate
		t.Errorf("expected at least 6 'rate' matches, got %d", len(got))
	}
	if got := Filter(all, "GIT"); len(got) == 0 {
		t.Error("filter should be case-insensitive")
	}
	if got := Filter(all, "zzzznope"); len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}
