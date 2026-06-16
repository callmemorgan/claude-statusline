package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── reconstructAt ───────────────────────────────────────────────────

func sampleHistory() []sample {
	rl5h := func(v float64) *float64 { return &v }
	base := time.Unix(1750000000, 0)
	return []sample{
		{T: base.Unix(), Cost: 0.0, CtxPct: 40, InTok: 1000, OutTok: 100, RL5h: rl5h(34)},
		{T: base.Add(5 * time.Minute).Unix(), Cost: 0.10, CtxPct: 45, InTok: 2000, OutTok: 200, RL5h: rl5h(38)},
		{T: base.Add(10 * time.Minute).Unix(), Cost: 0.20, CtxPct: 50, InTok: 3000, OutTok: 300, RL5h: rl5h(42)},
	}
}

func TestReconstructAtTruncatesHistory(t *testing.T) {
	samples := sampleHistory()
	anchors := anchorsFor(samples)
	base := samplePayload()

	for idx := range samples {
		_, st, now := reconstructAt(samples, idx, base, anchors)
		if got := len(st.Samples); got != idx+1 {
			t.Errorf("idx %d: state has %d samples, want %d", idx, got, idx+1)
		}
		wantNow := time.Unix(samples[idx].T, 0)
		if !now.Equal(wantNow) {
			t.Errorf("idx %d: now = %v, want %v", idx, now, wantNow)
		}
		// The truncated history's last sample must be this index's sample.
		if last := st.Samples[len(st.Samples)-1]; last.T != samples[idx].T {
			t.Errorf("idx %d: last sample T = %d, want %d", idx, last.T, samples[idx].T)
		}
	}
}

func TestReconstructAtDoesNotAliasOrMutate(t *testing.T) {
	samples := sampleHistory()
	anchors := anchorsFor(samples)
	_, st, _ := reconstructAt(samples, 1, samplePayload(), anchors)
	// Mutating the reconstructed state must not touch the source slice.
	st.Samples[0].Cost = 999
	if samples[0].Cost == 999 {
		t.Fatal("reconstructAt aliased the source samples backing array")
	}
}

func TestReconstructAtLayersSampledFields(t *testing.T) {
	samples := sampleHistory()
	anchors := anchorsFor(samples)
	p, _, _ := reconstructAt(samples, 2, samplePayload(), anchors)

	if p.Cost.TotalCostUSD != 0.20 {
		t.Errorf("cost = %v, want 0.20", p.Cost.TotalCostUSD)
	}
	if p.ContextWindow.UsedPercentage == nil || *p.ContextWindow.UsedPercentage != 50 {
		t.Errorf("ctx pct = %v, want 50", p.ContextWindow.UsedPercentage)
	}
	if p.ContextWindow.TotalInputTokens != 3000 || p.ContextWindow.TotalOutputTokens != 300 {
		t.Errorf("tokens = %d/%d, want 3000/300", p.ContextWindow.TotalInputTokens, p.ContextWindow.TotalOutputTokens)
	}
	if p.RateLimits.FiveHour.UsedPercentage == nil || *p.RateLimits.FiveHour.UsedPercentage != 42 {
		t.Errorf("rl5h = %v, want 42", p.RateLimits.FiveHour.UsedPercentage)
	}
	if p.RateLimits.FiveHour.ResetsAt == nil || *p.RateLimits.FiveHour.ResetsAt != anchors.fiveHour {
		t.Errorf("rl5h reset = %v, want %d", p.RateLimits.FiveHour.ResetsAt, anchors.fiveHour)
	}
}

// A sample that never carried a 7d rate limit must reconstruct as absent, so
// the rate-limit-7d segment auto-hides rather than reading as a hard 0%.
func TestReconstructAtPreservesAbsentRateLimit(t *testing.T) {
	samples := sampleHistory() // none of these carry RL7d
	anchors := anchorsFor(samples)
	p, _, _ := reconstructAt(samples, 1, samplePayload(), anchors)

	if p.RateLimits.SevenDay.UsedPercentage != nil {
		t.Errorf("rl7d = %v, want nil (absent in samples)", p.RateLimits.SevenDay.UsedPercentage)
	}
	if p.RateLimits.SevenDay.ResetsAt != nil {
		t.Errorf("rl7d reset = %v, want nil", p.RateLimits.SevenDay.ResetsAt)
	}
}

func TestReconstructAtClampsIndex(t *testing.T) {
	samples := sampleHistory()
	anchors := anchorsFor(samples)
	base := samplePayload()

	_, stLow, nowLow := reconstructAt(samples, -5, base, anchors)
	if len(stLow.Samples) != 1 || !nowLow.Equal(time.Unix(samples[0].T, 0)) {
		t.Errorf("negative index did not clamp to first frame: %d samples", len(stLow.Samples))
	}
	_, stHigh, nowHigh := reconstructAt(samples, 99, base, anchors)
	if len(stHigh.Samples) != len(samples) || !nowHigh.Equal(time.Unix(samples[len(samples)-1].T, 0)) {
		t.Errorf("over-range index did not clamp to last frame: %d samples", len(stHigh.Samples))
	}
}

