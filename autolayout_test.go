package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// packTestInput builds a deterministic measurement input from the claude-full
// fixture (so segments render with stable, non-zero widths) and the fixed test
// clock. previewState gives the state-derived segments history to render.
func packTestInput(t *testing.T) packMeasureInput {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // neutralize git/effort filesystem reads
	p := loadPayload(t, "claude-full.json")
	return packMeasureInput{P: p, State: previewState(testNow), Now: testNow}
}

func flatten(res packResult) []string {
	var out []string
	for _, l := range res.Lines {
		out = append(out, l...)
	}
	return out
}

// TestPackLayoutDeterministic locks that the solver is a pure function of its
// inputs: identical inputs yield identical line assignments and drop lists.
func TestPackLayoutDeterministic(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := []string{"directory", "git-branch", "cost", "model", "context-window", "rate-limit-5h"}
	budget := autoLayoutBudget{Width: 60, MaxLines: 3, Density: "comfortable"}

	a := packLayout(config{}, prios, budget, mi)
	b := packLayout(config{}, prios, budget, mi)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("packLayout not deterministic:\n a=%#v\n b=%#v", a, b)
	}
}

// TestPackLayoutFitsBudget verifies every packed physical line fits its
// lineBudget when re-rendered through the real builder at the budget width —
// the solver's correctness invariant.
func TestPackLayoutFitsBudget(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := defaultPriorities()
	budget := autoLayoutBudget{Width: 70, MaxLines: 4, Density: "comfortable"}

	res := packLayout(config{}, prios, budget, mi)
	if len(flatten(res)) == 0 {
		t.Fatal("expected some segments placed")
	}

	// Emit the concrete config and render it the way runtime would, then check
	// each physical line against the budget. group reflow keeps line boundaries.
	cfg := config{}
	applyPackResult(&cfg, res, budget.Density)
	lines := buildStatusline(buildInput{P: mi.P, C: palette{}, Cfg: cfg, State: mi.State, Width: budget.Width, Now: mi.Now})
	// group reflow honors the solver's line boundaries. Because the solver
	// already fit each line to budget, the rendered physical-line count must
	// match res.Lines (no additional runtime re-wrapping), and every multi-
	// segment line must respect its budget (a single over-wide segment can't be
	// split and is allowed to exceed it).
	if len(lines) != len(res.Lines) {
		t.Fatalf("rendered %d physical lines, solver produced %d (runtime re-wrapped): %q",
			len(lines), len(res.Lines), lines)
	}
	for i, l := range lines {
		w := visibleWidth(l)
		b := lineBudget(budget.Width, i == 0)
		segCount := len(res.Lines[i])
		if segCount > 1 && w > b {
			t.Errorf("line %d width %d exceeds budget %d: %q", i, w, b, stripANSI(l))
		}
	}
}

// TestPackLayoutTightDrops verifies the core demote/drop behavior: a very tight
// budget (narrow width, 1 line) keeps the highest-priority segments and drops
// the rest, in priority order.
func TestPackLayoutTightDrops(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := []string{"directory", "git-branch", "model", "context-window", "rate-limit-5h", "rate-limit-7d"}
	budget := autoLayoutBudget{Width: 30, MaxLines: 1, Density: "comfortable"}

	res := packLayout(config{}, prios, budget, mi)

	if len(res.Lines) != 1 {
		t.Fatalf("MaxLines=1 must produce exactly 1 line, got %d", len(res.Lines))
	}
	if len(res.Dropped) == 0 {
		t.Fatal("tight budget must drop some segments")
	}
	placed := flatten(res)
	if len(placed) == 0 {
		t.Fatal("at least the highest-priority segment should be placed")
	}
	// Highest priority (directory) must survive.
	if placed[0] != "directory" {
		t.Errorf("highest-priority segment should be placed first, got %q", placed[0])
	}
	// Every placed segment must outrank every dropped segment in the priority
	// list (priority is respected — we never drop a higher-priority segment to
	// keep a lower-priority one).
	rank := map[string]int{}
	for i, id := range prios {
		rank[id] = i
	}
	maxPlaced := -1
	for _, id := range placed {
		if rank[id] > maxPlaced {
			maxPlaced = rank[id]
		}
	}
	for _, id := range res.Dropped {
		if rank[id] < maxPlaced {
			t.Errorf("dropped %q (rank %d) outranks a placed segment (max placed rank %d)", id, rank[id], maxPlaced)
		}
	}
}

