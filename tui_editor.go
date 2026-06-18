package main

// ─── Layout DSL Editor ───────────────────────────────────────────────
//
// `claude-statusline edit` is a buffer-editing configuration mode: instead of
// navigating a segment list, the user edits the statusline as a textual LAYOUT
// (see dsl.go for the grammar). The buffer is the source of truth — on every
// keystroke it is parsed into a config, the live preview re-renders through the
// real buildStatusline, and unknown tokens / invalid settings are reported as
// inline diagnostics. Saving serializes that parsed config to the same TOML as
// every other mode (saveConfig).
//
// This is an additive entry point: it touches neither the bare render path nor
// the existing list-based `configure` TUI.

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

func runEditor() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "claude-statusline edit requires an interactive terminal.")
		fmt.Fprintf(os.Stderr, "Edit %s directly, or run from a terminal.\n", configPath())
		os.Exit(1)
	}

	cfg, _ := loadConfigWarn()
	initSegments(cfg.Plugins)

	// Synthetic preview data so every feature renders, exactly as the list TUI
	// does. Both git previews MUST be reset to nil on exit (locked invariant:
	// they must never leak onto the real render path).
	pvState := previewState(time.Now())
	gitStatusPreview = &gitStatusInfo{Dirty: true, Ahead: 1, Behind: 2}
	defer func() { gitStatusPreview = nil }()
	stashPreview := 3
	gitStashPreview = &stashPreview
	defer func() { gitStashPreview = nil }()

	app := tview.NewApplication()

	// ── Widgets ──────────────────────────────────────────────────────
	editor := tview.NewTextArea().
		SetWrap(false).
		SetPlaceholder("one render line per buffer line — e.g.  directory git-branch cost")
	editor.SetBorder(true).SetTitle(" Layout (edit me) ")

	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	preview.SetBorder(true).SetTitle(" Preview ")

	diag := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	diag.SetBorder(true).SetTitle(" Diagnostics ")

	complete := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	complete.SetBorder(true).SetTitle(" Completions (tab) ")

	footer := tview.NewTextView().SetDynamicColors(true)
	footer.SetText(editorFooterText())

	status := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)

	// dirty tracks unsaved edits relative to the last save / load.
	dirty := false

	// lastValid is the last successfully-parsed config (used by save: we save
	// what's currently parseable even if a stray token is unknown — unknown
	// segments are kept and skipped by the renderer, matching the TUI).
	lastValid := cfg

	flash := func(color, msg string) {
		status.SetText(fmt.Sprintf("[%s]%s[-]", color, msg))
		go func() {
			time.AfterFunc(2500*time.Millisecond, func() {
				app.QueueUpdateDraw(func() { status.SetText("") })
			})
		}()
	}

	// ── Preview / diagnostics refresh from the buffer ────────────────
	previewWidth := 0 // 0 = auto (track panel width); else fixed columns

	refresh := func() {
		text := editor.GetText()
		parsed, errs := parseDSL(text)
		// Normalize (this also surfaces problems the DSL didn't catch and is
		// what gets saved). validateConfig mutates in place and returns warns.
		warns := validateConfig(&parsed)
		lastValid = parsed

		// Live preview through the real renderer.
		width := previewWidth
		_, _, panelW, _ := preview.GetInnerRect()
		if width == 0 && panelW > 0 {
			width = panelW
		}
		lines := buildStatusline(buildInput{P: samplePayload(), C: currentPalette(parsed), Cfg: parsed, State: pvState, Width: width, Now: time.Now()})
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
			preview.SetTitle(fmt.Sprintf(" Preview (%d cols — Ctrl-W to cycle) ", previewWidth))
		} else {
			for i, l := range lines {
				lines[i] = strings.TrimLeft(l, " ")
			}
			previewText = strings.TrimSpace(strings.Join(lines, "\n"))
			if panelW > 0 {
				preview.SetTitle(fmt.Sprintf(" Preview (auto · %d cols) ", panelW))
			}
		}
		if strings.TrimSpace(previewText) == "" {
			preview.SetText("[gray](statusline hidden — no segments)[-]")
		} else {
			preview.SetText(ansiToTview(previewText))
		}

		// Diagnostics panel.
		if len(errs) == 0 && len(warns) == 0 {
			diag.SetText("[green]✓ no problems[-]")
			editor.SetTitle(" Layout (edit me) ")
		} else if len(errs) == 0 {
			// Surface the first normalization warning so users see when a
			// directive was ignored or normalized.
			diag.SetText(fmt.Sprintf("[yellow]⚠ %s[-]", tview.Escape(warns[0].String())))
			editor.SetTitle(fmt.Sprintf(" Layout — %d warning(s) ", len(warns)))
		} else {
			var b strings.Builder
			for _, e := range errs {
				fmt.Fprintf(&b, "[red]•[-] %s\n", tview.Escape(e.String()))
			}
			diag.SetText(b.String())
			editor.SetTitle(fmt.Sprintf(" Layout — %d problem(s) ", len(errs)))
		}
	}

	// completions panel reflects the token under the cursor.
	refreshCompletions := func() {
		prefix := cursorPrefix(editor)
		cs := dslCompletions(prefix)
		if len(cs) == 0 {
			complete.SetText("[gray](no completions)[-]")
			return
		}
		var b strings.Builder
		max := 12
		for i, c := range cs {
			if i >= max {
				fmt.Fprintf(&b, "[gray]… +%d more[-]\n", len(cs)-max)
				break
			}
			fmt.Fprintf(&b, "[yellow]%s[-]  [gray]%s[-]\n", tview.Escape(c.Text), tview.Escape(truncateToWidth(c.Label, 40)))
		}
		complete.SetText(b.String())
	}

	editor.SetChangedFunc(func() {
		dirty = true
		refresh()
		refreshCompletions()
	})
	editor.SetMovedFunc(func() {
		refreshCompletions()
	})

	// Seed the buffer from the loaded config (serialize → buffer).
	editor.SetText(configToDSL(cfg), false)
	dirty = false // SetText fired Changed; the initial load isn't a user edit.

	// ── Layout ───────────────────────────────────────────────────────
	rightTop := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(preview, 0, 2, false).
		AddItem(complete, 0, 3, false)
	body := tview.NewFlex().
		AddItem(editor, 0, 3, true).
		AddItem(rightTop, 0, 2, false)
	bottom := tview.NewFlex().
		AddItem(footer, 0, 4, false).
		AddItem(status, 0, 1, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(body, 0, 3, true).
		AddItem(diag, 5, 0, false).
		AddItem(bottom, 1, 0, false)

	pages := tview.NewPages()
	pages.AddPage("editor", root, true, true)

	// ── Help overlay ─────────────────────────────────────────────────
	helpView := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	helpView.SetBorder(true).SetTitle(" Help — esc to close ")
	helpView.SetText(buildEditorHelpText())
	pages.AddPage("help", floatPicker(helpView, 80, 28), true, false)

	// ── Quit-confirm overlay ─────────────────────────────────────────
	quitModal := tview.NewModal().
		SetText("You have unsaved changes. Save before quitting?").
		AddButtons([]string{"Save & quit", "Quit anyway", "Cancel"})
	quitModal.SetDoneFunc(func(_ int, label string) {
		switch label {
		case "Save & quit":
			if err := saveConfig(lastValid); err != nil {
				pages.SwitchToPage("editor")
				app.SetFocus(editor)
				flash("red", fmt.Sprintf("✗ save failed: %v", err))
				return
			}
			app.Stop()
		case "Quit anyway":
			app.Stop()
		default:
			pages.SwitchToPage("editor")
			app.SetFocus(editor)
		}
	})
	pages.AddPage("quit", quitModal, true, false)

	requestQuit := func() {
		if !dirty {
			app.Stop()
			return
		}
		pages.SwitchToPage("quit")
		app.SetFocus(quitModal)
	}

	doSave := func() {
		if err := saveConfig(lastValid); err != nil {
			flash("red", fmt.Sprintf("✗ save failed: %v", err))
			return
		}
		dirty = false
		flash("green", "✓ Saved to "+configPath())
	}

	// applyTopCompletion inserts the best completion for the token under the
	// cursor, replacing the partial word the cursor sits on.
	applyTopCompletion := func() {
		prefix := cursorPrefix(editor)
		cs := dslCompletions(prefix)
		if len(cs) == 0 {
			return
		}
		insertCompletion(editor, prefix, cs[0].Text)
	}

	// ── Global key routing ───────────────────────────────────────────
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		page, _ := pages.GetFrontPage()
		if page == "help" {
			if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
				pages.SwitchToPage("editor")
				app.SetFocus(editor)
			}
			return nil
		}
		if page == "quit" {
			return event // the modal handles its own keys
		}

		// Editor page. Ctrl-keys are reserved for editor actions so plain
		// typing (including '#', '[', letters) always reaches the TextArea.
		switch event.Key() {
		case tcell.KeyCtrlS:
			doSave()
			return nil
		case tcell.KeyCtrlQ:
			requestQuit()
			return nil
		case tcell.KeyTab:
			applyTopCompletion()
			return nil
		case tcell.KeyCtrlW:
			previewWidth = cycleEditorWidth(previewWidth)
			refresh()
			return nil
		case tcell.KeyCtrlR:
			// Reset the buffer to defaults.
			editor.SetText(configToDSL(defaultConfig()), false)
			flash("yellow", "reset to defaults (not yet saved)")
			return nil
		case tcell.KeyCtrlV:
			// Render straight to the terminal to check real colors.
			app.Suspend(func() {
				w, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil || w <= 0 {
					w = 80
				}
				lines := buildStatusline(buildInput{P: samplePayload(), C: currentPalette(lastValid), Cfg: lastValid, State: pvState, Width: w, Now: time.Now()})
				themeName := lastValid.Theme
				if themeName == "" {
					themeName = "classic"
				}
				fmt.Printf("\n  theme: %s · %d cols — as rendered by your terminal\n\n", themeName, w)
				for _, l := range lines {
					fmt.Println(l)
				}
				fmt.Print("\n  press enter to return to the editor… ")
				_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			})
			return nil
		case tcell.KeyF1:
			pages.SwitchToPage("help")
			app.SetFocus(helpView)
			return nil
		case tcell.KeyEscape:
			requestQuit()
			return nil
		case tcell.KeyRune:
			if event.Rune() == '?' && event.Modifiers()&tcell.ModAlt != 0 {
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			}
		}
		return event
	})

	// Prime the preview/diagnostics/completions before the first draw. At this
	// point tview has not laid out the widgets yet, so preview.GetInnerRect()
	// returns a pre-layout width — the auto preview must be re-rendered once the
	// real panel width is known (and again on every resize). Mirrors the main
	// configure TUI's SetBeforeDrawFunc.
	refresh()
	refreshCompletions()

	lastAutoWidth := -1
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		if previewWidth == 0 {
			if _, _, w, _ := preview.GetInnerRect(); w > 0 && w != lastAutoWidth {
				lastAutoWidth = w
				refresh()
			}
		}
		return false
	})

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ─── Editor helpers ──────────────────────────────────────────────────

