package main

import "os"

type palette struct {
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
	Purple  string
	Session string
}

// ─── Palette ─────────────────────────────────────────────────────────

func currentPalette() palette {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return palette{}
	}
	return palette{
		Model:   "\x1b[35m",
		Dir:     "\x1b[36m",
		Git:     "\x1b[32m",
		Chg:     "\x1b[33m",
		Dur:     "\x1b[34m",
		Cost:    "\x1b[33m",
		Dim:     "\x1b[90m",
		Rst:     "\x1b[0m",
		ROK:     "\x1b[32m",
		RWarn:   "\x1b[33m",
		RCrit:   "\x1b[91m",
		Agent:   "\x1b[95m",
		Vim:     "\x1b[97m",
		Purple:  "\x1b[35m",
		Session: "\x1b[96m",
	}
}

// colorCycle is the ordered list of color names cycled by the TUI and offered
// in flyout color sub-features. Includes both the 8 standard and 8 bright
// variants to match the documented "Supported names" surface in the README.
var colorCycle = []string{
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
// replaced by the ANSI code for colorName.
func paletteWithOverride(c palette, primaryColor, colorName string) palette {
	code := colorCodes[colorName]
	if code == "" {
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

// pickColor resolves a per-segment color override against the segment's natural
// color for this threshold state. An empty string or "default" both mean
// "use the natural color" — the same no-op semantics as paletteWithOverride.
func pickColor(override *string, natural string) string {
	if override == nil || *override == "" || *override == "default" {
		return natural
	}
	return *override
}

// resolveColor maps a color name to its ANSI escape code. Returns the palette's
// ok color if the name is unknown or unset, so callers never have to handle the
// "no code found" case inline. When colors are disabled (NO_COLOR / TERM=dumb,
// signalled by an empty palette), it returns "" so settings-driven bar colors
// respect the disable too.
func resolveColor(name string, c palette) string {
	if c.Rst == "" {
		return ""
	}
	if code := colorCodes[name]; code != "" {
		return code
	}
	return c.ROK
}
