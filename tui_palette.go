package main

// ─── Command-Palette Configure Mode ──────────────────────────────────
//
// An intent-driven configurator: the live preview is the only persistent UI.
// Every change is a fuzzy-searchable command summoned from a palette (Ctrl-P
// or ":"). You name what you want ("disable cost", "context bar width",
// "theme nord", "preset zen") instead of hunting through a list. The action
// set is generated from the segment registry + settings schema (actions.go),
// so it can never drift from the real configuration surface.
//
// This mode round-trips through the same config model and saves through the
// same saveConfig path as the list-based TUI; it is a separate front-end, not
// a separate config format.

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

func runPalette() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "claude-statusline palette requires an interactive terminal.")
		fmt.Fprintf(os.Stderr, "Edit %s directly, or run from a terminal.\n", configPath())
		os.Exit(1)
	}

	cfg, _ := loadConfigWarn()
	initSegments(cfg.Plugins)

	// Synthetic preview inputs (same contract as the list TUI): an hour of
	// session history plus a fake rich-git result so every feature renders.
	// Both are preview-only and MUST be nil on the real render path.
	pvState := previewState(time.Now())
	gitStatusPreview = &gitStatusInfo{Dirty: true, Ahead: 1, Behind: 2}
	defer func() { gitStatusPreview = nil }()
	stashPreview := 3
	gitStashPreview = &stashPreview
	defer func() { gitStashPreview = nil }()

	app := tview.NewApplication()

	dirty := false
	previewWidth := 0 // 0 = auto (track the panel width); else fixed columns

	// ─── Persistent UI: the preview ──────────────────────────────────────

	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	previewBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(preview, 0, 1, false)
	previewBox.SetBorder(true).SetTitle(" Preview ")

	stripLeft := tview.NewTextView().SetDynamicColors(true)
	stripRight := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	statusStrip := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(stripLeft, 0, 1, false).
		AddItem(stripRight, 0, 1, false)

	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("palette"))

	pages := tview.NewPages()

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

	updateStrip := func() {
		theme := cfg.Theme
		if theme == "" {
			theme = "classic"
		}
		n := len(cfg.Segments)
		marker := ""
		if dirty {
			marker = " [yellow]●[-]"
		}
		reflow := cfg.Reflow
		if reflow == "" {
			reflow = "off"
		}
		stripLeft.SetText(fmt.Sprintf(" theme: [::b]%s[-:-:-] · reflow: %s · %d segments%s", theme, reflow, n, marker))
	}

	refreshPreview := func() {
		width := previewWidth
		_, _, panelW, _ := preview.GetInnerRect()
		if width == 0 && panelW > 0 {
			width = panelW
		}
		lines := buildStatusline(buildInput{P: samplePayload(), C: currentPalette(cfg), Cfg: cfg, State: pvState, Width: width, Now: time.Now()})
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
		updateStrip()
	}

	// applyMut is the single mutation funnel: run fn, mark dirty, refresh.
	applyMut := func(fn func(*config)) {
		fn(&cfg)
		dirty = true
		refreshPreview()
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

	// ─── Palette overlay ─────────────────────────────────────────────────

	// allActions is regenerated whenever the config changes shape (enabling a
	// segment flips its "Enable"→"Disable" command, etc.), so the palette
	// always reflects current state.
	var allActions []action
	var filtered []action

	palInput := tview.NewInputField().SetLabel(" › ")
	palInput.SetFieldBackgroundColor(tcell.ColorDefault)
	palList := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	palHint := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[gray] enter run · ↑/↓ select · esc close · type to filter[-]")
	palFlex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(palInput, 1, 0, true).
		AddItem(palList, 0, 1, false).
		AddItem(palHint, 1, 0, false)
	palFlex.SetBorder(true).SetTitle(" Command palette — name what you want ")

	rebuildPalList := func(query string) {
		filtered = rankActions(allActions, query)
		palList.Clear()
		max := len(filtered)
		if max > 200 {
			max = 200 // cap the rendered rows; ranking already put the best first
		}
		for i := 0; i < max; i++ {
			palList.AddItem(filtered[i].Title, "", 0, nil)
		}
		if palList.GetItemCount() > 0 {
			palList.SetCurrentItem(0)
		}
	}

	var runAction func(a action)

	closePalette := func() {
		pages.RemovePage("palette")
		app.SetFocus(preview)
	}

	openPalette := func() {
		allActions = generateActions(cfg)
		palInput.SetText("")
		rebuildPalList("")
		pages.AddPage("palette", floatPicker(palFlex, 64, 22), true, true)
		app.SetFocus(palInput)
	}

	palInput.SetChangedFunc(func(text string) {
		rebuildPalList(text)
	})

	selectedAction := func() (action, bool) {
		idx := palList.GetCurrentItem()
		if idx < 0 || idx >= len(filtered) {
			return action{}, false
		}
		return filtered[idx], true
	}

	// runAction dispatches by kind: plain mutations apply immediately; color
	// actions open the swatch picker; prompt actions open a text input. The
	// palette closes before any sub-overlay so only one floats at a time.
	runAction = func(a action) {
		switch a.Kind {
		case actionColorPicker:
			closePalette()
			// Snapshot so cancel restores cleanly — SetColor may target either
			// cfg.Colors (segment primary) or cfg.Settings (a color setting).
			snapshot := cloneConfig(cfg)
			openColorPicker(app, pages, currentPalette(cfg), a.Title,
				func(spec string) { // hover preview
					a.SetColor(&cfg, spec)
					refreshPreview()
				},
				func(spec string, picked bool) {
					if picked {
						cfg = cloneConfig(snapshot)
						a.SetColor(&cfg, spec)
						pushRecentColor(spec)
						dirty = true
						flash("green", "set "+spec)
					} else {
						cfg = snapshot
					}
					refreshPreview()
					app.SetFocus(preview)
				})
		case actionPrompt:
			closePalette()
			openPromptModal(app, pages, a.PromptLabel, func(val string) {
				if err := a.SetValue(&cfg, val); err != nil {
					flash("red", "✗ "+err.Error())
					return
				}
				dirty = true
				refreshPreview()
				flash("green", "✓ "+a.Title)
			})
		default:
			applyMut(a.Apply)
			flash("green", "✓ "+a.Title)
			closePalette()
		}
	}

	palList.SetSelectedFunc(func(int, string, string, rune) {
		if a, ok := selectedAction(); ok {
			runAction(a)
		}
	})

	// Palette key handling: keep arrow/enter/esc for navigation, let the rest
	// reach the input field for typing.
	palInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			if a, ok := selectedAction(); ok {
				runAction(a)
			}
			return nil
		case tcell.KeyEscape:
			closePalette()
			return nil
		case tcell.KeyDown, tcell.KeyCtrlN:
			cur := palList.GetCurrentItem()
			if cur < palList.GetItemCount()-1 {
				palList.SetCurrentItem(cur + 1)
			}
			return nil
		case tcell.KeyUp, tcell.KeyCtrlP:
			cur := palList.GetCurrentItem()
			if cur > 0 {
				palList.SetCurrentItem(cur - 1)
			}
			return nil
		case tcell.KeyTab:
			cur := palList.GetCurrentItem()
			if cur < palList.GetItemCount()-1 {
				palList.SetCurrentItem(cur + 1)
			} else {
				palList.SetCurrentItem(0)
			}
			return nil
		}
		return event
	})

	// ─── Help overlay (declared before key routing references it) ────────

	helpView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetText(buildPaletteHelpText())
	helpView.SetBorder(true).SetTitle(" Help (q/Esc close) ")
	helpView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			pages.SwitchToPage("home")
			app.SetFocus(preview)
			return nil
		case tcell.KeyRune:
			if event.Rune() == 'q' || event.Rune() == 'Q' {
				pages.SwitchToPage("home")
				app.SetFocus(preview)
				return nil
			}
		}
		return event
	})

	// ─── Global key routing ──────────────────────────────────────────────

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		pageName, _ := pages.GetFrontPage()
		if pageName == "palette" || isPickerPage(pageName) || pageName == "prompt" {
			return event // overlays handle their own keys
		}
		switch event.Key() {
		case tcell.KeyCtrlP:
			openPalette()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case ':', '/', 'p', 'P', 'i':
				openPalette()
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
					fmt.Print("\n  press enter to return… ")
					_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
				})
				return nil
			case 's', 'S':
				doSave()
				return nil
			case 'h', 'H', '?':
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case 'q', 'Q':
				requestQuitPalette(app, pages, dirty, doSave)
				return nil
			}
		case tcell.KeyEscape:
			requestQuitPalette(app, pages, dirty, doSave)
			return nil
		}
		return event
	})

	// ─── Layout ──────────────────────────────────────────────────────────

	home := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(previewBox, 0, 1, true).
		AddItem(statusStrip, 1, 0, false).
		AddItem(help, 1, 0, false)

	pages.AddPage("home", home, true, true)
	pages.AddPage("help", helpView, true, false)

	// Recompute the preview on resize (auto width only) and grow the footer.
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
			home.ResizeItem(help, footerRows(footerText("palette"), sw), 0)
		}
		return false
	})

	updateStrip()

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// requestQuitPalette shows a save/discard/cancel modal when there are unsaved
// changes, else stops immediately.
func requestQuitPalette(app *tview.Application, pages *tview.Pages, dirty bool, doSave func() bool) {
	if !dirty {
		app.Stop()
		return
	}
	modal := tview.NewModal().
		SetText("Unsaved changes.").
		AddButtons([]string{"Save & quit", "Discard", "Cancel"}).
		SetDoneFunc(func(_ int, label string) {
			switch label {
			case "Save & quit":
				if doSave() {
					app.Stop()
					fmt.Printf("Saved to %s\n", configPath())
					return
				}
				pages.RemovePage("palquit")
			case "Discard":
				app.Stop()
			default:
				pages.RemovePage("palquit")
			}
		})
	pages.AddPage("palquit", modal, true, true)
	app.SetFocus(modal)
}

