package main

// ─── Theme & Preset Pickers ──────────────────────────────────────────
//
// Floating list pickers opened with t (themes) and p (presets). The main
// screen's live preview stays visible underneath, and the highlighted entry
// is applied to it as you move — Enter keeps it, Esc restores the original.

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/callmemorgan/claude-statusline/internal/palette"
)

// floatPicker wraps a primitive in spacer flexes so it floats centered over
// the underlying page with the preview still visible below.
func floatPicker(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 2, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}

// openListPicker is the shared list-with-live-preview machinery. onHover
// fires for the highlighted entry; onDone fires once, with picked=false on
// cancel.
func openListPicker(app *tview.Application, pages *tview.Pages, pageName, title string,
	items [][2]string, // id, description
	currentID string,
	onHover func(id string),
	onDone func(id string, picked bool),
) {
	list := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey)
	list.SetBorder(true).SetTitle(" " + title + " — enter apply · esc cancel ")

	startIdx := 0
	for i, it := range items {
		list.AddItem(it[0], "    "+it[1], 0, nil)
		if it[0] == currentID {
			startIdx = i
		}
	}

	done := false
	finish := func(id string, picked bool) {
		if done {
			return
		}
		done = true
		pages.RemovePage(pageName)
		onDone(id, picked)
	}

	list.SetChangedFunc(func(idx int, mainText, _ string, _ rune) {
		if onHover != nil && idx >= 0 && idx < len(items) {
			onHover(items[idx][0])
		}
	})
	list.SetSelectedFunc(func(idx int, mainText, _ string, _ rune) {
		if idx >= 0 && idx < len(items) {
			finish(items[idx][0], true)
		}
	})
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			finish("", false)
			return nil
		case tcell.KeyRune:
			if event.Rune() == 'q' || event.Rune() == 'Q' {
				finish("", false)
				return nil
			}
		}
		return event
	})

	height := len(items)*2 + 2
	if height > 20 {
		height = 20
	}
	pages.AddPage(pageName, floatPicker(list, 56, height), true, true)
	app.SetFocus(list)
	list.SetCurrentItem(startIdx)
	if onHover != nil {
		onHover(items[startIdx][0])
	}
}

func openThemePicker(app *tview.Application, pages *tview.Pages, current string, onHover func(id string), onDone func(id string, picked bool)) {
	if current == "" {
		current = "classic"
	}
	items := make([][2]string, 0, len(palette.BuiltinThemes))
	for _, t := range palette.BuiltinThemes {
		items = append(items, [2]string{t.ID, t.Desc})
	}
	openListPicker(app, pages, "themepicker", "Theme", items, current, onHover, onDone)
}

func openPresetPicker(app *tview.Application, pages *tview.Pages, onHover func(id string), onDone func(id string, picked bool)) {
	items := make([][2]string, 0, len(layoutPresets))
	for _, p := range layoutPresets {
		desc := p.Desc
		if p.Theme != "" {
			desc += " · suggests " + p.Theme
		}
		items = append(items, [2]string{p.ID, desc})
	}
	openListPicker(app, pages, "presetpicker", "Preset", items, "", onHover, onDone)
}

// pickerPageNames lets the main input capture pass keys through to whichever
// picker is on top.
func isPickerPage(name string) bool {
	return name == "colorpicker" || name == "themepicker" || name == "presetpicker" ||
		strings.HasPrefix(name, "picker:")
}
