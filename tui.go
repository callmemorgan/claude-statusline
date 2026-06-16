package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

// ─── Configure Mode ──────────────────────────────────────────────────

func effectiveLine(id string, cfg config) int {
	if override, ok := cfg.Lines[id]; ok && override >= 1 {
		return override
	}
	if s, ok := segmentByID(id); ok {
		return s.line
	}
	return 1
}

// offLine is the sentinel "line" used for the disabled-segment group in the
// grouped list view. It sorts after every real render line (1–9).
const offLine = 100

// listRow is one rendered row in the grouped Segments list: either a
// non-selectable line header or a selectable segment.
type listRow struct {
	header bool
	line   int         // header: the line this header introduces (offLine = "off")
	seg    segmentInfo // segment: the segment for this row
	segIdx int         // segment: index into the visible slice
}

// groupRows turns the (possibly filtered) visible slice into the grouped list
// model: a header row per occupied line (in ascending line order, off last),
// followed by that line's segments in render order. "Render order" within a
// line is cfg.Segments order; the off group keeps the visible-slice order.
// rowToSeg maps each list-row index to its index in visible (-1 for headers).
func groupRows(visible []segmentInfo, cfg config) (rows []listRow, rowToSeg []int) {
	// Index segments by id for O(1) lookup back into visible.
	idxByID := make(map[string]int, len(visible))
	inVisible := make(map[string]bool, len(visible))
	for i, s := range visible {
		idxByID[s.id] = i
		inVisible[s.id] = true
	}
	enabled := make(map[string]bool, len(cfg.Segments))
	for _, id := range cfg.Segments {
		enabled[id] = true
	}

	// Bucket enabled+visible segments by effective line, in cfg.Segments order.
	byLine := map[int][]segmentInfo{}
	var lineOrder []int
	seen := map[int]bool{}
	for _, id := range cfg.Segments {
		if !inVisible[id] {
			continue
		}
		s := visible[idxByID[id]]
		l := effectiveLine(id, cfg)
		if !seen[l] {
			seen[l] = true
			lineOrder = append(lineOrder, l)
		}
		byLine[l] = append(byLine[l], s)
	}
	// Sort occupied lines ascending so the list's vertical order is render order.
	for i := 0; i < len(lineOrder); i++ {
		for j := i + 1; j < len(lineOrder); j++ {
			if lineOrder[j] < lineOrder[i] {
				lineOrder[i], lineOrder[j] = lineOrder[j], lineOrder[i]
			}
		}
	}

	emit := func(line int, segs []segmentInfo) {
		rows = append(rows, listRow{header: true, line: line})
		rowToSeg = append(rowToSeg, -1)
		for _, s := range segs {
			rows = append(rows, listRow{seg: s, segIdx: idxByID[s.id]})
			rowToSeg = append(rowToSeg, idxByID[s.id])
		}
	}

	for _, l := range lineOrder {
		emit(l, byLine[l])
	}

	// Off group: visible segments not enabled, in visible order.
	var off []segmentInfo
	for _, s := range visible {
		if !enabled[s.id] {
			off = append(off, s)
		}
	}
	if len(off) > 0 {
		emit(offLine, off)
	}
	return rows, rowToSeg
}

// assignLineCfg writes a segment's line override, deleting the entry when it
// equals the natural line so the config stays minimal (matches the 1–9 verb).
func assignLineCfg(cfg *config, id string, line int) {
	natural := 1
	if s, ok := segmentByID(id); ok {
		natural = s.line
	}
	if line == natural {
		delete(cfg.Lines, id)
		return
	}
	if cfg.Lines == nil {
		cfg.Lines = make(map[string]int)
	}
	cfg.Lines[id] = line
}

// reorderSegmentBefore moves id within cfg.Segments to sit immediately before
// anchor (anchor == "" → move to the end). Both must already be present.
func reorderSegmentBefore(cfg *config, id, anchor string) {
	// Remove id.
	rest := cfg.Segments[:0:0]
	for _, sid := range cfg.Segments {
		if sid != id {
			rest = append(rest, sid)
		}
	}
	if anchor == "" {
		cfg.Segments = append(rest, id)
		return
	}
	out := make([]string, 0, len(rest)+1)
	for _, sid := range rest {
		if sid == anchor {
			out = append(out, id)
		}
		out = append(out, sid)
	}
	cfg.Segments = out
}

