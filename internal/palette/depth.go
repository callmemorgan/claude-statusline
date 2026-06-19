package palette

// ─── Color Depth ─────────────────────────────────────────────────────
//
// Terminal color capability detection and quantization. Themes store 24-bit
// hex values; depending on the detected (or configured) depth they render as
// truecolor escapes, the nearest xterm-256 index, or a 16-color fallback.

import (
	"fmt"
	"os"
	"strings"
)

type ColorDepth int

const (
	DepthNone ColorDepth = iota // NO_COLOR, TERM=dumb
	Depth16
	Depth256
	DepthTrue
)

// detectDepth sniffs terminal color capability from the environment. Claude
// Code may strip COLORTERM from the statusline subprocess env, so known
// truecolor terminal programs are also checked; the color_depth config key
// overrides all of this.
func detectDepth() ColorDepth {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return DepthNone
	}
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if ct == "truecolor" || ct == "24bit" {
		return DepthTrue
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "ghostty", "WezTerm", "vscode":
		return DepthTrue
	}
	if os.Getenv("WT_SESSION") != "" {
		return DepthTrue
	}
	if strings.Contains(os.Getenv("TERM"), "256color") {
		return Depth256
	}
	return Depth16
}

// resolveDepth applies the config override ("auto" | "truecolor" | "256" |
// "16" | "none") on top of detection.
func resolveDepth(override string) ColorDepth {
	switch strings.ToLower(override) {
	case "none":
		return DepthNone
	case "16":
		return Depth16
	case "256":
		return Depth256
	case "truecolor", "24bit":
		// NO_COLOR still wins — it is an explicit user-wide opt-out.
		if os.Getenv("NO_COLOR") != "" {
			return DepthNone
		}
		return DepthTrue
	}
	return detectDepth()
}

// parseHexRGB parses "#rrggbb" (or "rrggbb").
func parseHexRGB(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		n := 0
		for _, c := range s[i*2 : i*2+2] {
			n <<= 4
			switch {
			case c >= '0' && c <= '9':
				n += int(c - '0')
			case c >= 'a' && c <= 'f':
				n += int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				n += int(c-'A') + 10
			default:
				return 0, 0, 0, false
			}
		}
		v[i] = n
	}
	return v[0], v[1], v[2], true
}

// cubeLevels are the xterm 6x6x6 color-cube channel values.
var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

func nearestCubeIndex(v int) int {
	best, bestDist := 0, 1<<30
	for i, l := range cubeLevels {
		d := (v - l) * (v - l)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// rgbTo256 returns the nearest xterm-256 index: the better of the 6x6x6 cube
// match (16-231) and the grayscale ramp match (232-255).
func rgbTo256(r, g, b int) int {
	cr, cg, cb := nearestCubeIndex(r), nearestCubeIndex(g), nearestCubeIndex(b)
	cubeDist := sqDist(cubeLevels[cr], r) + sqDist(cubeLevels[cg], g) + sqDist(cubeLevels[cb], b)
	cubeIdx := 16 + 36*cr + 6*cg + cb

	gray := (r + g + b) / 3
	gi := (gray - 8 + 5) / 10
	if gi < 0 {
		gi = 0
	}
	if gi > 23 {
		gi = 23
	}
	gv := 8 + 10*gi
	grayDist := sqDist(gv, r) + sqDist(gv, g) + sqDist(gv, b)

	if grayDist < cubeDist {
		return 232 + gi
	}
	return cubeIdx
}

func sqDist(a, b int) int {
	return (a - b) * (a - b)
}

// ansi16Palette maps the 16 basic color names to representative RGB values
// (standard xterm defaults) for nearest-color quantization of hex values on
// 16-color terminals.
var ansi16Palette = []struct {
	name    string
	r, g, b int
}{
	{"red", 205, 0, 0},
	{"green", 0, 205, 0},
	{"yellow", 205, 205, 0},
	{"blue", 0, 0, 238},
	{"magenta", 205, 0, 205},
	{"cyan", 0, 205, 205},
	{"white", 229, 229, 229},
	{"bright-red", 255, 0, 0},
	{"bright-green", 0, 255, 0},
	{"bright-yellow", 255, 255, 0},
	{"bright-blue", 92, 92, 255},
	{"bright-magenta", 255, 0, 255},
	{"bright-cyan", 0, 255, 255},
	{"bright-white", 255, 255, 255},
}

// nearestAnsi16 returns the escape code of the closest basic color. Black is
// excluded — quantizing a dark theme color to invisible-on-dark black is
// always worse than the dimmest gray.
func nearestAnsi16(r, g, b int) string {
	bestName, bestDist := "white", 1<<30
	for _, c := range ansi16Palette {
		d := sqDist(c.r, r) + sqDist(c.g, g) + sqDist(c.b, b)
		if d < bestDist {
			bestName, bestDist = c.name, d
		}
	}
	return colorCodes[bestName]
}

// index256ToRGB converts an xterm-256 index back to RGB (for degrading
// 256-index color specs on 16-color terminals).
func Index256ToRGB(idx int) (r, g, b int) {
	switch {
	case idx >= 232 && idx <= 255:
		v := 8 + 10*(idx-232)
		return v, v, v
	case idx >= 16 && idx <= 231:
		idx -= 16
		return cubeLevels[idx/36], cubeLevels[(idx/6)%6], cubeLevels[idx%6]
	default:
		// Basic 16: use the representative palette (idx 0..15 maps roughly).
		if idx >= 1 && idx <= 14 {
			c := ansi16Palette[(idx-1)%len(ansi16Palette)]
			return c.r, c.g, c.b
		}
		return 229, 229, 229
	}
}

// hexEscape renders a hex color at the given depth (quantizing as needed).
func hexEscape(hex string, d ColorDepth) (string, bool) {
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return "", false
	}
	switch d {
	case DepthNone:
		return "", true
	case Depth16:
		return nearestAnsi16(r, g, b), true
	case Depth256:
		return fmt.Sprintf("\x1b[38;5;%dm", rgbTo256(r, g, b)), true
	default:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b), true
	}
}