// openPromptModal floats a single-line text input for an int-valued action.
func openPromptModal(app *tview.Application, pages *tview.Pages, label string, onSubmit func(string)) {
	input := tview.NewInputField().SetLabel(" " + label + ": ")
	input.SetFieldBackgroundColor(tcell.ColorDefault)
	form := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(input, 1, 0, true)
	form.SetBorder(true).SetTitle(" Set value — enter apply · esc cancel ")
	dismiss := func() {
		pages.RemovePage("prompt")
		app.SetFocus(pages)
	}
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			val := input.GetText()
			dismiss()
			onSubmit(val)
			return nil
		case tcell.KeyEscape:
			dismiss()
			return nil
		}
		return event
	})
	pages.AddPage("prompt", floatPicker(form, 50, 3), true, true)
	app.SetFocus(input)
}

// buildPaletteHelpText renders the help overlay for the palette mode.
func buildPaletteHelpText() string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline palette[-::-]\n\n")
	b.WriteString("An intent-driven configurator. The preview is the only persistent\n")
	b.WriteString("screen; every change is a command you summon from the palette and\n")
	b.WriteString("name by intent — no list to hunt through.\n\n")
	b.WriteString("[cyan::b]Commands[-::-]\n")
	for _, kb := range keymap {
		if kb.Context != "palette" {
			continue
		}
		b.WriteString(fmt.Sprintf("  [::b]%-12s[-:-:-] %s\n", kb.Keys, kb.Desc))
	}
	b.WriteString("\n[cyan::b]In the palette[-::-]\n")
	b.WriteString("  Type to fuzzy-match. Try: [::b]disable cost[-:-:-], [::b]context bar width[-:-:-],\n")
	b.WriteString("  [::b]move git line 2[-:-:-], [::b]theme nord[-:-:-], [::b]preset zen[-:-:-], [::b]reflow group[-:-:-].\n")
	b.WriteString("  Commands are generated from the segment registry and settings schema,\n")
	b.WriteString("  so they always match what is actually configurable.\n")
	b.WriteString("\n[gray]Changes are in memory until you press s · saves to[-]\n")
	b.WriteString(fmt.Sprintf("[green]%s[-]\n", configPath()))
	return b.String()
}
