package main

import (
	"sort"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/state"
)

const (
	barWidth = 20

	// Layout budget: line 1 reserves room for the trailing " │ X.Xms" timing
	// suffix, and every line keeps a small safety margin before wrapping.
	timingSuffixReserve = 15
	safetyMargin        = 5
)

// lineBudget is the visible-column budget for one physical line: the full
// width minus the safety margin, and on the first line also the timing-suffix
// reserve (floored so pathological widths stay renderable). Shared by both
// reflow modes and the release-notes takeover so the reserves can't desync.
func lineBudget(columns int, first bool) int {
	b := columns - safetyMargin
	if first {
		b -= timingSuffixReserve
		if b < 10 {
			b = 10
		}
	}
	return b
}

// ─── Statusline Builder ──────────────────────────────────────────────

// renderCtx carries everything a segment renderer needs. The palette already
// has the per-segment color override applied, and S holds the segment's own
// resolved settings. Now is injected so countdowns and rates are testable.
type renderCtx struct {
	P     payload.Payload
	C     palette.Palette
	S     config.Settings
	State *state.SessionState // nil unless the segment declares needsState
	Cfg   config.Config       // resolved config, rarely needed (e.g. update segment)
	Width int
	Now   time.Time
}

// buildInput is the top-level input to buildStatusline.
type buildInput struct {
	P     payload.Payload
	C     palette.Palette
	Cfg   config.Config
	State *state.SessionState
	Width int
	Now   time.Time
}

// separators maps style names to glyphs (all single-cell wide).
var separators = map[string]string{
	"bar":       " │ ",
	"dot":       " · ",
	"slash":     " / ",
	"chevron":   " ❯ ",
	"powerline": "  ",
	"space":     "  ",
}

// lineStyle is the resolved [style] config: the separator (optionally colored
// with the theme's sep role) and per-line left padding.
type lineStyle struct {
	sep      string // rendered separator, color included
	sepWidth int    // visible width of the separator
	padding  int
}

func styleFor(cfg config.Config, c palette.Palette) lineStyle {
	glyph, ok := separators[cfg.Style.Separator]
	if !ok {
		if cfg.Style.Separator == "custom" && cfg.Style.SeparatorCustom != "" {
			glyph = cfg.Style.SeparatorCustom
		} else {
			glyph = separators["bar"]
		}
	}
	st := lineStyle{sep: glyph, sepWidth: ansi.VisibleWidth(glyph), padding: 1}
	if cfg.Style.Padding != nil {
		st.padding = *cfg.Style.Padding
	}
	if c.Sep != "" {
		st.sep = c.Sep + glyph + c.Rst
	}
	return st
}

func buildStatusline(in buildInput) []string {
	clearPluginCache()
	parts := map[int][]string{}
	for _, id := range in.Cfg.Segments {
		if s, ok := segmentByID(id); ok {
			segPalette := in.C
			if in.C.Rst != "" {
				if colorName := in.Cfg.Colors[id]; colorName != "" && colorName != "default" {
					segPalette = palette.PaletteWithOverride(in.C, s.primaryColor, colorName)
				}
			}
			ctx := renderCtx{
				P:     in.P,
				C:     segPalette,
				S:     config.SettingsFor(in.Cfg, s.id, s.settings),
				Cfg:   in.Cfg,
				Width: in.Width,
				Now:   in.Now,
			}
			if s.needsState {
				ctx.State = in.State
			}
			if rendered, show := s.render(ctx); show {
				line := s.line
				if override, ok := in.Cfg.Lines[id]; ok && override >= 1 {
					line = override
				}
				parts[line] = append(parts[line], rendered)
			}
		}
	}
	if len(parts) == 0 {
		return []string{}
	}

	st := styleFor(in.Cfg, in.C)
	switch {
	case in.Width > 0 && in.Cfg.Reflow == "group":
		return buildStatuslineGroup(parts, in.Width, st)
	case in.Cfg.Reflow == "cascade":
		return buildStatuslineCascade(parts, in.Width, st)
	default:
		// Default (and explicit "off"): line wrapping is opt-in. Emit each
		// logical line as-is and let the terminal soft-wrap anything too wide,
		// rather than reflowing segments across lines.
		return buildStatuslineNoWrap(parts, st)
	}
}

