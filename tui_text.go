package main

import (
	_ "embed"
	"regexp"
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
