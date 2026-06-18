package main

import (
	"strings"

	"github.com/rivo/tview"
)

// ─── Direct-manipulation helpers for the configure TUI ───────────────────────
//
// These functions were extracted from tui.go to keep the shared configure
// framework focused. They operate on the rendered span layout produced by
// buildStatuslineSpans and mutate the passed-in config directly; the caller
// (runConfigure) still owns dirty tracking, the active preset label, and UI
// refresh.

// effectiveLine returns the configured line override for id, falling back to
// the segment's natural line. Unknown ids return 1 only when no override is
// present, so callers can still place them somewhere sensible.
func effectiveLine(id string, cfg config) int {
	if override, ok := cfg.Lines[id]; ok && override >= 1 {
		return override
	}
	if s, ok := segmentByID(id); ok {
		return s.line
	}
	return 1
}

// cursorSegment returns the segment id under the cursor, or "".
func cursorSegment(curSpans [][]segSpan, curLine, curCol int) string {
	if curLine >= 0 && curLine < len(curSpans) {
		row := curSpans[curLine]
		if curCol >= 0 && curCol < len(row) {
			return row[curCol].ID
		}
	}
	return ""
}

// clampCursor keeps (curLine,curCol) on a real span and syncs cursorID. It
// prefers to re-find cursorID after a rebuild so the cursor tracks the same
// segment across toggles and moves.
func clampCursor(curSpans [][]segSpan, cursorID string, curLine, curCol int) (int, int, string) {
	// Drop empty (spacer) rows from consideration by clamping into range.
	if len(curSpans) == 0 {
		return 0, 0, ""
	}
	// Try to follow cursorID to wherever it moved.
	if cursorID != "" {
		for li, row := range curSpans {
			for ci, sp := range row {
				if sp.ID == cursorID {
					return li, ci, cursorID
				}
			}
		}
	}
	// Otherwise clamp to the nearest valid position.
	if curLine >= len(curSpans) {
		curLine = len(curSpans) - 1
	}
	if curLine < 0 {
		curLine = 0
	}
	// Skip empty rows by scanning for the next non-empty one.
	if len(curSpans[curLine]) == 0 {
		placed := false
		for d := 0; d < len(curSpans) && !placed; d++ {
			for _, cand := range []int{curLine - d, curLine + d} {
				if cand >= 0 && cand < len(curSpans) && len(curSpans[cand]) > 0 {
					curLine, placed = cand, true
					break
				}
			}
		}
	}
	if curLine < 0 || curLine >= len(curSpans) || len(curSpans[curLine]) == 0 {
		return 0, 0, ""
	}
	if curCol >= len(curSpans[curLine]) {
		curCol = len(curSpans[curLine]) - 1
	}
	if curCol < 0 {
		curCol = 0
	}
	return curLine, curCol, curSpans[curLine][curCol].ID
}