// enabledLinesAsc returns the occupied render lines (1–9) in ascending order
// and the enabled segments on each, in cfg.Segments (render) order.
func enabledLinesAsc(cfg config) (lines []int, byLine map[int][]string) {
	byLine = map[int][]string{}
	seen := map[int]bool{}
	for _, id := range cfg.Segments {
		l := effectiveLine(id, cfg)
		if !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
		byLine[l] = append(byLine[l], id)
	}
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			if lines[j] < lines[i] {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
	return lines, byLine
}

// moveSegmentInGroup moves segment id one slot in the grouped (render) order:
// step -1 = up, +1 = down. Within a line it swaps with the adjacent peer;
// crossing a line header reassigns the line (inserting at the near edge of the
// target group); crossing the "── off ──" boundary toggles enabled. Returns
// false (no change) when the segment is already at the far end of the order.
func moveSegmentInGroup(cfg *config, id string, step int) bool {
	enabled := false
	for _, sid := range cfg.Segments {
		if sid == id {
			enabled = true
			break
		}
	}

	if !enabled {
		// In the off group, which sits at the very bottom. Only an upward move
		// is meaningful: it re-enables the segment onto the last occupied line
		// (or its natural line if nothing is enabled), at that group's bottom.
		if step >= 0 {
			return false
		}
		lines, _ := enabledLinesAsc(*cfg)
		if len(lines) == 0 {
			cfg.Segments = append(cfg.Segments, id)
			return true
		}
		last := lines[len(lines)-1]
		assignLineCfg(cfg, id, last)
		cfg.Segments = append(cfg.Segments, id) // bottom of cfg = bottom of its line
		return true
	}

	myLine := effectiveLine(id, *cfg)
	lines, byLine := enabledLinesAsc(*cfg)
	peers := byLine[myLine]
	pos := -1
	for i, sid := range peers {
		if sid == id {
			pos = i
			break
		}
	}
	// Index of myLine among occupied lines.
	li := -1
	for i, l := range lines {
		if l == myLine {
			li = i
			break
		}
	}

	if step > 0 { // down
		if pos < len(peers)-1 {
			// Swap with the next peer within the line (cfg order).
			reorderSegmentBefore(cfg, peers[pos+1], id) // put next peer before id
			return true
		}
		// Last peer on its line: cross the header below.
		if li+1 < len(lines) {
			// Into the next occupied line, at its top.
			target := lines[li+1]
			assignLineCfg(cfg, id, target)
			reorderSegmentBefore(cfg, id, byLine[target][0]) // id at top of target group
			return true
		}
		if myLine < 9 {
			// New empty line below (id is the sole occupant; keep cfg position).
			assignLineCfg(cfg, id, myLine+1)
			return true
		}
		// Last line, last peer: drop into the off group (disable).
		removeSegment(cfg, id)
		return true
	}

	// up
	if pos > 0 {
		// Swap with the previous peer within the line.
		reorderSegmentBefore(cfg, id, peers[pos-1]) // id before previous peer
		return true
	}
	// First peer on its line: cross the header above.
	if li-1 >= 0 {
		// Into the previous occupied line, at its bottom.
		target := lines[li-1]
		group := byLine[target]
		assignLineCfg(cfg, id, target)
		// Place id immediately after the last segment of the target group.
		afterAnchor := ""
		// Find the segment that currently follows the target group's last
		// element in cfg.Segments; insert before it (== after the group).
		lastInGroup := group[len(group)-1]
		idxLast := -1
		for i, sid := range cfg.Segments {
			if sid == lastInGroup {
				idxLast = i
				break
			}
		}
		if idxLast >= 0 && idxLast+1 < len(cfg.Segments) && cfg.Segments[idxLast+1] != id {
			afterAnchor = cfg.Segments[idxLast+1]
		}
		reorderSegmentBefore(cfg, id, afterAnchor)
		return true
	}
	if myLine > 1 {
		// New empty line above.
		assignLineCfg(cfg, id, myLine-1)
		return true
	}
	return false
}

// removeSegment drops a segment id from cfg.Segments (disable).
func removeSegment(cfg *config, id string) {
	out := cfg.Segments[:0:0]
	for _, sid := range cfg.Segments {
		if sid != id {
			out = append(out, sid)
		}
	}
	cfg.Segments = out
}

// rowForSegID returns the list-row index of a segment by id, or -1.
func rowForSegID(rows []listRow, id string) int {
	if id == "" {
		return -1
	}
	for i, r := range rows {
		if !r.header && r.seg.id == id {
			return i
		}
	}
	return -1
}

// firstSegRow returns the first non-header row index, or -1 if there are none.
func firstSegRow(rowToSeg []int) int {
	for i, si := range rowToSeg {
		if si >= 0 {
			return i
		}
	}
	return -1
}

// nextSegRow returns the next non-header row at or after `from` in `step`
// direction (+1 down, -1 up), or -1 if none. It never lands on a header.
func nextSegRow(rowToSeg []int, from, step int) int {
	for r := from; r >= 0 && r < len(rowToSeg); r += step {
		if rowToSeg[r] >= 0 {
			return r
		}
	}
	return -1
}

// headerLabel renders the non-selectable line-header text for the grouped list.
func headerLabel(line int) string {
	if line == offLine {
		return "[gray::d]── off ──[-:-:-]"
	}
	return fmt.Sprintf("[gray::d]── line %d ──[-:-:-]", line)
}

// filterSegments returns the segments whose id or description contains the
// query (case-insensitive). An empty query returns everything.
func filterSegments(all []segmentInfo, query string) []segmentInfo {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all
	}
	var out []segmentInfo
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.id), q) || strings.Contains(strings.ToLower(s.desc), q) {
			out = append(out, s)
		}
	}
	return out
}

// footerRows returns how many rows a footer needs at the given width, using
// tview's own word-wrap so the count matches what gets drawn. Clamped to 3 so
// a pathologically narrow terminal can't squeeze the segment list away.
func footerRows(text string, width int) int {
	if width <= 0 {
		return 1
	}
	rows := len(tview.WordWrap(text, width))
	if rows < 1 {
		return 1
	}
	if rows > 3 {
		return 3
	}
	return rows
}

// previewState returns an hour of synthetic, steadily-rising session history
// consistent with samplePayload's current numbers, so state-derived features
// (cost-rate, rate-limit projections, the context trend) render in the TUI
// preview and their settings visibly change the output. Rates: $0.42/h cost,
// 24%/h context growth (↗ ~37m to the default 80% compact threshold), 16%/h
// on the 5h quota, 0.4%/h on the 7d quota.
func previewState(now time.Time) *sessionState {
	st := &sessionState{SessionID: "tui-preview", retention: 48 * time.Hour}
	const n = 13 // a sample every 5 minutes over the last hour
	for i := 0; i < n; i++ {
		frac := float64(i) / float64(n-1)
		rl5h := 34 + 16*frac
		rl7d := 29.6 + 0.4*frac
		st.Samples = append(st.Samples, sample{
			T:      now.Add(-time.Duration(float64(time.Hour) * (1 - frac))).Unix(),
			Cost:   0.42 * frac,
			CtxPct: 41 + 24*frac,
			InTok:  int64(45678 * frac),
			OutTok: int64(1234 * frac),
			RL5h:   &rl5h,
			RL7d:   &rl7d,
		})
	}
	return st
}

