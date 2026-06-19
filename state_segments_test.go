package main

import (
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/state"
)

// rlState builds session history where the 5h rate limit climbs `perHour`
// %/h ending at `last` percent, and cost climbs costPerHour $/h ending at
// lastCost, over the trailing `span`.
func rlState(now time.Time, span time.Duration, perHour, last, costPerHour, lastCost float64) *state.SessionState {
	st := &state.SessionState{}
	const n = 10
	for i := 0; i <= n; i++ {
		frac := float64(i) / n
		ts := now.Add(-time.Duration((1 - frac) * float64(span)))
		hoursBack := (1 - frac) * span.Hours()
		rl := last - perHour*hoursBack
		ctxPct := rl // reuse the same ramp for ctx
		st.Samples = append(st.Samples, state.Sample{
			T:      ts.Unix(),
			Cost:   lastCost - costPerHour*hoursBack,
			CtxPct: ctxPct,
			RL5h:   &rl,
		})
	}
	return st
}

func renderWithState(t *testing.T, id string, p payload.Payload, st *state.SessionState, now time.Time, overrides map[string]any) string {
	t.Helper()
	initSegments(nil)
	seg, ok := segmentByID(id)
	if !ok {
		t.Fatalf("no segment %q", id)
	}
	cfg := config{}
	if overrides != nil {
		cfg.Settings = map[string]map[string]any{id: overrides}
	}
	out, _ := seg.render(renderCtx{P: p, S: settingsFor(cfg, seg), State: st, Now: now})
	return out
}

func TestRateLimitProjection(t *testing.T) {
	now := time.Unix(1750000000, 0)
	resetAt := now.Add(2 * time.Hour).Unix()
	pct := 40.0
	var p payload.Payload
	p.RateLimits.FiveHour = payload.LimitWindow{UsedPercentage: &pct, ResetsAt: &resetAt}

	// 10%/h ending at 40% → projected 60% at reset in 2h.
	st := rlState(now, 30*time.Minute, 10, 40, 0, 0)
	out := renderWithState(t, "rate-limit-5h", p, st, now, nil)
	if !strings.Contains(out, "→60%") {
		t.Errorf("expected →60%% projection, got %q", out)
	}

	// No state → no projection.
	out = renderWithState(t, "rate-limit-5h", p, nil, now, nil)
	if strings.Contains(out, "→") {
		t.Errorf("projection without state: %q", out)
	}

	// Flat usage → no projection.
	flat := rlState(now, 30*time.Minute, 0, 40, 0, 0)
	out = renderWithState(t, "rate-limit-5h", p, flat, now, nil)
	if strings.Contains(out, "→") {
		t.Errorf("projection with flat usage: %q", out)
	}

	// Too little history → no projection.
	short := rlState(now, 2*time.Minute, 10, 40, 0, 0)
	out = renderWithState(t, "rate-limit-5h", p, short, now, nil)
	if strings.Contains(out, "→") {
		t.Errorf("projection with 2min history: %q", out)
	}

	// Toggle off.
	out = renderWithState(t, "rate-limit-5h", p, st, now, map[string]any{"show_projection": false})
	if strings.Contains(out, "→") {
		t.Errorf("projection despite show_projection=false: %q", out)
	}

	// Steep burn projecting past 100% still renders (callers see →130%).
	steep := rlState(now, 30*time.Minute, 45, 40, 0, 0)
	out = renderWithState(t, "rate-limit-5h", p, steep, now, nil)
	if !strings.Contains(out, "→130%") {
		t.Errorf("expected →130%%, got %q", out)
	}
}

func TestCostRateSegment(t *testing.T) {
	now := time.Unix(1750000000, 0)
	st := rlState(now, 30*time.Minute, 0, 0, 1.50, 2.0)
	out := renderWithState(t, "cost-rate", payload.Payload{}, st, now, nil)
	if out != "$1.50/h" {
		t.Errorf("cost-rate = %q", out)
	}

	if out := renderWithState(t, "cost-rate", payload.Payload{}, nil, now, nil); out != "" {
		t.Errorf("cost-rate without state = %q", out)
	}
	// Negligible rate hides.
	tiny := rlState(now, 30*time.Minute, 0, 0, 0.001, 0.01)
	if out := renderWithState(t, "cost-rate", payload.Payload{}, tiny, now, nil); out != "" {
		t.Errorf("cost-rate with negligible burn = %q", out)
	}
}

func TestContextTrend(t *testing.T) {
	now := time.Unix(1750000000, 0)
	pct := 50.0
	var p payload.Payload
	p.ContextWindow.UsedPercentage = &pct
	p.ContextWindow.ContextWindowSize = 200000

	// Growing 20%/h at 50%, compact at 80% → ETA 1h30m.
	growing := rlState(now, 15*time.Minute, 20, 50, 0, 0)
	out := renderWithState(t, "context-window", p, growing, now, nil)
	if !strings.Contains(out, "↗ ~1h30m") {
		t.Errorf("expected ↗ ~1h30m, got %q", out)
	}

	// Shrinking (post-compact) → ↘ with no ETA.
	shrinking := rlState(now, 15*time.Minute, -20, 50, 0, 0)
	out = renderWithState(t, "context-window", p, shrinking, now, nil)
	if !strings.Contains(out, "↘") || strings.Contains(out, "~") {
		t.Errorf("expected bare ↘, got %q", out)
	}

	// Flat → no arrow.
	flat := rlState(now, 15*time.Minute, 1, 50, 0, 0)
	out = renderWithState(t, "context-window", p, flat, now, nil)
	if strings.Contains(out, "↗") || strings.Contains(out, "↘") {
		t.Errorf("expected no trend when flat, got %q", out)
	}

	// Already past compact threshold → arrow without ETA.
	pct90 := 90.0
	p.ContextWindow.UsedPercentage = &pct90
	past := rlState(now, 15*time.Minute, 20, 90, 0, 0)
	out = renderWithState(t, "context-window", p, past, now, nil)
	if !strings.Contains(out, "↗") || strings.Contains(out, "~") {
		t.Errorf("expected ↗ without ETA past compact_at, got %q", out)
	}
}

func TestShortDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:            "1m",
		35 * time.Minute:            "35m",
		80 * time.Minute:            "1h20m",
		2*time.Hour + 5*time.Minute: "2h05m",
	}
	for d, want := range cases {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