// cursorPrefix returns the text on the cursor's line up to (not including) the
// cursor column — what dslCompletions consumes to decide what to suggest.
func cursorPrefix(editor *tview.TextArea) string {
	row, col, _, _ := editor.GetCursor()
	full := editor.GetText()
	lines := strings.Split(full, "\n")
	if row < 0 || row >= len(lines) {
		return ""
	}
	line := lines[row]
	r := []rune(line)
	if col > len(r) {
		col = len(r)
	}
	if col < 0 {
		col = 0
	}
	return string(r[:col])
}

// insertCompletion replaces the partial word under the cursor with full. The
// "partial word" is the trailing run consistent with how dslCompletions reads
// the prefix: the active segment-id word, or the partial key/value inside a
// bracket.
func insertCompletion(editor *tview.TextArea, prefix, full string) {
	// Replace exactly the partial the suggestions were computed for, so the
	// completion logic and the insert stay in sync (shared bracketContext).
	partial := bracketContext(prefix).partial
	row, col, _, _ := editor.GetCursor()
	// Cursor offset in the whole text.
	text := editor.GetText()
	offset := byteOffset(text, row, col)
	start := offset - len(partial)
	if start < 0 {
		start = 0
	}
	editor.Replace(start, offset, full)
}

// byteOffset converts a (row, column) cursor position to a byte offset into
// text. column is a rune index within the row, but tview.TextArea.Replace
// expects byte positions, so previous lines are counted in bytes and the
// current line is converted from runes to bytes.
func byteOffset(text string, row, col int) int {
	lines := strings.Split(text, "\n")
	off := 0
	for i := 0; i < row && i < len(lines); i++ {
		off += len(lines[i]) + 1 // +1 for the newline
	}
	if row < len(lines) {
		r := []rune(lines[row])
		if col > len(r) {
			col = len(r)
		}
		off += byteOffsetOfRunes(lines[row], col)
	}
	return off
}