func runConfigure() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "claude-statusline configure requires an interactive terminal.")
		fmt.Fprintf(os.Stderr, "Edit %s directly, or run from a terminal.\n", configPath())
		os.Exit(1)
	}

	cfg, cfgWarns := loadConfigWarn()
	initSegments(cfg.Plugins)

	// Synthetic data so every feature previews: an hour of session history
	// for the state-derived segments, and a fake rich-git result (the sample
	// payload's workspace isn't a real repo). Both are preview-only.
	pvState := previewState(time.Now())
	gitStatusPreview = &gitStatusInfo{Dirty: true, Ahead: 1, Behind: 2}
	defer func() { gitStatusPreview = nil }()
	stashPreview := 3
	gitStashPreview = &stashPreview
	defer func() { gitStashPreview = nil }()

	// demoActive animates the whole preview through all states (d). Session-
	// only, like the per-segment stress test.
	demoActive := false

	// visible is the (possibly filtered) slice the list renders from; every
	// handler resolves the selection through it, never registeredSegments.
	visible := registeredSegments

	// The list is grouped by render line with non-selectable header rows, so
	// list-row index no longer maps 1:1 to visible. rowToSeg[row] is the index
	// into visible for that row, or -1 for a header. updateUI rebuilds both.
	var rows []listRow
	var rowToSeg []int

	// dirty tracks unsaved changes; mutate is the single mutation funnel.
	dirty := false

	app := tview.NewApplication()

	// Scrollable list of all segments with toggle state.
	list := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	list.SetBorder(true)

	// segAtRow resolves a list-row index to its segment, skipping header rows.
	segAtRow := func(row int) (segmentInfo, bool) {
		if row < 0 || row >= len(rowToSeg) {
			return segmentInfo{}, false
		}
		si := rowToSeg[row]
		if si < 0 || si >= len(visible) {
			return segmentInfo{}, false
		}
		return visible[si], true
	}

	selectedSegment := func() (segmentInfo, bool) {
		return segAtRow(list.GetCurrentItem())
	}

	// Filter input, hidden until / is pressed.
	filterInput := tview.NewInputField().
		SetLabel(" / ").
		SetFieldBackgroundColor(tcell.ColorDefault)

	// Description panel — shows the description of the currently selected segment.
	descView := tview.NewTextView().SetWrap(true).SetDynamicColors(true)
	descView.SetBorder(true).SetTitle(" Description ")

	// Live preview of the statusline.
	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)

	previewBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(preview, 0, 1, false)
	previewBox.SetBorder(true).SetTitle(" Preview ")

	// Status strip: persistent context on the left (active theme), transient
	// flash messages on the right.
	stripLeft := tview.NewTextView().SetDynamicColors(true)
	stripRight := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	statusStrip := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(stripLeft, 0, 1, false).
		AddItem(stripRight, 0, 1, false)

	flashGen := 0
	flash := func(color, msg string) {
		flashGen++
		gen := flashGen
		stripRight.SetText(fmt.Sprintf("[%s]%s[-] ", color, msg))
		time.AfterFunc(2500*time.Millisecond, func() {
			app.QueueUpdateDraw(func() {
				if flashGen == gen {
					stripRight.SetText("")
				}
			})
		})
	}

	// activePreset names the last preset applied; manual edits flip it back
	// to "" ("custom"). Session-only — never persisted by the TUI.
	activePreset := ""

	updateStrip := func() {
		theme := cfg.Theme
		if theme == "" {
			theme = "classic"
		}
		preset := activePreset
		if preset == "" {
			preset = "(custom)"
		}
		marker := ""
		if dirty {
			marker = " [yellow]●[-]"
		}
		stripLeft.SetText(fmt.Sprintf(" theme: [::b]%s[-:-:-] · preset: %s%s", theme, preset, marker))
	}

	// Footer generated from the keymap table. Word-wrapped: the before-draw
	// hook grows its row to fit, so keys never trail off narrow terminals.
	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("main"))

	// Help overlay — generated from the keymap table.
	helpView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false).
		SetText(buildHelpText())
	helpView.SetBorder(true).SetTitle(" Help (r README • q/Esc close) ")

	// Full README behind the help overlay.
	readmeView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetText(markdownToTview(readmeContent))
	readmeView.SetBorder(true).SetTitle(" README (↑/↓ scroll • q/Esc back) ")

	// ─── Flyout Panel ────────────────────────────────────────────────────

	flyoutTitle := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true)

	flyoutList := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	flyoutList.SetBorder(true)

	var confirmModal *tview.Modal

	flyoutDescView := tview.NewTextView().SetWrap(true)
	flyoutDescView.SetBorder(true).SetTitle(" Description ")

	flyoutPreview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	flyoutPreview.SetBorder(true).SetTitle(" Preview ")

	flyoutHelp := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("flyout"))

	var currentFlyoutSegment string

	updateFlyout := func() {
		if currentFlyoutSegment == "" {
			return
		}
		specs := segmentSpecs(currentFlyoutSegment)
		currentIdx := flyoutList.GetCurrentItem()
		flyoutList.Clear()
		for _, sp := range specs {
			val := flyoutValueStr(currentFlyoutSegment, sp, cfg)
			mark := "  "
			display := sp.Name
			if sp.Kind == kindBool {
				if val == "on" {
					mark = "✓ "
				}
			} else {
				display = fmt.Sprintf("%s: %s", sp.Name, val)
			}
			flyoutList.AddItem(mark+display, "", 0, nil)
		}
		if currentIdx >= 0 && currentIdx < len(specs) {
			flyoutList.SetCurrentItem(currentIdx)
		}
		flyoutList.SetTitle(fmt.Sprintf(" %s settings ", currentFlyoutSegment))

		// Update preview
		p := flyoutPreviewPayload(currentFlyoutSegment, samplePayload())
		segPalette := currentPalette(cfg)
		if s, ok := segmentByID(currentFlyoutSegment); ok && segPalette.Rst != "" {
			if colorName := cfg.Colors[currentFlyoutSegment]; colorName != "" && colorName != "default" {
				segPalette = paletteWithOverride(segPalette, s.primaryColor, colorName)
			}
		}
		if s, ok := segmentByID(currentFlyoutSegment); ok {
			ctx := renderCtx{
				P:     p,
				C:     segPalette,
				S:     settingsFor(cfg, s),
				Cfg:   cfg,
				State: pvState,
				Now:   time.Now(),
			}
			if rendered, show := s.render(ctx); show {
				flyoutPreview.SetText(ansiToTview(strings.TrimLeft(rendered, " ")))
			} else {
				flyoutPreview.SetText("(segment hidden)")
			}
		}
	}

	flyoutList.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if currentFlyoutSegment == "" {
			return
		}
		specs := segmentSpecs(currentFlyoutSegment)
		if idx >= 0 && idx < len(specs) {
			flyoutDescView.SetText(specs[idx].Desc)
		} else {
			flyoutDescView.SetText("")
		}
	})

	pages := tview.NewPages()

	openFlyout := func(segID string) {
		specs := segmentSpecs(segID)
		if len(specs) == 0 {
			descView.SetText("(no configurable options for this segment)")
			return
		}
		currentFlyoutSegment = segID
		flyoutTitle.SetText(fmt.Sprintf("[yellow::b]  %s — settings[-::-]", segID))
		updateFlyout()
		flyoutDescView.SetText(specs[0].Desc)
		pages.SwitchToPage("flyout")
		app.SetFocus(flyoutList)
	}

	var updateUI func()

	// mutate funnels every config change: marks the session dirty, drops the
	// active-preset label (the layout is custom now), and refreshes the UI.
	mutate := func(fn func()) {
		fn()
		dirty = true
		activePreset = ""
		updateUI()
	}

	toggleSegment := func(id string) {
		mutate(func() {
			found := -1
			for i, segID := range cfg.Segments {
				if segID == id {
					found = i
					break
				}
			}
			if found >= 0 {
				cfg.Segments = append(cfg.Segments[:found], cfg.Segments[found+1:]...)
			} else {
				cfg.Segments = append(cfg.Segments, id)
			}
		})
	}

	// ensureEnabled appends a segment that's being customized while off.
	ensureEnabled := func(id string) {
		for _, segID := range cfg.Segments {
			if segID == id {
				return
			}
		}
		cfg.Segments = append(cfg.Segments, id)
	}

	list.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftDoubleClick && list.InRect(event.Position()) {
			if seg, ok := selectedSegment(); ok {
				openFlyout(seg.id)
			}
			return tview.MouseConsumed, nil
		}
		if action == tview.MouseLeftClick && list.InRect(event.Position()) {
			x, y := event.Position()
			innerX, innerY, _, _ := list.GetInnerRect()
			if x >= innerX && x <= innerX+1 {
				itemOff, _ := list.GetOffset()
				clickedRow := y - innerY + itemOff
				if seg, ok := segAtRow(clickedRow); ok {
					list.SetCurrentItem(clickedRow)
					app.SetFocus(list)
					toggleSegment(seg.id)
					return tview.MouseConsumed, nil
				}
			}
		}
		return action, event
	})

	// openFlyoutColorPicker opens the swatch picker for a color setting row,
	// live-previewing hovered colors through the flyout preview.
	openFlyoutColorPicker := func(sp settingSpec) {
		segID := currentFlyoutSegment
		seg, ok := segmentByID(segID)
		if !ok {
			return
		}
		orig := settingsFor(cfg, seg).Str(sp.Key)
		openColorPicker(app, pages, currentPalette(cfg), sp.Name+" — "+segID,
			func(spec string) { // hover
				setFlyoutValue(segID, sp, &cfg, spec)
				updateFlyout()
			},
			func(spec string, picked bool) { // done
				if picked {
					setFlyoutValue(segID, sp, &cfg, spec)
					pushRecentColor(spec)
					dirty = true
				} else {
					setFlyoutValue(segID, sp, &cfg, orig)
				}
				updateFlyout()
				updateStrip()
				app.SetFocus(flyoutList)
			})
	}

	// activateFlyoutRow handles "primary action" on a flyout row (space, enter,
	// double-click): bools toggle, enums cycle forward, ints step up. Enter on
	// a color row opens the swatch picker instead of cycling.
	// sync_to_all opens the confirm modal instead of mutating directly.
	activateFlyoutRow := func(idx int, viaEnter bool) {
		specs := segmentSpecs(currentFlyoutSegment)
		if idx < 0 || idx >= len(specs) {
			return
		}
		sp := specs[idx]
		if viaEnter && sp.Kind == kindColor {
			openFlyoutColorPicker(sp)
			return
		}
		if sp.Key == "sync_to_all" {
			targets := []string{}
			for _, id := range progressBarSegmentIDs() {
				if id != currentFlyoutSegment {
					targets = append(targets, id)
				}
			}
			confirmModal.SetText(fmt.Sprintf("Copy settings from %s to %s?",
				currentFlyoutSegment, strings.Join(targets, ", ")))
			pages.SwitchToPage("confirm")
			app.SetFocus(confirmModal)
			return
		}
		applyFlyoutChange(currentFlyoutSegment, sp, &cfg, 1)
		if sp.Key == "stress_test" {
			if stressTestActive[currentFlyoutSegment] {
				scheduleStressTick(app, currentFlyoutSegment, updateFlyout)
			}
		} else {
			dirty = true
			updateStrip()
		}
		updateFlyout()
	}

	flyoutList.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftDoubleClick && flyoutList.InRect(event.Position()) {
			activateFlyoutRow(flyoutList.GetCurrentItem(), true)
			return tview.MouseConsumed, nil
		}
		return action, event
	})

	flyoutList.SetSelectedFunc(func(idx int, _, _ string, _ rune) {
		activateFlyoutRow(idx, true)
	})

	flyoutTopRow := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(flyoutList, 0, 1, true).
		AddItem(flyoutDescView, 0, 3, false)

	flyoutFlex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(flyoutTitle, 1, 0, false).
		AddItem(flyoutTopRow, 0, 1, true).
		AddItem(flyoutPreview, 5, 0, false).
		AddItem(flyoutHelp, 1, 0, false)

	// describeSegment renders the description panel for a segment, including
	// the "press o" discoverability hint when it has settings.
	describeSegment := func(seg segmentInfo) {
		text := seg.desc
		if n := len(seg.settings); n > 0 {
			text += fmt.Sprintf("\n\n[gray]%d options — press o to configure[-]", n)
		}
		descView.SetText(text)
	}

	// previewWidth is the user's width override for testing reflow: 0 = auto
	// (track the preview panel's real width), else a fixed column count.
	previewWidth := 0

	// refreshPreview re-renders the preview text at the effective width. With
	// an override, lines render verbatim with a dim ruler at the constraint
	// column; in auto mode they're left-trimmed to sit flush in the panel.
	refreshPreview := func() {
		width := previewWidth
		_, _, panelW, _ := preview.GetInnerRect()
		if width == 0 && panelW > 0 {
			width = panelW
		}
		p := samplePayload()
		if demoActive {
			p = demoPreviewPayload(p, time.Now())
		}
		lines := buildStatusline(buildInput{P: p, C: currentPalette(cfg), Cfg: cfg, State: pvState, Width: width, Now: time.Now()})
		var previewText string
		if previewWidth > 0 {
			for i, l := range lines {
				pad := previewWidth - visibleWidth(l)
				if pad < 0 {
					pad = 0
				}
				lines[i] = l + strings.Repeat(" ", pad) + "\x1b[90m│\x1b[0m"
			}
			previewText = strings.Join(lines, "\n")
		} else {
			for i, l := range lines {
				lines[i] = strings.TrimLeft(l, " ")
			}
			previewText = strings.TrimSpace(strings.Join(lines, "\n"))
		}
		if strings.TrimSpace(previewText) == "" {
			previewText = "(statusline hidden — no segments enabled)"
		} else {
			previewText = ansiToTview(previewText)
		}
		preview.SetText(previewText)
		if previewWidth > 0 {
			previewBox.SetTitle(fmt.Sprintf(" Preview (%d cols — w to cycle) ", previewWidth))
		} else if panelW > 0 {
			previewBox.SetTitle(fmt.Sprintf(" Preview (auto · %d cols) ", panelW))
		}
	}

	// scheduleDemoTick drives demo mode the same way the flyout stress test
	// is driven: a self-rescheduling 50ms timer that stops re-arming once
	// demoActive flips off.
	var scheduleDemoTick func()
	scheduleDemoTick = func() {
		time.AfterFunc(50*time.Millisecond, func() {
			app.QueueUpdateDraw(func() {
				if demoActive {
					refreshPreview()
					scheduleDemoTick()
				}
			})
		})
	}

	// Update list items and preview from current cfg.
	updateUI = func() {
		// Preserve the selection by segment id — row indices shift as groups
		// are rebuilt (a toggle/move can reorder the whole list).
		var selID string
		if seg, ok := segAtRow(list.GetCurrentItem()); ok {
			selID = seg.id
		}

		rows, rowToSeg = groupRows(visible, cfg)

		list.Clear()
		for _, row := range rows {
			if row.header {
				// Non-selectable header: nil selected-func, dimmed label.
				list.AddItem(headerLabel(row.line), "", 0, nil)
				continue
			}
			s := row.seg
			mark := "  "
			for _, id := range cfg.Segments {
				if id == s.id {
					mark = "✓ "
					break
				}
			}

			line := effectiveLine(s.id, cfg)
			lineStr := ""
			if line != s.line {
				lineStr = fmt.Sprintf(" [L%d]", line)
			}

			colorStr := ""
			if colorName := cfg.Colors[s.id]; colorName != "" && colorName != "default" {
				colorStr = fmt.Sprintf("[%s]", colorName)
			}

			arrow := ""
			if len(s.settings) > 0 {
				arrow = " →"
			}
			mainText := fmt.Sprintf("%s%s%s%s", mark, s.id, lineStr, colorStr)
			if arrow != "" {
				_, _, innerWidth, _ := list.GetInnerRect()
				pad := innerWidth - tview.TaggedStringWidth(mainText) - tview.TaggedStringWidth(arrow)
				if pad < 0 {
					pad = 0
				}
				mainText += strings.Repeat(" ", pad) + arrow
			}
			list.AddItem(mainText, "", 0, nil)
		}

		// Restore selection onto the segment's new row (clamped to a segment
		// row so the highlight never rests on a header).
		if r := rowForSegID(rows, selID); r >= 0 {
			list.SetCurrentItem(r)
		} else if r := firstSegRow(rowToSeg); r >= 0 {
			list.SetCurrentItem(r)
		}
		title := fmt.Sprintf(" Segments (%d/%d) ", len(cfg.Segments), len(registeredSegments))
		if q := filterInput.GetText(); q != "" {
			title = fmt.Sprintf(" Segments (%d/%d) — /%s ", len(cfg.Segments), len(registeredSegments), q)
		}
		list.SetTitle(title)

		refreshPreview()
		updateStrip()
	}

	updateUI()

	// lastSegRow tracks the previously-selected row so the changed-func can
	// infer travel direction and bounce off header rows (covers PageUp/Down,
	// Home/End, j/k — any nav path that isn't the explicit Up/Down handler).
	lastSegRow := -1
	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if _, ok := segAtRow(idx); !ok {
			// Landed on a header: skip to the next segment row in the direction
			// of travel, falling back to the opposite direction at the ends.
			step := 1
			if idx < lastSegRow {
				step = -1
			}
			next := nextSegRow(rowToSeg, idx, step)
			if next < 0 {
				next = nextSegRow(rowToSeg, idx, -step)
			}
			if next >= 0 {
				list.SetCurrentItem(next) // re-enters this func with a segment row
			} else {
				descView.SetText("")
			}
			return
		}
		lastSegRow = idx
		if seg, ok := segAtRow(idx); ok {
			describeSegment(seg)
		}
	})
	// Seed the description for the initial selection.
	if seg, ok := selectedSegment(); ok {
		describeSegment(seg)
		lastSegRow = list.GetCurrentItem()
	}

	// ─── Filter wiring ───────────────────────────────────────────────────

	var leftCol *tview.Flex // assigned below with the layout

	showFilter := func(show bool) {
		h := 0
		if show {
			h = 1
		}
		leftCol.ResizeItem(filterInput, h, 0)
	}

	applyFilter := func(query string) {
		visible = filterSegments(registeredSegments, query)
		updateUI()
		if r := firstSegRow(rowToSeg); r >= 0 {
			list.SetCurrentItem(r)
			if seg, ok := segAtRow(r); ok {
				describeSegment(seg)
			}
		} else {
			descView.SetText("(no segments match)")
		}
	}

	clearFilter := func() {
		filterInput.SetText("")
		applyFilter("")
		showFilter(false)
		app.SetFocus(list)
	}

	filterInput.SetChangedFunc(func(text string) {
		applyFilter(text)
	})
	filterInput.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			app.SetFocus(list)
		case tcell.KeyEscape:
			clearFilter()
		}
	})
	// Surface config warnings once on open.
	if len(cfgWarns) > 0 {
		flash("yellow", fmt.Sprintf("config: %s", cfgWarns[0]))
	}

	// quitModal guards unsaved changes; resetModal guards the reset key.
	var quitModal, resetModal *tview.Modal

	requestQuit := func() {
		if !dirty {
			app.Stop()
			return
		}
		pages.SwitchToPage("quit")
		app.SetFocus(quitModal)
	}

	doSave := func() bool {
		if err := saveConfig(cfg); err != nil {
			flash("red", fmt.Sprintf("✗ save failed: %v", err))
			return false
		}
		dirty = false
		updateStrip()
		flash("green", "✓ Saved to "+configPath())
		return true
	}

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// When an overlay page is visible, only intercept close/nav keys;
		// let everything else pass through to the inner widget.
		pageName, _ := pages.GetFrontPage()
		if isPickerPage(pageName) {
			return event // pickers handle their own keys
		}
		if pageName == "help" {
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage("configure")
				app.SetFocus(list)
				return nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'q', 'Q':
					pages.SwitchToPage("configure")
					app.SetFocus(list)
					return nil
				case 'r', 'R':
					pages.SwitchToPage("readme")
					app.SetFocus(readmeView)
					return nil
				}
			}
			return event
		}
		if pageName == "readme" {
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' || event.Rune() == 'Q' {
					pages.SwitchToPage("help")
					app.SetFocus(helpView)
					return nil
				}
			}
			return event
		}
		if pageName == "flyout" {
			switch event.Key() {
			case tcell.KeyEscape:
				stopStressTest(currentFlyoutSegment)
				pages.SwitchToPage("configure")
				app.SetFocus(list)
				updateUI()
				return nil
			case tcell.KeyRune:
				r := event.Rune()
				if r == 'q' || r == 'Q' {
					stopStressTest(currentFlyoutSegment)
					pages.SwitchToPage("configure")
					app.SetFocus(list)
					updateUI()
					return nil
				}
				if r == ' ' {
					activateFlyoutRow(flyoutList.GetCurrentItem(), false)
					return nil
				}
			case tcell.KeyLeft, tcell.KeyRight:
				idx := flyoutList.GetCurrentItem()
				specs := segmentSpecs(currentFlyoutSegment)
				if idx >= 0 && idx < len(specs) && specs[idx].Kind == kindInt {
					delta := 1
					if event.Key() == tcell.KeyLeft {
						delta = -1
					}
					if event.Modifiers()&tcell.ModShift != 0 && specs[idx].Step > 1 {
						delta *= specs[idx].Step
					}
					applyFlyoutChange(currentFlyoutSegment, specs[idx], &cfg, delta)
					dirty = true
					updateFlyout()
					return nil
				}
			}
			return event
		}
		if pageName == "confirm" || pageName == "quit" || pageName == "reset" {
			// Modals handle their own keys; offer Esc/q as cancel.
			back := "configure"
			focus := tview.Primitive(list)
			if pageName == "confirm" {
				back = "flyout"
				focus = flyoutList
			}
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage(back)
				app.SetFocus(focus)
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' || event.Rune() == 'Q' {
					pages.SwitchToPage(back)
					app.SetFocus(focus)
					return nil
				}
			}
			return event
		}

		// While typing in the filter, every key belongs to the input field.
		if app.GetFocus() == filterInput {
			return event
		}

		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case '/':
				showFilter(true)
				app.SetFocus(filterInput)
				return nil
			case 'o', 'O':
				if seg, ok := selectedSegment(); ok {
					openFlyout(seg.id)
				}
				return nil
			case 'h', 'H', '?':
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case ' ':
				if seg, ok := selectedSegment(); ok {
					toggleSegment(seg.id)
				}
				return nil
			case 'c':
				seg, ok := selectedSegment()
				if !ok {
					return nil
				}
				mutate(func() {
					if cfg.Colors == nil {
						cfg.Colors = make(map[string]string)
					}
					currentColor := cfg.Colors[seg.id]
					if currentColor == "" {
						currentColor = "default"
					}
					nextColor := "default"
					for i, name := range colorCycle {
						if name == currentColor {
							nextColor = colorCycle[(i+1)%len(colorCycle)]
							break
						}
					}
					if nextColor == "default" {
						delete(cfg.Colors, seg.id)
					} else {
						cfg.Colors[seg.id] = nextColor
					}
					ensureEnabled(seg.id)
				})
				return nil
			case 'C':
				seg, ok := selectedSegment()
				if !ok {
					return nil
				}
				orig, hadOrig := cfg.Colors[seg.id]
				applyColor := func(spec string) {
					if cfg.Colors == nil {
						cfg.Colors = make(map[string]string)
					}
					if spec == "" || spec == "default" {
						delete(cfg.Colors, seg.id)
					} else {
						cfg.Colors[seg.id] = spec
					}
					refreshPreview()
				}
				openColorPicker(app, pages, currentPalette(cfg), "color — "+seg.id,
					applyColor,
					func(spec string, picked bool) {
						if picked {
							mutate(func() {
								applyColor(spec)
								ensureEnabled(seg.id)
							})
							pushRecentColor(spec)
						} else {
							if hadOrig {
								cfg.Colors[seg.id] = orig
							} else {
								delete(cfg.Colors, seg.id)
							}
							updateUI()
						}
						app.SetFocus(list)
					})
				return nil
			default:
				r := event.Rune()
				if r >= '1' && r <= '9' {
					seg, ok := selectedSegment()
					if !ok {
						return nil
					}
					mutate(func() {
						n := int(r - '0')
						if cfg.Lines == nil {
							cfg.Lines = make(map[string]int)
						}
						if seg.line == n {
							delete(cfg.Lines, seg.id)
						} else {
							cfg.Lines[seg.id] = n
						}
						ensureEnabled(seg.id)
					})
					return nil
				}
			case 't', 'T':
				origTheme := cfg.Theme
				openThemePicker(app, pages, cfg.Theme,
					func(id string) { // hover
						if id == "classic" {
							id = ""
						}
						cfg.Theme = id
						refreshPreview()
						updateStrip()
					},
					func(id string, picked bool) {
						if picked {
							if id == "classic" {
								id = ""
							}
							cfg.Theme = id
							dirty = true
							flash("green", "theme: "+func() string {
								if id == "" {
									return "classic"
								}
								return id
							}())
						} else {
							cfg.Theme = origTheme
						}
						refreshPreview()
						updateStrip()
						app.SetFocus(list)
					})
				return nil
			case 'p', 'P':
				snapshot := cloneConfig(cfg)
				openPresetPicker(app, pages,
					func(id string) { // hover
						if p, ok := presetByID(id); ok {
							cfg = cloneConfig(snapshot)
							applyPreset(&cfg, p)
							updateUI()
						}
					},
					func(id string, picked bool) {
						if picked {
							if p, ok := presetByID(id); ok {
								cfg = cloneConfig(snapshot)
								applyPreset(&cfg, p)
								dirty = true
								activePreset = id
								flash("green", "preset: "+id+" (not yet saved)")
							}
						} else {
							cfg = snapshot
						}
						updateUI()
						app.SetFocus(list)
					})
				return nil
			case 'w', 'W':
				switch previewWidth {
				case 0:
					previewWidth = 80
				case 80:
					previewWidth = 60
				case 60:
					previewWidth = 40
				default:
					previewWidth = 0
				}
				refreshPreview()
				return nil
			case 'd', 'D':
				demoActive = !demoActive
				if demoActive {
					flash("green", "demo on — d to stop")
					scheduleDemoTick()
				} else {
					flash("yellow", "demo off")
					refreshPreview()
				}
				return nil
			case 'v', 'V':
				// Render straight to the terminal with the TUI hidden: the
				// in-TUI preview approximates colors with tview tags, but
				// only the real terminal shows the theme against its actual
				// background, font, and color handling.
				app.Suspend(func() {
					w, _, err := term.GetSize(int(os.Stdout.Fd()))
					if err != nil || w <= 0 {
						w = 80
					}
					lines := buildStatusline(buildInput{P: samplePayload(), C: currentPalette(cfg), Cfg: cfg, State: pvState, Width: w, Now: time.Now()})
					themeName := cfg.Theme
					if themeName == "" {
						themeName = "classic"
					}
					fmt.Printf("\n  theme: %s · %d cols — as rendered by your terminal\n\n", themeName, w)
					for _, l := range lines {
						fmt.Println(l)
					}
					fmt.Print("\n  press enter to return to the configurator… ")
					_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
				})
				return nil
			case 'r', 'R':
				pages.SwitchToPage("reset")
				app.SetFocus(resetModal)
				return nil
			case 's', 'S':
				doSave()
				return nil
			case 'q', 'Q':
				requestQuit()
				return nil
			}
		case tcell.KeyEscape:
			if filterInput.GetText() != "" {
				clearFilter()
				return nil
			}
			requestQuit()
			return nil
		case tcell.KeyUp, tcell.KeyDown:
			step := -1
			if event.Key() == tcell.KeyDown {
				step = 1
			}
			// Unmodified Up/Down: navigate the list, skipping header rows so the
			// highlight never lands on a "── line N ──" divider.
			if event.Modifiers()&tcell.ModShift == 0 {
				cur := list.GetCurrentItem()
				if r := nextSegRow(rowToSeg, cur+step, step); r >= 0 {
					list.SetCurrentItem(r)
				}
				return nil
			}
			// Shift+Up / Shift+Down: move the selected segment one slot in the
			// grouped (== render) order. Within a line this reorders it among
			// its peers; crossing a line header reassigns its line; crossing
			// the "── off ──" header toggles the segment on/off.
			seg, ok := selectedSegment()
			if !ok {
				return nil
			}
			// Probe first so a no-op move (already at an end) doesn't flip dirty.
			probe := cloneConfig(cfg)
			if moveSegmentInGroup(&probe, seg.id, step) {
				mutate(func() { moveSegmentInGroup(&cfg, seg.id, step) })
			}
			return nil
		case tcell.KeyLeft, tcell.KeyRight:
			seg, ok := selectedSegment()
			if !ok {
				return event
			}
			myLine := effectiveLine(seg.id, cfg)
			// Collect indices in cfg.Segments that share the same line, in order.
			var peers []int
			for i, sid := range cfg.Segments {
				if effectiveLine(sid, cfg) == myLine {
					peers = append(peers, i)
				}
			}
			// Find this segment's position within peers.
			pos := -1
			for i, pi := range peers {
				if cfg.Segments[pi] == seg.id {
					pos = i
					break
				}
			}
			if event.Key() == tcell.KeyLeft && pos > 0 {
				mutate(func() {
					cfg.Segments[peers[pos]], cfg.Segments[peers[pos-1]] =
						cfg.Segments[peers[pos-1]], cfg.Segments[peers[pos]]
				})
				return nil
			} else if event.Key() == tcell.KeyRight && pos >= 0 && pos < len(peers)-1 {
				mutate(func() {
					cfg.Segments[peers[pos]], cfg.Segments[peers[pos+1]] =
						cfg.Segments[peers[pos+1]], cfg.Segments[peers[pos]]
				})
				return nil
			}
		}
		return event
	})

	leftCol = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(filterInput, 0, 0, false).
		AddItem(list, 0, 1, true)

	topRow := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(leftCol, 0, 1, true).
		AddItem(descView, 0, 3, false)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(topRow, 0, 1, true).
		AddItem(previewBox, 12, 0, false).
		AddItem(statusStrip, 1, 0, false).
		AddItem(help, 1, 0, false)

	pages.AddPage("configure", flex, true, true)
	pages.AddPage("help", helpView, true, false)
	pages.AddPage("readme", readmeView, true, false)
	pages.AddPage("flyout", flyoutFlex, true, false)

	// Re-render the preview when the terminal (and so the panel) resizes —
	// only the text is recomputed, never the list, to avoid re-entrancy —
	// and grow the footers to however many rows their keys need at this
	// width, so commands never trail off the end.
	lastAutoWidth := -1
	lastScreenWidth := -1
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		if previewWidth == 0 {
			if _, _, w, _ := preview.GetInnerRect(); w != lastAutoWidth {
				lastAutoWidth = w
				refreshPreview()
			}
		}
		if sw, _ := screen.Size(); sw != lastScreenWidth {
			lastScreenWidth = sw
			flex.ResizeItem(help, footerRows(footerText("main"), sw), 0)
			flyoutFlex.ResizeItem(flyoutHelp, footerRows(footerText("flyout"), sw), 0)
		}
		return false
	})

	confirmModal = tview.NewModal().
		SetText("Copy these settings to all progress bar segments?").
		AddButtons([]string{"Yes", "No"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Yes" {
				syncSettingsToAllBars(&cfg, currentFlyoutSegment)
				dirty = true
			}
			pages.SwitchToPage("flyout")
			app.SetFocus(flyoutList)
			updateFlyout()
		})
	pages.AddPage("confirm", confirmModal, true, false)

	resetModal = tview.NewModal().
		SetText("Reset to defaults? This discards your current layout, colors, and settings.").
		AddButtons([]string{"Reset", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			pages.SwitchToPage("configure")
			app.SetFocus(list)
			if buttonLabel == "Reset" {
				mutate(func() { cfg = defaultConfig() })
				flash("yellow", "reset to defaults (not yet saved)")
			}
		})
	pages.AddPage("reset", resetModal, true, false)

	quitModal = tview.NewModal().
		SetText("Unsaved changes.").
		AddButtons([]string{"Save & quit", "Discard", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			switch buttonLabel {
			case "Save & quit":
				if doSave() {
					app.Stop()
					fmt.Printf("Saved to %s\n", configPath())
					return
				}
				pages.SwitchToPage("configure")
				app.SetFocus(list)
			case "Discard":
				app.Stop()
			default:
				pages.SwitchToPage("configure")
				app.SetFocus(list)
			}
		})
	pages.AddPage("quit", quitModal, true, false)

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
