package main

// ─── First-run Wizard: guided TUI ────────────────────────────────────
//
// A multi-step, opinionated flow that assembles a good statusline from a few
// high-level choices, for someone who has never seen the tool. Each step shows
// a live preview rendered through the real buildStatusline pipeline (never a
// faked string). On finish it hands the assembled config to a save callback —
// the same saveConfig path the list editor uses.
//
// Two entry points share one implementation:
//   - openWizard(app, pages, ...)  — launched from the main TUI with the 'g'
//     key; runs inside the existing application and pages.
//   - runWizard()                  — the `configure --wizard` / `wizard`
//     subcommand; builds its own application, then saves and exits on finish.
//
// tview/tcell stay confined to this file (and the other tui_*.go files).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

// wizardStep enumerates the flow's ordered steps.
type wizardStep int

const (
	stepCategories wizardStep = iota
	stepDensity
	stepTheme
	stepReview
	stepCount
)

func (s wizardStep) title() string {
	switch s {
	case stepCategories:
		return "What do you want to see?"
	case stepDensity:
		return "How much room?"
	case stepTheme:
		return "Pick a look"
	case stepReview:
		return "Review & save"
	}
	return ""
}

// wizardState holds the live choices and the widgets the flow swaps between.
type wizardState struct {
	app     *tview.Application
	pages   *tview.Pages
	choices wizardChoices
	step    wizardStep

	// preview is the shared live-render pane shown under every step.
	preview *tview.TextView
	// header shows the step title + progress.
	header *tview.TextView
	// footer shows the per-step key hints.
	footer *tview.TextView
	// body is the swappable per-step content area.
	body *tview.Flex

	// pvState is the synthetic session history so state-driven segments render.
	pvState *sessionState

	// baseConfig is the config that was loaded when the wizard launched. The
	// wizard mutates only the fields it controls and preserves everything else.
	baseConfig config

	// onDone is invoked when the wizard finishes or is cancelled: finished is
	// true with the assembled config on accept, false on cancel.
	onDone func(cfg config, finished bool)

	// done guards onDone against double-fire.
	done bool
}

// wizardPageName is the single page the wizard occupies; its body swaps per
// step rather than registering one page per step.
const wizardPageName = "wizard"

// openWizard builds and shows the wizard on the given app/pages. start is the
// existing config used to seed initial choices (nil Segments means defaults).
// The flow drives onDone exactly once.
func openWizard(app *tview.Application, pages *tview.Pages, start config, onDone func(cfg config, finished bool)) {
	ws := &wizardState{
		app:        app,
		pages:      pages,
		choices:    deriveWizardChoices(start),
		step:       stepCategories,
		pvState:    previewState(time.Now()),
		baseConfig: start,
		onDone:     onDone,
	}

	ws.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	previewBox := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(ws.preview, 0, 1, false)
	previewBox.SetBorder(true).SetTitle(" Live preview ")

	ws.header = tview.NewTextView().SetDynamicColors(true)
	ws.header.SetBorder(false)

	ws.footer = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true).SetTextAlign(tview.AlignCenter)

	ws.body = tview.NewFlex().SetDirection(tview.FlexRow)

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(ws.header, 2, 0, false).
		AddItem(ws.body, 0, 1, true).
		AddItem(previewBox, 9, 0, false).
		AddItem(ws.footer, 2, 0, false)
	root.SetBorder(true).SetTitle(" claude-statusline — guided setup ")

	pages.AddPage(wizardPageName, root, true, true)
	ws.render()
}

