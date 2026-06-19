package main

import (
	"github.com/callmemorgan/claude-statusline/internal/config"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/payload"
)

// The TUI preview must demonstrate every feature, including ones whose real
// data source (session history, payload fields) the sample payload can't
// carry on its own.

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

	out = renderWithState(t, "context-window", p, st, now, nil)
	if !strings.Contains(out, "↗") {
		t.Errorf("context-window with previewState = %q, want ↗ trend", out)
	}
}

func TestSamplePayloadShowsNewSegments(t *testing.T) {
	initSegments(nil)
	p := payload.SamplePayload()
	for _, tc := range []struct{ id, want string }{
		{"output-style", "Explanatory"},
		{"added-dirs", "+1 dir"},
		{"email", "you@…"},
	} {
		seg, ok := segmentByID(tc.id)
		if !ok {
			t.Fatalf("no segment %q", tc.id)
		}
		out, show := seg.render(renderCtx{P: p, S: config.SettingsFor(config.Config{}, seg.id, seg.settings), Now: time.Unix(1750000000, 0)})
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
	if p.Cost.TotalCostUSD != 2.25 {
		t.Errorf("cost = %v, want 2.25 (90%% of $2.50)", p.Cost.TotalCostUSD)
	}
	if p.Cost.TotalLinesAdded != 270 || p.Cost.TotalLinesRemoved != 90 {
		t.Errorf("lines = +%d/-%d, want +270/-90", p.Cost.TotalLinesAdded, p.Cost.TotalLinesRemoved)
	}
}
