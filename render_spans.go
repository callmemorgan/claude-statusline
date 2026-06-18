package main

import "sort"

// assignSegmentLine sets cfg.Lines[id] to line, deleting the entry when line
// equals the segment's natural line so the saved config stays minimal. Shared
// by the TUI's add/move gestures. Unknown segment ids keep their override
// verbatim rather than being silently normalized to line 1.
func assignSegmentLine(cfg *config, id string, line int) {
	if s, ok := segmentByID(id); ok && line == s.line {
		if cfg.Lines != nil {
			delete(cfg.Lines, id)
		}
		return
	}
	if cfg.Lines == nil {
		cfg.Lines = make(map[string]int)
	}
	cfg.Lines[id] = line
}

// ─── Position-aware statusline build (TUI-only) ──────────────────────────────
//
// The render path uses buildStatusline, which returns []string with no
// per-segment positions. The direct-manipulation TUI needs to know, for every
// physical line, which segment occupies which column range, so it can paint a
// cursor over a real rendered segment and hit-test clicks. buildStatuslineSpans
// mirrors buildStatusline's reflow exactly but carries segment identity along,
// returning the same []string plus a parallel [][]segSpan.
//
// This is additive and TUI-only: buildStatusline itself is untouched, so the
// sacred render path is unaffected.

// segSpan locates one rendered segment within one physical line. Col is the
// visible-column offset (after the line's leading padding) of the segment's
// first cell; Width is its visible width. Text is the ANSI-colored render.
type segSpan struct {
	ID    string
	Text  string
	Col   int // visible column where the segment's glyphs start
	Width int // visible width of the segment (no separators)
}

// buildStatuslineSpans renders like buildStatusline and additionally returns,
// per physical output line, the ordered spans of the segments on it. lines and
// spans are index-aligned (spans[i] describes lines[i]). Blank spacer lines
// (cascade overflow markers) get an empty span slice.
func buildStatuslineSpans(in buildInput) ([]string, [][]segSpan) {
	clearPluginCache()

	// Mirror buildStatusline's parts assembly, but keep a parallel slice of
	// segment IDs so identity survives the reflow.
	parts := map[int][]string{}
	ids := map[int][]string{}
	for _, id := range in.Cfg.Segments {
		s, ok := segmentByID(id)
		if !ok {
			continue
		}
		segPalette := in.C
		if in.C.Rst != "" {
			if colorName := in.Cfg.Colors[id]; colorName != "" && colorName != "default" {
				segPalette = paletteWithOverride(in.C, s.primaryColor, colorName)
			}
		}
		ctx := renderCtx{
			P:     in.P,
			C:     segPalette,
			S:     settingsFor(in.Cfg, s),
			Cfg:   in.Cfg,
			Width: in.Width,
			Now:   in.Now,
		}
		if s.needsState {
			ctx.State = in.State
		}
		if rendered, show := s.render(ctx); show {
			line := s.line
			if override, ok := in.Cfg.Lines[id]; ok && override >= 1 {
				line = override
			}
			parts[line] = append(parts[line], rendered)
			ids[line] = append(ids[line], id)
		}
	}
	if len(parts) == 0 {
		return []string{}, [][]segSpan{}
	}

	st := styleFor(in.Cfg, in.C)
	switch {
	case in.Width > 0 && in.Cfg.Reflow == "group":
		return spansGroup(parts, ids, in.Width, st)
	case in.Cfg.Reflow == "cascade":
		return spansCascade(parts, ids, in.Width, st)
	default:
		return spansCascade(parts, ids, 0, st)
	}
}

