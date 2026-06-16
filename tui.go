package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
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

	// dirty tracks unsaved changes; mutate is the single mutation funnel.
	dirty := false

	app := tview.NewApplication()

	// ─── Drawer + Canvas (shelf + stage) ─────────────────────────────────
	//
	// The layout is split into two panes. The DRAWER (left) is the inventory:
	// every segment that is currently OFF (not in cfg.Segments), optionally
	// narrowed by the / filter. The CANVAS (centre) is the layout: the ENABLED
	// segments, grouped by render line, in cfg.Segments order. Enter/space moves
	// the focused segment between the two (off↔on). Focus moves between panes
	// with Tab or ←/→; within the canvas a grabbed segment is repositioned with
	// the arrows (←/→ across slots in a line, ↑/↓ across lines).

	// Description panel — shows the description of the focused-pane selection.
	// Declared early because the focus-chrome helper writes to it.
	descView := tview.NewTextView().SetWrap(true).SetDynamicColors(true)
	descView.SetBorder(true).SetTitle(" Description ")

	// focusPane tracks which pane owns the keyboard: "drawer" or "canvas".
	focusPane := "canvas"

	// drawerSegs is the off-segment slice the drawer renders, after the filter.
	// drawerRowSeg maps each drawer row index → its segmentInfo (1:1 today, but
	// resolved through the slice so the invariant stays explicit).
	var drawerSegs []segmentInfo

	// canvasRowSeg maps each canvas list row → a segment id, or "" for a
	// non-selectable line-header row. The canvas inserts a header per occupied
	// line, so the row↔segment mapping is NOT 1:1 — every handler resolves the
	// canvas selection through this slice, never by raw row index.
	var canvasRowSeg []string

	// grabbed is the id of the segment currently "picked up" on the canvas for
	// repositioning (grab/move gesture), or "" when nothing is grabbed.
	grabbed := ""

	// Selection styles. The focused pane gets a bright, high-contrast cursor
	// (black text on the accent yellow that also colors its border); the
	// unfocused pane keeps a dim slate cursor so its position is still visible
	// but plainly secondary. Both force the row's foreground so a dim/colored
	// item label can never wash out against the selection background.
	// Explicit truecolor backgrounds so the cursor reads the same on every
	// terminal: the bright accent must out-contrast the dim one. (ANSI-palette
	// "yellow"/103 renders darker than a 256-grey on some emulators, which would
	// invert the focus cue — pin RGB to avoid it.)
	focusedSelStyle := tcell.StyleDefault.
		Background(tcell.NewRGBColor(0xE6, 0xC0, 0x4A)). // warm amber
		Foreground(tcell.ColorBlack).
		Bold(true)
	unfocusedSelStyle := tcell.StyleDefault.
		Background(tcell.NewRGBColor(0x3A, 0x3F, 0x4B)). // muted slate
		Foreground(tcell.NewRGBColor(0xC8, 0xCC, 0xD4))

	// Drawer list: the available/off segments.
	drawer := tview.NewList().
		SetHighlightFullLine(true).
		ShowSecondaryText(false)
	drawer.SetBorder(true)

	// Canvas list: the enabled segments, grouped by render line.
	canvas := tview.NewList().
		SetHighlightFullLine(true).
		ShowSecondaryText(false)
	canvas.SetBorder(true)

	// canvasHasSegment reports whether the canvas holds at least one selectable
	// (non-header) row — i.e. any enabled segment that actually resolved.
	canvasHasSegment := func() bool {
		for _, sid := range canvasRowSeg {
			if sid != "" {
				return true
			}
		}
		return false
	}

	// canvasSelectedID returns the segment id under the canvas cursor, skipping
	// header rows. Returns ("", false) when the cursor sits on a header or the
	// canvas is empty.
	canvasSelectedID := func() (string, bool) {
		idx := canvas.GetCurrentItem()
		if idx < 0 || idx >= len(canvasRowSeg) {
			return "", false
		}
		if canvasRowSeg[idx] == "" {
			return "", false
		}
		return canvasRowSeg[idx], true
	}

	// selectedSegment returns the segmentInfo under the focused pane's cursor.
	// This is the single selection resolver the rest of the handlers lean on.
	selectedSegment := func() (segmentInfo, bool) {
		if focusPane == "drawer" {
			idx := drawer.GetCurrentItem()
			if idx < 0 || idx >= len(drawerSegs) {
				return segmentInfo{}, false
			}
			return drawerSegs[idx], true
		}
		id, ok := canvasSelectedID()
		if !ok {
			return segmentInfo{}, false
		}
		return segmentByID(id)
	}

	// updateUI/describeSegment/rebuild* are assigned further down; focus changes
	// and reposition helpers call them, so they're forward-declared here.
	var updateUI func()
	var describeSegment func(seg segmentInfo)
	var rebuildDrawer func(wantID string)
	var rebuildCanvas func(wantID string)

	// focusedPrimitive returns the tview widget that currently owns focus.
	focusedPrimitive := func() tview.Primitive {
		if focusPane == "drawer" {
			return drawer
		}
		return canvas
	}

	// syncFocusChrome highlights the focused pane (bright border, accented
	// title) and dims the other, then refreshes the description for whatever the
	// focused pane has selected.
	syncFocusChrome := func() {
		if focusPane == "drawer" {
			drawer.SetBorderColor(tcell.ColorYellow)
			canvas.SetBorderColor(tcell.ColorGray)
			drawer.SetSelectedStyle(focusedSelStyle)
			canvas.SetSelectedStyle(unfocusedSelStyle)
		} else {
			canvas.SetBorderColor(tcell.ColorYellow)
			drawer.SetBorderColor(tcell.ColorGray)
			canvas.SetSelectedStyle(focusedSelStyle)
			drawer.SetSelectedStyle(unfocusedSelStyle)
		}
		if describeSegment != nil {
			if seg, ok := selectedSegment(); ok {
				describeSegment(seg)
			} else {
				descView.SetText("")
			}
		}
	}

	// setFocusPane moves keyboard focus to a pane ("drawer"/"canvas"). Grabbing
	// is cancelled when focus leaves the canvas — a grabbed segment can only be
	// repositioned while the canvas is focused.
	setFocusPane := func(pane string) {
		if pane != "drawer" && pane != "canvas" {
			return
		}
		if pane == "drawer" && len(drawerSegs) == 0 {
			// Nothing to focus in an empty drawer; stay put.
			return
		}
		if pane == "drawer" {
			grabbed = ""
		}
		focusPane = pane
		app.SetFocus(focusedPrimitive())
		syncFocusChrome()
	}

	// Filter input, hidden until / is pressed.
	filterInput := tview.NewInputField().
		SetLabel(" / ").
		SetFieldBackgroundColor(tcell.ColorDefault)

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
		SetSelectedStyle(focusedSelStyle).
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

	// returnToConfigure dismisses an overlay (flyout, picker, or modal) back to
	// the home screen and restores focus to whichever pane currently owns it.
	returnToConfigure := func() {
		pages.SwitchToPage("configure")
		app.SetFocus(focusedPrimitive())
	}

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

	// ─── Canvas repositioning ────────────────────────────────────────────

	// segIndex returns id's index in cfg.Segments, or -1.
	segIndex := func(id string) int {
		for i, sid := range cfg.Segments {
			if sid == id {
				return i
			}
		}
		return -1
	}

	// enableToCanvas turns an off segment on (appended to cfg.Segments), then
	// moves focus to the canvas with the cursor on the freshly-placed segment.
	enableToCanvas := func(id string) {
		mutate(func() {
			if segIndex(id) < 0 {
				cfg.Segments = append(cfg.Segments, id)
			}
		})
		setFocusPane("canvas")
		rebuildCanvas(id)
	}

	// disableToDrawer turns an enabled segment off, then moves focus to the
	// drawer with the cursor on it (when the filter still admits it).
	disableToDrawer := func(id string) {
		mutate(func() {
			if i := segIndex(id); i >= 0 {
				cfg.Segments = append(cfg.Segments[:i], cfg.Segments[i+1:]...)
			}
		})
		// Land focus on the drawer if the now-off segment is visible there.
		for _, s := range drawerSegs {
			if s.id == id {
				setFocusPane("drawer")
				rebuildDrawer(id)
				if seg, ok := segmentByID(id); ok {
					describeSegment(seg)
				}
				return
			}
		}
	}

	// moveSelectedAcrossPanes moves the focused-pane selection between the drawer
	// (off) and the canvas (on). Shared by the space and enter handlers.
	moveSelectedAcrossPanes := func() {
		seg, ok := selectedSegment()
		if !ok {
			return
		}
		if focusPane == "drawer" {
			enableToCanvas(seg.id)
		} else {
			disableToDrawer(seg.id)
		}
	}

	// dropGrabbed releases a grabbed canvas segment (if any) and repaints the
	// canvas so its grab marker clears. Reports whether something was dropped.
	dropGrabbed := func() bool {
		if grabbed == "" {
			return false
		}
		id := grabbed
		grabbed = ""
		flash("yellow", "dropped")
		rebuildCanvas(id)
		return true
	}

	// canvasReorderHoriz swaps id with its neighbour among the segments on the
	// same render line (dir -1 = earlier slot, +1 = later). No-op at the ends.
	canvasReorderHoriz := func(id string, dir int) bool {
		myLine := effectiveLine(id, cfg)
		var peers []int // indices into cfg.Segments on this line, in order
		for i, sid := range cfg.Segments {
			if effectiveLine(sid, cfg) == myLine {
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
			return false
		}
		tgt := pos + dir
		if tgt < 0 || tgt >= len(peers) {
			return false
		}
		mutate(func() {
			cfg.Segments[peers[pos]], cfg.Segments[peers[tgt]] =
				cfg.Segments[peers[tgt]], cfg.Segments[peers[pos]]
		})
		rebuildCanvas(id)
		return true
	}

	// canvasMoveVert moves id to the adjacent render line (dir -1 = up, +1 =
	// down), clamped to 1-9, and regroups it in cfg.Segments so it sits with that
	// line's other segments. Setting the line back to natural drops the override.
	canvasMoveVert := func(id string, dir int) bool {
		s, ok := segmentByID(id)
		if !ok {
			return false
		}
		myLine := effectiveLine(id, cfg)
		target := myLine + dir
		if target < 1 || target > 9 {
			return false
		}
		mutate(func() {
			if cfg.Lines == nil {
				cfg.Lines = make(map[string]int)
			}
			if target == s.line {
				delete(cfg.Lines, id)
			} else {
				cfg.Lines[id] = target
			}
			// Regroup in cfg.Segments: place id adjacent to the target line's
			// run. Moving down → after the last target-line segment; moving up →
			// before the first. Computed against the slice *without* id.
			rest := []string{}
			for _, sid := range cfg.Segments {
				if sid != id {
					rest = append(rest, sid)
				}
			}
			insertAt := len(rest)
			if dir > 0 { // after the last segment already on the target line
				insertAt = 0
				for i, sid := range rest {
					if effectiveLine(sid, cfg) == target {
						insertAt = i + 1
					}
				}
				if insertAt == 0 { // target line empty: keep id where it was-ish
					insertAt = len(rest)
				}
			} else { // before the first segment on the target line
				insertAt = len(rest)
				for i, sid := range rest {
					if effectiveLine(sid, cfg) == target {
						insertAt = i
						break
					}
				}
			}
			if insertAt < 0 {
				insertAt = 0
			}
			if insertAt > len(rest) {
				insertAt = len(rest)
			}
			out := make([]string, 0, len(rest)+1)
			out = append(out, rest[:insertAt]...)
			out = append(out, id)
			out = append(out, rest[insertAt:]...)
			cfg.Segments = out
		})
		rebuildCanvas(id)
		return true
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

	// Drawer mouse: click a row to focus + select it; double-click moves it onto
	// the canvas (off→on).
	drawer.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if !drawer.InRect(event.Position()) {
			return action, event
		}
		_, y := event.Position()
		_, innerY, _, _ := drawer.GetInnerRect()
		itemOff, _ := drawer.GetOffset()
		clickedIdx := y - innerY + itemOff
		if clickedIdx < 0 || clickedIdx >= len(drawerSegs) {
			// Empty space below the last item: claim focus for the drawer
			// rather than letting tview's native handler desync focusPane.
			if action == tview.MouseLeftClick || action == tview.MouseLeftDoubleClick {
				setFocusPane("drawer")
				return tview.MouseConsumed, nil
			}
			return action, event
		}
		if action == tview.MouseLeftDoubleClick {
			drawer.SetCurrentItem(clickedIdx)
			toggleSegment(drawerSegs[clickedIdx].id) // off → on (to canvas)
			return tview.MouseConsumed, nil
		}
		if action == tview.MouseLeftClick {
			drawer.SetCurrentItem(clickedIdx)
			setFocusPane("drawer")
			return tview.MouseConsumed, nil
		}
		return action, event
	})

	// Canvas mouse: click a segment row to focus + select it; double-click opens
	// its settings flyout. Header rows are inert.
	canvas.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if !canvas.InRect(event.Position()) {
			return action, event
		}
		_, y := event.Position()
		_, innerY, _, _ := canvas.GetInnerRect()
		itemOff, _ := canvas.GetOffset()
		clickedIdx := y - innerY + itemOff
		onSegment := clickedIdx >= 0 && clickedIdx < len(canvasRowSeg) && canvasRowSeg[clickedIdx] != ""
		if !onSegment {
			// Header row or empty space: a left-click still belongs to the
			// canvas, so claim focus here rather than letting tview's native
			// handler move the cursor onto a non-selectable header. Consume so
			// the pane state stays in sync; the cursor keeps its valid row.
			if action == tview.MouseLeftClick || action == tview.MouseLeftDoubleClick {
				setFocusPane("canvas")
				return tview.MouseConsumed, nil
			}
			return action, event
		}
		if action == tview.MouseLeftDoubleClick {
			canvas.SetCurrentItem(clickedIdx)
			openFlyout(canvasRowSeg[clickedIdx])
			return tview.MouseConsumed, nil
		}
		if action == tview.MouseLeftClick {
			canvas.SetCurrentItem(clickedIdx)
			setFocusPane("canvas")
			return tview.MouseConsumed, nil
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
	describeSegment = func(seg segmentInfo) {
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

	// isEnabled reports whether a segment id is in cfg.Segments.
	isEnabled := func(id string) bool { return segIndex(id) >= 0 }

	// canvasOrder returns the enabled segment ids grouped by render line: a
	// slice of (line, ids) buckets in ascending line order, each bucket's ids in
	// cfg.Segments order. This is the canonical canvas layout the render path
	// also produces (effectiveLine + cfg.Segments order).
	type lineBucket struct {
		line int
		ids  []string
	}
	canvasOrder := func() []lineBucket {
		byLine := map[int][]string{}
		var order []int
		for _, sid := range cfg.Segments {
			if _, ok := segmentByID(sid); !ok {
				continue
			}
			l := effectiveLine(sid, cfg)
			if _, seen := byLine[l]; !seen {
				order = append(order, l)
			}
			byLine[l] = append(byLine[l], sid)
		}
		sort.Ints(order) // ascending render-line order
		out := make([]lineBucket, 0, len(order))
		for _, l := range order {
			out = append(out, lineBucket{line: l, ids: byLine[l]})
		}
		return out
	}

	// segDecor returns the two trailing label decorations a segment row shows in
	// either pane: its color tag (when overridden) and the "→ has settings" arrow.
	segDecor := func(s segmentInfo) (colorStr, arrow string) {
		if colorName := cfg.Colors[s.id]; colorName != "" && colorName != "default" {
			colorStr = fmt.Sprintf("  [%s]", colorName)
		}
		if len(s.settings) > 0 {
			arrow = " →"
		}
		return colorStr, arrow
	}

	// rememberSel captures the focused pane's selected segment id so a rebuild
	// can restore the cursor onto the same segment even though row indices shift.
	rememberSel := func() string {
		if seg, ok := selectedSegment(); ok {
			return seg.id
		}
		return ""
	}

	// rebuildDrawer fills the drawer with the off segments (optionally filtered),
	// restoring the cursor onto wantID when present.
	rebuildDrawer = func(wantID string) {
		query := filterInput.GetText()
		drawerSegs = drawerSegs[:0]
		for _, s := range filterSegments(registeredSegments, query) {
			if !isEnabled(s.id) {
				drawerSegs = append(drawerSegs, s)
			}
		}
		drawer.Clear()
		want := 0
		for i, s := range drawerSegs {
			colorStr, arrow := segDecor(s)
			drawer.AddItem(fmt.Sprintf("%s%s%s", s.id, colorStr, arrow), "", 0, nil)
			if s.id == wantID {
				want = i
			}
		}
		if len(drawerSegs) > 0 {
			drawer.SetCurrentItem(want)
		}
		title := fmt.Sprintf(" Drawer · available (%d) ", len(drawerSegs))
		if q := filterInput.GetText(); q != "" {
			title = fmt.Sprintf(" Drawer · available (%d) — /%s ", len(drawerSegs), q)
		}
		drawer.SetTitle(title)
	}

	// rebuildCanvas fills the canvas with the enabled segments grouped by line,
	// inserting an inert header row per occupied line. canvasRowSeg maps every
	// row back to a segment id ("" for headers). Restores the cursor onto wantID.
	rebuildCanvas = func(wantID string) {
		canvasRowSeg = canvasRowSeg[:0]
		canvas.Clear()
		want := -1
		for _, b := range canvasOrder() {
			canvas.AddItem(fmt.Sprintf("[gray::b]── line %d ──[-::-]", b.line), "", 0, nil)
			canvasRowSeg = append(canvasRowSeg, "")
			for _, sid := range b.ids {
				s, _ := segmentByID(sid)
				colorStr, arrow := segDecor(s)
				grab := "  "
				if grabbed == sid {
					// The grabbed row is always the focused-pane cursor, so the
					// marker rides on the bright yellow selection — black keeps
					// it visible there (a yellow glyph would vanish).
					grab = "[black::b]✥[-::-] "
				}
				canvas.AddItem(fmt.Sprintf("%s%s%s%s", grab, sid, colorStr, arrow), "", 0, nil)
				if sid == wantID {
					want = len(canvasRowSeg)
				}
				canvasRowSeg = append(canvasRowSeg, sid)
			}
		}
		if want < 0 {
			// Fall back to the first selectable (segment) row.
			for i, sid := range canvasRowSeg {
				if sid != "" {
					want = i
					break
				}
			}
		}
		if want >= 0 {
			canvas.SetCurrentItem(want)
		}
		canvas.SetTitle(fmt.Sprintf(" Canvas · layout (%d on) ", len(cfg.Segments)))
	}

	// Update both panes and the preview from the current cfg.
	updateUI = func() {
		drawerSel := ""
		canvasSel := ""
		if focusPane == "drawer" {
			drawerSel = rememberSel()
		} else {
			canvasSel = rememberSel()
		}
		rebuildDrawer(drawerSel)
		rebuildCanvas(canvasSel)
		// If the focused pane went empty (e.g. the last off segment was enabled,
		// or every enabled segment resolved to nothing), move focus to the other
		// pane so the cursor always lands on something live.
		if focusPane == "drawer" && len(drawerSegs) == 0 && canvasHasSegment() {
			focusPane = "canvas"
			app.SetFocus(canvas)
		} else if focusPane == "canvas" && !canvasHasSegment() && len(drawerSegs) > 0 {
			focusPane = "drawer"
			app.SetFocus(drawer)
		}
		syncFocusChrome()
		refreshPreview()
		updateStrip()
	}

	updateUI()

	// Keep the description in sync as the cursor moves within either pane.
	drawer.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if focusPane == "drawer" {
			if idx >= 0 && idx < len(drawerSegs) {
				describeSegment(drawerSegs[idx])
			}
		}
	})
	canvas.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if focusPane != "canvas" {
			return
		}
		if idx >= 0 && idx < len(canvasRowSeg) && canvasRowSeg[idx] != "" {
			if seg, ok := segmentByID(canvasRowSeg[idx]); ok {
				describeSegment(seg)
			}
		} else {
			descView.SetText("[gray](line header)[-]")
		}
	})
	// Seed the description for the initial selection.
	if seg, ok := selectedSegment(); ok {
		describeSegment(seg)
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
		// The filter only narrows the drawer (the inventory of off segments).
		rebuildDrawer(query)
		refreshPreview()
		updateStrip()
		if len(drawerSegs) > 0 {
			drawer.SetCurrentItem(0)
			if focusPane == "drawer" {
				describeSegment(drawerSegs[0])
			}
		} else if focusPane == "drawer" {
			descView.SetText("(no available segments match)")
		}
	}

	clearFilter := func() {
		filterInput.SetText("")
		applyFilter("")
		showFilter(false)
		app.SetFocus(focusedPrimitive())
	}

	filterInput.SetChangedFunc(func(text string) {
		applyFilter(text)
	})
	filterInput.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			// The filter narrows the drawer, so land focus there — but if it
			// matched nothing, setFocusPane("drawer") no-ops, so fall back to
			// the canvas rather than stranding focus on the hidden input.
			if len(drawerSegs) > 0 {
				setFocusPane("drawer")
			} else {
				setFocusPane("canvas")
			}
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
				returnToConfigure()
				return nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'q', 'Q':
					returnToConfigure()
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
				returnToConfigure()
				updateUI()
				return nil
			case tcell.KeyRune:
				r := event.Rune()
				if r == 'q' || r == 'Q' {
					stopStressTest(currentFlyoutSegment)
					returnToConfigure()
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
			focus := focusedPrimitive()
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
				// Space mirrors enter: drop a grabbed canvas segment first,
				// otherwise move the focused segment between panes (off↔on).
				if dropGrabbed() {
					return nil
				}
				moveSelectedAcrossPanes()
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
						app.SetFocus(focusedPrimitive())
					})
				return nil
			case 'g', 'G', 'm':
				// Grab / drop the focused canvas segment for repositioning.
				if focusPane != "canvas" {
					return nil
				}
				if id, ok := canvasSelectedID(); ok {
					if grabbed == id {
						dropGrabbed()
					} else {
						grabbed = id
						flash("green", "grabbed "+id+" — arrows move it, g/enter to drop")
						rebuildCanvas(id)
					}
				}
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
					// Sending to a line implies it's on the canvas now.
					setFocusPane("canvas")
					rebuildCanvas(seg.id)
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
						app.SetFocus(focusedPrimitive())
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
						app.SetFocus(focusedPrimitive())
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
			if dropGrabbed() {
				return nil
			}
			if filterInput.GetText() != "" {
				clearFilter()
				return nil
			}
			requestQuit()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			// Tab / Shift-Tab toggle focus between the two panes.
			if focusPane == "drawer" {
				setFocusPane("canvas")
			} else {
				setFocusPane("drawer")
			}
			return nil
		case tcell.KeyEnter:
			// Enter mirrors space: move the focused segment between panes —
			// except a grabbed canvas segment, where Enter drops it.
			if dropGrabbed() {
				return nil
			}
			moveSelectedAcrossPanes()
			return nil
		case tcell.KeyUp, tcell.KeyDown:
			down := event.Key() == tcell.KeyDown
			// Canvas + grabbed: the arrows reposition the segment instead of
			// moving the cursor. ↑/↓ move it across render lines.
			if focusPane == "canvas" && grabbed != "" {
				dir := -1
				if down {
					dir = 1
				}
				canvasMoveVert(grabbed, dir)
				return nil
			}
			// Canvas navigation must skip the inert line-header rows.
			if focusPane == "canvas" {
				n := canvas.GetItemCount()
				if n == 0 {
					return nil
				}
				cur := canvas.GetCurrentItem()
				next := cur
				step := -1
				if down {
					step = 1
				}
				for i := 0; i < n; i++ {
					next += step
					if next < 0 || next >= n {
						return nil // at an edge: stop, don't wrap
					}
					if next < len(canvasRowSeg) && canvasRowSeg[next] != "" {
						canvas.SetCurrentItem(next)
						return nil
					}
				}
				return nil
			}
			// Drawer: plain list navigation.
			return event
		case tcell.KeyLeft, tcell.KeyRight:
			right := event.Key() == tcell.KeyRight
			// Canvas + grabbed: ←/→ reorder the segment within its render line.
			if focusPane == "canvas" && grabbed != "" {
				dir := -1
				if right {
					dir = 1
				}
				canvasReorderHoriz(grabbed, dir)
				return nil
			}
			// Otherwise ←/→ move focus between panes: ← to the drawer, → to the
			// canvas. This is the "across regions" gesture.
			if right {
				setFocusPane("canvas")
			} else {
				setFocusPane("drawer")
			}
			return nil
		}
		return event
	})

	// leftCol = filter + drawer (the inventory shelf).
	leftCol = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(filterInput, 0, 0, false).
		AddItem(drawer, 0, 1, false)

	// topRow = drawer | canvas | description. The canvas (the stage) gets the
	// most room; the description rides along on the right.
	topRow := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(leftCol, 0, 2, false).
		AddItem(canvas, 0, 3, true).
		AddItem(descView, 0, 2, false)

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
			returnToConfigure()
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
				returnToConfigure()
			case "Discard":
				app.Stop()
			default:
				returnToConfigure()
			}
		})
	pages.AddPage("quit", quitModal, true, false)

	// Start on the canvas (the layout). If it has no selectable rows (nothing
	// enabled, or every enabled id was unknown), fall back to the drawer so the
	// cursor always lands on something selectable.
	if !canvasHasSegment() && len(drawerSegs) > 0 {
		focusPane = "drawer"
	}
	app.SetFocus(focusedPrimitive())
	syncFocusChrome()

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
