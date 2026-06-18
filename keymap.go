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
	{Keys: "space", Action: "toggle", Desc: "Enable or disable the selected segment", Context: "main", Footer: true},
	{Keys: "1-9", Action: "line", Desc: "Move the segment to line N (toggles back to its natural line)", Context: "main", Footer: true},
	{Keys: "c", Action: "color", Desc: "Cycle the segment's color", Context: "main", Footer: true},
	{Keys: "C", Action: "palette", Desc: "Open the color picker (theme roles, ANSI, hex, recent)", Context: "main", Footer: true},
	{Keys: "←/→", Action: "reorder", Desc: "Reorder the segment within its line", Context: "main", Footer: true},
	{Keys: "⇧↑/↓", Action: "move row", Desc: "Swap every segment on this line with the adjacent line", Context: "main", Footer: true},
	{Keys: "o", Action: "options", Desc: "Open the segment's settings flyout", Context: "main", Footer: true},
	{Keys: "t", Action: "theme", Desc: "Pick a color theme with live preview", Context: "main", Footer: true},
	{Keys: "p", Action: "presets", Desc: "Apply a named layout preset with live preview", Context: "main", Footer: true},
	{Keys: "L", Action: "auto-layout", Desc: "Open the priority+budget auto-layout solver: rank segments, set a budget, the solver packs/demotes/drops to fit", Context: "main", Footer: true},
	{Keys: "/", Action: "find", Desc: "Filter the segment list (enter keeps it, esc clears it)", Context: "main", Footer: true},
	{Keys: "w", Action: "width", Desc: "Cycle the preview width (auto/80/60/40) to check the layout", Context: "main", Footer: true},
	{Keys: "d", Action: "demo", Desc: "Animate the whole preview: bars sweep, countdowns tick, cost grows (session-only)", Context: "main", Footer: true},
	{Keys: "v", Action: "view", Desc: "Hide the TUI and render the statusline directly in your terminal, to check the theme against its real colors and background", Context: "main", Footer: true},
	{Keys: "r", Action: "reset", Desc: "Reset the configuration to defaults (asks first)", Context: "main", Footer: true},
	{Keys: "s", Action: "save", Desc: "Save to config.toml and keep editing", Context: "main", Footer: true},
	{Keys: "q", Action: "quit", Desc: "Quit (asks if there are unsaved changes)", Context: "main", Footer: true},
	{Keys: "?", Action: "help", Desc: "Show help", Context: "main", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move the selection", Context: "main"},

	{Keys: "space/enter", Action: "toggle/cycle", Desc: "Toggle a switch or cycle an option", Context: "flyout", Footer: true},
	{Keys: "←/→", Action: "adjust", Desc: "Decrease / increase a numeric setting", Context: "flyout", Footer: true},
	{Keys: "⇧←/→", Action: "coarse", Desc: "Adjust a numeric setting in larger steps", Context: "flyout", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move between settings", Context: "flyout", Footer: true},
	{Keys: "q/esc", Action: "close", Desc: "Close the flyout", Context: "flyout", Footer: true},

	{Keys: "⇧↑/↓", Action: "rank", Desc: "Move the selected segment up / down the priority ranking", Context: "autolayout", Footer: true},
	{Keys: "j/k", Action: "rank (vi)", Desc: "Move the selected segment down / up the priority ranking (vi-style aliases)", Context: "autolayout", Footer: true},
	{Keys: "tab", Action: "panes", Desc: "Switch focus between the priority list and the budget knobs", Context: "autolayout", Footer: true},
	{Keys: "b", Action: "budget pane", Desc: "Move focus to the budget pane", Context: "autolayout", Footer: true},
	{Keys: "←/→", Action: "budget", Desc: "Adjust the focused budget knob (width / max lines / density)", Context: "autolayout", Footer: true},
	{Keys: "enter/a", Action: "apply", Desc: "Apply the packed layout to the config (concrete Segments/Lines/Reflow)", Context: "autolayout", Footer: true},
	{Keys: "q/esc", Action: "cancel", Desc: "Close without applying", Context: "autolayout", Footer: true},
	{Keys: "↑/↓", Action: "nav", Desc: "Move the selection", Context: "autolayout"},
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