// nearestSpanCol returns the index of the span whose start column is closest to
// targetCol, used to keep the cursor near the same horizontal position when it
// jumps between lines.
func nearestSpanCol(row []segSpan, targetCol int) int {
	best, bestDist := 0, 1<<30
	for i, sp := range row {
		d := sp.Col - targetCol
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// applyWidthRuler appends the dim width ruler when a fixed preview width is set,
// matching the old preview's constraint marker. Returns the (still ANSI) line.
func applyWidthRuler(line string, previewWidth int) string {
	if previewWidth <= 0 {
		return line
	}
	pad := previewWidth - visibleWidth(line)
	if pad < 0 {
		pad = 0
	}
	return line + strings.Repeat(" ", pad) + "\x1b[90m│\x1b[0m"
}

// paintCursorLine rebuilds one physical line from its spans, wrapping the span
// at curCol in a tview highlight region (reverse-video for the cursor; a yellow
// background while grabbing). The REAL rendered text of every segment is kept
// verbatim — only the bracketing region tags differ, so colors stay real. The
// non-span gaps (leading padding, separators) are reproduced from the original
// line by slicing on visible columns.
func paintCursorLine(line string, row []segSpan, curCol int, grabbing bool) string {
	if curCol < 0 || curCol >= len(row) {
		return ansiToTview(line)
	}
	// Work in visible columns. Reconstruct: gap-before-span0, span0, gap,
	// span1, ... by walking the stripped line and re-inserting the original
	// ANSI runs. Simpler and robust: take the original line's runes (with
	// ANSI), and split at the cursor span's visible column boundaries.
	target := row[curCol]
	pre := sliceVisible(line, 0, target.Col)
	seg := sliceVisible(line, target.Col, target.Col+target.Width)
	post := sliceVisible(line, target.Col+target.Width, -1)

	// White text on a solid truecolor background — the cursor must be the most
	// legible thing on the preview line. Indigo for the resting cursor, amber
	// while grabbing, matching the list selection color elsewhere. Hex (not the
	// 16-color names) so headless capture and real terminals agree.
	hi := "[white:#3a5bdb:b]"
	if grabbing {
		hi = "[black:#ffb000:b]"
	}
	var b strings.Builder
	b.WriteString(ansiToTview(pre))
	b.WriteString(hi)
	// Strip ANSI inside the highlighted segment so the reverse-video region
	// reads cleanly; the segment's own foreground would otherwise fight the
	// highlight background.
	b.WriteString(tview.Escape(stripANSI(seg)))
	b.WriteString("[-:-:-]")
	b.WriteString(ansiToTview(post))
	return b.String()
}

// sliceVisible returns the substring of s (which may contain ANSI escapes)
// spanning visible columns [start,end). end<0 means "to the end". ANSI escape
// sequences are preserved with the runs they precede so colors stay intact for
// the parts outside any highlight.
//
// Limitation: this parser understands CSI sequences (ESC [ ... final byte). It
// skips other escape sequences safely (including non-SGR CSI and simple
// one-letter escapes) but does not try to preserve their semantics across the
// slice window. OSC/hyperlink sequences (ESC ] ... BEL or ESC \) are not
// currently supported and will be passed through only if they fall inside the
// visible window.
func sliceVisible(s string, start, end int) string {
	var b strings.Builder
	col := 0
	i := 0
	runes := []rune(s)
	for i < len(runes) {
		// Pass through an ANSI escape sequence wholesale.
		if runes[i] == '\x1b' && i+1 < len(runes) {
			seq := ""
			seqEnd := -1
			if runes[i+1] == '[' {
				// CSI sequence: parameters end at a byte in [@A-Za-z].
				for j := i + 2; j < len(runes); j++ {
					r := runes[j]
					if ('@' <= r && r <= 'Z') || ('a' <= r && r <= 'z') {
						seqEnd = j + 1
						break
					}
				}
			} else {
				// Simple one-letter escape (e.g. ESC N, ESC c).
				seqEnd = i + 2
			}
			if seqEnd > i {
				seq = string(runes[i:seqEnd])
				// Emit escapes if we are inside the visible window (or before it,
				// so the active color carries into the window). They are zero-width
				// so they don't shift columns.
				if end < 0 || col <= end {
					b.WriteString(seq)
				}
				i = seqEnd
				continue
			}
		}
		if col >= start && (end < 0 || col < end) {
			b.WriteRune(runes[i])
		}
		col++
		i++
	}
	return b.String()
}

// moveCursorSegmentHoriz swaps the grabbed segment with its adjacent peer
// on the same line (reorder within line). It returns the id that should be
// followed across the rebuild and whether a move happened.
func moveCursorSegmentHoriz(cfg *config, curSpans [][]segSpan, curLine, curCol int, dir int) (string, bool) {
	id := cursorSegment(curSpans, curLine, curCol)
	if id == "" {
		return "", false
	}
	myLine := effectiveLine(id, *cfg)
	var peers []int
	for i, sid := range cfg.Segments {
		if effectiveLine(sid, *cfg) == myLine {
			peers = append(peers, i)
		}
	}
	pos := -1
	for i, pi := range peers {
		if cfg.Segments[pi] == id {
			pos = i
			break
		}
	}
	if pos < 0 {
		return id, false
	}
	target := pos + dir
	if target < 0 || target >= len(peers) {
		return id, false
	}
	cfg.Segments[peers[pos]], cfg.Segments[peers[target]] =
		cfg.Segments[peers[target]], cfg.Segments[peers[pos]]
	return id, true
}

// moveCursorSegmentVert relocates the grabbed segment to the adjacent line
// (real "move this segment to another line"), placing it at the column-
// nearest slot on the destination line. It returns the id that should be
// followed across the rebuild and whether a move happened.
func moveCursorSegmentVert(cfg *config, curSpans [][]segSpan, curLine, curCol int, dir int) (string, bool) {
	id := cursorSegment(curSpans, curLine, curCol)
	if id == "" {
		return "", false
	}
	myLine := effectiveLine(id, *cfg)
	targetLine := myLine + dir
	if targetLine < 1 || targetLine > 9 {
		return id, false
	}
	// Where on the destination line should it land? Use the grabbed
	// segment's current column to pick the nearest insertion slot among the
	// destination line's existing spans.
	anchorBefore := "" // insert before this segment id (or "" = append)
	if curLine >= 0 && curLine < len(curSpans) {
		myCol := 0
		if curCol >= 0 && curCol < len(curSpans[curLine]) {
			myCol = curSpans[curLine][curCol].Col
		}
		// Pick the drop slot on the destination line: the first dest-line
		// peer whose rendered column is at or past the grabbed segment's
		// column. Peers are taken in cfg order so insertion preserves it.
		var destPeers []string
		for _, sid := range cfg.Segments {
			if sid != id && effectiveLine(sid, *cfg) == targetLine {
				destPeers = append(destPeers, sid)
			}
		}
		// Map each dest peer to its rendered column if visible this frame.
		colOf := map[string]int{}
		for _, r := range curSpans {
			for _, sp := range r {
				colOf[sp.ID] = sp.Col
			}
		}
		for _, sid := range destPeers {
			if c, ok := colOf[sid]; ok && c >= myCol {
				anchorBefore = sid
				break
			}
		}
	}

	// Reassign line.
	assignSegmentLine(cfg, id, targetLine)
	// Reposition in cfg.Segments so order among dest peers matches the
	// drop column. Remove then re-insert.
	for i, sid := range cfg.Segments {
		if sid == id {
			cfg.Segments = append(cfg.Segments[:i], cfg.Segments[i+1:]...)
			break
		}
	}
	insertAt := len(cfg.Segments)
	if anchorBefore != "" {
		for i, sid := range cfg.Segments {
			if sid == anchorBefore {
				insertAt = i
				break
			}
		}
	} else {
		// Append after the last dest-line peer so it lands at the end
		// of the destination line rather than the very end of all
		// segments (which could be a different line).
		last := -1
		for i, sid := range cfg.Segments {
			if effectiveLine(sid, *cfg) == targetLine {
				last = i
			}
		}
		if last >= 0 {
			insertAt = last + 1
		}
	}
	cfg.Segments = append(cfg.Segments, "")
	copy(cfg.Segments[insertAt+1:], cfg.Segments[insertAt:])
	cfg.Segments[insertAt] = id
	return id, true
}

// insertSegmentAtCursor adds id to the render set, placing it on the
// cursor's current line, immediately after the cursor's segment so it lands
// where the user is pointing. It returns the id that should be followed across
// the rebuild and whether a change happened.
func insertSegmentAtCursor(cfg *config, curSpans [][]segSpan, curLine, curCol int, cursorID, id string) (string, bool) {
	anchorID := cursorSegment(curSpans, curLine, curCol)
	anchorLine := 0
	if anchorID != "" {
		anchorLine = effectiveLine(anchorID, *cfg)
	}
	// Drop any stale enabled entry first (shouldn't happen — palette
	// only lists off segments — but stay defensive).
	for i, sid := range cfg.Segments {
		if sid == id {
			cfg.Segments = append(cfg.Segments[:i], cfg.Segments[i+1:]...)
			break
		}
	}
	// Assign the new segment to the cursor's line.
	if anchorID != "" {
		assignSegmentLine(cfg, id, anchorLine)
	}
	// Insert right after the anchor in cfg.Segments; else append.
	insertAt := len(cfg.Segments)
	if anchorID != "" {
		for i, sid := range cfg.Segments {
			if sid == anchorID {
				insertAt = i + 1
				break
			}
		}
	}
	cfg.Segments = append(cfg.Segments, "")
	copy(cfg.Segments[insertAt+1:], cfg.Segments[insertAt:])
	cfg.Segments[insertAt] = id
	return id, true
}
