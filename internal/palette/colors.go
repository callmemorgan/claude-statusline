package palette

// Palette holds the resolved escape string for every semantic role. An empty
// Palette (Rst == "") means colors are disabled. The unexported theme/depth
// fields let color-spec resolution (per-segment overrides, bar threshold
// colors) honor the active theme and terminal capability.
type Palette struct {
	Model   string
	Dir     string
	Git     string
	Chg     string
	Dur     string
	Cost    string
	Dim     string
	Rst     string
	ROK     string
	RWarn   string
	RCrit   string
	Agent   string
	Vim     string
	Purple  string // the "accent" role
	Session string
	Sep     string // separator color between segments

	Theme *Theme
	depth ColorDepth
}

// ─── Palette ─────────────────────────────────────────────────────────

// CurrentPalette resolves the configured theme at the detected (or
// configured) color depth. NO_COLOR / TERM=dumb yield an empty palette.
func CurrentPalette(themeID, colorDepth string, themeColors map[string]string) Palette {
	d := resolveDepth(colorDepth)
	if d == DepthNone {
		return Palette{}
	}
	t := ApplyThemeOverrides(ThemeByID(themeID), themeColors)
	return ResolvePalette(t, d)
}

// colorCycle is the ordered list of color names cycled by the TUI and offered
// in flyout color sub-features. Includes both the 8 standard and 8 bright
// variants to match the documented "Supported names" surface in the README.
var ColorCycle = []string{
	"default",
	"red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright-red", "bright-green", "bright-yellow", "bright-blue",
	"bright-magenta", "bright-cyan", "bright-white",
}

// colorCodes maps color names to ANSI escape codes.
var colorCodes = map[string]string{
	"default":        "",
	"red":            "\x1b[31m",
	"green":          "\x1b[32m",
	"yellow":         "\x1b[33m",
	"blue":           "\x1b[34m",
	"magenta":        "\x1b[35m",
	"cyan":           "\x1b[36m",
	"white":          "\x1b[37m",
	"bright-red":     "\x1b[91m",
	"bright-green":   "\x1b[92m",
	"bright-yellow":  "\x1b[93m",
	"bright-blue":    "\x1b[94m",
	"bright-magenta": "\x1b[95m",
	"bright-cyan":    "\x1b[96m",
	"bright-white":   "\x1b[97m",
}

// paletteWithOverride returns a copy of c with the named primary color field
// replaced by the resolved color spec (hex, 256-index, theme role, or legacy
// 16-color name).
func PaletteWithOverride(c Palette, primaryColor, colorName string) Palette {
	code, ok := ResolveColorSpec(colorName, c)
	if !ok || code == "" {
		return c
	}
	p := c
	switch primaryColor {
	case "Model":
		p.Model = code
	case "Dir":
		p.Dir = code
	case "Git":
		p.Git = code
	case "Chg":
		p.Chg = code
	case "Dur":
		p.Dur = code
	case "Cost":
		p.Cost = code
	case "Dim":
		p.Dim = code
	case "ROK":
		p.ROK = code
	case "RWarn":
		p.RWarn = code
	case "RCrit":
		p.RCrit = code
	case "Agent":
		p.Agent = code
	case "Vim":
		p.Vim = code
	case "Purple":
		p.Purple = code
	case "Session":
		p.Session = code
	}
	return p
}

// resolveColor maps a color spec to its escape code. Returns the palette's
// ok color if the spec is unknown or unset, so callers never have to handle
// the "no code found" case inline. When colors are disabled (NO_COLOR /
// TERM=dumb, signalled by an empty palette), it returns "" so settings-driven
// bar colors respect the disable too.
func ResolveColor(name string, c Palette) string {
	if c.Rst == "" {
		return ""
	}
	if code, ok := ResolveColorSpec(name, c); ok && code != "" {
		return code
	}
	return c.ROK
}