func TestReconstructAtEmpty(t *testing.T) {
	p, st, now := reconstructAt(nil, 0, samplePayload(), resetAnchors{})
	if st == nil || len(st.Samples) != 0 {
		t.Errorf("empty reconstruct should yield empty state, got %v", st)
	}
	if p.Cost.TotalCostUSD != samplePayload().Cost.TotalCostUSD {
		t.Error("empty reconstruct should return the base payload unchanged")
	}
	if now.IsZero() {
		t.Error("empty reconstruct now should be non-zero")
	}
}

// ─── replay segments come alive across the scrub ─────────────────────

// The whole point of the scrubber: the trend/projection/rate segments must
// render at the *real* builder over the reconstructed frames. This asserts
// the late frames of the synthetic rising session produce cost-rate, a
// context trend, and a rate-limit projection — through buildStatusline, the
// same function the render path uses.
func TestReplayFramesProduceTrendFeatures(t *testing.T) {
	initSegments(nil)
	now := time.Unix(1750000000, 0)
	sess := syntheticReplaySession(now)
	anchors := anchorsFor(sess.Samples)
	base := samplePayload()
	cfg := config{Segments: []string{"cost-rate", "context-window", "rate-limit-5h"}}

	// Last frame has the most history → all features should be live.
	last := len(sess.Samples) - 1
	p, st, frameNow := reconstructAt(sess.Samples, last, base, anchors)
	lines := buildStatusline(buildInput{P: p, C: palette{}, Cfg: cfg, State: st, Width: 0, Now: frameNow})
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "/h") {
		t.Errorf("late frame missing cost-rate (/h): %q", joined)
	}
	if !strings.Contains(joined, "↗") {
		t.Errorf("late frame missing context trend (↗): %q", joined)
	}
	if !strings.Contains(joined, "→") {
		t.Errorf("late frame missing rate-limit projection (→): %q", joined)
	}

	// First frame has no history → no trend/projection (they gate on span).
	p0, st0, frameNow0 := reconstructAt(sess.Samples, 0, base, anchors)
	lines0 := buildStatusline(buildInput{P: p0, C: palette{}, Cfg: cfg, State: st0, Width: 0, Now: frameNow0})
	joined0 := strings.Join(lines0, "\n")
	if strings.Contains(joined0, "↗") || strings.Contains(joined0, "→") {
		t.Errorf("first frame should have no trend/projection yet: %q", joined0)
	}
}

// ─── session listing ─────────────────────────────────────────────────

func writeSessionFile(t *testing.T, dir, id string, samples []sample) {
	t.Helper()
	st := sessionState{SessionID: id, Samples: samples}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sanitizeSessionID(id)+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListReplaySessionsReadsRecorded(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	dir := stateDir()

	writeSessionFile(t, dir, "older-session", sampleHistory())
	// A second session, written after, with more samples.
	time.Sleep(10 * time.Millisecond)
	writeSessionFile(t, dir, "newer-session", append(sampleHistory(), sample{T: 1750000999, Cost: 0.5}))

	sessions := listReplaySessions(time.Now())
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	// Newest (by mtime) first.
	if sessions[0].ID != "newer-session" {
		t.Errorf("first session = %q, want newer-session (newest mtime first)", sessions[0].ID)
	}
	if sessions[0].synth {
		t.Error("recorded session marked synthetic")
	}
}

func TestListReplaySessionsFallsBackToSynthetic(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)

	sessions := listReplaySessions(time.Now())
	if len(sessions) != 1 || !sessions[0].synth {
		t.Fatalf("with no recorded sessions, want one synthetic fallback, got %d (synth=%v)",
			len(sessions), len(sessions) > 0 && sessions[0].synth)
	}
	if len(sessions[0].Samples) == 0 {
		t.Error("synthetic fallback has no samples")
	}
}

func TestReadSessionFileBypassesRetention(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	dir := stateDir()

	// A sample far older than the 48h retention window loadState would prune.
	old := time.Now().Add(-200 * time.Hour).Unix()
	writeSessionFile(t, dir, "ancient", []sample{
		{T: old, Cost: 0.1, CtxPct: 20},
		{T: old + 300, Cost: 0.2, CtxPct: 25},
	})

	st, err := readSessionFile(filepath.Join(dir, "ancient.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Samples) != 2 {
		t.Errorf("readSessionFile pruned old samples (%d), should keep all for replay", len(st.Samples))
	}
}

func TestReplayDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{12 * time.Minute, "12m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := replayDuration(c.d); got != c.want {
			t.Errorf("replayDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
