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
//
// Direct-manipulation model: the Preview is the primary editing surface. A
// cursor highlights one rendered segment on its real line; arrows walk the
// cursor through rendered segments and across lines. space toggles the segment
// under the cursor (off removes it from the render). Grab/move (m) picks the
// segment up and relocates it in real space across slots and lines, then drops
// it. To add an off segment, a palette overlay (the former list, filtered to
// off segments) inserts at the cursor. color/options/theme/preset/save all act
// on the cursor's segment. The Preview always renders REAL buildStatusline
// output — the cursor is painted on top, never faked.

// selectionBG is the high-contrast background for the selected row in TUI
// lists (palette, flyout, theme/preset pickers). A truecolor RGB value (not a
// 16-color ANSI name) so it renders identically across terminals and headless
// capture, paired with white text — the highlighted row is meant to be the most
// legible thing on screen, not the least.
var selectionBG = tcell.NewRGBColor(58, 91, 219) // indigo (#3a5bdb)

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
// a pathologically narrow terminal can't squeeze the preview away.
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

	// Synthetic data so every feature previews: an hour of session history for
	// the state-derived segments, and a fake rich-git result (the sample
	// payload's workspace isn't a real repo). Both are preview-only.
	pvState := previewState(time.Now())
	gitStatusPreview = &gitStatusInfo{Dirty: true, Ahead: 1, Behind: 2}
	defer func() { gitStatusPreview = nil }()
	stashPreview := 3
	gitStashPreview = &stashPreview
	defer func() { gitStashPreview = nil }()

	// demoActive animates the whole preview through all states (d).
	demoActive := false

	// resizePreviewBox is wired up after the layout flex exists; refreshPreview
	// calls it with the rendered line count so the preview pane stays just tall
	// enough for the statusline (plus its border) rather than sprawling.
	resizePreviewBox := func(int) {}

	// dirty tracks unsaved changes; mutate is the single mutation funnel.
	dirty := false

	app := tview.NewApplication()

	// ─── Cursor state (the heart of the direct-manipulation model) ───────
	//
	// curLine indexes into the current physical span rows; curCol indexes into
	// that row's spans. curSpans is the latest span layout from
	// buildStatuslineSpans, rebuilt on every refresh. cursorID remembers the
	// segment under the cursor across rebuilds so toggles/moves keep their
	// place. grabbing!="" means we are in move mode, relocating that segment.
	var curSpans [][]segSpan
	curLine, curCol := 0, 0
	cursorID := ""
	grabbing := ""

	// grabSnapshot captures the config and dirty/preset flags when a grab
	// begins, so esc can truly cancel the move: arrow moves mutate cfg in place,
	// and esc restores this snapshot. enter keeps the moves.
	var grabSnapshot config
	grabSnapshotDirty := false
	grabSnapshotPreset := ""

	// ─── Widgets ─────────────────────────────────────────────────────────

	// The Preview is the primary editing surface.
	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)

	previewBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(preview, 0, 1, true)
	previewBox.SetBorder(true).SetTitle(" Preview ")

	// Description / hint panel for the cursor's segment.
	descView := tview.NewTextView().SetWrap(true).SetDynamicColors(true)
	descView.SetBorder(true).SetTitle(" Segment ")

	// Status strip: persistent context on the left (theme/preset), transient
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

	// activePreset names the last preset applied; manual edits flip it back to
	// "" ("custom"). Session-only — never persisted by the TUI.
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
		mode := ""
		if grabbing != "" {
			mode = fmt.Sprintf(" · [black:yellow] MOVING %s [-:-:-]", grabbing)
		}
		stripLeft.SetText(fmt.Sprintf(" theme: [::b]%s[-:-:-] · preset: %s%s%s", theme, preset, marker, mode))
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

	// helpBackPage is the page to return to when the help overlay is closed.
	helpBackPage := "configure"

	// Full README behind the help overlay.
	readmeView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetText(markdownToTview(readmeContent))
	readmeView.SetBorder(true).SetTitle(" README (↑/↓ scroll • q/Esc back) ")

	// ─── Palette overlay (the former segment list, off segments only) ────
	//
	// visible is the (possibly filtered) slice the palette renders from; the
	// palette only lists segments that are currently off, so it is purely an
	// "add" surface.
	var visible []segmentInfo

	// The palette overlay is a single bordered box: a filter row, the list of
	// off segments, then a help row. The list and filter carry no border of
	// their own (the wrapping flex does), so the filter's "/" label can't leak
	// above the box. Selection uses a bright background for contrast.
	paletteList := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(selectionBG).
		SetSelectedTextColor(tcell.ColorWhite).
		ShowSecondaryText(true)

	paletteFilter := tview.NewInputField().
		SetLabel(" / ").
		SetFieldBackgroundColor(tcell.ColorDefault)

	paletteHelp := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("palette"))

	paletteFlex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(paletteFilter, 1, 0, false).
		AddItem(paletteList, 0, 1, true).
		AddItem(paletteHelp, 1, 0, false)
	paletteFlex.SetBorder(true).
		SetTitle(" Add segment — enter insert · esc cancel ")

	// ─── Flyout Panel ────────────────────────────────────────────────────

	flyoutTitle := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true)

	flyoutList := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(selectionBG).
		SetSelectedTextColor(tcell.ColorWhite).
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

	pages := tview.NewPages()

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

	openFlyout := func(segID string) {
		specs := segmentSpecs(segID)
		if len(specs) == 0 {
			flash("yellow", segID+": no configurable options")
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

	// segmentEnabled reports whether id is in the render set.
	segmentEnabled := func(id string) bool {
		for _, segID := range cfg.Segments {
			if segID == id {
				return true
			}
		}
		return false
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

	// ─── Cursor helpers ──────────────────────────────────────────────────

	// previewWidth is the user's width override for testing reflow: 0 = auto
	// (track the preview panel's real width), else a fixed column count.
	previewWidth := 0

	// describeCursor renders the description panel for the cursor's segment.
	describeCursor := func() {
		id := cursorSegment(curSpans, curLine, curCol)
		if id == "" {
			if len(cfg.Segments) == 0 {
				descView.SetText("[gray]No segments enabled. Press [::b]a[::-] to add one.[-]")
			} else {
				descView.SetText("[gray]Cursor is off the rendered segments — use the arrow keys.[-]")
			}
			return
		}
		seg, ok := segmentByID(id)
		if !ok {
			descView.SetText(id)
			return
		}
		line := effectiveLine(id, cfg)
		var b strings.Builder
		fmt.Fprintf(&b, "[yellow::b]%s[-::-]  [gray](line %d)[-]\n\n", id, line)
		b.WriteString(seg.desc)
		if n := len(seg.settings); n > 0 {
			fmt.Fprintf(&b, "\n\n[gray]%d options — press o to configure[-]", n)
		}
		if grabbing != "" {
			b.WriteString("\n\n[yellow::b]MOVING[-::-] — arrows relocate, enter drops, esc cancels")
		}
		descView.SetText(b.String())
	}

	// refreshPreview re-renders the preview at the effective width, builds the
	// span map, re-clamps the cursor onto a real span, and paints the cursor
	// highlight on top of the REAL rendered output.
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
		in := buildInput{P: p, C: currentPalette(cfg), Cfg: cfg, State: pvState, Width: width, Now: time.Now()}
		lines, spans := buildStatuslineSpans(in)
		curSpans = spans
		curLine, curCol, cursorID = clampCursor(curSpans, cursorID, curLine, curCol)

		// Paint the cursor/selection highlight over the cursor's span. We
		// rebuild each physical line from its spans so the highlight wraps
		// exactly the segment's cells, leaving the rest of the REAL render
		// untouched. Lines with no span (spacers) and the off-cursor portions
		// pass through verbatim.
		curID := cursorSegment(curSpans, curLine, curCol)
		var rendered []string
		for li, l := range lines {
			row := spans[li]
			if curID == "" || li != curLine || len(row) == 0 {
				rendered = append(rendered, ansiToTview(applyWidthRuler(l, previewWidth)))
				continue
			}
			painted := paintCursorLine(l, row, curCol, grabbing != "")
			if previewWidth > 0 {
				pad := previewWidth - visibleWidth(l)
				if pad < 0 {
					pad = 0
				}
				painted += strings.Repeat(" ", pad) + "[gray]│[-]"
			}
			rendered = append(rendered, painted)
		}

		var previewText string
		if previewWidth > 0 {
			previewText = strings.Join(rendered, "\n")
		} else {
			previewText = strings.TrimRight(strings.Join(rendered, "\n"), "\n")
		}
		if strings.TrimSpace(stripANSI(strings.Join(lines, ""))) == "" {
			previewText = "[gray](statusline hidden — no segments enabled · press [::b]a[::-] to add)[-]"
		}
		preview.SetText(previewText)
		resizePreviewBox(len(rendered))

		title := " Preview — direct edit "
		if grabbing != "" {
			title = fmt.Sprintf(" Preview — MOVING %s (enter drop · esc cancel) ", grabbing)
		} else if previewWidth > 0 {
			title = fmt.Sprintf(" Preview (%d cols — w to cycle) ", previewWidth)
		} else if panelW > 0 {
			title = fmt.Sprintf(" Preview (auto · %d cols) ", panelW)
		}
		previewBox.SetTitle(title)
		describeCursor()
	}

	// scheduleDemoTick drives demo mode: a self-rescheduling 50ms timer that
	// stops re-arming once demoActive flips off.
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

	// ─── Palette overlay population ──────────────────────────────────────

	refreshPalette := func() {
		query := paletteFilter.GetText()
		all := filterSegments(registeredSegments, query)
		visible = visible[:0]
		for _, s := range all {
			if !segmentEnabled(s.id) {
				visible = append(visible, s)
			}
		}
		cur := paletteList.GetCurrentItem()
		paletteList.Clear()
		for _, s := range visible {
			line := s.line
			if override, ok := cfg.Lines[s.id]; ok && override >= 1 {
				line = override
			}
			sec := fmt.Sprintf("    L%d · %s", line, s.desc)
			paletteList.AddItem(s.id, sec, 0, nil)
		}
		if cur >= 0 && cur < len(visible) {
			paletteList.SetCurrentItem(cur)
		}
		n := len(visible)
		paletteFlex.SetTitle(fmt.Sprintf(" Add segment (%d off) — enter insert · esc cancel ", n))
	}

	openPalette := func() {
		paletteFilter.SetText("")
		refreshPalette()
		if len(visible) == 0 {
			flash("yellow", "all segments are already enabled")
			return
		}
		paletteList.SetCurrentItem(0)
		pages.SwitchToPage("palette")
		app.SetFocus(paletteList)
	}

	paletteFilter.SetChangedFunc(func(string) {
		refreshPalette()
		if len(visible) > 0 {
			paletteList.SetCurrentItem(0)
		}
	})
	paletteFilter.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter, tcell.KeyDown:
			app.SetFocus(paletteList)
		case tcell.KeyEscape:
			pages.SwitchToPage("configure")
			app.SetFocus(preview)
		}
	})
	paletteList.SetSelectedFunc(func(idx int, _, _ string, _ rune) {
		if idx >= 0 && idx < len(visible) {
			id := visible[idx].id
			pages.SwitchToPage("configure")
			app.SetFocus(preview)
			mutate(func() {
				if newID, ok := insertSegmentAtCursor(&cfg, curSpans, curLine, curCol, cursorID, id); ok {
					cursorID = newID
				}
			})
			flash("green", "added "+id)
		}
	})
	paletteList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			pages.SwitchToPage("configure")
			app.SetFocus(preview)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q', 'Q':
				pages.SwitchToPage("configure")
				app.SetFocus(preview)
				return nil
			case '/':
				app.SetFocus(paletteFilter)
				return nil
			case '?':
				helpBackPage = "palette"
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			}
		}
		return event
	})

	// ─── Color picker for the cursor's segment color setting ─────────────

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

	// ─── Preview mouse: click to place cursor, double-click to open flyout ─

	preview.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if grabbing != "" {
			return action, event
		}
		hit := func() bool {
			x, y := event.Position()
			innerX, innerY, _, _ := preview.GetInnerRect()
			row := y - innerY
			col := x - innerX
			if row < 0 || row >= len(curSpans) {
				return false
			}
			for ci, sp := range curSpans[row] {
				if col >= sp.Col && col < sp.Col+sp.Width {
					curLine, curCol = row, ci
					cursorID = sp.ID
					return true
				}
			}
			return false
		}
		switch action {
		case tview.MouseLeftDoubleClick:
			if preview.InRect(event.Position()) && hit() {
				refreshPreview()
				if id := cursorSegment(curSpans, curLine, curCol); id != "" {
					openFlyout(id)
				}
				return tview.MouseConsumed, nil
			}
		case tview.MouseLeftClick:
			if preview.InRect(event.Position()) && hit() {
				app.SetFocus(preview)
				refreshPreview()
				return tview.MouseConsumed, nil
			}
		}
		return action, event
	})

	// ─── updateUI: refresh strip + preview after any config change ───────

	updateUI = func() {
		refreshPreview()
		updateStrip()
	}

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

	// ─── Master input router ─────────────────────────────────────────────

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		pageName, _ := pages.GetFrontPage()
		if isPickerPage(pageName) {
			return event // pickers handle their own keys
		}
		if pageName == "palette" {
			// The palette list/filter handle their own keys.
			return event
		}
		if pageName == "help" {
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage(helpBackPage)
				if helpBackPage == "palette" {
					app.SetFocus(paletteList)
				} else {
					app.SetFocus(preview)
				}
				return nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'q', 'Q':
					pages.SwitchToPage(helpBackPage)
					if helpBackPage == "palette" {
						app.SetFocus(paletteList)
					} else {
						app.SetFocus(preview)
					}
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
				app.SetFocus(preview)
				updateUI()
				return nil
			case tcell.KeyRune:
				r := event.Rune()
				if r == 'q' || r == 'Q' {
					stopStressTest(currentFlyoutSegment)
					pages.SwitchToPage("configure")
					app.SetFocus(preview)
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
			back := "configure"
			focus := tview.Primitive(preview)
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

		// ─── Main (preview canvas) context ───────────────────────────────

		// Grab/move mode owns the arrows and enter/esc/space.
		if grabbing != "" {
			switch event.Key() {
			case tcell.KeyEnter:
				flash("green", "moved "+grabbing)
				grabbing = ""
				refreshPreview()
				updateStrip()
				return nil
			case tcell.KeyEscape:
				// Cancel: undo every move made since the grab began.
				cursorID = grabbing
				cfg = grabSnapshot
				dirty = grabSnapshotDirty
				activePreset = grabSnapshotPreset
				grabbing = ""
				flash("yellow", "move cancelled")
				refreshPreview()
				updateStrip()
				return nil
			case tcell.KeyLeft:
				mutate(func() {
					if newID, moved := moveCursorSegmentHoriz(&cfg, curSpans, curLine, curCol, -1); moved {
						cursorID = newID
					}
				})
				return nil
			case tcell.KeyRight:
				mutate(func() {
					if newID, moved := moveCursorSegmentHoriz(&cfg, curSpans, curLine, curCol, 1); moved {
						cursorID = newID
					}
				})
				return nil
			case tcell.KeyUp:
				mutate(func() {
					if newID, moved := moveCursorSegmentVert(&cfg, curSpans, curLine, curCol, -1); moved {
						cursorID = newID
					}
				})
				return nil
			case tcell.KeyDown:
				mutate(func() {
					if newID, moved := moveCursorSegmentVert(&cfg, curSpans, curLine, curCol, 1); moved {
						cursorID = newID
					}
				})
				return nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'm', 'M', ' ':
					flash("green", "moved "+grabbing)
					grabbing = ""
					refreshPreview()
					updateStrip()
					return nil
				}
			}
			return nil
		}

		switch event.Key() {
		case tcell.KeyLeft:
			if curLine >= 0 && curLine < len(curSpans) && curCol > 0 {
				curCol--
				cursorID = curSpans[curLine][curCol].ID
				refreshPreview()
			}
			return nil
		case tcell.KeyRight:
			if curLine >= 0 && curLine < len(curSpans) && curCol < len(curSpans[curLine])-1 {
				curCol++
				cursorID = curSpans[curLine][curCol].ID
				refreshPreview()
			}
			return nil
		case tcell.KeyUp:
			// Move to the nearest non-empty row above, keeping the column near.
			targetCol := 0
			if curLine >= 0 && curLine < len(curSpans) && curCol < len(curSpans[curLine]) {
				targetCol = curSpans[curLine][curCol].Col
			}
			for li := curLine - 1; li >= 0; li-- {
				if len(curSpans[li]) > 0 {
					curLine = li
					curCol = nearestSpanCol(curSpans[li], targetCol)
					cursorID = curSpans[li][curCol].ID
					refreshPreview()
					break
				}
			}
			return nil
		case tcell.KeyDown:
			targetCol := 0
			if curLine >= 0 && curLine < len(curSpans) && curCol < len(curSpans[curLine]) {
				targetCol = curSpans[curLine][curCol].Col
			}
			for li := curLine + 1; li < len(curSpans); li++ {
				if len(curSpans[li]) > 0 {
					curLine = li
					curCol = nearestSpanCol(curSpans[li], targetCol)
					cursorID = curSpans[li][curCol].ID
					refreshPreview()
					break
				}
			}
			return nil
		case tcell.KeyEscape:
			requestQuit()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case ' ':
				if id := cursorSegment(curSpans, curLine, curCol); id != "" {
					toggleSegment(id)
				}
				return nil
			case 'm', 'M':
				if id := cursorSegment(curSpans, curLine, curCol); id != "" {
					grabbing = id
					// Snapshot so esc can cancel the in-place move mutations.
					grabSnapshot = cloneConfig(cfg)
					grabSnapshotDirty = dirty
					grabSnapshotPreset = activePreset
					flash("yellow", "moving "+id+" — arrows relocate, enter drops, esc cancels")
					refreshPreview()
					updateStrip()
				} else {
					flash("yellow", "no segment under the cursor")
				}
				return nil
			case 'a', 'A':
				openPalette()
				return nil
			case 'o', 'O':
				if id := cursorSegment(curSpans, curLine, curCol); id != "" {
					openFlyout(id)
				}
				return nil
			case 'h', 'H', '?':
				helpBackPage = "configure"
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case 'c':
				id := cursorSegment(curSpans, curLine, curCol)
				if id == "" {
					return nil
				}
				cursorID = id // keep the cursor on this segment across the rebuild
				mutate(func() {
					if cfg.Colors == nil {
						cfg.Colors = make(map[string]string)
					}
					currentColor := cfg.Colors[id]
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
						delete(cfg.Colors, id)
					} else {
						cfg.Colors[id] = nextColor
					}
				})
				return nil
			case 'C':
				id := cursorSegment(curSpans, curLine, curCol)
				if id == "" {
					return nil
				}
				orig, hadOrig := cfg.Colors[id]
				applyColor := func(spec string) {
					if cfg.Colors == nil {
						cfg.Colors = make(map[string]string)
					}
					if spec == "" || spec == "default" {
						delete(cfg.Colors, id)
					} else {
						cfg.Colors[id] = spec
					}
					refreshPreview()
				}
				openColorPicker(app, pages, currentPalette(cfg), "color — "+id,
					applyColor,
					func(spec string, picked bool) {
						if picked {
							mutate(func() { applyColor(spec) })
							pushRecentColor(spec)
						} else {
							if hadOrig {
								cfg.Colors[id] = orig
							} else {
								delete(cfg.Colors, id)
							}
							refreshPreview()
						}
						app.SetFocus(preview)
					})
				return nil
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
						app.SetFocus(preview)
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
						app.SetFocus(preview)
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
		}
		return event
	})

	// ─── Layout assembly ─────────────────────────────────────────────────
	//
	// The Preview is the primary editing surface, so it spans the FULL terminal
	// width on top — the thing being edited must never be clipped by a sidebar.
	// Its height is bounded to the rendered statusline (a few lines) plus a
	// little breathing room, sized live in the before-draw hook; the Segment
	// description fills the space below it. This keeps the preview from
	// sprawling into a mostly-empty void while still letting the line render in
	// full at the terminal's real width.

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(previewBox, 5, 0, true).
		AddItem(descView, 0, 1, false).
		AddItem(statusStrip, 1, 0, false).
		AddItem(help, 1, 0, false)

	// Size the preview pane to the rendered statusline: lines + 2 for the
	// border. Clamped to a sane band so an empty statusline still shows its
	// title and a pathological many-line layout can't crowd out the
	// description. The remaining vertical space flows to the description.
	resizePreviewBox = func(lineCount int) {
		h := lineCount + 2
		if h < 3 {
			h = 3
		}
		if h > 12 {
			h = 12
		}
		flex.ResizeItem(previewBox, h, 0)
	}

	pages.AddPage("configure", flex, true, true)
	pages.AddPage("help", helpView, true, false)
	pages.AddPage("readme", readmeView, true, false)
	pages.AddPage("flyout", flyoutFlex, true, false)
	pages.AddPage("palette", floatPicker(paletteFlex, 84, 24), true, false)

	// Re-render the preview when the terminal (and so the panel) resizes — and
	// grow the footers to however many rows their keys need at this width.
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
			app.SetFocus(preview)
			if buttonLabel == "Reset" {
				cursorID = ""
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
				app.SetFocus(preview)
			case "Discard":
				app.Stop()
			default:
				pages.SwitchToPage("configure")
				app.SetFocus(preview)
			}
		})
	pages.AddPage("quit", quitModal, true, false)

	// Seed the initial render + cursor.
	updateUI()

	if err := app.SetRoot(pages, true).EnableMouse(true).SetFocus(preview).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
