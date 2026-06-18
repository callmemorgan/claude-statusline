package main

import (
	"strings"
	"time"
)

// ─── Scenario Matrix ─────────────────────────────────────────────────
//
// A scenario is a concrete (payload, state, width, reflow) tuple that the REAL
// build function renders. The matrix shows how a single config behaves across
// many runtime conditions at once — segments auto-hiding when their data is
// missing, and reflow degrading as the terminal narrows — which a single
// synthetic preview frame hides.
//
// Every scenario renders through buildStatusline (render.go); nothing here
// fakes a rendered string. Generation is pure and deterministic given a clock,
// so it is unit-tested in scenarios_test.go.

// scenarioNow is the fixed clock the matrix renders against, mirroring the
// goldens' testNow so countdowns/projections are deterministic regardless of
// wall time. Fixtures derive their resets_at relative to this instant.
func scenarioNow() time.Time { return time.Unix(1750000000, 0) }

// scenario is one pane of the matrix: a labelled (payload, state, width,
// reflow) tuple plus a short note on what condition it exercises.
type scenario struct {
	Name   string        // pane label, e.g. "wide · full"
	Note   string        // one-line description of the condition under test
	P      payload       // the payload to render
	State  *sessionState // optional history (nil = no state-derived features)
	Width  int           // render width in columns (drives reflow)
	Reflow string        // "", "off", "cascade", "group" — overrides cfg.Reflow
}

// ─── Payload builders ────────────────────────────────────────────────
//
// Each builder starts from samplePayload() (fully populated) and overrides the
// fields that define the condition. Because segments auto-hide on missing/zero
// data, zeroing a field is how a scenario makes a segment disappear.

// scenarioBasePayload is the deterministic base every scenario derives from. It
// is samplePayload() with the clock-relative rate-limit resets re-pinned to
// scenarioNow so the matrix renders identically run-to-run.
func scenarioBasePayload(now time.Time) payload {
	p := samplePayload()
	reset5h := now.Unix() + 3600*2 + 1800
	reset7d := now.Unix() + 86400*3 + 3600*4
	p.RateLimits.FiveHour.ResetsAt = &reset5h
	p.RateLimits.SevenDay.ResetsAt = &reset7d
	return p
}

// payloadFull is the fully-populated base — every segment has data.
func payloadFull(now time.Time) payload { return scenarioBasePayload(now) }

// payloadNoGit drops the git branch/worktree so git-branch and git-stash hide.
func payloadNoGit(now time.Time) payload {
	p := scenarioBasePayload(now)
	p.Worktree = worktree{}
	p.Workspace.GitWorktree = ""
	return p
}

// payloadHighCost models an expensive, long session.
func payloadHighCost(now time.Time) payload {
	p := scenarioBasePayload(now)
	p.Cost.TotalCostUSD = 18.73
	p.Cost.TotalLinesAdded = 4210
	p.Cost.TotalLinesRemoved = 1880
	p.Cost.TotalDurationMS = 3 * 3600 * 1000
	return p
}

// payloadNearLimit pins the 5-hour quota near exhaustion with a soon reset, so
// the rate-limit bar renders hot with a short countdown.
func payloadNearLimit(now time.Time) payload {
	p := scenarioBasePayload(now)
	hot := 96.0
	warm := 71.0
	soon := now.Unix() + 540 // ~9 minutes
	p.RateLimits.FiveHour.UsedPercentage = &hot
	p.RateLimits.FiveHour.ResetsAt = &soon
	p.RateLimits.SevenDay.UsedPercentage = &warm
	ctx := 92.0
	p.ContextWindow.UsedPercentage = &ctx
	return p
}

// payloadMinimal models a brand-new session: no model name, no cost, no git,
// no usage data. Only segments that always have data survive.
func payloadMinimal(now time.Time) payload {
	return payload{
		Workspace: workspace{CurrentDir: "/Users/me/code/fresh"},
	}
}

// payloadFresh is a started-but-empty session: a model and dir, but zero cost,
// no context %, no rate limits — exercises the "fresh / nothing burned yet"
// auto-hide path against an otherwise live payload.
func payloadFresh(now time.Time) payload {
	p := scenarioBasePayload(now)
	p.Cost = cost{}
	p.ContextWindow = contextWin{}
	p.RateLimits = rateLimits{}
	p.Exceeds200K = nil
	return p
}

// ─── Curated scenario set ────────────────────────────────────────────

