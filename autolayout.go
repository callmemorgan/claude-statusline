package main

// ─── Priority + Budget Auto-Layout Solver ────────────────────────────
//
// This is a DESIGN-TIME solver. The user ranks segments by priority and sets a
// budget (max width, max lines, density); the solver packs them onto physical
// lines so each line fits the width budget, demoting lower-priority segments to
// later lines and DROPPING them when the line budget is exhausted. On save it
// emits a CONCRETE config through the existing model (cfg.Segments / cfg.Lines /
// cfg.Reflow / cfg.Style) — the sacred render path never sees the solver.
//
// The packer never measures widths by hand: it drives the real builder
// (buildStatusline) with an empty palette and the budget width, then reads back
// each segment's rendered visible width. Because runtime rendering calls the
// identical function, a packed layout is guaranteed to match what the user sees.

import (
	"sort"
	"time"
)

// densityStyle maps an auto-layout density knob to a concrete [style] config.
// Density affects per-segment spacing, which changes how much fits on a line,
// so it flows through the real builder during measurement.
var densityStyles = map[string]styleConfig{
	"compact":     {Separator: "space", Padding: ptrInt(0)},
	"comfortable": {Separator: "bar", Padding: ptrInt(1)},
	"airy":        {Separator: "bar", Padding: ptrInt(2)},
}

// densityOrder is the cycle order offered in the TUI (map iteration is random).
var densityOrder = []string{"compact", "comfortable", "airy"}

// autoLayoutBudget is the user-editable budget for the solver. Width is the
// column budget the packed layout must fit within (per physical line, via
// lineBudget); MaxLines caps how many physical lines the solver may use before
// it starts dropping segments; Density selects the spacing style.
type autoLayoutBudget struct {
	Width    int    `toml:"width,omitempty"`     // target columns (default 80)
	MaxLines int    `toml:"max_lines,omitempty"` // 1..9 (default 3)
	Density  string `toml:"density,omitempty"`   // compact|comfortable|airy
}

func (b autoLayoutBudget) width() int {
	if b.Width <= 0 {
		return 80
	}
	return b.Width
}

func (b autoLayoutBudget) maxLines() int {
	if b.MaxLines < 1 {
		return 3
	}
	if b.MaxLines > 9 {
		return 9
	}
	return b.MaxLines
}

func (b autoLayoutBudget) density() string {
	if _, ok := densityStyles[b.Density]; ok {
		return b.Density
	}
	return "comfortable"
}

// autoLayoutConfig is the optional [auto_layout] metadata persisted alongside
// the concrete config so the priority ranking + budget can be re-edited later.
// It is METADATA ONLY: the render path ignores it entirely; only the solver and
// the TUI read it. The saved Segments/Lines remain concrete.
type autoLayoutConfig struct {
	Priorities []string         `toml:"priorities,omitempty"`
	Budget     autoLayoutBudget `toml:"budget,omitempty"`
}

// packResult is the outcome of a solve: the concrete per-line assignment in
// priority order, plus which priority-ranked segments were dropped because the
// budget ran out. Lines is 1-indexed-dense: Lines[0] is physical line 1.
type packResult struct {
	Lines   [][]string // segment IDs per physical line, in render order
	Dropped []string   // segments that didn't fit, in priority order
}

// packMeasureInput carries the deterministic inputs the solver uses to measure
// rendered widths. Tests inject a fixed payload/state/clock; the TUI injects its
// synthetic preview data. The palette is always empty (color-free) so widths are
// pure rune counts — exactly how the renderer measures at runtime.
type packMeasureInput struct {
	P     payload
	State *sessionState
	Now   time.Time
}