// TestPackLayoutMoreLinesPlaceMore verifies the budget knob is monotone: giving
// the solver more lines never drops MORE segments than a tighter line budget.
func TestPackLayoutMoreLinesPlaceMore(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := defaultPriorities()

	tight := packLayout(config{}, prios, autoLayoutBudget{Width: 50, MaxLines: 1}, mi)
	loose := packLayout(config{}, prios, autoLayoutBudget{Width: 50, MaxLines: 5}, mi)

	if len(flatten(loose)) < len(flatten(tight)) {
		t.Errorf("more lines placed fewer segments: tight=%d loose=%d", len(flatten(tight)), len(flatten(loose)))
	}
	if len(loose.Dropped) > len(tight.Dropped) {
		t.Errorf("more lines dropped more segments: tight=%d loose=%d", len(tight.Dropped), len(loose.Dropped))
	}
}

// TestPackLayoutDensityAffectsPacking verifies density flows through the real
// builder: compact spacing fits at least as many segments per line as airy.
func TestPackLayoutDensityAffectsPacking(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := defaultPriorities()

	compact := packLayout(config{}, prios, autoLayoutBudget{Width: 60, MaxLines: 1, Density: "compact"}, mi)
	airy := packLayout(config{}, prios, autoLayoutBudget{Width: 60, MaxLines: 1, Density: "airy"}, mi)

	if len(flatten(compact)) < len(flatten(airy)) {
		t.Errorf("compact density packed fewer segments than airy: compact=%d airy=%d",
			len(flatten(compact)), len(flatten(airy)))
	}
}

// TestPackLayoutSkipsHiddenSegments verifies that segments which auto-hide on
// missing data (here: a payload with no rate-limit data) never consume a
// priority slot or appear in the packed output.
func TestPackLayoutSkipsHiddenSegments(t *testing.T) {
	initSegments(nil)
	t.Setenv("HOME", t.TempDir())
	p := loadPayload(t, "minimal.json") // sparse payload: many segments hide
	mi := packMeasureInput{P: p, State: nil, Now: testNow}

	prios := []string{"directory", "rate-limit-5h", "rate-limit-7d", "cost"}
	res := packLayout(config{}, prios, autoLayoutBudget{Width: 80, MaxLines: 3}, mi)

	all := append(flatten(res), res.Dropped...)
	for _, id := range all {
		if id == "rate-limit-5h" || id == "rate-limit-7d" {
			t.Errorf("hidden segment %q should not appear in pack result", id)
		}
	}
}

// TestApplyPackResultRoundTrips verifies the solver emits a concrete config
// through the existing model: Segments/Lines/Reflow/Style, with dropped
// segments removed and physical lines encoded as Line overrides.
func TestApplyPackResultRoundTrips(t *testing.T) {
	initSegments(nil)
	res := packResult{
		Lines: [][]string{
			{"directory", "git-branch"},
			{"model", "cost"},
		},
	}
	cfg := config{}
	applyPackResult(&cfg, res, "compact")

	want := []string{"directory", "git-branch", "model", "cost"}
	if !reflect.DeepEqual(cfg.Segments, want) {
		t.Errorf("Segments = %v, want %v", cfg.Segments, want)
	}
	if cfg.Reflow != "group" {
		t.Errorf("Reflow = %q, want group", cfg.Reflow)
	}
	// Every placed segment is pinned to its exact physical line so "group"
	// reflow reproduces the packed layout regardless of natural lines.
	wantLines := map[string]int{"directory": 1, "git-branch": 1, "model": 2, "cost": 2}
	for id, want := range wantLines {
		if cfg.Lines[id] != want {
			t.Errorf("Lines[%q] = %d, want %d", id, cfg.Lines[id], want)
		}
	}
	// compact density → space separator, padding 0.
	if cfg.Style.Separator != "space" {
		t.Errorf("Style.Separator = %q, want space", cfg.Style.Separator)
	}
}

