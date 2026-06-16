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
	b.WriteString("Two panes. The [::b]drawer[-:-:-] (left) is the inventory of available, OFF\n")
	b.WriteString("segments; the [::b]canvas[-:-:-] (centre) is the layout — the ON segments,\n")
	b.WriteString("grouped by render line. Tab or ←/→ switch panes; space/enter move the\n")
	b.WriteString("focused segment between them (off↔on). On the canvas, grab a segment (g)\n")
	b.WriteString("and use the arrows to reposition it across slots and lines. Recolor (c/C),\n")
	b.WriteString("tune per-segment settings in the flyout (o), and the preview renders live\n")
	b.WriteString("at your terminal width below. Nothing touches disk until you press s —\n")
	b.WriteString(fmt.Sprintf("changes save to [green]%s[-].\n", configPath()))

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