// curatedScenarios returns the default matrix: a spread across width (narrow vs
// wide), data completeness (full / fresh / minimal / no-git), and cost / quota
// stress (high cost, near rate-limit). Each pane renders the SAME user config
// through the real builder at its own width, so the matrix shows conditional
// hiding and reflow degradation side by side.
//
// State is attached where a scenario is meant to show burn-rate / projection /
// trend features; the synthetic previewState supplies the rising history.
func curatedScenarios(now time.Time) []scenario {
	hist := previewState(now)
	return []scenario{
		{
			Name:   "wide · full",
			Note:   "200 cols, every segment populated — the happy path",
			P:      payloadFull(now),
			State:  hist,
			Width:  200,
			Reflow: "",
		},
		{
			Name:   "narrow · full · cascade",
			Note:   "80 cols, cascade reflow spills trailing segments down",
			P:      payloadFull(now),
			State:  hist,
			Width:  80,
			Reflow: "cascade",
		},
		{
			Name:   "narrow · full · group",
			Note:   "80 cols, group reflow wraps each logical line independently",
			P:      payloadFull(now),
			State:  hist,
			Width:  80,
			Reflow: "group",
		},
		{
			Name:   "tiny · full · cascade",
			Note:   "40 cols, the worst-case squeeze — heavy spilling",
			P:      payloadFull(now),
			State:  hist,
			Width:  40,
			Reflow: "cascade",
		},
		{
			Name:   "wide · no-git",
			Note:   "not a repo — git-branch and git-stash auto-hide",
			P:      payloadNoGit(now),
			State:  hist,
			Width:  200,
			Reflow: "",
		},
		{
			Name:   "wide · high cost",
			Note:   "$18.73 session — cost / lines / duration run large",
			P:      payloadHighCost(now),
			State:  hist,
			Width:  200,
			Reflow: "",
		},
		{
			Name:   "wide · near rate-limit",
			Note:   "5h quota at 96% with a ~9m reset — bar runs hot",
			P:      payloadNearLimit(now),
			State:  hist,
			Width:  200,
			Reflow: "",
		},
		{
			Name:   "wide · fresh session",
			Note:   "started but nothing burned — cost and rate-limit bars hide, context reads 0%",
			P:      payloadFresh(now),
			State:  nil,
			Width:  200,
			Reflow: "",
		},
		{
			Name:   "narrow · minimal payload",
			Note:   "almost-empty payload — only always-present segments survive",
			P:      payloadMinimal(now),
			State:  nil,
			Width:  80,
			Reflow: "",
		},
	}
}

// ─── Rendering ───────────────────────────────────────────────────────

// withScenarioReflow returns a copy of cfg with Reflow overridden, unless the
// override is "" (keep the config's own reflow). The copy is shallow except for
// Reflow, which is the only field a scenario mutates — safe because the builder
// never writes back through cfg.
func withScenarioReflow(cfg config, reflow string) config {
	if reflow == "" {
		return cfg
	}
	out := cfg
	out.Reflow = reflow
	return out
}

// renderScenario renders one scenario through the real buildStatusline with the
// given palette. Pass palette{} for a color-free render (width measurement);
// pass a real palette to show colors. The clock is fixed to scenarioNow so
// output is deterministic.
func renderScenario(sc scenario, cfg config, c palette, now time.Time) []string {
	return buildStatusline(buildInput{
		P:     sc.P,
		C:     c,
		Cfg:   withScenarioReflow(cfg, sc.Reflow),
		State: sc.State,
		Width: sc.Width,
		Now:   now,
	})
}

// scenarioFits reports whether every rendered physical line of the scenario
// fits within its width budget (the same lineBudget the runtime renderer uses).
// A scenario with Width <= 0 always "fits" (no budget). Used by the matrix to
// flag panes whose lines overflow and will be soft-wrapped by the terminal.
//
// It measures visibleWidth (ANSI stripped), so a colored render is checked
// exactly as a color-free one would be — callers need not re-render in plain.
func scenarioFits(lines []string, width int) bool {
	if width <= 0 {
		return true
	}
	for i, l := range lines {
		if visibleWidth(l) > lineBudget(width, i == 0) {
			return false
		}
	}
	return true
}

// scenarioReflowLabel returns a human label for a scenario's reflow override,
// falling back to the config's effective reflow when the override is "".
func scenarioReflowLabel(sc scenario, cfg config) string {
	r := sc.Reflow
	if r == "" {
		r = cfg.Reflow
	}
	if r == "" {
		r = "off"
	}
	return r
}

// joinScenarioLines is a convenience for the subcommand: the scenario's lines
// joined by newline, or a placeholder when nothing rendered (all segments
// hidden under this payload).
func joinScenarioLines(lines []string) string {
	if len(lines) == 0 {
		return "(statusline hidden — no segments produced output)"
	}
	return strings.Join(lines, "\n")
}