// TestApplyPackResultDropsDisabled verifies segments not in any packed line are
// absent from the emitted Segments slice (dropped = disabled in the layout).
func TestApplyPackResultDropsDisabled(t *testing.T) {
	initSegments(nil)
	res := packResult{
		Lines:   [][]string{{"directory"}},
		Dropped: []string{"cost", "model"},
	}
	cfg := config{}
	applyPackResult(&cfg, res, "comfortable")
	for _, id := range cfg.Segments {
		if id == "cost" || id == "model" {
			t.Errorf("dropped segment %q leaked into Segments", id)
		}
	}
}

// TestPackLayoutSaveRoundTrip verifies the emitted config survives a TOML
// round-trip through the real save/load path (the same one the list-based TUI
// uses), including the optional [auto_layout] metadata.
func TestPackLayoutSaveRoundTrip(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := defaultPriorities()
	budget := autoLayoutBudget{Width: 70, MaxLines: 3, Density: "comfortable"}
	res := packLayout(config{}, prios, budget, mi)

	cfg := config{}
	applyPackResult(&cfg, res, budget.Density)
	cfg.AutoLayout = autoLayoutConfig{Priorities: prios, Budget: budget}

	data, err := marshalConfigTOML(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "[auto_layout]") {
		t.Errorf("expected [auto_layout] metadata in saved TOML:\n%s", data)
	}

	var back config
	if err := toml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back.Segments, cfg.Segments) {
		t.Errorf("Segments round-trip mismatch:\n got %v\nwant %v", back.Segments, cfg.Segments)
	}
	if back.Reflow != "group" {
		t.Errorf("Reflow round-trip = %q, want group", back.Reflow)
	}
	if !reflect.DeepEqual(back.AutoLayout.Priorities, prios) {
		t.Errorf("AutoLayout.Priorities round-trip mismatch")
	}
	if back.AutoLayout.Budget.width() != budget.width() {
		t.Errorf("AutoLayout.Budget.Width round-trip = %d, want %d", back.AutoLayout.Budget.width(), budget.width())
	}

	// mergeWithDefaults field-copies every config field; it must carry the
	// design-time [auto_layout] metadata through, or re-opening the solver
	// would always see an empty ranking after a reload.
	merged := mergeWithDefaults(back)
	if !reflect.DeepEqual(merged.AutoLayout.Priorities, prios) {
		t.Errorf("AutoLayout.Priorities dropped by mergeWithDefaults: %v", merged.AutoLayout.Priorities)
	}
	if merged.AutoLayout.Budget.width() != budget.width() {
		t.Errorf("AutoLayout.Budget dropped by mergeWithDefaults: %d", merged.AutoLayout.Budget.width())
	}
}

// TestPrioritiesFromConfig verifies re-deriving a ranking from an existing
// config preserves enabled-segment order and appends disabled ones.
func TestPrioritiesFromConfig(t *testing.T) {
	initSegments(nil)
	cfg := config{
		Segments: []string{"cost", "directory"},
		Lines:    map[string]int{"cost": 2},
	}
	got := prioritiesFromConfig(cfg)
	// directory (line 1) ranks above cost (overridden to line 2).
	di, ci := indexOf(got, "directory"), indexOf(got, "cost")
	if di < 0 || ci < 0 || di > ci {
		t.Errorf("expected directory before cost, got order %v", got[:3])
	}
	// Every registered segment appears exactly once.
	if len(got) != len(registeredSegments) {
		t.Errorf("priorities length = %d, want %d (all registered)", len(got), len(registeredSegments))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("duplicate %q in priorities", id)
		}
		seen[id] = true
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// TestDefaultPrioritiesComplete verifies the default ranking covers every
// registered segment exactly once, ordered by natural line.
func TestDefaultPrioritiesComplete(t *testing.T) {
	initSegments(nil)
	got := defaultPriorities()
	if len(got) != len(registeredSegments) {
		t.Fatalf("defaultPriorities length = %d, want %d", len(got), len(registeredSegments))
	}
	seen := map[string]bool{}
	prevLine := 0
	for _, id := range got {
		if seen[id] {
			t.Errorf("duplicate %q", id)
		}
		seen[id] = true
		s, _ := segmentByID(id)
		if s.line < prevLine {
			t.Errorf("ranking not ordered by line: %q (line %d) after line %d", id, s.line, prevLine)
		}
		prevLine = s.line
	}
}

// TestDedupePriorities verifies duplicate/unknown ids are dropped and missing
// registered segments are appended so the ranking is always complete.
func TestDedupePriorities(t *testing.T) {
	initSegments(nil)
	got := dedupePriorities([]string{"cost", "cost", "not-a-segment", "directory"})
	if indexOf(got, "cost") != 0 || indexOf(got, "directory") != 1 {
		t.Errorf("expected cost, directory first; got %v", got[:2])
	}
	if indexOf(got, "not-a-segment") != -1 {
		t.Errorf("unknown id leaked into ranking")
	}
	if len(got) != len(registeredSegments) {
		t.Errorf("length = %d, want %d (all registered)", len(got), len(registeredSegments))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("duplicate %q after dedupe", id)
		}
		seen[id] = true
	}
}

