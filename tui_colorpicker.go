package main

// ─── Color Picker ────────────────────────────────────────────────────
//
// A floating swatch grid opened with C on a segment (or Enter on a flyout
// color row). Sections: the active theme's roles, the 16 ANSI names, and
// recently picked colors. Arrows navigate, Enter picks, d resets to default,
// Esc/q cancels. Hovering live-previews through the caller's onHover.

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// recentColors is the session-only MRU of picked color specs.
var recentColors []string

func pushRecentColor(spec string) {
	if spec == "" || spec == "default" {
		return
	}
	out := []string{spec}
	for _, c := range recentColors {
		if c != spec && len(out) < 8 {
			out = append(out, c)
		}
	}
	recentColors = out
}

// specToColor resolves a color spec to a tcell color for swatch rendering.
func specToColor(spec string, c palette) tcell.Color {
	code, ok := resolveColorSpec(spec, c)
	if !ok || code == "" {
		return tcell.ColorDefault
	}
	params := strings.TrimSuffix(strings.TrimPrefix(code, "\x1b["), "m")
	tag := sgrToTag(params)
	if strings.HasPrefix(tag, "[#") {
		return tcell.GetColor(strings.Trim(tag, "[]"))
	}
	return tcell.ColorDefault
}

type pickerEntry struct {
	spec   string
	header string // non-empty → section header cell, not selectable
}

func colorPickerEntries() []pickerEntry {
	entries := []pickerEntry{{header: "Theme"}}
	for _, role := range themeRoles {
		entries = append(entries, pickerEntry{spec: role})
	}
	entries = append(entries, pickerEntry{header: "ANSI"})
	for _, name := range colorCycle[1:] { // skip "default" — that's the d key
		entries = append(entries, pickerEntry{spec: name})
	}
	if len(recentColors) > 0 {
		entries = append(entries, pickerEntry{header: "Recent"})
		for _, spec := range recentColors {
			entries = append(entries, pickerEntry{spec: spec})
		}
	}
	return entries
}

const pickerCols = 4

// openColorPicker shows the picker as a floating page. onHover fires as the
// selection moves; onDone fires exactly once with picked=false on cancel.
func openColorPicker(app *tview.Application, pages *tview.Pages, c palette, title string, onHover func(spec string), onDone func(spec string, picked bool)) {
	entries := colorPickerEntries()

	table := tview.NewTable().SetSelectable(true, true)
	table.SetBorder(true).SetTitle(fmt.Sprintf(" %s — enter pick · d default · esc cancel ", title))

	// Lay entries into a grid; headers occupy their own row.
	specAt := map[[2]int]string{}
	row, col := 0, 0
	for _, e := range entries {
		if e.header != "" {
			if col != 0 {
				row++
			}
			cell := tview.NewTableCell("[::b]" + e.header + "[-:-:-]").
				SetSelectable(false).
				SetExpansion(1)
			table.SetCell(row, 0, cell)
			row, col = row+1, 0
			continue
		}
		swatch := "██ " + e.spec
		cell := tview.NewTableCell(swatch).
			SetTextColor(specToColor(e.spec, c)).
			SetExpansion(1)
		table.SetCell(row, col, cell)
		specAt[[2]int{row, col}] = e.spec
		col++
		if col == pickerCols {
			row, col = row+1, 0
		}
	}

	closePicker := func() {
		pages.RemovePage("colorpicker")
	}
	done := false
	finish := func(spec string, picked bool) {
		if done {
			return
		}
		done = true
		closePicker()
		onDone(spec, picked)
	}

	table.SetSelectionChangedFunc(func(row, col int) {
		if spec, ok := specAt[[2]int{row, col}]; ok && onHover != nil {
			onHover(spec)
		}
	})
	table.SetSelectedFunc(func(row, col int) {
		if spec, ok := specAt[[2]int{row, col}]; ok {
			finish(spec, true)
		}
	})
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			finish("", false)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q', 'Q':
				finish("", false)
				return nil
			case 'd', 'D':
				finish("default", true)
				return nil
			}
		}
		return event
	})

	// Float the table over the configure page so the live preview underneath
	// stays visible while hovering.
	floating := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(table, 18, 0, true).
			AddItem(nil, 0, 2, false), 64, 0, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("colorpicker", floating, true, true)
	app.SetFocus(table)

	// Select the first selectable cell.
	table.Select(1, 0)
}
