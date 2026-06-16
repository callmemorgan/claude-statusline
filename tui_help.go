package main

// ─── In-TUI Help ─────────────────────────────────────────────────────
//
// The ? overlay is generated from the keymap table, so it always matches the
// real bindings. The full README remains available behind it (press r).

import (
	"fmt"
	"strings"
)

func buildHelpText() string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline configure[-::-]\n\n")
	b.WriteString("Segments are the building blocks of the statusline. Toggle them on or\n")
	b.WriteString("off, assign them to lines 1-9, recolor them, and tune per-segment\n")
	b.WriteString("settings in the flyout (o). The list is grouped under line headers\n")
	b.WriteString("(── line 1 ──, … , ── off ──), so its top-to-bottom order is exactly the\n")
	b.WriteString("render order. The preview renders live at your terminal width. Nothing\n")
	b.WriteString(fmt.Sprintf("touches disk until you press s — changes save to [green]%s[-].\n", configPath()))

	section := func(title, context string) {
		b.WriteString(fmt.Sprintf("\n[cyan::b]%s[-::-]\n", title))
		for _, kb := range keymap {
			if kb.Context != context {
				continue
			}
			b.WriteString(fmt.Sprintf("  [::b]%-12s[-:-:-] %s\n", kb.Keys, kb.Desc))
		}
	}
	section("Main screen", "main")
	section("Settings flyout", "flyout")

	b.WriteString("\n[cyan::b]Concepts[-::-]\n")
	b.WriteString("  [::b]themes[-:-:-]       6 built-in palettes; truecolor with automatic 256/16 fallback;\n")
	b.WriteString("               classic (alias: original) is the pre-1.0 default look — v shows\n")
	b.WriteString("               any theme against your real terminal\n")
	b.WriteString("  [::b]presets[-:-:-]      named layouts — applying one replaces segments, lines, settings\n")
	b.WriteString("  [::b]plugins[-:-:-]      executable segments defined in config.toml ([[plugins]])\n")
	b.WriteString("  [::b]projections[-:-:-]  →58% burn-rate forecasts appear after ~5 minutes of session\n")
	b.WriteString("               history (the preview here fakes that history so you can see them)\n")

	b.WriteString("\n[gray]r full README · q/esc close[-]\n")
	return b.String()
}
