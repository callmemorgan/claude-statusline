package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/plugins"
	"github.com/callmemorgan/claude-statusline/internal/render"
	"github.com/callmemorgan/claude-statusline/internal/segments"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/update"
)

// The TUI preview must demonstrate every feature, including ones whose real
// data source (session history, payload fields) the sample payload can't
// carry on its own.

// renderWithState renders one segment with synthetic session state for tests.
func renderWithState(t testing.TB, id string, p payload.Payload, st *state.SessionState, now time.Time, overrides map[string]any) string {
	t.Helper()
	segments.Init()
	seg, ok := segments.ByID(id)
	if !ok {
		t.Fatalf("no segment %q", id)
	}
	cfg := config.Config{}
	if overrides != nil {
		cfg.Settings = map[string]map[string]any{id: overrides}
	}
	out, _ := seg.Render(segments.RenderCtx{P: p, S: config.SettingsFor(cfg, seg.ID, seg.Settings), State: st, Now: now})
	return out
}

func TestPreviewStateRendersStateFeatures(t *testing.T) {
	// samplePayload computes resets_at from the wall clock, so this test
	// must too (the TUI runs both against time.Now()).
	now := time.Now()
	st := previewState(now)
	p := payload.SamplePayload()

	out := renderWithState(t, "cost-rate", p, st, now, nil)
	if !strings.Contains(out, "$0.42/h") {
		t.Errorf("cost-rate with previewState = %q, want $0.42/h", out)
	}

	// 16%/h ending at 50%, reset in 2h30m → ~90% (sub-second skew between
	// this test's clock and samplePayload's wobbles the floor by 1).
	out = renderWithState(t, "rate-limit-5h", p, st, now, nil)
	m := regexp.MustCompile(`→(\d+)%`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("rate-limit-5h with previewState = %q, want a →NN%% projection", out)
	}
	if v, _ := strconv.Atoi(m[1]); v < 88 || v > 92 {
		t.Errorf("rate-limit-5h projection = %s%%, want ~90%%", m[1])
	}

	// The hour of history clears the 7d projection's 45-minute minimum span.
	out = renderWithState(t, "rate-limit-7d", p, st, now, nil)
	if !strings.Contains(out, "→") {
		t.Errorf("rate-limit-7d with previewState = %q, want a projection", out)
	}
	for _, id := range []string{"rate-limit-fable", "rate-limit-sonnet", "rate-limit-opus"} {
		out = renderWithState(t, id, p, st, now, nil)
		if !strings.Contains(out, "→") {
			t.Errorf("%s with previewState = %q, want a projection", id, out)
		}
	}

	out = renderWithState(t, "context-window", p, st, now, nil)
	if !strings.Contains(out, "↗") {
		t.Errorf("context-window with previewState = %q, want ↗ trend", out)
	}
}

func TestSamplePayloadShowsNewSegments(t *testing.T) {
	segments.Init()
	plugins.Load(nil)
	segments.UpdateRenderer = update.RenderSegment

	p := payload.SamplePayload()
	for _, tc := range []struct{ id, want string }{
		{"output-style", "Explanatory"},
		{"added-dirs", "+1 dir"},
		{"email", "you@…"},
		{"rate-limit-fable", "Fable"},
		{"rate-limit-sonnet", "Sonnet"},
		{"rate-limit-opus", "Opus"},
	} {
		seg, ok := segments.ByID(tc.id)
		if !ok {
			t.Fatalf("no segment %q", tc.id)
		}
		out, show := seg.Render(segments.RenderCtx{P: p, S: config.SettingsFor(config.Config{}, seg.ID, seg.Settings), Now: time.Unix(1750000000, 0)})
		if !show {
			t.Errorf("%s hidden with samplePayload, want visible", tc.id)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s = %q, want substring %q", tc.id, out, tc.want)
		}
	}
}

func TestDemoPreviewPayload(t *testing.T) {
	// 1750000000000 is a multiple of the 5000ms sweep; +4500ms → pct 90.
	now := time.UnixMilli(1750000000000 + 4500)
	p := demoPreviewPayload(payload.SamplePayload(), now)

	if p.ContextWindow.UsedPercentage == nil || *p.ContextWindow.UsedPercentage != 90 {
		t.Errorf("ctx pct = %v, want 90", p.ContextWindow.UsedPercentage)
	}
	if p.Exceeds200K == nil || !*p.Exceeds200K {
		t.Errorf("Exceeds200K = %v, want true at 90%%", p.Exceeds200K)
	}
	if p.RateLimits.FiveHour.UsedPercentage == nil || *p.RateLimits.FiveHour.UsedPercentage != 90 {
		t.Errorf("5h pct = %v, want 90", p.RateLimits.FiveHour.UsedPercentage)
	}
	if got := *p.RateLimits.FiveHour.ResetsAt - now.Unix(); got != 1800 {
		t.Errorf("5h reset winds down with the bar: in %ds, want 1800s (10%% of 5h)", got)
	}
	if p.RateLimits.SevenDayOverageIncluded.UsedPercentage == nil || *p.RateLimits.SevenDayOverageIncluded.UsedPercentage != 90 {
		t.Errorf("fable pct = %v, want 90", p.RateLimits.SevenDayOverageIncluded.UsedPercentage)
	}
	if p.RateLimits.SevenDaySonnet.UsedPercentage == nil || *p.RateLimits.SevenDaySonnet.UsedPercentage != 90 {
		t.Errorf("sonnet pct = %v, want 90", p.RateLimits.SevenDaySonnet.UsedPercentage)
	}
	if p.RateLimits.SevenDayOpus.UsedPercentage == nil || *p.RateLimits.SevenDayOpus.UsedPercentage != 90 {
		t.Errorf("opus pct = %v, want 90", p.RateLimits.SevenDayOpus.UsedPercentage)
	}
	if p.Cost.TotalCostUSD != 2.25 {
		t.Errorf("cost = %v, want 2.25 (90%% of $2.50)", p.Cost.TotalCostUSD)
	}
	if p.Cost.TotalLinesAdded != 270 || p.Cost.TotalLinesRemoved != 90 {
		t.Errorf("lines = +%d/-%d, want +270/-90", p.Cost.TotalLinesAdded, p.Cost.TotalLinesRemoved)
	}
}

func TestPluginPreviewRendersInAssembler(t *testing.T) {
	segments.Init()

	// Drop a script that would produce "real" output when actually executed.
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho real-output\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	plugins.Load([]config.PluginDef{{
		ID:      "demo-plugin",
		Command: script,
		Preview: "preview-output",
	}})

	cfg := config.Config{Segments: []string{"demo-plugin"}}
	lines := render.Statusline(render.Input{
		P:       payload.SamplePayload(),
		C:       palette.CurrentPalette("classic", "", nil),
		Cfg:     cfg,
		Now:     time.Now(),
		Preview: true,
	})

	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "preview-output") {
		t.Errorf("assembler output = %q, want substring %q", joined, "preview-output")
	}
	if strings.Contains(joined, "real-output") {
		t.Errorf("assembler output should not execute plugin, got %q", joined)
	}
}