func TestCycleDensity(t *testing.T) {
	cases := []struct {
		cur  string
		dir  int
		want string
	}{
		{"compact", +1, "comfortable"},
		{"comfortable", +1, "airy"},
		{"airy", +1, "compact"},
		{"compact", -1, "airy"},
		{"unknown", +1, "comfortable"}, // unknown maps to index 0 → +1
	}
	for _, c := range cases {
		if got := cycleDensity(c.cur, c.dir); got != c.want {
			t.Errorf("cycleDensity(%q, %d) = %q, want %q", c.cur, c.dir, got, c.want)
		}
	}
}

func TestClampInt(t *testing.T) {
	if clampInt(5, 1, 9) != 5 {
		t.Error("in-range value changed")
	}
	if clampInt(-3, 1, 9) != 1 {
		t.Error("below-min not clamped")
	}
	if clampInt(99, 1, 9) != 9 {
		t.Error("above-max not clamped")
	}
}

// TestBudgetAccessorDefaults locks the budget accessor defaults the solver and
// TUI rely on.
func TestBudgetAccessorDefaults(t *testing.T) {
	var b autoLayoutBudget // zero value
	if b.width() != 80 {
		t.Errorf("default width = %d, want 80", b.width())
	}
	if b.maxLines() != 3 {
		t.Errorf("default maxLines = %d, want 3", b.maxLines())
	}
	if b.density() != "comfortable" {
		t.Errorf("default density = %q, want comfortable", b.density())
	}
	// Out-of-range clamping via accessors.
	if (autoLayoutBudget{MaxLines: 50}).maxLines() != 9 {
		t.Error("maxLines not clamped to 9")
	}
}

// TestSegmentRenderWidthAgnostic guards the auto-layout invariant: segments are
// measured with Width: 0 and must not change width when the terminal width
// changes. If this test fails, the solver needs to measure at the budget width
// instead of assuming width independence.
func TestSegmentRenderWidthAgnostic(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	for _, s := range registeredSegments {
		w0, ok0 := renderSegmentAtWidth(s.id, 0, mi)
		w200, ok200 := renderSegmentAtWidth(s.id, 200, mi)
		if ok0 != ok200 {
			t.Errorf("%s: visibility differs between width 0 and 200", s.id)
			continue
		}
		if ok0 && w0 != w200 {
			t.Errorf("%s: width depends on terminal width (%d at 0 cols, %d at 200 cols); auto-layout assumes segments are width-agnostic", s.id, w0, w200)
		}
	}
}

// renderSegmentAtWidth renders a single segment at the given terminal width and
// returns its visible width (with padding stripped). It mirrors the measurement
// logic in segmentRenderWidth but exposes the width parameter for testing.
func renderSegmentAtWidth(id string, width int, mi packMeasureInput) (int, bool) {
	probe := config{}
	probe.Segments = []string{id}
	probe.Lines = map[string]int{id: 1}
	probe.Reflow = ""
	lines := buildStatusline(buildInput{
		P:     mi.P,
		C:     palette{},
		Cfg:   probe,
		State: mi.State,
		Width: width,
		Now:   mi.Now,
	})
	var line string
	for _, l := range lines {
		if visibleWidth(l) > 0 {
			line = l
			break
		}
	}
	if line == "" {
		return 0, false
	}
	return visibleWidth(line) - styleFor(probe, palette{}).padding, true
}

