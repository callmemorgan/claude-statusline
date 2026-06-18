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
	Context string // "list" | "canvas" | "flyout" | "picker"
	Footer  bool   // include in the one-line footer (space is tight)
}

var keymap = []keyBinding{
	// List UI (default `configure`).
	{Keys: "space", Action: "toggle", Desc: "Enable or disable the selected segment", Context: "list", Footer: true},
	{Keys: "1-9", Action: "line", Desc: "Move the segment to line N (toggles back to its natural line)", Context: "list", Footer: true},
	{Keys: "c", Action: "color", Desc: "Cycle the segment's color", Context: "list", Footer: true},
	{Keys: "C", Action: "palette", Desc: "Open the color picker (theme roles, ANSI, hex, recent)", Context: "list", Footer: true},
	{Keys: "←/→", Action: "reorder", Desc: "Reorder the segment within its line", Context: "list", Footer: true},
	{Keys: "⇧↑/↓", Action: "move row", Desc: "Swap every segment on this line with the adjacent line", Context: "list", Footer: true},
	{Keys: "o", Action: "options", Desc: "Open the segment's settings flyout", Context: "list", Footer: true},
	{Keys: "t", Action: "theme", Desc: "Pick a color theme with live preview", Context: "list", Footer: true},
	{Keys: "p", Action: "presets", Desc: "Apply a named layout preset with live preview", Context: "list", Footer: true},
	{Keys: "/", Action: "find", Desc: "Filter the segment list (enter keeps it, esc clears it)", Context: "list", Footer: true},
	{Keys: "w", Action: "width", Desc: "Cycle the preview width (auto/80/60/40) to check the layout", Context: "list", Footer: true},
	{Keys: "d", Action: "demo", Desc: "Animate the whole preview: bars sweep, countdowns tick, cost grows (session-only)", Context: "list", Footer: true},
	{Keys: "v", Action: "view", Desc: "Hide the TUI and render the statusline directly in your terminal, to check the theme against its real colors and background", Context: "list", Footer: true},
	{Keys: "r", Action: "reset", Desc: "Reset the configuration to defaults (asks first)", Context: "list", Footer: true},
	{Keys: "s", Action: "save", Desc: "Save to config.toml and keep editing", Context: "list", Footer: true},
	{Keys: "q", Action: "quit", Desc: "Quit (asks if there are unsaved changes)", Context: "list", Footer: true},
	{Keys: "?", Action: "help", Desc: "Show help", Context: "list", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move the selection", Context: "list"},

	// Canvas UI (`configure --drawer`).
	{Keys: "space/enter", Action: "move on/off", Desc: "Move the focused segment between the drawer (off) and the canvas (on)", Context: "canvas", Footer: true},
	{Keys: "tab", Action: "switch pane", Desc: "Switch focus between the drawer (available) and the canvas (layout)", Context: "canvas", Footer: true},
	{Keys: "←/→", Action: "pane / move", Desc: "Switch panes (← drawer, → canvas); while a canvas segment is grabbed, reorder it within its line", Context: "canvas", Footer: true},
	{Keys: "g", Action: "grab/drop", Desc: "Grab the focused canvas segment to reposition it, then use the arrows; press g again or enter/esc to drop", Context: "canvas", Footer: true},
	{Keys: "⇧↑/↓", Action: "(grabbed) line", Desc: "While grabbed, move the segment to the adjacent render line", Context: "canvas"},
	{Keys: "1-9", Action: "line", Desc: "Send the focused segment to line N (toggles back to its natural line); enables it if off", Context: "canvas", Footer: true},
	{Keys: "c", Action: "color", Desc: "Cycle the segment's color", Context: "canvas", Footer: true},
	{Keys: "C", Action: "palette", Desc: "Open the color picker (theme roles, ANSI, hex, recent)", Context: "canvas", Footer: true},
	{Keys: "o", Action: "options", Desc: "Open the segment's settings flyout", Context: "canvas", Footer: true},
	{Keys: "t", Action: "theme", Desc: "Pick a color theme with live preview", Context: "canvas", Footer: true},
	{Keys: "p", Action: "presets", Desc: "Apply a named layout preset with live preview", Context: "canvas", Footer: true},
	{Keys: "/", Action: "find", Desc: "Filter the drawer's available segments (enter keeps it, esc clears it)", Context: "canvas", Footer: true},
	{Keys: "w", Action: "width", Desc: "Cycle the preview width (auto/80/60/40) to check the layout", Context: "canvas", Footer: true},
	{Keys: "d", Action: "demo", Desc: "Animate the whole preview: bars sweep, countdowns tick, cost grows (session-only)", Context: "canvas", Footer: true},
	{Keys: "v", Action: "view", Desc: "Hide the TUI and render the statusline directly in your terminal, to check the theme against its real colors and background", Context: "canvas", Footer: true},
	{Keys: "r", Action: "reset", Desc: "Reset the configuration to defaults (asks first)", Context: "canvas", Footer: true},
	{Keys: "s", Action: "save", Desc: "Save to config.toml and keep editing", Context: "canvas", Footer: true},
	{Keys: "q", Action: "quit", Desc: "Quit (asks if there are unsaved changes)", Context: "canvas", Footer: true},
	{Keys: "?", Action: "help", Desc: "Show help", Context: "canvas", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move the cursor within the focused pane (the canvas skips line headers)", Context: "canvas"},

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
