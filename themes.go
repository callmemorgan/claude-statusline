package main

// ─── Themes ──────────────────────────────────────────────────────────
//
// A theme maps semantic roles to colors. Roles resolve to the palette struct
// renderers already consume, so themes slot in underneath without touching
// any render function. Every non-classic role carries a 24-bit hex value;
// 16-color terminals get a nearest-match fallback automatically.

import (
	"sort"
	"strconv"
	"strings"
)

type themeColor struct {
	Hex    string // "#cba6f7"; empty = ANSI-only role (classic theme)
	Ansi16 string // explicit escape-code fallback; computed from Hex if empty
}

type theme struct {
	ID    string
	Desc  string
	Roles map[string]themeColor
}

// themeRoles is the canonical role list (and the names accepted in the
// [theme_colors] config table and per-segment color overrides).
var themeRoles = []string{
	"model", "dir", "git", "changes", "duration", "cost", "dim",
	"ok", "warn", "crit", "agent", "vim", "accent", "session", "sep",
}

func ansiRole(code string) themeColor { return themeColor{Ansi16: code} }

// builtinThemes — classic reproduces the pre-1.0 hardcoded palette exactly
// and stays the default. Hex palettes follow each scheme's published spec.
var builtinThemes = []theme{
	{
		ID:   "classic",
		Desc: "The original pre-1.0 16-color look — the default (alias: original)",
		Roles: map[string]themeColor{
			"model":    ansiRole("\x1b[35m"),
			"dir":      ansiRole("\x1b[36m"),
			"git":      ansiRole("\x1b[32m"),
			"changes":  ansiRole("\x1b[33m"),
			"duration": ansiRole("\x1b[34m"),
			"cost":     ansiRole("\x1b[33m"),
			"dim":      ansiRole("\x1b[90m"),
			"ok":       ansiRole("\x1b[32m"),
			"warn":     ansiRole("\x1b[33m"),
			"crit":     ansiRole("\x1b[91m"),
			"agent":    ansiRole("\x1b[95m"),
			"vim":      ansiRole("\x1b[97m"),
			"accent":   ansiRole("\x1b[35m"),
			"session":  ansiRole("\x1b[96m"),
			// Classic separators are uncolored — the pre-1.0 renderer joined
			// segments with a plain " │ ", and classic stays byte-identical.
			"sep": ansiRole(""),
		},
	},
	{
		ID:   "catppuccin-mocha",
		Desc: "Soothing pastels on a dark base",
		Roles: map[string]themeColor{
			"model":    {Hex: "#cba6f7"}, // mauve
			"dir":      {Hex: "#89dceb"}, // sky
			"git":      {Hex: "#a6e3a1"}, // green
			"changes":  {Hex: "#fab387"}, // peach
			"duration": {Hex: "#89b4fa"}, // blue
			"cost":     {Hex: "#f9e2af"}, // yellow
			"dim":      {Hex: "#6c7086"}, // overlay0
			"ok":       {Hex: "#a6e3a1"},
			"warn":     {Hex: "#f9e2af"},
			"crit":     {Hex: "#f38ba8"}, // red
			"agent":    {Hex: "#f5c2e7"}, // pink
			"vim":      {Hex: "#cdd6f4"}, // text
			"accent":   {Hex: "#cba6f7"},
			"session":  {Hex: "#94e2d5"}, // teal
			"sep":      {Hex: "#45475a"}, // surface1
		},
	},
	{
		ID:   "nord",
		Desc: "Arctic, north-bluish palette",
		Roles: map[string]themeColor{
			"model":    {Hex: "#b48ead"},
			"dir":      {Hex: "#88c0d0"},
			"git":      {Hex: "#a3be8c"},
			"changes":  {Hex: "#ebcb8b"},
			"duration": {Hex: "#81a1c1"},
			"cost":     {Hex: "#ebcb8b"},
			"dim":      {Hex: "#4c566a"},
			"ok":       {Hex: "#a3be8c"},
			"warn":     {Hex: "#ebcb8b"},
			"crit":     {Hex: "#bf616a"},
			"agent":    {Hex: "#b48ead"},
			"vim":      {Hex: "#eceff4"},
			"accent":   {Hex: "#5e81ac"},
			"session":  {Hex: "#8fbcbb"},
			"sep":      {Hex: "#3b4252"},
		},
	},
	{
		ID:   "dracula",
		Desc: "Dark theme with vivid accents",
		Roles: map[string]themeColor{
			"model":    {Hex: "#ff79c6"},
			"dir":      {Hex: "#8be9fd"},
			"git":      {Hex: "#50fa7b"},
			"changes":  {Hex: "#ffb86c"},
			"duration": {Hex: "#bd93f9"},
			"cost":     {Hex: "#f1fa8c"},
			"dim":      {Hex: "#6272a4"},
			"ok":       {Hex: "#50fa7b"},
			"warn":     {Hex: "#f1fa8c"},
			"crit":     {Hex: "#ff5555"},
			"agent":    {Hex: "#bd93f9"},
			"vim":      {Hex: "#f8f8f2"},
			"accent":   {Hex: "#bd93f9"},
			"session":  {Hex: "#8be9fd"},
			"sep":      {Hex: "#44475a"},
		},
	},
	{
		ID:   "gruvbox-dark",
		Desc: "Retro groove, warm and dusty",
		Roles: map[string]themeColor{
			"model":    {Hex: "#d3869b"},
			"dir":      {Hex: "#83a598"},
			"git":      {Hex: "#b8bb26"},
			"changes":  {Hex: "#fe8019"},
			"duration": {Hex: "#83a598"},
			"cost":     {Hex: "#fabd2f"},
			"dim":      {Hex: "#928374"},
			"ok":       {Hex: "#b8bb26"},
			"warn":     {Hex: "#fabd2f"},
			"crit":     {Hex: "#fb4934"},
			"agent":    {Hex: "#d3869b"},
			"vim":      {Hex: "#ebdbb2"},
			"accent":   {Hex: "#8ec07c"},
			"session":  {Hex: "#fe8019"},
			"sep":      {Hex: "#665c54"},
		},
	},
	{
		ID:   "tokyo-night",
		Desc: "Neon-on-navy city lights",
		Roles: map[string]themeColor{
			"model":    {Hex: "#bb9af7"},
			"dir":      {Hex: "#7dcfff"},
			"git":      {Hex: "#9ece6a"},
			"changes":  {Hex: "#ff9e64"},
			"duration": {Hex: "#7aa2f7"},
			"cost":     {Hex: "#e0af68"},
			"dim":      {Hex: "#565f89"},
			"ok":       {Hex: "#9ece6a"},
			"warn":     {Hex: "#e0af68"},
			"crit":     {Hex: "#f7768e"},
			"agent":    {Hex: "#bb9af7"},
			"vim":      {Hex: "#c0caf5"},
			"accent":   {Hex: "#73daca"},
			"session":  {Hex: "#73daca"},
			"sep":      {Hex: "#3b4261"},
		},
	},
	{
		ID:   "newsprint",
		Desc: "Aged newsprint: warm greys and sepia on dark stock",
		Roles: map[string]themeColor{
			"model":    {Hex: "#f0e6d8"}, // bright headline
			"dir":      {Hex: "#c4b8a8"}, // body text
			"git":      {Hex: "#d6c4b0"}, // warm grey
			"changes":  {Hex: "#c4a884"}, // sepia accent
			"duration": {Hex: "#a8a090"}, // cool grey
			"cost":     {Hex: "#d8c8b8"}, // light warm grey
			"dim":      {Hex: "#706860"}, // dark ink
			"ok":       {Hex: "#a8a090"},
			"warn":     {Hex: "#c4a884"},
			"crit":     {Hex: "#f0e6d8"},
			"agent":    {Hex: "#c4b8a8"},
			"vim":      {Hex: "#f5efe6"}, // bright paper
			"accent":   {Hex: "#c4a884"},
			"session":  {Hex: "#b8aca0"},
			"sep":      {Hex: "#585048"},
		},
	},
}