// TestSegmentRenderWidthCache verifies the memoization layer in
// segmentRenderWidth populates the cache and returns consistent results.
func TestSegmentRenderWidthCache(t *testing.T) {
	initSegments(nil)
	mi := packMeasureInput{P: packTestInput(t).P, State: packTestInput(t).State, Now: testNow, widthCache: map[string]cachedWidth{}}
	base := config{}

	w1, ok1 := segmentRenderWidth("directory", base, mi)
	if !ok1 {
		t.Fatal("directory segment hidden on first measure")
	}
	if len(mi.widthCache) == 0 {
		t.Error("widthCache was not populated")
	}
	w2, ok2 := segmentRenderWidth("directory", base, mi)
	if w1 != w2 || ok1 != ok2 {
		t.Errorf("cached width mismatch: first (%d,%v) second (%d,%v)", w1, ok1, w2, ok2)
	}
}

// TestSegmentRenderWidthCachePerStyle verifies the cache key includes style, so
// the same segment measured under two densities occupies two distinct cache
// entries (even if the segment itself happens to be the same width in both).
func TestSegmentRenderWidthCachePerStyle(t *testing.T) {
	initSegments(nil)
	mi := packMeasureInput{P: packTestInput(t).P, State: packTestInput(t).State, Now: testNow, widthCache: map[string]cachedWidth{}}

	compact := withDensity(config{}, "compact")
	airy := withDensity(config{}, "airy")

	_, _ = segmentRenderWidth("directory", compact, mi)
	_, _ = segmentRenderWidth("directory", airy, mi)
	if len(mi.widthCache) != 2 {
		t.Errorf("expected 2 cached entries (one per style), got %d", len(mi.widthCache))
	}
}

// TestWithDensityPreservesSeparatorCustom verifies density presets preserve a
// user-customized separator glyph while still applying the density's separator
// style and padding.
func TestWithDensityPreservesSeparatorCustom(t *testing.T) {
	base := config{}
	base.Style.Separator = "custom"
	base.Style.SeparatorCustom = "::"
	base.Style.Padding = ptrInt(5)

	dense := withDensity(base, "compact")
	if dense.Style.Separator != "space" {
		t.Errorf("Separator = %q, want space", dense.Style.Separator)
	}
	if dense.Style.SeparatorCustom != "::" {
		t.Errorf("SeparatorCustom = %q, want preserved '::'", dense.Style.SeparatorCustom)
	}
	if dense.Style.Padding == nil || *dense.Style.Padding != 0 {
		t.Errorf("Padding = %v, want 0", dense.Style.Padding)
	}
}

// TestApplyPackResultPreservesSeparatorCustom verifies the emitted concrete
// config keeps a user-customized separator glyph even though density sets the
// separator style.
func TestApplyPackResultPreservesSeparatorCustom(t *testing.T) {
	initSegments(nil)
	res := packResult{Lines: [][]string{{"directory"}}}
	cfg := config{}
	cfg.Style.SeparatorCustom = "::"
	applyPackResult(&cfg, res, "compact")

	if cfg.Style.SeparatorCustom != "::" {
		t.Errorf("SeparatorCustom = %q, want preserved '::'", cfg.Style.SeparatorCustom)
	}
}

// TestPackLayoutExactFit verifies a segment whose width exactly matches the
// first-line budget is placed solo with no drops.
func TestPackLayoutExactFit(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	budget := autoLayoutBudget{Density: "compact"}
	dense := withDensity(config{}, budget.density())
	st := styleFor(dense, palette{})

	w, ok := segmentRenderWidth("directory", dense, mi)
	if !ok {
		t.Fatal("directory segment hidden")
	}
	// budget.width is the user-visible column budget; lineBudget subtracts the
	// safety margin and line-1 timing suffix reserve. Set it so padding + width
	// exactly equals the first-line budget.
	budget.Width = w + st.padding + safetyMargin + timingSuffixReserve

	res := packLayout(config{}, []string{"directory"}, budget, mi)
	if len(res.Lines) != 1 || len(res.Lines[0]) != 1 || res.Lines[0][0] != "directory" {
		t.Fatalf("expected directory placed solo on line 1, got %#v", res)
	}
	if len(res.Dropped) != 0 {
		t.Errorf("expected no drops, got %v", res.Dropped)
	}
}