// deriveWizardChoices seeds the wizard from an existing config so re-running it
// over a customized config starts from sensible state rather than blank. The
// category set is the categories whose segments are mostly present; density is
// inferred from the line spread; theme and git-status are read directly. When
// the config is the bare default (a true first run), this lands on
// defaultWizardChoices.
func deriveWizardChoices(cfg config) wizardChoices {
	// Bare default / unset segments → opinionated first-run defaults.
	if cfg.Segments == nil {
		return defaultWizardChoices()
	}

	// Explicit empty slice means "hide everything"; preserve that intent rather
	// than falling back to defaults.
	if len(cfg.Segments) == 0 {
		return wizardChoices{
			Categories: map[string]bool{},
			Density:    densityBalanced,
			Theme:      cfg.Theme,
			GitStatus:  false,
		}
	}

	present := map[string]bool{}
	for _, id := range cfg.Segments {
		present[id] = true
	}
	cats := map[string]bool{}
	for _, c := range wizardCategories() {
		// Enable the category if at least one of its segments is present.
		for _, id := range c.Segments {
			if present[id] {
				cats[c.ID] = true
				break
			}
		}
	}
	// If nothing matched (e.g. plugin-only config), fall back to defaults so
	// the user isn't stranded on an empty step.
	if len(cats) == 0 {
		cats = defaultWizardChoices().Categories
	}

	density := densityBalanced
	maxLine := 1
	for id := range present {
		if l := effectiveLine(id, cfg); l > maxLine {
			maxLine = l
		}
	}
	switch {
	case maxLine <= 1:
		density = densityCompact
	case maxLine >= 4:
		density = densitySpacious
	}

	git := false
	if raw := cfg.Settings["git-branch"]; raw != nil {
		if v, ok := raw["git_status"].(bool); ok {
			git = v
		}
	}

	return wizardChoices{
		Categories: cats,
		Density:    density,
		Theme:      cfg.Theme,
		GitStatus:  git,
	}
}

// liveConfig is the config assembled from the current choices — what the
// preview renders and what gets saved.
func (ws *wizardState) liveConfig() config {
	return assembleWizardConfig(ws.baseConfig, ws.choices, registeredSegments)
}

// refreshPreview re-renders the live preview through the real pipeline.
func (ws *wizardState) refreshPreview() {
	cfg := ws.liveConfig()
	width := 0
	if _, _, w, _ := ws.preview.GetInnerRect(); w > 0 {
		width = w
	}
	lines := buildStatusline(buildInput{
		P:     samplePayload(),
		C:     currentPalette(cfg),
		Cfg:   cfg,
		State: ws.pvState,
		Width: width,
		Now:   time.Now(),
	})
	for i, l := range lines {
		lines[i] = strings.TrimLeft(l, " ")
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		ws.preview.SetText("[gray](nothing selected — the statusline would be empty)[-]")
		return
	}
	ws.preview.SetText(ansiToTview(text))
}

// render rebuilds the header/footer and swaps the body to the current step.
func (ws *wizardState) render() {
	ws.header.SetText(fmt.Sprintf(" [yellow::b]Step %d of %d[-:-:-]  %s",
		int(ws.step)+1, int(stepCount), ws.step.title()))

	ws.body.Clear()
	switch ws.step {
	case stepCategories:
		ws.buildCategoriesStep()
	case stepDensity:
		ws.buildDensityStep()
	case stepTheme:
		ws.buildThemeStep()
	case stepReview:
		ws.buildReviewStep()
	}
	ws.refreshPreview()
}

// next/prev advance the step machine, clamping at the ends.
func (ws *wizardState) next() {
	if ws.step < stepReview {
		ws.step++
		ws.render()
	}
}

func (ws *wizardState) prev() {
	if ws.step > stepCategories {
		ws.step--
		ws.render()
	}
}

// finish closes the wizard, firing onDone with the assembled config.
func (ws *wizardState) finish(accepted bool) {
	if ws.done {
		return
	}
	ws.done = true
	ws.pages.RemovePage(wizardPageName)
	if ws.onDone != nil {
		ws.onDone(ws.liveConfig(), accepted)
	}
}

// stepFooter sets the per-step key hints with shared nav suffix.
func (ws *wizardState) stepFooter(stepHints string) {
	nav := "tab/→ next · ⇧tab/← back · esc cancel"
	if ws.step == stepReview {
		nav = "enter save · ⇧tab/← back · esc cancel"
	}
	if stepHints != "" {
		ws.footer.SetText(" " + stepHints + "  •  " + nav)
	} else {
		ws.footer.SetText(" " + nav)
	}
}

