package render

import (
	"sort"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/plugins"
	"github.com/callmemorgan/claude-statusline/internal/segments"
	"github.com/callmemorgan/claude-statusline/internal/state"
)

const (
	// Layout budget: line 1 reserves room for the trailing " │ X.Xms" timing
	// suffix, and every line keeps a small safety margin before wrapping.
	timingSuffixReserve = 15
	safetyMargin        = 5
)

// LineBudget is the visible-column budget for one physical line: the full
// width minus the safety margin, and on the first line also the timing-suffix
// reserve (floored so pathological widths stay renderable). Shared by both
// reflow modes and the release-notes takeover so the reserves can't desync.
func LineBudget(columns int, first bool) int {
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

// Input is the top-level input to Statusline.
type Input struct {
	P       payload.Payload
	C       palette.Palette
	Cfg     config.Config
	State   *state.SessionState
	Width   int
	Now     time.Time
	Preview bool // true when rendering for the TUI assembler/preview
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

// LineStyle is the resolved [style] config: the separator (optionally colored
// with the theme's sep role) and per-line left padding.
type LineStyle struct {
	Sep      string // rendered separator, color included
	SepWidth int    // visible width of the separator
	Padding  int
}

// StyleFor resolves the [style] config into a LineStyle.
func StyleFor(cfg config.Config, c palette.Palette) LineStyle {
	glyph, ok := separators[cfg.Style.Separator]
	if !ok {
		if cfg.Style.Separator == "custom" && cfg.Style.SeparatorCustom != "" {
			glyph = cfg.Style.SeparatorCustom
		} else {
			glyph = separators["bar"]
		}
	}
	st := LineStyle{Sep: glyph, SepWidth: ansi.VisibleWidth(glyph), Padding: 1}
	if cfg.Style.Padding != nil {
		st.Padding = *cfg.Style.Padding
	}
	if c.Sep != "" {
		st.Sep = c.Sep + glyph + c.Rst
	}
	return st
}

// Statusline builds the statusline from the configured segments.
func Statusline(in Input) []string {
	plugins.ClearCache()
	parts := map[int][]string{}
	for _, id := range in.Cfg.Segments {
		if s, ok := segments.ByID(id); ok {
			segPalette := in.C
			if in.C.Rst != "" {
				if colorName := in.Cfg.Colors[id]; colorName != "" && colorName != "default" {
					segPalette = palette.PaletteWithOverride(in.C, s.PrimaryColor, colorName)
				}
			}
			ctx := segments.RenderCtx{
				P:       in.P,
				C:       segPalette,
				S:       config.SettingsFor(in.Cfg, s.ID, s.Settings),
				Cfg:     in.Cfg,
				Width:   in.Width,
				Now:     in.Now,
				Preview: in.Preview,
			}
			if s.NeedsState {
				ctx.State = in.State
			}
			if rendered, show := s.Render(ctx); show {
				line := s.Line
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

	st := StyleFor(in.Cfg, in.C)
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
func buildStatuslineNoWrap(parts map[int][]string, st LineStyle) []string {
	return buildStatuslineCascade(parts, 0, st)
}

// buildStatuslineCascade is the original reflow behaviour: segments spill
// greedily from line 1 → 2 → 3 regardless of which logical line they belong to.
func buildStatuslineCascade(parts map[int][]string, columns int, st LineStyle) []string {
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
			budget := LineBudget(columns, lineNum == 1)
			for {
				segs := parts[lineNum]
				if len(segs) <= 1 {
					break
				}
				width := st.Padding
				for i, seg := range segs {
					if i > 0 {
						width += st.SepWidth
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
func buildStatuslineGroup(parts map[int][]string, columns int, st LineStyle) []string {
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
			sep := st.Padding // leading padding
			if len(current) > 0 {
				sep = st.SepWidth
			}

			budget := LineBudget(columns, firstPhysicalLine && len(out) == 0)

			if len(current) == 0 || currentWidth+sep+segWidth <= budget {
				current = append(current, seg)
				currentWidth += sep + segWidth
			} else {
				out = append(out, joinParts(current, st))
				current = []string{seg}
				currentWidth = st.Padding + segWidth
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

func joinParts(parts []string, st LineStyle) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Repeat(" ", st.Padding) + strings.Join(parts, st.Sep)
}
