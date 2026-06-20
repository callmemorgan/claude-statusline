package main

// ─── Wizard ───────────────────────────────────────────────────────────
//
// An interactive onboarding wizard built with rivo/tview. It walks first-time
// users through theme, color depth, segment selection, and optional Claude Code
// installation, then persists config.toml and runs the install subcommand if
// requested.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// wizardState carries the user's choices through the onboarding flow.
type wizardState struct {
	cfg      config.Config
	selected map[string]bool
	install  bool
}

// runWizard launches the tview onboarding wizard. It guides the user through
// theme, color depth, segment selection, and Claude Code installation, then
// saves config.toml and optionally runs the install subcommand.
func runWizard() {
	for _, arg := range os.Args[2:] {
		if arg == "--help" || arg == "-h" {
			fmt.Println("Usage: claude-statusline wizard")
			fmt.Println()
			fmt.Println("Launch an interactive onboarding wizard to configure claude-statusline.")
			fmt.Println("The wizard sets theme, color depth, segments, and optionally wires the")
			fmt.Println("binary into Claude Code's settings.json.")
			os.Exit(0)
		}
	}

	st := wizardState{
		cfg:      startingConfig(),
		selected: startingSegments(),
		install:  true,
	}
	segments.Init()

	app := tview.NewApplication()
	pages := tview.NewPages()

	handleNext := func() {
		name, _ := pages.GetFrontPage()
		switch name {
		case "welcome":
			pages.SwitchToPage("theme")
		case "theme":
			pages.SwitchToPage("depth")
		case "depth":
			pages.SwitchToPage("segments")
		case "segments":
			segs := make([]string, 0, len(segments.All()))
			for _, s := range segments.All() {
				if st.selected[s.ID] {
					segs = append(segs, s.ID)
				}
			}
			st.cfg.Segments = segs
			pages.SwitchToPage("install")
		case "install":
			pages.SwitchToPage("summary")
		case "summary":
			app.Stop()
			if err := config.SaveConfig(st.cfg); err != nil {
				fmt.Fprintf(os.Stderr, "wizard: failed to save config: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ Saved config to %s\n", config.ConfigPath())
			if st.install {
				cmd := exec.Command(os.Args[0], "install", "--target", "claude", "--yes")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "wizard: install failed: %v\n", err)
					os.Exit(1)
				}
			}
			os.Exit(0)
		}
		app.SetFocus(pages)
	}

	handleBack := func() {
		name, _ := pages.GetFrontPage()
		switch name {
		case "theme":
			pages.SwitchToPage("welcome")
		case "depth":
			pages.SwitchToPage("theme")
		case "segments":
			pages.SwitchToPage("depth")
		case "install":
			pages.SwitchToPage("segments")
		case "summary":
			pages.SwitchToPage("install")
		}
		app.SetFocus(pages)
	}

	pages.AddPage("welcome", buildWelcomePage(&st, handleNext, func() { cancelWizard(app) }), true, true)
	pages.AddPage("theme", buildThemePage(&st, handleNext, handleBack), true, false)
	pages.AddPage("depth", buildDepthPage(&st, handleNext, handleBack), true, false)
	pages.AddPage("segments", buildSegmentsPage(&st, handleNext, handleBack), true, false)
	pages.AddPage("install", buildInstallPage(&st, handleNext, handleBack), true, false)
	pages.AddPage("summary", buildSummaryPage(pages, &st, handleNext, handleBack), true, false)

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			cancelWizard(app)
			return nil
		}
		switch event.Key() {
		case tcell.KeyF3, tcell.KeyCtrlN:
			handleNext()
			return nil
		case tcell.KeyF2, tcell.KeyCtrlB:
			handleBack()
			return nil
		}
		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case 'q', 'Q':
				cancelWizard(app)
				return nil
			}
		}
		return event
	})

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "wizard: %v\n", err)
		os.Exit(1)
	}
}

// startingConfig returns the current config with theme and color depth
// normalized to safe defaults. Segment selection is handled separately because
// the wizard defaults to DefaultConfig().Segments rather than the user's
// current list.
func startingConfig() config.Config {
	cfg := config.LoadConfig()
	if cfg.Theme == "" {
		cfg.Theme = "classic"
	}
	if cfg.ColorDepth == "" {
		cfg.ColorDepth = "auto"
	}
	cfg.Segments = nil
	return cfg
}

// startingSegments returns the default segment selection as a set.
func startingSegments() map[string]bool {
	selected := make(map[string]bool)
	for _, id := range config.DefaultConfig().Segments {
		selected[id] = true
	}
	return selected
}

// cancelWizard stops the TUI and exits without writing anything.
func cancelWizard(app *tview.Application) {
	app.Stop()
	fmt.Fprintln(os.Stderr, "Wizard cancelled — no changes were saved.")
	os.Exit(0)
}

// wizardPage wraps a page's content in a standard frame with a header and a
// button bar. The content primitive receives initial focus.
func wizardPage(title string, content tview.Primitive, buttons *tview.Form) *tview.Flex {
	header := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText(title)
	header.SetBorder(true)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 3, 0, false).
		AddItem(content, 0, 1, true).
		AddItem(buttons, 1, 0, true)
	return flex
}

