package main

// ─── In-TUI Help ─────────────────────────────────────────────────────
//
// The ? overlay is generated from the keymap table, so it always matches the
// real bindings. The full README remains available behind it (press r).

import (
	"fmt"
	"strings"
)

func buildHelpText(context string) string {
	if context == "direct" {
		return buildDirectHelpText()
	}
	return buildListHelpText()
}

func buildDirectHelpText() string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline configure --direct[-::-]\n\n")
	b.WriteString("The Preview is the editing surface. A cursor highlights one rendered\n")
	b.WriteString("segment on its real line; the arrow keys walk it through the segments\n")
	b.WriteString("and across lines. [::b]space[::-] toggles the segment under the cursor;\n")
	b.WriteString("[::b]m[::-] grabs it so the arrows relocate it in real space (enter drops,\n")
	b.WriteString("esc cancels); [::b]a[::-] opens the palette of off segments to insert one\n")
	b.WriteString("at the cursor. color (c/C), options (o), theme (t) and presets (p) act\n")
	b.WriteString("on the cursor's segment. Everything you see is the REAL render — the\n")
	b.WriteString("cursor is painted on top. Nothing touches disk until you press s —\n")
	fmt.Fprintf(&b, "changes save to [green]%s[-].\n", configPath())

	section := func(title, context string) {
		b.WriteString(fmt.Sprintf("\n[cyan::b]%s[-::-]\n", title))
		for _, kb := range keymap {
			if kb.Context != context {
				continue
			}
			b.WriteString(fmt.Sprintf("  [::b]%-12s[-:-:-] %s\n", kb.Keys, kb.Desc))
		}
	}
	section("Preview canvas", "direct")
	section("Add palette", "palette")
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

func buildListHelpText() string {
	var b strings.Builder
	b.WriteString("[yellow::b]claude-statusline configure[-::-]\n\n")
	b.WriteString("The segment list (left) shows every built-in and plugin segment.\n")
	b.WriteString("[::b]space[::-] toggles a segment on or off, [::b]↑/↓[::-] moves the selection,\n")
	b.WriteString("[::b]/[::-] filters the list, and [::b]o[::-] opens the selected segment's settings.\n")
	b.WriteString("Use [::b]1-9[::-] to assign a line, [::b]←/→[::-] to reorder within a line, and\n")
	b.WriteString("[::b]⇧↑/⇧↓[::-] to swap whole lines. Press [::b]v[::-] at any time to switch to\n")
	b.WriteString("the direct-manipulation (visual) editing mode. The live preview on the\n")
	b.WriteString("right is the REAL render — nothing is faked. Nothing touches disk until\n")
	fmt.Fprintf(&b, "you press s — changes save to [green]%s[-].\n", configPath())

	section := func(title, context string) {
		b.WriteString(fmt.Sprintf("\n[cyan::b]%s[-::-]\n", title))
		for _, kb := range keymap {
			if kb.Context != context {
				continue
			}
			b.WriteString(fmt.Sprintf("  [::b]%-12s[-:-:-] %s\n", kb.Keys, kb.Desc))
		}
	}
	section("Segment list", "main")
	section("Settings flyout", "flyout")

	b.WriteString("\n[cyan::b]Concepts[-::-]\n")
	b.WriteString("  [::b]themes[-:-:-]       6 built-in palettes; truecolor with automatic 256/16 fallback;\n")
	b.WriteString("               classic (alias: original) is the pre-1.0 default look — V shows\n")
	b.WriteString("               any theme against your real terminal\n")
	b.WriteString("  [::b]presets[-:-:-]      named layouts — applying one replaces segments, lines, settings\n")
	b.WriteString("  [::b]plugins[-:-:-]      executable segments defined in config.toml ([[plugins]])\n")
	b.WriteString("  [::b]projections[-:-:-]  →58% burn-rate forecasts appear after ~5 minutes of session\n")
	b.WriteString("               history (the preview here fakes that history so you can see them)\n")

	b.WriteString("\n[gray]r full README · q/esc close[-]\n")
	return b.String()
}
