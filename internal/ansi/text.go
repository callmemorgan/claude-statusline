package ansi

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/tview"

	"github.com/callmemorgan/claude-statusline/internal/palette"
)

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// StripANSI removes SGR escape sequences from s.
func StripANSI(s string) string { return reANSI.ReplaceAllString(s, "") }

// VisibleWidth returns the number of printable runes after stripping ANSI
// escapes.
func VisibleWidth(s string) int { return utf8.RuneCountInString(StripANSI(s)) }

// FormatPath shortens a path relative to a project directory.
func FormatPath(current, project string) string {
	display := filepath.Base(current)
	if display == "." || display == string(filepath.Separator) || display == "" {
		display = current
	}
	if project != "" && current != project && strings.HasPrefix(current, project+"/") {
		return filepath.Base(project) + "→" + strings.TrimPrefix(current, project+"/")
	}
	return display
}

// FormatHHMMSS renders milliseconds as HH:MM:SS.
func FormatHHMMSS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	totalSeconds := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", totalSeconds/3600, (totalSeconds%3600)/60, totalSeconds%60)
}

// FormatTokens renders a token count in k/M when large.
func FormatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%d.%dM", n/1_000_000, (n%1_000_000)/100_000)
	case n >= 1_000:
		return fmt.Sprintf("%d.%dk", n/1_000, (n%1_000)/100)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// ResetCountdown renders the time until resetUnix as a compact countdown.
func ResetCountdown(resetUnix int64, now time.Time) string {
	remaining := resetUnix - now.Unix()
	if remaining <= 0 {
		return "now"
	}
	days := remaining / 86400
	hours := (remaining % 86400) / 3600
	minutes := (remaining % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// EffortBadge maps an effort level to a compact arrow badge.
func EffortBadge(effort string) string {
	switch strings.ToLower(effort) {
	case "low":
		return "⬇"
	case "medium":
		return "→"
	case "high":
		return "⬆"
	case "xhigh":
		return "⬆⬆"
	case "max":
		return "⬆⬆⬆"
	default:
		return ""
	}
}

// sgrFgHex maps the basic 16 SGR foreground codes to hex strings for tview.
var sgrFgHex = map[int]string{
	30: "#000000", 31: "#cd0000", 32: "#00cd00", 33: "#cdcd00",
	34: "#0000ee", 35: "#cd00cd", 36: "#00cdcd", 37: "#e5e5e5",
	90: "#7f7f7f", 91: "#ff0000", 92: "#00ff00", 93: "#ffff00",
	94: "#5c5cff", 95: "#ff00ff", 96: "#00ffff", 97: "#ffffff",
}

// sgrToTag converts one SGR parameter string to a tview color tag.
func SGRToTag(params string) string {
	if params == "" || params == "0" {
		return "[-:-:-]"
	}
	parts := strings.Split(params, ";")
	if parts[0] == "38" && len(parts) >= 3 && parts[1] == "5" {
		if n, err := strconv.Atoi(parts[2]); err == nil && n >= 0 && n <= 255 {
			r, g, b := palette.Index256ToRGB(n)
			return "[" + palette.HexFromRGB(r, g, b) + "]"
		}
		return ""
	}
	if parts[0] == "38" && len(parts) >= 5 && parts[1] == "2" {
		r, err1 := strconv.Atoi(parts[2])
		g, err2 := strconv.Atoi(parts[3])
		b, err3 := strconv.Atoi(parts[4])
		if err1 == nil && err2 == nil && err3 == nil {
			return "[" + palette.HexFromRGB(r&0xff, g&0xff, b&0xff) + "]"
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

// FooterRows returns how many rows a footer needs at the given width, using
// tview's own word-wrap so the count matches what gets drawn. Clamped to 3 so
// a pathologically narrow terminal can't squeeze the segment list away.
func FooterRows(text string, width int) int {
	if width <= 0 {
		return 1
	}
	rows := len(tview.WordWrap(text, width))
	if rows < 1 {
		return 1
	}
	if rows > 3 {
		return 3
	}
	return rows
}

// AnsiToTview converts ANSI SGR escapes into tview color tags, escaping
// literal '[' characters so they are not interpreted as tags.
func AnsiToTview(s string) string {
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
		s = strings.ReplaceAll(s, ph, SGRToTag(params))
	}
	return s
}