// spansCascade mirrors buildStatuslineCascade, carrying the parallel id slice
// through the same spill logic so the returned spans stay aligned with the
// segment that produced each rendered chunk.
func spansCascade(parts, ids map[int][]string, columns int, st lineStyle) ([]string, [][]segSpan) {
	maxLine := 0
	originalLines := map[int]bool{}
	for k := range parts {
		if k > maxLine {
			maxLine = k
		}
		originalLines[k] = true
	}

	receivedOverflow := map[int]bool{}

	if columns > 0 {
		lineNum := 1
		for lineNum <= maxLine {
			budget := lineBudget(columns, lineNum == 1)
			for {
				segs := parts[lineNum]
				if len(segs) <= 1 {
					break
				}
				width := st.padding
				for i, seg := range segs {
					if i > 0 {
						width += st.sepWidth
					}
					width += visibleWidth(seg)
				}
				if width <= budget {
					break
				}
				moved := segs[len(segs)-1]
				movedID := ids[lineNum][len(ids[lineNum])-1]
				parts[lineNum] = segs[:len(segs)-1]
				ids[lineNum] = ids[lineNum][:len(ids[lineNum])-1]
				parts[lineNum+1] = append([]string{moved}, parts[lineNum+1]...)
				ids[lineNum+1] = append([]string{movedID}, ids[lineNum+1]...)
				receivedOverflow[lineNum+1] = true
				if lineNum+1 > maxLine {
					maxLine = lineNum + 1
				}
			}
			lineNum++
		}
	}

	out := []string{}
	var outSpans [][]segSpan
	for i := 1; i <= maxLine; i++ {
		line := joinParts(parts[i], st)
		if receivedOverflow[i] && originalLines[i] && i > 1 && (len(out) == 0 || out[len(out)-1] != "") {
			out = append(out, "")
			outSpans = append(outSpans, nil)
		}
		out = append(out, line)
		outSpans = append(outSpans, spansForLine(parts[i], ids[i], st))
	}
	return out, outSpans
}

// spansGroup mirrors buildStatuslineGroup, wrapping each logical line
// independently and recording spans for each physical line it emits.
func spansGroup(parts, ids map[int][]string, columns int, st lineStyle) ([]string, [][]segSpan) {
	var lineNums []int
	for k := range parts {
		lineNums = append(lineNums, k)
	}
	sort.Ints(lineNums)

	var out []string
	var outSpans [][]segSpan
	firstPhysicalLine := true

	for _, lineNum := range lineNums {
		segs := parts[lineNum]
		segIDs := ids[lineNum]
		if len(segs) == 0 {
			continue
		}

		var current []string
		var currentIDs []string
		currentWidth := 0

		flush := func() {
			out = append(out, joinParts(current, st))
			outSpans = append(outSpans, spansForLine(current, currentIDs, st))
		}

		for i, seg := range segs {
			segWidth := visibleWidth(seg)
			sep := st.padding
			if len(current) > 0 {
				sep = st.sepWidth
			}
			budget := lineBudget(columns, firstPhysicalLine && len(out) == 0)

			if len(current) == 0 || currentWidth+sep+segWidth <= budget {
				current = append(current, seg)
				currentIDs = append(currentIDs, segIDs[i])
				currentWidth += sep + segWidth
			} else {
				flush()
				current = []string{seg}
				currentIDs = []string{segIDs[i]}
				currentWidth = st.padding + segWidth
				firstPhysicalLine = false
			}
		}

		if len(current) > 0 {
			flush()
			firstPhysicalLine = false
		}
	}

	return out, outSpans
}

// spansForLine computes the column ranges of the segments on a single physical
// line, replicating joinParts's layout (leading padding, then segments joined
// by the separator).
func spansForLine(segs, ids []string, st lineStyle) []segSpan {
	if len(segs) == 0 {
		return nil
	}
	spans := make([]segSpan, 0, len(segs))
	col := st.padding
	for i, seg := range segs {
		if i > 0 {
			col += st.sepWidth
		}
		w := visibleWidth(seg)
		id := ""
		if i < len(ids) {
			id = ids[i]
		}
		spans = append(spans, segSpan{ID: id, Text: seg, Col: col, Width: w})
		col += w
	}
	return spans
}
