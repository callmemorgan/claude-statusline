package main

import (
	_ "embed"
	"regexp"
	"strconv"
	"strings"

	"github.com/rivo/tview"
)

//go:embed README.md
var readmeContent string

// ─── Markdown → tview renderer ───────────────────────────────────────

var (
	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
)

func inlineMarkdown(s string) string {
	s = reBold.ReplaceAllString(s, "[::b]$1[::-]")
	s = reInlineCode.ReplaceAllString(s, "[green]$1[-]")
	return s
}

func markdownToTview(md string) string {
	var b strings.Builder
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		// Code fence open/close.
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			if inCode {
				b.WriteString("[gray]")
			} else {
				b.WriteString("[-]\n")
			}
			continue
		}
		if inCode {
			b.WriteString(tview.Escape(line) + "\n")
			continue
		}

		esc := tview.Escape(line)
		switch {
		case strings.HasPrefix(line, "# "):
			b.WriteString("\n[yellow::b]" + tview.Escape(strings.TrimPrefix(line, "# ")) + "[-::-]\n")
		case strings.HasPrefix(line, "## "):
			b.WriteString("\n[cyan::b]  " + tview.Escape(strings.TrimPrefix(line, "## ")) + "[-::-]\n")
		case strings.HasPrefix(line, "### "):
			b.WriteString("[green::b]    " + tview.Escape(strings.TrimPrefix(line, "### ")) + "[-::-]\n")
		case strings.HasPrefix(line, "|"):
			// Table rows and separators — dim.
			b.WriteString("[::d]" + esc + "[-::-]\n")
		case strings.HasPrefix(line, "---"):
			// Horizontal rule.
			b.WriteString("[::d]────────────────────────────────────────[-::-]\n")
		default:
			b.WriteString(inlineMarkdown(esc) + "\n")
		}
	}
	return b.String()
}

// sgrFgHex maps the basic ANSI foreground codes to representative hex values
// (standard xterm defaults), so TUI previews render them via tview hex tags.
var sgrFgHex = map[int]string{
	30: "#000000", 31: "#cd0000", 32: "#00cd00", 33: "#cdcd00",
	34: "#5c5cff", 35: "#cd00cd", 36: "#00cdcd", 37: "#e5e5e5",
	90: "#7f7f7f", 91: "#ff0000", 92: "#00ff00", 93: "#ffff00",
	94: "#8787ff", 95: "#ff00ff", 96: "#00ffff", 97: "#ffffff",
}

// sgrToTag converts one SGR parameter string ("32", "38;5;208",
// "38;2;187;154;247", "0") to a tview color tag. Unknown sequences map to
// the empty string and disappear from the preview.
func sgrToTag(params string) string {
	if params == "" || params == "0" {
		return "[-:-:-]"
	}
	parts := strings.Split(params, ";")
	if parts[0] == "38" && len(parts) >= 3 && parts[1] == "5" {
		if n, err := strconv.Atoi(parts[2]); err == nil && n >= 0 && n <= 255 {
			r, g, b := index256ToRGB(n)
			return "[" + hexFromRGB(r, g, b) + "]"
		}
		return ""
	}
	if parts[0] == "38" && len(parts) >= 5 && parts[1] == "2" {
		r, err1 := strconv.Atoi(parts[2])
		g, err2 := strconv.Atoi(parts[3])
		b, err3 := strconv.Atoi(parts[4])
		if err1 == nil && err2 == nil && err3 == nil {
			return "[" + hexFromRGB(r&0xff, g&0xff, b&0xff) + "]"
		}
		return ""
	}
	if n, err := strconv.Atoi(parts[0]); err == nil {
		if hex, ok := sgrFgHex[n]; ok {
			return "[" + hex + "]"
		}
	}
	return ""
}

// ansiToTview converts ANSI SGR escapes \u2014 16-color, 256-color, and truecolor
// \u2014 into tview color tags, escaping literal '[' characters so they are not
// interpreted as tags. Escapes are swapped to private-use placeholders before
// tview.Escape runs and substituted with tags afterwards, so the generated
// tags themselves never get escaped.
func ansiToTview(s string) string {
	placeholders := map[string]string{}
	next := rune(0xE000)
	for _, seq := range reANSI.FindAllString(s, -1) {
		if _, ok := placeholders[seq]; ok {
			continue
		}
		placeholders[seq] = string(next)
		next++
	}
	for seq, ph := range placeholders {
		s = strings.ReplaceAll(s, seq, ph)
	}
	s = tview.Escape(s)
	for seq, ph := range placeholders {
		params := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
		s = strings.ReplaceAll(s, ph, sgrToTag(params))
	}
	return s
}