// segmentRenderWidth measures one segment's rendered visible width under the
// given base config + measurement input, by rendering a single-segment config
// through the real builder. Hidden segments (auto-hide on missing data) return
// (0, false) and are skipped by the packer.
func segmentRenderWidth(id string, base config, mi packMeasureInput) (int, bool) {
	probe := base
	probe.Segments = []string{id}
	// Pin the probe to line 1 so the segment's own line is the only non-empty
	// one regardless of its natural line (model/version/bars default to lines
	// 2–3, which would otherwise emit a leading empty line we'd mis-measure).
	probe.Lines = map[string]int{id: 1}
	probe.Reflow = "" // no reflow: we want the bare segment string
	lines := buildStatusline(buildInput{
		P:     mi.P,
		C:     palette{}, // empty palette ⇒ color-free ⇒ pure rune width
		Cfg:   probe,
		State: mi.State,
		Width: 0, // unconstrained: render the segment verbatim
		Now:   mi.Now,
	})
	// The segment renders empty (auto-hidden) ⇒ no non-empty line to measure.
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
	// joinParts prepends `padding` spaces; strip it back off so we measure the
	// segment's own width independent of line padding.
	return visibleWidth(line) - styleFor(probe, palette{}).padding, true
}

// packLayout is the pure solver. Given a base config (theme/colors/settings/
// plugins), a priority-ordered list of segment IDs (highest first), and a
// budget, it returns a concrete per-line assignment that fits the width budget,
// demoting lower-priority segments to later lines and dropping them once the
// line budget (maxLines) is exhausted.
//
// Determinism: the result depends only on (base, priorities, budget, mi). No
// map iteration, time.Now, or global mutable state leaks in (the measurement
// uses an empty palette and the injected clock). Unit-tested in autolayout_test.
func packLayout(base config, priorities []string, budget autoLayoutBudget, mi packMeasureInput) packResult {
	dense := withDensity(base, budget.density())
	st := styleFor(dense, palette{})
	maxLines := budget.maxLines()
	width := budget.width()

	// Pre-measure every candidate segment once. Segments that render empty
	// (auto-hidden because their source data is missing/zero) are excluded up
	// front: they'd add nothing and shouldn't consume a priority slot.
	type cand struct {
		id    string
		width int
	}
	var cands []cand
	seen := map[string]bool{}
	for _, id := range priorities {
		if seen[id] {
			continue // de-dupe; first occurrence wins the priority slot
		}
		seen[id] = true
		w, ok := segmentRenderWidth(id, dense, mi)
		if !ok {
			continue // auto-hidden: nothing to place
		}
		cands = append(cands, cand{id: id, width: w})
	}

	res := packResult{Lines: [][]string{{}}}
	curLine := 0           // index into res.Lines for the line we're filling
	curWidth := st.padding // accumulated visible width of the current line

	budgetFor := func(lineIdx int) int {
		return lineBudget(width, lineIdx == 0)
	}
	// advance opens a fresh physical line if the budget allows; returns false
	// when maxLines is exhausted (the caller then drops the segment).
	advance := func() bool {
		if curLine+1 >= maxLines {
			return false
		}
		res.Lines = append(res.Lines, []string{})
		curLine++
		curWidth = st.padding
		return true
	}

	for _, c := range cands {
		// Width this segment adds to the current line (a separator precedes
		// every segment but the first on a line).
		add := c.width
		if len(res.Lines[curLine]) > 0 {
			add += st.sepWidth
		}
		if curWidth+add <= budgetFor(curLine) {
			res.Lines[curLine] = append(res.Lines[curLine], c.id)
			curWidth += add
			continue
		}
		// Doesn't fit on the current line. If the current line already has
		// segments, try demoting to a fresh line; if that fresh line is full
		// budget and the segment STILL doesn't fit, it's wider than any line —
		// place it solo anyway (the terminal will soft-wrap) rather than drop a
		// higher-priority segment for an un-splittable one.
		if len(res.Lines[curLine]) > 0 {
			if !advance() {
				res.Dropped = append(res.Dropped, c.id)
				continue
			}
		}
		// Now on an empty current line. Place the segment here regardless of
		// whether it fits (an over-wide solo segment can't be split).
		res.Lines[curLine] = append(res.Lines[curLine], c.id)
		curWidth = st.padding + c.width
	}

	// Trim a trailing empty line (can't normally occur, but keep packResult
	// well-formed). Always keep at least one line.
	for len(res.Lines) > 1 && len(res.Lines[len(res.Lines)-1]) == 0 {
		res.Lines = res.Lines[:len(res.Lines)-1]
	}
	return res
}

