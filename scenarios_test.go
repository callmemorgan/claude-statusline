package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// matrixNow is the fixed clock these tests render against. It matches both
// scenarioNow() and the golden testNow, so countdown/projection output is
// deterministic.
var matrixNow = time.Unix(1750000000, 0)

func init() { initSegments(nil) }

// TestScenarioNowMatchesGolden documents the (deliberate) coupling: the matrix
// renders on the same fixed instant the goldens use, so a scenario's output is
// reproducible run-to-run.
func TestScenarioNowMatchesGolden(t *testing.T) {
	if !scenarioNow().Equal(testNow) {
		t.Fatalf("scenarioNow() = %v, want testNow %v", scenarioNow(), testNow)
	}
}

// TestCuratedScenariosShape locks the curated set's invariants: a non-empty,
// uniquely-named set spanning multiple widths and at least one of each reflow
// flavour the matrix is meant to demonstrate.
func TestCuratedScenariosShape(t *testing.T) {
	scs := curatedScenarios(matrixNow)
	if len(scs) < 5 {
		t.Fatalf("expected a rich curated set, got %d scenarios", len(scs))
	}

	seenName := map[string]bool{}
	widths := map[int]bool{}
	reflows := map[string]bool{}
	for _, sc := range scs {
		if sc.Name == "" {
			t.Errorf("scenario has empty name: %+v", sc)
		}
		if seenName[sc.Name] {
			t.Errorf("duplicate scenario name %q", sc.Name)
		}
		seenName[sc.Name] = true
		if sc.Note == "" {
			t.Errorf("scenario %q has no note", sc.Name)
		}
		if sc.Width <= 0 {
			t.Errorf("scenario %q has non-positive width %d", sc.Name, sc.Width)
		}
		widths[sc.Width] = true
		reflows[scenarioReflowLabel(sc, config{})] = true
	}
	if len(widths) < 3 {
		t.Errorf("expected scenarios across several widths, got %v", widths)
	}
	for _, want := range []string{"cascade", "group"} {
		if !reflows[want] {
			t.Errorf("curated set never exercises reflow %q (have %v)", want, reflows)
		}
	}
}

