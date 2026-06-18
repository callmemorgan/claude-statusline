package main

// ─── In-TUI Help ─────────────────────────────────────────────────────
//
// The ? overlay is generated from the keymap table, so it always matches the
// real bindings. The full README remains available behind it (press r).

import (
	"fmt"
	"strings"
)

func buildHelpText(mode string) string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline configure")
	if mode == "canvas" {
		b.WriteString(" --drawer")
	}
	b.WriteString("[-::-]\n\n")

	if mode == "canvas" {
		b.WriteString("Two panes. The [::b]drawer[-:-:-] (left) is the inventory of available, OFF\n")
		b.WriteString("segments; the [::b]canvas[-:-:-] (centre) is the layout — the ON segments,\n")
		b.WriteString("grouped by render line. Tab or ←/→ switch panes; space/enter move the\n")
		b.WriteString("focused segment between them (off↔on). On the canvas, grab a segment (g)\n")
		b.WriteString("and use the arrows to reposition it across slots and lines. Recolor (c/C),\n")
	} else {
		b.WriteString("Segments are the building blocks of the statusline. Toggle them on or\n")
		b.WriteString("off, assign them to lines 1-9, recolor them, and tune per-segment\n")
		b.WriteString("settings in the flyout (o). Use ←/→ to reorder within a line and ⇧↑/↓ to\n")
		b.WriteString("swap whole lines. The preview renders live at your terminal width.\n")
	}
	b.WriteString("Nothing touches disk until you press s — changes save to\n")
	b.WriteString(fmt.Sprintf("[green]%s[-].\n", configPath()))

	section := func(title, context string) {
		b.WriteString(fmt.Sprintf("\n[cyan::b]%s[-::-]\n", title))
		for _, kb := range keymap {
			if kb.Context != context {
				continue
			}
			b.WriteString(fmt.Sprintf("  [::b]%-12s[-:-:-] %s\n", kb.Keys, kb.Desc))
		}
	}
	if mode == "canvas" {
		section("Canvas screen", "canvas")
	} else {
		section("Main screen", "list")
	}
	section("Settings flyout", "flyout")

	b.WriteString("\n[cyan::b]Concepts[-::-]\n")
	b.WriteString("  [::b]themes[-:-:-]       built-in palettes; truecolor with automatic 256/16 fallback;\n")
	b.WriteString("               classic (alias: original) is the pre-1.0 default look — v shows\n")
	b.WriteString("               any theme against your real terminal\n")
	b.WriteString("  [::b]presets[-:-:-]      named layouts — applying one replaces segments, lines, settings\n")
	b.WriteString("  [::b]plugins[-:-:-]      executable segments defined in config.toml ([[plugins]])\n")
	b.WriteString("  [::b]projections[-:-:-]  →58% burn-rate forecasts appear after ~5 minutes of session\n")
	b.WriteString("               history (the preview here fakes that history so you can see them)\n")

	b.WriteString("\n[gray]r full README · q/esc close[-]\n")
	return b.String()
}