// withDensity returns a copy of base with its [style] replaced by the density
// preset. Used both for measurement and for the emitted concrete config.
func withDensity(base config, density string) config {
	out := base
	if s, ok := densityStyles[density]; ok {
		out.Style = s
	}
	return out
}

// applyPackResult writes a packResult into a config as a CONCRETE layout: it
// sets cfg.Segments to the packed, in-render-order ID list, cfg.Lines to the
// per-segment physical-line overrides, cfg.Reflow to "group" (so the saved
// layout honors the solver's line boundaries at runtime instead of cascading
// across them), and cfg.Style to the density preset. Dropped segments are
// removed from cfg.Segments. This goes through the same config model the
// existing list-based TUI saves through.
func applyPackResult(cfg *config, res packResult, density string) {
	var segs []string
	lines := map[string]int{}
	for li, lineSegs := range res.Lines {
		physical := li + 1
		for _, id := range lineSegs {
			segs = append(segs, id)
			// Always pin an explicit line override so the saved layout
			// reproduces the solver's exact physical assignment under "group"
			// reflow, independent of each segment's natural line. (Without an
			// explicit pin, group would re-group a no-override segment by its
			// natural line and break the packed layout.)
			lines[id] = physical
		}
	}
	cfg.Segments = segs
	if len(lines) > 0 {
		cfg.Lines = lines
	} else {
		cfg.Lines = nil
	}
	// "group" keeps each logical (here: physical) line independent at runtime,
	// so the solver's line assignment survives terminal-width changes without
	// cascading segments across the boundaries it deliberately chose.
	cfg.Reflow = "group"
	if s, ok := densityStyles[density]; ok {
		cfg.Style = s
	}
}

// rankedSeg is one entry in a line/order ranking: a segment id with its
// physical line and a tiebreak ordinal.
type rankedSeg struct {
	id   string
	line int
	ord  int
}

// rankByLineOrder stable-sorts entries by line (ascending), ties broken by
// ord, and returns the resulting ids. Shared by the priority derivations so
// "line 1 ranks above line 2, registry/config order breaks ties" lives once.
func rankByLineOrder(rs []rankedSeg) []string {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].line != rs[j].line {
			return rs[i].line < rs[j].line
		}
		return rs[i].ord < rs[j].ord
	})
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.id
	}
	return out
}

// defaultPriorities derives a sensible initial priority ranking from the
// registered segments' natural lines and registry order: line 1 segments rank
// above line 2, line 2 above line 3, ties broken by registry order. This gives
// the solver a reasonable starting point that mirrors the default layout.
func defaultPriorities() []string {
	rs := make([]rankedSeg, len(registeredSegments))
	for i, s := range registeredSegments {
		rs[i] = rankedSeg{id: s.id, line: s.line, ord: i}
	}
	return rankByLineOrder(rs)
}

// prioritiesFromConfig builds an initial priority ranking from an existing
// config's enabled segments (in their current line/order), appending any
// remaining registered segments after them. So re-opening the solver on a
// configured statusline preserves the user's current ordering as the ranking.
func prioritiesFromConfig(cfg config) []string {
	rs := make([]rankedSeg, 0, len(cfg.Segments))
	seen := map[string]bool{}
	for i, id := range cfg.Segments {
		seen[id] = true
		rs = append(rs, rankedSeg{id: id, line: effectiveLine(id, cfg), ord: i})
	}
	out := rankByLineOrder(rs)
	// Append any registered-but-disabled segments so the ranking can promote
	// them. Registry order is the tiebreak.
	for _, s := range registeredSegments {
		if !seen[s.id] {
			out = append(out, s.id)
		}
	}
	return out
}