// buildWelcomePage creates the intro step.
func buildWelcomePage(_ *wizardState, onNext, onCancel func()) tview.Primitive {
	text := tview.NewTextView().
		SetWrap(true).
		SetTextAlign(tview.AlignCenter).
		SetText("claude-statusline renders a colorful, information-dense status line for Claude Code.\n\nThis wizard will set your theme, color depth, and segments, and optionally wire the binary into Claude Code.")
	text.SetBorder(true).SetTitle(" Welcome ")

	buttons := tview.NewForm().
		AddButton("Start setup", onNext).
		AddButton("Cancel", onCancel)

	return wizardPage("Welcome", text, buttons)
}

// buildThemePage creates the single-select theme step.
func buildThemePage(st *wizardState, onNext, onBack func()) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Theme ")

	themeIDs := palette.ThemeIDs()
	for _, id := range themeIDs {
		id := id
		list.AddItem(id, "", 0, func() { st.cfg.Theme = id })
	}

	for i, id := range themeIDs {
		if id == st.cfg.Theme {
			list.SetCurrentItem(i)
			break
		}
	}

	list.SetChangedFunc(func(idx int, mainText, _ string, _ rune) {
		st.cfg.Theme = mainText
	})

	buttons := tview.NewForm().
		AddButton("Back", onBack).
		AddButton("Next", onNext)

	return wizardPage("Choose a theme", list, buttons)
}

// depthOptions maps display labels to the values persisted in config.toml.
var depthOptions = []struct {
	label string
	value string
}{
	{"Auto-detect", "auto"},
	{"Truecolor", "truecolor"},
	{"256 colors", "256"},
	{"16 colors", "16"},
	{"No color", "none"},
}

// buildDepthPage creates the single-select color-depth step.
func buildDepthPage(st *wizardState, onNext, onBack func()) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Color Depth ")

	for _, opt := range depthOptions {
		opt := opt
		list.AddItem(opt.label, "", 0, func() { st.cfg.ColorDepth = opt.value })
	}

	for i, opt := range depthOptions {
		if opt.value == st.cfg.ColorDepth {
			list.SetCurrentItem(i)
			break
		}
	}

	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		st.cfg.ColorDepth = depthOptions[idx].value
	})

	buttons := tview.NewForm().
		AddButton("Back", onBack).
		AddButton("Next", onNext)

	return wizardPage("Choose a color depth", list, buttons)
}

// buildSegmentsPage creates the multi-select checklist step.
func buildSegmentsPage(st *wizardState, onNext, onBack func()) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Segments ")

	all := segments.All()
	ids := make([]string, 0, len(all))
	desc := make(map[string]string, len(all))
	for _, s := range all {
		ids = append(ids, s.ID)
		desc[s.ID] = s.Desc
	}

	itemText := func(id string) string {
		mark := "  "
		if st.selected[id] {
			mark = "✓ "
		}
		return fmt.Sprintf("%s%s — %s", mark, id, desc[id])
	}

	for _, id := range ids {
		list.AddItem(itemText(id), "", 0, nil)
	}

	toggle := func() {
		idx := list.GetCurrentItem()
		if idx < 0 || idx >= len(ids) {
			return
		}
		id := ids[idx]
		st.selected[id] = !st.selected[id]
		list.SetItemText(idx, itemText(id), "")
	}

	list.SetSelectedFunc(func(int, string, string, rune) { toggle() })
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			toggle()
			return nil
		}
		return event
	})

	buttons := tview.NewForm().
		AddButton("Back", onBack).
		AddButton("Next", onNext)

	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("space/enter to toggle · q/esc to cancel")

	content := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(help, 1, 0, false)

	return wizardPage("Select segments", content, buttons)
}

// installOptions maps display labels to the install decision.
var installOptions = []struct {
	label string
	value bool
}{
	{"Yes — wire claude-statusline into Claude Code", true},
	{"No — I'll install it manually later", false},
}

// buildInstallPage creates the yes/no install step.
func buildInstallPage(st *wizardState, onNext, onBack func()) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Install ")

	for _, opt := range installOptions {
		opt := opt
		list.AddItem(opt.label, "", 0, func() { st.install = opt.value })
	}

	if !st.install {
		list.SetCurrentItem(1)
	}

	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		st.install = installOptions[idx].value
	})

	buttons := tview.NewForm().
		AddButton("Back", onBack).
		AddButton("Next", onNext)

	return wizardPage("Install into Claude Code?", list, buttons)
}

// buildSummaryPage creates the final review step.
func buildSummaryPage(pages *tview.Pages, st *wizardState, onNext, onBack func()) tview.Primitive {
	text := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	text.SetBorder(true).SetTitle(" Summary ")

	refresh := func() {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Theme: %s\n", st.cfg.Theme))
		b.WriteString(fmt.Sprintf("Color depth: %s\n", st.cfg.ColorDepth))
		b.WriteString(fmt.Sprintf("Segments (%d):\n", len(st.cfg.Segments)))
		for _, id := range st.cfg.Segments {
			b.WriteString(fmt.Sprintf("  • %s\n", id))
		}
		if st.install {
			b.WriteString("\n[yellow]Install:[-] Yes — will wire into ~/.claude/settings.json")
		} else {
			b.WriteString("\n[yellow]Install:[-] No")
		}
		text.SetText(b.String())
	}

	buttons := tview.NewForm().
		AddButton("Back", onBack).
		AddButton("Save & finish", onNext)

	pages.SetChangedFunc(func() {
		if name, _ := pages.GetFrontPage(); name == "summary" {
			refresh()
		}
	})

	return wizardPage("Review your choices", text, buttons)
}
