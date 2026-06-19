package config

// ─── Layout Presets ──────────────────────────────────────────────────
//
// Named segment layouts applied from the TUI (p) or via `preset = "..."` in
// config.toml (used only when `segments` is absent). A preset's theme is a
// suggestion: it applies only when the user hasn't chosen a theme.

type LayoutPreset struct {
	ID       string
	Desc     string
	Segments []string
	Lines    map[string]int
	Settings map[string]map[string]any
	Theme    string
}

var LayoutPresets = []LayoutPreset{
	{
		ID:       "classic",
		Desc:     "The default layout — everything on three lines",
		Segments: DefaultConfig().Segments,
	},
	{
		ID:       "minimal",
		Desc:     "Directory, branch, model — one quiet line",
		Segments: []string{"directory", "git-branch", "model"},
		Lines:    map[string]int{"model": 1},
	},
	{
		ID:       "zen",
		Desc:     "Model and a bar-less context percentage",
		Segments: []string{"model", "context-window"},
		Lines:    map[string]int{"model": 1, "context-window": 1},
		Settings: map[string]map[string]any{"context-window": {"show_bar": false}},
		Theme:    "nord",
	},
	{
		ID:       "cost-tracker",
		Desc:     "Spend, burn rate, and quota bars with projections",
		Segments: []string{"directory", "cost", "cost-rate", "tokens", "rate-limit-5h", "rate-limit-7d"},
		Lines:    map[string]int{"cost": 1, "cost-rate": 1, "tokens": 1, "rate-limit-5h": 2, "rate-limit-7d": 2},
		Settings: map[string]map[string]any{
			"rate-limit-5h": {"bar_width": 30},
			"rate-limit-7d": {"bar_width": 30},
		},
		Theme: "gruvbox-dark",
	},
	{
		ID:       "git-focus",
		Desc:     "Branch with dirty/ahead/behind, diff stats, context",
		Segments: []string{"directory", "git-branch", "lines-changed", "model", "context-window"},
		Lines:    map[string]int{"context-window": 2},
		Settings: map[string]map[string]any{"git-branch": {"git_status": true}},
		Theme:    "nord",
	},
	{
		ID:       "vim-coder",
		Desc:     "Vim mode up front, the essentials behind it",
		Segments: []string{"vim-mode", "directory", "git-branch", "model", "context-window"},
		Lines:    map[string]int{"context-window": 2},
		Theme:    "gruvbox-dark",
	},
	{
		ID:       "quota-watch",
		Desc:     "Wide smooth bars for context and both rate limits",
		Segments: []string{"model", "context-window", "rate-limit-5h", "rate-limit-7d"},
		Lines:    map[string]int{"model": 1, "context-window": 1, "rate-limit-5h": 2, "rate-limit-7d": 2},
		Settings: map[string]map[string]any{
			"context-window": {"bar_width": 30, "iconset": "smooth"},
			"rate-limit-5h":  {"bar_width": 30, "iconset": "smooth"},
			"rate-limit-7d":  {"bar_width": 30, "iconset": "smooth"},
		},
		Theme: "tokyo-night",
	},
	{
		ID:   "full-dashboard",
		Desc: "Everything, including burn rates and rich git status",
		Segments: []string{
			"vim-mode", "session-name", "directory", "added-dirs", "git-branch",
			"lines-changed", "cache-percent", "cost", "cost-rate",
			"model", "output-style", "version", "duration", "api-efficiency", "tokens",
			"context-window", "rate-limit-5h", "rate-limit-7d",
		},
		Lines: map[string]int{"cost-rate": 1},
		Settings: map[string]map[string]any{
			"git-branch":     {"git_status": true},
			"context-window": {"iconset": "blocks"},
			"rate-limit-5h":  {"iconset": "blocks"},
			"rate-limit-7d":  {"iconset": "blocks"},
		},
		Theme: "catppuccin-mocha",
	},
}

func PresetByID(id string) (LayoutPreset, bool) {
	for _, p := range LayoutPresets {
		if p.ID == id {
			return p, true
		}
	}
	return LayoutPreset{}, false
}

// ApplyPreset replaces the layout-shaped parts of cfg (segments, lines,
// per-segment settings) with the preset's, deep-copied so later edits never
// mutate the preset data. Colors and plugins are kept; the preset theme
// applies only when the user hasn't set one.
func ApplyPreset(cfg *Config, p LayoutPreset) {
	cfg.Segments = append([]string(nil), p.Segments...)
	cfg.Lines = nil
	if len(p.Lines) > 0 {
		cfg.Lines = make(map[string]int, len(p.Lines))
		for k, v := range p.Lines {
			cfg.Lines[k] = v
		}
	}
	cfg.Settings = nil
	if len(p.Settings) > 0 {
		cfg.Settings = make(map[string]map[string]any, len(p.Settings))
		for id, vals := range p.Settings {
			m := make(map[string]any, len(vals))
			for k, v := range vals {
				m[k] = v
			}
			cfg.Settings[id] = m
		}
	}
	if cfg.Theme == "" && p.Theme != "" {
		cfg.Theme = p.Theme
	}
	// Re-append plugin segments so a preset doesn't silently drop them.
	inSegments := make(map[string]bool, len(cfg.Segments))
	for _, id := range cfg.Segments {
		inSegments[id] = true
	}
	for _, pl := range cfg.Plugins {
		if len(pl.Fields) > 0 {
			for _, f := range pl.Fields {
				if f.ID != "" && !inSegments[f.ID] {
					cfg.Segments = append(cfg.Segments, f.ID)
					inSegments[f.ID] = true
				}
			}
		} else if pl.ID != "" && !inSegments[pl.ID] {
			cfg.Segments = append(cfg.Segments, pl.ID)
		}
	}
}

// CloneConfig deep-copies a Config so the TUI can snapshot and restore it.
func CloneConfig(c Config) Config {
	out := c
	out.Segments = append([]string(nil), c.Segments...)
	if c.Lines != nil {
		out.Lines = make(map[string]int, len(c.Lines))
		for k, v := range c.Lines {
			out.Lines[k] = v
		}
	}
	if c.Colors != nil {
		out.Colors = make(map[string]string, len(c.Colors))
		for k, v := range c.Colors {
			out.Colors[k] = v
		}
	}
	if c.ThemeColors != nil {
		out.ThemeColors = make(map[string]string, len(c.ThemeColors))
		for k, v := range c.ThemeColors {
			out.ThemeColors[k] = v
		}
	}
	if c.Settings != nil {
		out.Settings = make(map[string]map[string]any, len(c.Settings))
		for id, vals := range c.Settings {
			m := make(map[string]any, len(vals))
			for k, v := range vals {
				m[k] = v
			}
			out.Settings[id] = m
		}
	}
	out.Plugins = append([]PluginDef(nil), c.Plugins...)
	return out
}