// byteOffsetOfRunes returns the byte length of the first n runes of s.
func byteOffsetOfRunes(s string, n int) int {
	r := []rune(s)
	if n > len(r) {
		n = len(r)
	}
	if n < 0 {
		n = 0
	}
	return len(string(r[:n]))
}

func cycleEditorWidth(w int) int {
	switch w {
	case 0:
		return 80
	case 80:
		return 60
	case 60:
		return 40
	default:
		return 0
	}
}

// editorFooterText is the one-line key hint, generated from the editor keymap.
func editorFooterText() string {
	return footerText("editor")
}

func buildEditorHelpText() string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline edit — layout DSL[-::-]\n\n")
	b.WriteString("Edit the statusline as text. [::b]Each buffer line is one render line[-::-]\n")
	b.WriteString("(top line = line 1). Tokens are segment ids separated by spaces and\n")
	b.WriteString("render left-to-right. The buffer is the source of truth: it parses to\n")
	b.WriteString("the config live, and the preview re-renders on every keystroke.\n\n")

	b.WriteString("[cyan::b]Token syntax[-::-]\n")
	b.WriteString("  [yellow]directory[-]                      a bare segment\n")
	b.WriteString("  [yellow]cost[color=cyan][-]               override the primary color\n")
	b.WriteString("  [yellow]git-branch[git_status=true][-]    a per-segment setting\n")
	b.WriteString("  [yellow]rate-limit-5h[bar_width=20, show_countdown=false][-]\n")
	b.WriteString("                                  several overrides, comma-separated\n\n")

	b.WriteString("[cyan::b]Directives (top of buffer)[-::-]\n")
	b.WriteString("  [green]# theme: gruvbox[-]      [green]# reflow: cascade[-]\n")
	b.WriteString("  [green]# separator: dot[-]      [green]# padding: 2[-]\n")
	b.WriteString("  [green]# color_depth: 256[-]    [green]# separator_custom:  ▸ [-]\n\n")

	section := func(title, context string) {
		fmt.Fprintf(&b, "[cyan::b]%s[-::-]\n", title)
		for _, kb := range keymap {
			if kb.Context != context {
				continue
			}
			fmt.Fprintf(&b, "  [::b]%-10s[-:-:-] %s\n", kb.Keys, kb.Desc)
		}
	}
	section("Keys", "editor")

	b.WriteString("\n[cyan::b]Concepts[-::-]\n")
	b.WriteString("  [::b]autocomplete[-:-:-]  tab inserts the top suggestion for the token under\n")
	b.WriteString("                the cursor (segment ids, then setting keys, then values)\n")
	b.WriteString("  [::b]diagnostics[-:-:-]   unknown segments and invalid settings are flagged\n")
	b.WriteString("                inline with their buffer line/column\n")
	b.WriteString("  [::b]save[-:-:-]          ctrl-s writes config.toml (same file as configure)\n")
	return b.String()
}