// TestScenariosDeterministic: identical clocks produce byte-identical renders.
// This guards the re-pinning of clock-relative resets in scenarioBasePayload.
func TestScenariosDeterministic(t *testing.T) {
	cfg := defaultConfig()
	a := curatedScenarios(matrixNow)
	b := curatedScenarios(matrixNow)
	if len(a) != len(b) {
		t.Fatalf("scenario count drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		la := renderScenario(a[i], cfg, palette{}, matrixNow)
		lb := renderScenario(b[i], cfg, palette{}, matrixNow)
		if strings.Join(la, "\n") != strings.Join(lb, "\n") {
			t.Errorf("scenario %q not deterministic:\n%q\n vs \n%q", a[i].Name, la, lb)
		}
	}
}

// TestScenariosRenderColorFree: with an empty palette no scenario emits ANSI
// escapes — the matrix's width measurement relies on this.
func TestScenariosRenderColorFree(t *testing.T) {
	cfg := defaultConfig()
	for _, sc := range curatedScenarios(matrixNow) {
		for _, l := range renderScenario(sc, cfg, palette{}, matrixNow) {
			if strings.Contains(l, "\x1b[") {
				t.Errorf("scenario %q emitted ANSI under empty palette: %q", sc.Name, l)
			}
		}
	}
}

// TestScenarioConditionalHiding: the data-completeness scenarios actually hide
// the segments they're built to hide. This is the matrix's whole point — that
// segments auto-hide on missing data — so it must hold against the real builder.
func TestScenarioConditionalHiding(t *testing.T) {
	cfg := defaultConfig()

	render := func(p payload, state *sessionState, width int) string {
		lines := buildStatusline(buildInput{P: p, C: palette{}, Cfg: cfg, State: state, Width: width, Now: matrixNow})
		return strings.Join(lines, "\n")
	}

	full := render(payloadFull(matrixNow), previewState(matrixNow), 200)
	noGit := render(payloadNoGit(matrixNow), previewState(matrixNow), 200)
	fresh := render(payloadFresh(matrixNow), nil, 200)
	minimal := render(payloadMinimal(matrixNow), nil, 80)
	nearLimit := render(payloadNearLimit(matrixNow), previewState(matrixNow), 200)

	// Near-limit: the 5h rate-limit bar renders (96%).
	if !strings.Contains(nearLimit, "5h") {
		t.Errorf("near-limit payload should show the 5h rate-limit bar; got:\n%s", nearLimit)
	}
	if !strings.Contains(nearLimit, "96%") {
		t.Errorf("near-limit payload should show 96%%; got:\n%s", nearLimit)
	}

	if !strings.Contains(full, "feature/config") {
		t.Errorf("full payload should show the git branch; got:\n%s", full)
	}
	if strings.Contains(noGit, "feature/config") {
		t.Errorf("no-git payload should hide the git branch; got:\n%s", noGit)
	}
	// Fresh session: zero cost hides the cost segment ("$") and the rate-limit
	// bars ("5h") hide; git is unchanged so it still shows.
	if strings.Contains(fresh, "$") {
		t.Errorf("fresh payload (zero cost) should hide cost; got:\n%s", fresh)
	}
	if strings.Contains(fresh, "5h") {
		t.Errorf("fresh payload should hide the 5h rate-limit bar; got:\n%s", fresh)
	}
	if !strings.Contains(fresh, "feature/config") {
		t.Errorf("fresh payload should still show git (branch unchanged); got:\n%s", fresh)
	}
	// Minimal payload: no model name, no cost, no git.
	if strings.Contains(minimal, "$") {
		t.Errorf("minimal payload should hide cost; got:\n%s", minimal)
	}
	if strings.Contains(minimal, "feature/config") {
		t.Errorf("minimal payload should hide git; got:\n%s", minimal)
	}
}

// TestScenarioReflowDegradation: at a tight width, cascade and group reflow
// produce more physical lines than the wide unconstrained render. This proves
// the matrix surfaces real layout degradation via the real builder.
func TestScenarioReflowDegradation(t *testing.T) {
	cfg := defaultConfig()
	p := payloadFull(matrixNow)
	st := previewState(matrixNow)

	wide := buildStatusline(buildInput{P: p, C: palette{}, Cfg: withScenarioReflow(cfg, "cascade"), State: st, Width: 200, Now: matrixNow})

	for _, mode := range []string{"cascade", "group"} {
		narrow := buildStatusline(buildInput{P: p, C: palette{}, Cfg: withScenarioReflow(cfg, mode), State: st, Width: 40, Now: matrixNow})
		if len(narrow) <= len(wide) {
			t.Errorf("%s at 40 cols should spill to more lines than wide (%d) but got %d",
				mode, len(wide), len(narrow))
		}
	}
}

// TestScenarioFits: the fit oracle agrees with lineBudget. A scenario rendered
// at a generous width fits; the same payload squeezed to 30 cols overflows.
func TestScenarioFits(t *testing.T) {
	cfg := defaultConfig()
	p := payloadFull(matrixNow)
	st := previewState(matrixNow)

	wideLines := buildStatusline(buildInput{P: p, C: palette{}, Cfg: cfg, State: st, Width: 200, Now: matrixNow})
	if !scenarioFits(wideLines, 200) {
		t.Errorf("full payload at 200 cols should fit; lines:\n%s", strings.Join(wideLines, "\n"))
	}

	tightLines := buildStatusline(buildInput{P: p, C: palette{}, Cfg: cfg, State: st, Width: 30, Now: matrixNow})
	if scenarioFits(tightLines, 30) {
		t.Errorf("full payload (default off-reflow) at 30 cols should overflow; lines:\n%s", strings.Join(tightLines, "\n"))
	}

	// Width 0 means "no budget" — always fits.
	if !scenarioFits(tightLines, 0) {
		t.Error("width 0 should always be reported as fitting")
	}
}

// TestWithScenarioReflow: "" keeps the config's reflow; a concrete mode wins;
// and the original config is never mutated.
func TestWithScenarioReflow(t *testing.T) {
	cfg := config{Reflow: "group"}

	if got := withScenarioReflow(cfg, ""); got.Reflow != "group" {
		t.Errorf(`empty override should keep cfg reflow, got %q`, got.Reflow)
	}
	if got := withScenarioReflow(cfg, "cascade"); got.Reflow != "cascade" {
		t.Errorf(`override should win, got %q`, got.Reflow)
	}
	if cfg.Reflow != "group" {
		t.Errorf("withScenarioReflow mutated the input cfg: %q", cfg.Reflow)
	}
}

// TestJoinScenarioLines covers the subcommand/TUI line-joining helper, including
// the all-segments-hidden placeholder.
func TestJoinScenarioLines(t *testing.T) {
	if joinScenarioLines(nil) == "" {
		t.Error("joinScenarioLines(nil) should return a placeholder, not empty")
	}
	if got := joinScenarioLines([]string{"one", "two"}); got != "one\ntwo" {
		t.Errorf("joinScenarioLines = %q", got)
	}
}

// TestScenarioReflowLabel falls back to the config's effective reflow when the
// scenario doesn't override, and reports "off" for the default.
func TestScenarioReflowLabel(t *testing.T) {
	if got := scenarioReflowLabel(scenario{Reflow: "group"}, config{}); got != "group" {
		t.Errorf("explicit reflow label = %q", got)
	}
	if got := scenarioReflowLabel(scenario{}, config{Reflow: "cascade"}); got != "cascade" {
		t.Errorf("fallback to cfg reflow = %q", got)
	}
	if got := scenarioReflowLabel(scenario{}, config{}); got != "off" {
		t.Errorf("default reflow label = %q, want off", got)
	}
}

// TestScenarioPropagatesTerminalWidth verifies that a scenario's Width is copied
// into the payload's TerminalWidth before rendering. Width-aware plugins receive
// STATUSLINE_COLUMNS from the payload, not from buildInput.Width, so without
// this propagation every pane would report 0 columns.
func TestScenarioPropagatesTerminalWidth(t *testing.T) {
	script := filepath.Join(t.TempDir(), "width-plugin.sh")
	body := "#!/bin/sh\necho \"cols=$STATUSLINE_COLUMNS\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// Restore the default segment registry after this test so later tests see
	// the normal built-in segments.
	oldSegments := registeredSegments
	t.Cleanup(func() { registeredSegments = oldSegments })

	def := pluginDef{ID: "width", Command: script}
	initSegments([]pluginDef{def})
	clearPluginCache()

	cfg := config{Segments: []string{"width"}}
	p := payloadMinimal(matrixNow)

	for _, width := range []int{40, 80, 200} {
		sc := scenario{P: p, Width: width}
		lines := renderScenario(sc, cfg, palette{}, matrixNow)
		want := "cols=" + strconv.Itoa(width)
		found := false
		for _, l := range lines {
			if strings.Contains(l, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("width %d: expected output to contain %q, got:\n%s", width, want, strings.Join(lines, "\n"))
		}
	}

	// The original payload must not be mutated by renderScenario.
	if p.TerminalWidth != 0 {
		t.Errorf("renderScenario mutated the source payload's TerminalWidth: got %d", p.TerminalWidth)
	}
}