// TestPackLayoutEmptyWhenAllHidden verifies that when every candidate segment
// auto-hides (no source data), the solver returns an empty layout with no
// crashes and no spurious drops.
func TestPackLayoutEmptyWhenAllHidden(t *testing.T) {
	initSegments(nil)
	t.Setenv("HOME", t.TempDir())
	p := loadPayload(t, "minimal.json")
	mi := packMeasureInput{P: p, State: nil, Now: testNow}
	prios := []string{"rate-limit-5h", "rate-limit-7d", "cost"}

	res := packLayout(config{}, prios, autoLayoutBudget{Width: 80, MaxLines: 3}, mi)
	if len(flatten(res)) != 0 {
		t.Errorf("expected no placed segments, got %v", flatten(res))
	}
	if len(res.Dropped) != 0 {
		t.Errorf("expected no drops when all candidates hide, got %v", res.Dropped)
	}
}

// TestPackLayoutDropsWhenBudgetExhausted verifies lower-priority segments are
// dropped once the line budget is exhausted, and that dropped segments never
// outrank placed ones.
func TestPackLayoutDropsWhenBudgetExhausted(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	prios := []string{"directory", "git-branch", "model", "context-window", "rate-limit-5h", "rate-limit-7d"}
	budget := autoLayoutBudget{Width: 30, MaxLines: 1, Density: "comfortable"}

	res := packLayout(config{}, prios, budget, mi)
	if len(res.Lines) != 1 {
		t.Fatalf("MaxLines=1 must produce exactly 1 line, got %d", len(res.Lines))
	}
	if len(res.Dropped) == 0 {
		t.Fatal("tight budget must drop some segments")
	}

	rank := map[string]int{}
	for i, id := range prios {
		rank[id] = i
	}
	maxPlaced := -1
	for _, id := range res.Lines[0] {
		if rank[id] > maxPlaced {
			maxPlaced = rank[id]
		}
	}
	for _, id := range res.Dropped {
		if rank[id] < maxPlaced {
			t.Errorf("dropped %q (rank %d) outranks a placed segment (max placed rank %d)", id, rank[id], maxPlaced)
		}
	}
}

// TestSegmentRenderWidthNaturalLine verifies segmentRenderWidth correctly
// measures a segment whose natural line is not 1 by pinning it to line 1. This
// is the "multi-line" measurement case: without the pin, the segment would emit
// a leading empty line and be mis-measured.
func TestSegmentRenderWidthNaturalLine(t *testing.T) {
	initSegments(nil)
	mi := packTestInput(t)
	s, ok := segmentByID("model")
	if !ok {
		t.Fatal("model segment not registered")
	}
	if s.line == 1 {
		t.Skip("model is now line 1; pick another natural-line-2 segment")
	}

	measured, ok := segmentRenderWidth("model", config{}, mi)
	if !ok {
		t.Fatal("model segment hidden")
	}

	// Render model at its natural line and compare widths.
	cfg := config{Segments: []string{"model"}}
	lines := buildStatusline(buildInput{P: mi.P, C: palette{}, Cfg: cfg, State: mi.State, Width: 0, Now: mi.Now})
	var natural string
	for _, l := range lines {
		if visibleWidth(l) > 0 {
			natural = l
			break
		}
	}
	if natural == "" {
		t.Fatal("model did not render at natural line")
	}
	naturalWidth := visibleWidth(natural) - styleFor(cfg, palette{}).padding
	if measured != naturalWidth {
		t.Errorf("segmentRenderWidth measured %d, want %d (natural line width)", measured, naturalWidth)
	}
}

// TestCountPlaced verifies the helper used by the TUI result strings.
func TestCountPlaced(t *testing.T) {
	res := packResult{
		Lines:   [][]string{{"a", "b"}, {"c"}, {}, {"d", "e", "f"}},
		Dropped: []string{"x"},
	}
	if got := countPlaced(res); got != 6 {
		t.Errorf("countPlaced = %d, want 6", got)
	}
}