// listNav adds the shared step-navigation keys to a list's input capture so
// every step navigates uniformly (tab/⇧tab/←/→/esc), while leaving ↑/↓ and
// space/enter to the list itself. Returns the event for keys it doesn't claim.
func (ws *wizardState) listNav(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab, tcell.KeyRight:
		ws.next()
		return nil
	case tcell.KeyBacktab, tcell.KeyLeft:
		ws.prev()
		return nil
	case tcell.KeyEscape:
		ws.finish(false)
		return nil
	}
	return event
}

// secIndent aligns a list row's secondary (description) text under its primary
// text, which tview prefixes with a marker column.
const secIndent = "      "

// newWizardIntro builds the dynamic-color, word-wrapping blurb shown above each
// step's list.
func newWizardIntro(text string) *tview.TextView {
	return tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true).SetText(text)
}

// newWizardList builds the bordered, full-line-highlight list every step uses,
// styled identically so the flow reads as one coherent picker.
func newWizardList(title string) *tview.List {
	list := tview.NewList().SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		SetSelectedTextColor(tcell.ColorWhite).
		SetMainTextColor(tcell.ColorWhite).
		ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(title)
	return list
}

// ─── Step 1: categories ──────────────────────────────────────────────

func (ws *wizardState) buildCategoriesStep() {
	intro := newWizardIntro("[gray]Pick the kinds of things you care about. Each toggles a small group of\nsegments; anything with no data hides itself, so a generous pick is safe.[-]")

	list := newWizardList(" Categories — space toggles ")

	cats := wizardCategories()
	rebuild := func(keep int) {
		list.Clear()
		for _, c := range cats {
			// Build the row as "<mark> <name>" with explicit colors throughout:
			// tview's dynamic-color "[-]" reset returns to the terminal default
			// (not the list's main-text color), so we color every span outright to
			// keep both the checkbox and the name legible on the selected row.
			mark := "[#9aa5b1]○[white]"
			if ws.choices.Categories[c.ID] {
				mark = "[#5fff87::b]●[white::-]"
			}
			list.AddItem(fmt.Sprintf("%s %s", mark, c.Name), secIndent+c.Desc, 0, nil)
		}
		if keep >= 0 && keep < len(cats) {
			list.SetCurrentItem(keep)
		}
	}
	rebuild(0)

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && (event.Rune() == ' ') {
			idx := list.GetCurrentItem()
			if idx >= 0 && idx < len(cats) {
				id := cats[idx].ID
				if ws.choices.Categories == nil {
					ws.choices.Categories = map[string]bool{}
				}
				ws.choices.Categories[id] = !ws.choices.Categories[id]
				rebuild(idx)
				ws.refreshPreview()
			}
			return nil
		}
		return ws.listNav(event)
	})

	ws.body.AddItem(intro, 3, 0, false).AddItem(list, 0, 1, true)
	ws.app.SetFocus(list)
	ws.stepFooter("space toggle · ↑/↓ move")
}

// ─── Step 2: density ─────────────────────────────────────────────────

func (ws *wizardState) buildDensityStep() {
	intro := newWizardIntro("[gray]How many lines should the statusline use? The preview updates as you move.[-]")

	list := newWizardList(" Density ")

	densities := wizardDensities()
	start := 0
	for i, di := range densities {
		list.AddItem(di.Name, secIndent+di.Desc, 0, nil)
		if di.Density == ws.choices.Density {
			start = i
		}
	}
	list.SetCurrentItem(start)

	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if idx >= 0 && idx < len(densities) {
			ws.choices.Density = densities[idx].Density
			ws.refreshPreview()
		}
	})
	list.SetSelectedFunc(func(int, string, string, rune) { ws.next() })
	list.SetInputCapture(ws.listNav)

	ws.body.AddItem(intro, 2, 0, false).AddItem(list, 0, 1, true)
	ws.app.SetFocus(list)
	ws.stepFooter("↑/↓ choose · enter next")
}

// ─── Step 3: theme ───────────────────────────────────────────────────