// buildStatuslineNoWrap emits each logical line as-is with no width-based
// reflow — the default. A line wider than the terminal is left for the terminal
// to soft-wrap. Equivalent to cascade with no column budget (so its trailing
// segments never spill); see TestReflowCascadeNoColumns.
func buildStatuslineNoWrap(parts map[int][]string, st lineStyle) []string {
	return buildStatuslineCascade(parts, 0, st)
}

// buildStatuslineCascade is the original reflow behaviour: segments spill
// greedily from line 1 → 2 → 3 regardless of which logical line they belong to.
func buildStatuslineCascade(parts map[int][]string, columns int, st lineStyle) []string {
	maxLine := 0
	originalLines := map[int]bool{}
	for k := range parts {
		if k > maxLine {
			maxLine = k
		}
		originalLines[k] = true
	}

	// Track which lines received overflow from a previous line.
	receivedOverflow := map[int]bool{}

	// Auto-reflow: spill trailing segments to the next line when a line
	// exceeds the available terminal width.
	if columns > 0 {
		lineNum := 1
		for lineNum <= maxLine {
			budget := lineBudget(columns, lineNum == 1)
			for {
				segs := parts[lineNum]
				if len(segs) <= 1 {
					break
				}
				width := st.padding
				for i, seg := range segs {
					if i > 0 {
						width += st.sepWidth
					}
					width += ansi.VisibleWidth(seg)
				}
				if width <= budget {
					break
				}
				// Move last segment to the next line.
				moved := segs[len(segs)-1]
				parts[lineNum] = segs[:len(segs)-1]
				parts[lineNum+1] = append([]string{moved}, parts[lineNum+1]...)
				receivedOverflow[lineNum+1] = true
				if lineNum+1 > maxLine {
					maxLine = lineNum + 1
				}
			}
			lineNum++
		}
	}

	out := []string{}
	for i := 1; i <= maxLine; i++ {
		line := joinParts(parts[i], st)
		if receivedOverflow[i] && originalLines[i] && i > 1 && (len(out) == 0 || out[len(out)-1] != "") {
			out = append(out, "")
		}
		out = append(out, line)
	}
	return out
}

// buildStatuslineGroup wraps each logical line independently. Segments from
// different logical lines never share a physical line, preserving the line
// boundaries defined in the configuration.
func buildStatuslineGroup(parts map[int][]string, columns int, st lineStyle) []string {
	var lineNums []int
	for k := range parts {
		lineNums = append(lineNums, k)
	}
	sort.Ints(lineNums)

	var out []string
	firstPhysicalLine := true

	for _, lineNum := range lineNums {
		segs := parts[lineNum]
		if len(segs) == 0 {
			continue
		}

		var current []string
		currentWidth := 0

		for _, seg := range segs {
			segWidth := ansi.VisibleWidth(seg)
			sep := st.padding // leading padding
			if len(current) > 0 {
				sep = st.sepWidth
			}

			budget := lineBudget(columns, firstPhysicalLine && len(out) == 0)

			if len(current) == 0 || currentWidth+sep+segWidth <= budget {
				current = append(current, seg)
				currentWidth += sep + segWidth
			} else {
				out = append(out, joinParts(current, st))
				current = []string{seg}
				currentWidth = st.padding + segWidth
				firstPhysicalLine = false
			}
		}

		if len(current) > 0 {
			out = append(out, joinParts(current, st))
			firstPhysicalLine = false
		}
	}

	return out
}

func joinParts(parts []string, st lineStyle) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Repeat(" ", st.padding) + strings.Join(parts, st.sep)
}