func themeIDs() []string {
	ids := make([]string, len(builtinThemes))
	for i, t := range builtinThemes {
		ids[i] = t.ID
	}
	return ids
}

// themeByID returns the named theme, defaulting to classic. "original" is an
// accepted alias for classic — it is the pre-1.0 default palette, extracted
// unchanged into the theme system.
func themeByID(id string) theme {
	if id == "original" {
		id = "classic"
	}
	for _, t := range builtinThemes {
		if t.ID == id {
			return t
		}
	}
	return builtinThemes[0]
}

// applyThemeOverrides layers [theme_colors] role overrides on a copy of the
// theme. Values use the same grammar as resolveColorSpec minus role names.
func applyThemeOverrides(t theme, overrides map[string]string) theme {
	if len(overrides) == 0 {
		return t
	}
	roles := make(map[string]themeColor, len(t.Roles))
	for k, v := range t.Roles {
		roles[k] = v
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, role := range keys {
		spec := overrides[role]
		if _, known := roles[role]; !known {
			continue // validated elsewhere; ignore here
		}
		switch {
		case strings.HasPrefix(spec, "#"):
			roles[role] = themeColor{Hex: spec}
		default:
			if code, ok := colorCodes[spec]; ok && code != "" {
				roles[role] = themeColor{Ansi16: code}
			} else if idx, err := strconv.Atoi(spec); err == nil && idx >= 0 && idx <= 255 {
				r, g, b := index256ToRGB(idx)
				roles[role] = themeColor{Hex: hexFromRGB(r, g, b)}
			}
		}
	}
	t.Roles = roles
	return t
}

func hexFromRGB(r, g, b int) string {
	const digits = "0123456789abcdef"
	return string([]byte{'#',
		digits[r>>4], digits[r&15],
		digits[g>>4], digits[g&15],
		digits[b>>4], digits[b&15],
	})
}

// roleEscape renders one theme color at a depth.
func roleEscape(tc themeColor, d colorDepth) string {
	if d == depthNone {
		return ""
	}
	if tc.Hex == "" {
		return tc.Ansi16
	}
	if d == depth16 {
		if tc.Ansi16 != "" {
			return tc.Ansi16
		}
		if r, g, b, ok := parseHexRGB(tc.Hex); ok {
			return nearestAnsi16(r, g, b)
		}
		return ""
	}
	esc, _ := hexEscape(tc.Hex, d)
	return esc
}

// resolvePalette renders a theme into the palette struct consumed by every
// renderer. The palette also remembers its theme and depth so per-segment
// color overrides can resolve hex/256/role specs later.
func resolvePalette(t theme, d colorDepth) palette {
	if d == depthNone {
		return palette{}
	}
	esc := func(role string) string { return roleEscape(t.Roles[role], d) }
	return palette{
		Model:   esc("model"),
		Dir:     esc("dir"),
		Git:     esc("git"),
		Chg:     esc("changes"),
		Dur:     esc("duration"),
		Cost:    esc("cost"),
		Dim:     esc("dim"),
		Rst:     "\x1b[0m",
		ROK:     esc("ok"),
		RWarn:   esc("warn"),
		RCrit:   esc("crit"),
		Agent:   esc("agent"),
		Vim:     esc("vim"),
		Purple:  esc("accent"),
		Session: esc("session"),
		Sep:     esc("sep"),
		theme:   &t,
		depth:   d,
	}
}

// validColorSpec reports whether a user-supplied color spec is syntactically
// acceptable: hex, 256 index, theme role name, legacy color name, or default.
func validColorSpec(spec string) bool {
	if spec == "" || spec == "default" {
		return true
	}
	if strings.HasPrefix(spec, "#") {
		_, _, _, ok := parseHexRGB(spec)
		return ok
	}
	if _, ok := colorCodes[spec]; ok {
		return true
	}
	for _, r := range themeRoles {
		if r == spec {
			return true
		}
	}
	if idx, err := strconv.Atoi(spec); err == nil && idx >= 0 && idx <= 255 {
		return true
	}
	return false
}

// resolveColorSpec turns a user-supplied color spec into an escape code.
// Accepted forms, in priority order:
//
//	"#rrggbb"            hex, quantized to the palette's depth
//	"123"                xterm-256 index (degraded on 16-color terminals)
//	"accent", "dim", ... theme role names
//	"magenta", ...       legacy 16-color names
//	"" / "default"       no override (ok=false)
func resolveColorSpec(spec string, c palette) (string, bool) {
	if spec == "" || spec == "default" {
		return "", false
	}
	if c.Rst == "" {
		return "", false // colors disabled
	}
	d := c.depth
	if d == depthNone {
		d = depth16
	}
	if strings.HasPrefix(spec, "#") {
		if esc, ok := hexEscape(spec, d); ok {
			return esc, true
		}
		return "", false
	}
	if code, ok := colorCodes[spec]; ok && code != "" {
		return code, true
	}
	if c.theme != nil {
		if tc, ok := c.theme.Roles[spec]; ok {
			return roleEscape(tc, d), true
		}
	}
	if idx, err := strconv.Atoi(spec); err == nil && idx >= 0 && idx <= 255 {
		if d == depth16 {
			r, g, b := index256ToRGB(idx)
			return nearestAnsi16(r, g, b), true
		}
		return "\x1b[38;5;" + strconv.Itoa(idx) + "m", true
	}
	return "", false
}