func (ws *wizardState) buildThemeStep() {
	intro := newWizardIntro("[gray]Pick a color theme. classic is the default 16-color look; the rest are\ntruecolor with automatic 256/16 fallback.[-]")

	list := newWizardList(" Theme ")

	current := ws.choices.Theme
	if current == "" {
		current = "classic"
	}
	start := 0
	for i, t := range builtinThemes {
		list.AddItem(t.ID, secIndent+t.Desc, 0, nil)
		if t.ID == current {
			start = i
		}
	}
	list.SetCurrentItem(start)

	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if idx >= 0 && idx < len(builtinThemes) {
			ws.choices.Theme = normalizeWizardTheme(builtinThemes[idx].ID)
			ws.refreshPreview()
		}
	})
	list.SetSelectedFunc(func(int, string, string, rune) { ws.next() })
	list.SetInputCapture(ws.listNav)

	ws.body.AddItem(intro, 3, 0, false).AddItem(list, 0, 1, true)
	ws.app.SetFocus(list)
	ws.stepFooter("↑/↓ preview · enter next")
}

// ─── Step 4: review ──────────────────────────────────────────────────

func (ws *wizardState) buildReviewStep() {
	cfg := ws.liveConfig()

	var b strings.Builder
	b.WriteString("[yellow::b]Your statusline[-:-:-]\n\n")

	theme := ws.choices.Theme
	if theme == "" {
		theme = "classic"
	}
	b.WriteString(fmt.Sprintf("  theme:    [::b]%s[-:-:-]\n", theme))
	di := densityInfo(ws.choices.Density)
	b.WriteString(fmt.Sprintf("  density:  [::b]%s[-:-:-] (%d line max)\n", di.Name, di.Lines))

	// Group enabled segments by physical line for a readable summary.
	byLine := map[int][]string{}
	maxLine := 1
	for _, id := range cfg.Segments {
		l := effectiveLine(id, cfg)
		byLine[l] = append(byLine[l], id)
		if l > maxLine {
			maxLine = l
		}
	}
	b.WriteString(fmt.Sprintf("  segments: [::b]%d[-:-:-]\n", len(cfg.Segments)))
	for l := 1; l <= maxLine; l++ {
		if ids := byLine[l]; len(ids) > 0 {
			b.WriteString(fmt.Sprintf("    line %d: [gray]%s[-]\n", l, strings.Join(ids, ", ")))
		}
	}
	if ws.choices.GitStatus {
		b.WriteString("\n  [gray]git-branch shows rich dirty / ahead-behind status[-]\n")
	}
	b.WriteString(fmt.Sprintf("\n[green]Press enter to save to %s[-]\n", configPath()))
	b.WriteString("[gray]The live preview below is exactly what you'll get.[-]")

	review := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	review.SetText(b.String())
	review.SetBorder(true).SetTitle(" Summary ")

	review.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			ws.finish(true)
			return nil
		case tcell.KeyTab, tcell.KeyRight:
			// nowhere further forward; enter saves
			return nil
		case tcell.KeyBacktab, tcell.KeyLeft:
			ws.prev()
			return nil
		case tcell.KeyEscape:
			ws.finish(false)
			return nil
		}
		return event
	})

	ws.body.AddItem(review, 0, 1, true)
	ws.app.SetFocus(review)
	ws.stepFooter("")
}

// ─── Standalone entry: `configure --wizard` / `wizard` ───────────────

func runWizard() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "claude-statusline wizard requires an interactive terminal.")
		fmt.Fprintf(os.Stderr, "Edit %s directly, or run from a terminal.\n", configPath())
		os.Exit(1)
	}

	cfg, warns := loadConfigWarn()
	if os.Getenv("STATUSLINE_VERBOSE") != "" {
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "claude-statusline: config: %s\n", w)
		}
	}
	initSegments(cfg.Plugins)

	// Synthetic git/stash results so the rich-status preview renders; reset to
	// nil on exit so the real render path is never affected (locked by tests).
	defer installPreviewGlobals()()

	app := tview.NewApplication()
	pages := tview.NewPages()

	saved := false
	openWizard(app, pages, cfg, func(out config, finished bool) {
		if finished {
			if err := saveConfig(out); err != nil {
				fmt.Fprintf(os.Stderr, "save failed: %v\n", err)
			} else {
				saved = true
			}
		}
		app.Stop()
	})

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running wizard: %v\n", err)
		os.Exit(1)
	}
	if saved {
		fmt.Printf("Saved to %s\n", configPath())
	}
}
