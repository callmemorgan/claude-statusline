package main

// ─── Keymap ──────────────────────────────────────────────────────────
//
// Single source of truth for TUI keybindings: the footer bar and the help
// overlay are both generated from this table, so they can never drift from
// the actual handlers.

import "strings"

type keyBinding struct {
	Keys    string // display form, e.g. "space"
	Action  string // short label for the footer
	Desc    string // longer description for the help overlay
	Context string // "main" | "flyout" | "picker"
	Footer  bool   // include in the one-line footer (space is tight)
}

var keymap = []keyBinding{
	{Keys: "←↑↓→", Action: "cursor", Desc: "Move the cursor through the rendered segments and across lines, right on the preview", Context: "main", Footer: true},
	{Keys: "space", Action: "toggle", Desc: "Toggle the segment under the cursor on or off (off removes it from the render)", Context: "main", Footer: true},
	{Keys: "m", Action: "move", Desc: "Grab the cursor's segment and relocate it in real space — arrows move it across slots and lines, enter drops it, esc cancels", Context: "main", Footer: true},
	{Keys: "a", Action: "add", Desc: "Open the palette of off segments and insert one at the cursor", Context: "main", Footer: true},
	{Keys: "c", Action: "color", Desc: "Cycle the cursor segment's color", Context: "main", Footer: true},
	{Keys: "C", Action: "palette", Desc: "Open the color picker for the cursor's segment (theme roles, ANSI, hex, recent)", Context: "main", Footer: true},
	{Keys: "o", Action: "options", Desc: "Open the cursor segment's settings flyout", Context: "main", Footer: true},
	{Keys: "t", Action: "theme", Desc: "Pick a color theme with live preview", Context: "main", Footer: true},
	{Keys: "p", Action: "presets", Desc: "Apply a named layout preset with live preview", Context: "main", Footer: true},
	{Keys: "w", Action: "width", Desc: "Cycle the preview width (auto/80/60/40) to check the layout", Context: "main", Footer: true},
	{Keys: "d", Action: "demo", Desc: "Animate the whole preview: bars sweep, countdowns tick, cost grows (session-only)", Context: "main", Footer: true},
	{Keys: "v", Action: "view", Desc: "Hide the TUI and render the statusline directly in your terminal, to check the theme against its real colors and background", Context: "main", Footer: true},
	{Keys: "r", Action: "reset", Desc: "Reset the configuration to defaults (asks first)", Context: "main", Footer: true},
	{Keys: "s", Action: "save", Desc: "Save to config.toml and keep editing", Context: "main", Footer: true},
	{Keys: "q", Action: "quit", Desc: "Quit (asks if there are unsaved changes)", Context: "main", Footer: true},
	{Keys: "?", Action: "help", Desc: "Show help", Context: "main", Footer: true},

	{Keys: "↑↓/type", Action: "find", Desc: "In the add palette: type to filter, ↑/↓ to choose", Context: "palette", Footer: true},
	{Keys: "enter", Action: "insert", Desc: "Insert the chosen segment at the cursor", Context: "palette", Footer: true},
	{Keys: "q/esc", Action: "close", Desc: "Close the palette without adding", Context: "palette", Footer: true},

	{Keys: "space/enter", Action: "toggle/cycle", Desc: "Toggle a switch or cycle an option", Context: "flyout", Footer: true},
	{Keys: "←/→", Action: "adjust", Desc: "Decrease / increase a numeric setting", Context: "flyout", Footer: true},
	{Keys: "⇧←/→", Action: "coarse", Desc: "Adjust a numeric setting in larger steps", Context: "flyout", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move between settings", Context: "flyout", Footer: true},
	{Keys: "q/esc", Action: "close", Desc: "Close the flyout", Context: "flyout", Footer: true},
}

// footerText renders the footer hint line for a context.
func footerText(context string) string {
	var parts []string
	for _, kb := range keymap {
		if kb.Context == context && kb.Footer {
			parts = append(parts, kb.Keys+" "+kb.Action)
		}
	}
	return " " + strings.Join(parts, " • ")
}
