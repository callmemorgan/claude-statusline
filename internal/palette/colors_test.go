package palette

import (
	"strings"
	"testing"
)

func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"NO_COLOR", "TERM", "COLORTERM", "TERM_PROGRAM", "WT_SESSION"} {
		t.Setenv(k, "")
	}
}

func TestDetectDepth(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want ColorDepth
	}{
		{"no_color", map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, DepthNone},
		{"dumb", map[string]string{"TERM": "dumb"}, DepthNone},
		{"colorterm", map[string]string{"COLORTERM": "truecolor", "TERM": "xterm"}, DepthTrue},
		{"iterm", map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM": "xterm"}, DepthTrue},
		{"256", map[string]string{"TERM": "xterm-256color"}, Depth256},
		{"plain", map[string]string{"TERM": "xterm"}, Depth16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearColorEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := detectDepth(); got != tc.want {
				t.Errorf("detectDepth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveDepthOverride(t *testing.T) {
	clearColorEnv(t)
	t.Setenv("TERM", "xterm")
	if got := resolveDepth("truecolor"); got != DepthTrue {
		t.Errorf("override truecolor = %v", got)
	}
	if got := resolveDepth("none"); got != DepthNone {
		t.Errorf("override none = %v", got)
	}
	if got := resolveDepth(""); got != Depth16 {
		t.Errorf("auto on plain xterm = %v", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := resolveDepth("truecolor"); got != DepthNone {
		t.Errorf("NO_COLOR must beat config override, got %v", got)
	}
}

func TestParseHexRGB(t *testing.T) {
	if r, g, b, ok := parseHexRGB("#cba6f7"); !ok || r != 0xcb || g != 0xa6 || b != 0xf7 {
		t.Errorf("parseHexRGB = %d,%d,%d,%v", r, g, b, ok)
	}
	for _, bad := range []string{"", "#fff", "#zzzzzz", "12345", "#1234567"} {
		if _, _, _, ok := parseHexRGB(bad); ok {
			t.Errorf("parseHexRGB(%q) should fail", bad)
		}
	}
}

func TestRgbTo256(t *testing.T) {
	cases := []struct {
		r, g, b, want int
	}{
		{0, 0, 0, 16},        // cube black
		{255, 255, 255, 231}, // cube white
		{255, 0, 0, 196},     // pure red
		{0, 215, 0, 40},      // cube green level 3
		{128, 128, 128, 244}, // mid gray → gray ramp
	}
	for _, tc := range cases {
		if got := rgbTo256(tc.r, tc.g, tc.b); got != tc.want {
			t.Errorf("rgbTo256(%d,%d,%d) = %d, want %d", tc.r, tc.g, tc.b, got, tc.want)
		}
	}
}

func TestHexEscapeDepths(t *testing.T) {
	if esc, ok := hexEscape("#ff0000", DepthTrue); !ok || esc != "\x1b[38;2;255;0;0m" {
		t.Errorf("truecolor escape = %q %v", esc, ok)
	}
	if esc, ok := hexEscape("#ff0000", Depth256); !ok || esc != "\x1b[38;5;196m" {
		t.Errorf("256 escape = %q %v", esc, ok)
	}
	if esc, ok := hexEscape("#ff0000", Depth16); !ok || esc != colorCodes["bright-red"] {
		t.Errorf("16-color quantization = %q %v", esc, ok)
	}
	if esc, ok := hexEscape("#ff0000", DepthNone); !ok || esc != "" {
		t.Errorf("none depth should be empty, got %q", esc)
	}
}

// TestClassicThemeMatchesLegacyPalette is the back-compat gate: the default
// theme must reproduce the pre-1.0 hardcoded escape codes exactly.
func TestClassicThemeMatchesLegacyPalette(t *testing.T) {
	for _, d := range []ColorDepth{Depth16, Depth256, DepthTrue} {
		p := ResolvePalette(ThemeByID("classic"), d)
		want := map[string]string{
			"Model": "\x1b[35m", "Dir": "\x1b[36m", "Git": "\x1b[32m",
			"Chg": "\x1b[33m", "Dur": "\x1b[34m", "Cost": "\x1b[33m",
			"Dim": "\x1b[90m", "Rst": "\x1b[0m", "ROK": "\x1b[32m",
			"RWarn": "\x1b[33m", "RCrit": "\x1b[91m", "Agent": "\x1b[95m",
			"Vim": "\x1b[97m", "Purple": "\x1b[35m", "Session": "\x1b[96m",
			// Classic separators are uncolored, matching the pre-1.0 joiner.
			"Sep": "",
		}
		got := map[string]string{
			"Model": p.Model, "Dir": p.Dir, "Git": p.Git, "Chg": p.Chg,
			"Dur": p.Dur, "Cost": p.Cost, "Dim": p.Dim, "Rst": p.Rst,
			"ROK": p.ROK, "RWarn": p.RWarn, "RCrit": p.RCrit,
			"Agent": p.Agent, "Vim": p.Vim, "Purple": p.Purple,
			"Session": p.Session, "Sep": p.Sep,
		}
		for k, w := range want {
			if got[k] != w {
				t.Errorf("classic@depth%d %s = %q, want %q", d, k, got[k], w)
			}
		}
	}
}

// "original" is an accepted alias for classic, for people who know the
// pre-1.0 palette by that name.
func TestOriginalThemeAlias(t *testing.T) {
	if got := ThemeByID("original").ID; got != "classic" {
		t.Errorf(`themeByID("original").ID = %q, want "classic"`, got)
	}
}

func TestThemedPaletteDepths(t *testing.T) {
	nord := ThemeByID("nord")
	pTrue := ResolvePalette(nord, DepthTrue)
	if pTrue.Git != "\x1b[38;2;163;190;140m" { // #a3be8c
		t.Errorf("nord truecolor git = %q", pTrue.Git)
	}
	p256 := ResolvePalette(nord, Depth256)
	if !strings.HasPrefix(p256.Git, "\x1b[38;5;") {
		t.Errorf("nord 256 git = %q", p256.Git)
	}
	p16 := ResolvePalette(nord, Depth16)
	if !strings.HasPrefix(p16.Git, "\x1b[") || strings.Contains(p16.Git, ";") {
		t.Errorf("nord 16-color git should be a basic escape, got %q", p16.Git)
	}
	if p := ResolvePalette(nord, DepthNone); p.Rst != "" {
		t.Error("depthNone must yield an empty palette")
	}
}

func TestResolveColorSpec(t *testing.T) {
	p := ResolvePalette(ThemeByID("nord"), DepthTrue)
	cases := []struct {
		spec string
		want string
		ok   bool
	}{
		{"", "", false},
		{"default", "", false},
		{"#ff0000", "\x1b[38;2;255;0;0m", true},
		{"196", "\x1b[38;5;196m", true},
		{"magenta", "\x1b[35m", true},
		{"accent", "\x1b[38;2;94;129;172m", true}, // nord #5e81ac
		{"bogus", "", false},
		{"999", "", false},
	}
	for _, tc := range cases {
		got, ok := ResolveColorSpec(tc.spec, p)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveColorSpec(%q) = %q,%v want %q,%v", tc.spec, got, ok, tc.want, tc.ok)
		}
	}
	// Disabled palette resolves nothing.
	if _, ok := ResolveColorSpec("#ff0000", Palette{}); ok {
		t.Error("disabled palette must not resolve specs")
	}
}

func TestApplyThemeOverrides(t *testing.T) {
	tm := ApplyThemeOverrides(ThemeByID("nord"), map[string]string{
		"git":   "#112233",
		"cost":  "yellow",
		"dim":   "245",
		"bogus": "#ffffff", // unknown role ignored
	})
	if tm.Roles["git"].Hex != "#112233" {
		t.Errorf("hex override not applied: %+v", tm.Roles["git"])
	}
	if tm.Roles["cost"].Ansi16 != colorCodes["yellow"] || tm.Roles["cost"].Hex != "" {
		t.Errorf("name override not applied: %+v", tm.Roles["cost"])
	}
	if tm.Roles["dim"].Hex == "" {
		t.Errorf("256-index override should set hex: %+v", tm.Roles["dim"])
	}
	if _, ok := tm.Roles["bogus"]; ok {
		t.Error("unknown role must not be added")
	}
	// Original untouched.
	if ThemeByID("nord").Roles["git"].Hex != "#a3be8c" {
		t.Error("applyThemeOverrides mutated the builtin theme")
	}
}

func TestCurrentPaletteConfig(t *testing.T) {
	clearColorEnv(t)
	t.Setenv("TERM", "xterm")
	p := CurrentPalette("dracula", "truecolor", nil)
	if p.RCrit != "\x1b[38;2;255;85;85m" { // #ff5555
		t.Errorf("dracula crit = %q", p.RCrit)
	}
	if p := CurrentPalette("", "none", nil); p.Rst != "" {
		t.Error("color_depth none should disable colors")
	}
}