// iconset defines the glyphs of one progress-bar style. All glyphs are a
// single terminal cell wide. Partials, when present, are fractional-fill
// glyphs ordered low→high that multiply the bar's effective resolution.
type iconset struct {
	Filled, Empty string
	Partials      []string
}

var iconsets = map[string]iconset{
	"default":      {Filled: "#", Empty: "-"},
	"blocks":       {Filled: "█", Empty: "░"},
	"dots":         {Filled: "●", Empty: "○"},
	"ascii":        {Filled: "=", Empty: " "},
	"minimal":      {Filled: "|", Empty: " "},
	"braille":      {Filled: "⣿", Empty: "⣀"},
	"braille-fine": {Filled: "⣿", Empty: "⠀", Partials: []string{"⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷"}},
	"shade":        {Filled: "▓", Empty: "░"},
	"smooth":       {Filled: "█", Empty: " ", Partials: []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}},
	"line":         {Filled: "━", Empty: "─"},
	"slim":         {Filled: "▰", Empty: "▱"},
	"vertical":     {Filled: "▮", Empty: "▯"},
}

// iconsetOrder is the cycle order offered in the TUI (map iteration order is
// random, so the list is explicit).
var iconsetOrder = []string{
	"default", "blocks", "dots", "ascii", "minimal",
	"smooth", "braille", "braille-fine", "shade", "line", "slim", "vertical",
}

func iconsetNames() []string {
	return iconsetOrder
}

func iconsetByName(name string) iconset {
	if is, ok := iconsets[name]; ok {
		return is
	}
	return iconsets["default"]
}

func iconsetPair(name string) (string, string) {
	is := iconsetByName(name)
	return is.Filled, is.Empty
}

func progressBarWithIconset(pct int, fillColor, emptyColor string, c palette.Palette, width int, name string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	is := iconsetByName(name)

	if len(is.Partials) == 0 {
		filled := pct * width / 100
		return fillColor + strings.Repeat(is.Filled, filled) +
			emptyColor + strings.Repeat(is.Empty, width-filled) + c.Rst
	}

	// Fractional fill: each cell subdivides into len(Partials)+1 units; the
	// remainder renders as one partial glyph in the fill color.
	n := len(is.Partials) + 1
	units := pct * width * n / 100
	full := units / n
	rem := units % n
	var b strings.Builder
	b.WriteString(fillColor)
	b.WriteString(strings.Repeat(is.Filled, full))
	empty := width - full
	if rem > 0 && full < width {
		b.WriteString(is.Partials[rem-1])
		empty--
	}
	b.WriteString(emptyColor)
	b.WriteString(strings.Repeat(is.Empty, empty))
	b.WriteString(c.Rst)
	return b.String()
}

func progressBarWithTimeAndIconset(pct, timePct int, fillColor, emptyColor string, c palette.Palette, width int, iconset string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	filledChar, emptyChar := iconsetPair(iconset)

	timeSlot := -1
	if timePct >= 0 && timePct <= 100 {
		timeSlot = timePct * width / 100
		if timeSlot >= width {
			timeSlot = width - 1
		}
	}

	var b strings.Builder
	for i := 0; i < width; i++ {
		switch {
		case i == timeSlot:
			b.WriteString(c.Purple + "|")
		case i < filled:
			b.WriteString(fillColor + filledChar)
		default:
			b.WriteString(emptyColor + emptyChar)
		}
	}
	b.WriteString(c.Rst)
	return b.String()
}

func pctColorWithSettings(pct int, c palette.Palette, s config.Settings) string {
	warnAt, critAt := s.Int("warn_at"), s.Int("crit_at")
	var colorName, natural string
	switch {
	case pct > critAt:
		colorName, natural = s.Str("crit_color"), "bright-red"
	case pct >= warnAt:
		colorName, natural = s.Str("warn_color"), "yellow"
	default:
		colorName, natural = s.Str("ok_color"), "green"
	}
	// "" or "default" both mean "use the natural color for this state".
	if colorName == "" || colorName == "default" {
		colorName = natural
	}
	return palette.ResolveColor(colorName, c)
}
