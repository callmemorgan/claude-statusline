package main

// ─── Command Palette Actions ─────────────────────────────────────────
//
// The command-palette UI (tui_palette.go) is driven entirely by a generated
// set of actions. Every action is derived from the live segment registry
// (registeredSegments) and the settings schema (settingSpec), so the command
// list can never drift from the real configuration surface: enabling a new
// segment, adding a settingSpec, or registering a plugin automatically
// produces the corresponding palette commands.
//
// This file is pure logic (no tview) so the generator and the fuzzy matcher
// are unit-tested directly (actions_test.go).

import (
	"fmt"
	"sort"
	"strings"
)

// actionKind classifies how the palette executes an action. Most actions apply
// a config mutation directly; a few need an interactive sub-choice (a color
// swatch grid or a free-text prompt) the palette resolves before applying.
type actionKind int

const (
	// actionApply mutates the config immediately via Apply.
	actionApply actionKind = iota
	// actionColorPicker opens the swatch picker; the chosen spec is written to
	// the target via SetColor (a segment color override) — Apply is unused.
	actionColorPicker
	// actionPrompt asks for a free-text value, validated+applied via SetValue.
	actionPrompt
)

// action is a single palette command. Title is what the user reads; Search is
// the lowercased haystack the fuzzy matcher scores a query against (it folds in
// synonyms and the segment description so intent words like "hide" find
// "Disable"). Apply mutates a *config in place.
type action struct {
	Title  string
	Search string
	Kind   actionKind

	// Apply runs for actionApply actions.
	Apply func(*config)

	// Color-picker actions: the segment whose primary color override is set,
	// and a setter the palette calls with the picked spec.
	ColorTarget string
	SetColor    func(*config, string)

	// Prompt actions: a one-line label and a setter that returns an error when
	// the typed value is invalid (so the palette can surface it).
	PromptLabel string
	SetValue    func(*config, string) error
}

// ─── Generation ──────────────────────────────────────────────────────

// generateActions walks the registry + schema + global enumerations and emits
// the full command set for the current config. cfg is read for current state
// (so toggles read the right "enable" vs "disable" verb) but never mutated.
func generateActions(cfg config) []action {
	var acts []action

	enabled := map[string]bool{}
	for _, id := range cfg.Segments {
		enabled[id] = true
	}

	for _, seg := range registeredSegments {
		acts = append(acts, segmentActions(cfg, seg, enabled[seg.id])...)
	}

	acts = append(acts, globalActions(cfg)...)
	return acts
}

// segmentActions emits every command targeting a single segment: enable/disable,
// move to each line, reorder within its line, set its primary color, and one or
// more commands per non-ephemeral settingSpec.
func segmentActions(cfg config, seg segmentInfo, isEnabled bool) []action {
	id := seg.id
	var acts []action

	// Enable / disable.
	if isEnabled {
		acts = append(acts, action{
			Title:  "Disable " + id,
			Search: searchKey("disable hide remove off", id, seg.desc),
			Apply:  func(c *config) { setSegmentEnabled(c, id, false) },
		})
	} else {
		acts = append(acts, action{
			Title:  "Enable " + id,
			Search: searchKey("enable show add on", id, seg.desc),
			Apply:  func(c *config) { setSegmentEnabled(c, id, true) },
		})
	}

	// Move to line N (1..9). Skip the line the segment is already on.
	cur := effectiveLine(id, cfg)
	for n := 1; n <= 9; n++ {
		if n == cur {
			continue
		}
		line := n
		acts = append(acts, action{
			Title:  fmt.Sprintf("Move %s to line %d", id, line),
			Search: searchKey(fmt.Sprintf("move line %d row", line), id, seg.desc),
			Apply: func(c *config) {
				setSegmentLine(c, id, line)
				setSegmentEnabled(c, id, true)
			},
		})
	}

	// Reorder left / right within the segment's line.
	acts = append(acts,
		action{
			Title:  "Reorder " + id + " left",
			Search: searchKey("reorder move left earlier before", id, seg.desc),
			Apply:  func(c *config) { reorderSegment(c, id, -1) },
		},
		action{
			Title:  "Reorder " + id + " right",
			Search: searchKey("reorder move right later after", id, seg.desc),
			Apply:  func(c *config) { reorderSegment(c, id, +1) },
		},
	)

	// Set primary color: one quick command per cycle color, plus a picker entry
	// and a "default" (clear override) entry.
	acts = append(acts, action{
		Title:       "Set " + id + " color (picker)",
		Search:      searchKey("color colour picker swatch", id, seg.desc),
		Kind:        actionColorPicker,
		ColorTarget: id,
		SetColor:    func(c *config, spec string) { setSegmentColor(c, id, spec) },
	})
	for _, name := range colorCycle {
		spec := name
		acts = append(acts, action{
			Title:  fmt.Sprintf("Set %s color %s", id, spec),
			Search: searchKey("color colour "+spec, id, seg.desc),
			Apply:  func(c *config) { setSegmentColor(c, id, spec) },
		})
	}

	// Per-setting commands, straight from the schema.
	for _, sp := range seg.settings {
		if sp.Ephemeral {
			continue
		}
		acts = append(acts, settingActions(seg, sp)...)
	}

	return acts
}

// settingActions emits the commands for one settingSpec, shaped by its kind:
// bools toggle; enums get one command per option; ints get increase/decrease
// (clamped) plus a "set …" prompt; colors get a picker plus one command per
// cycle color.
func settingActions(seg segmentInfo, sp settingSpec) []action {
	id := seg.id
	label := sp.Name
	base := searchKey(sp.Name+" "+sp.Desc, id, sp.Key)
	var acts []action

	switch sp.Kind {
	case kindBool:
		acts = append(acts, action{
			Title:  fmt.Sprintf("Toggle %s · %s", id, label),
			Search: searchKey("toggle enable disable on off", base),
			Apply:  func(c *config) { stepSetting(c, id, sp, 1) },
		})
	case kindEnum:
		for _, opt := range sp.Options {
			o := opt
			acts = append(acts, action{
				Title:  fmt.Sprintf("Set %s · %s → %s", id, label, o),
				Search: searchKey("set "+o, base),
				Apply:  func(c *config) { setSettingValue(c, id, sp, o) },
			})
		}
	case kindInt:
		acts = append(acts,
			action{
				Title:  fmt.Sprintf("Increase %s · %s", id, label),
				Search: searchKey("increase increment raise up more", base),
				Apply:  func(c *config) { stepSetting(c, id, sp, intStep(sp)) },
			},
			action{
				Title:  fmt.Sprintf("Decrease %s · %s", id, label),
				Search: searchKey("decrease decrement lower down less", base),
				Apply:  func(c *config) { stepSetting(c, id, sp, -intStep(sp)) },
			},
			action{
				Title:       fmt.Sprintf("Set %s · %s …", id, label),
				Search:      searchKey("set value number", base),
				Kind:        actionPrompt,
				PromptLabel: fmt.Sprintf("%s — %s (%d..%d)", id, label, sp.Min, sp.Max),
				SetValue: func(c *config, raw string) error {
					return setSettingFromText(c, id, sp, raw)
				},
			},
		)
	case kindColor:
		acts = append(acts, action{
			Title:       fmt.Sprintf("Set %s · %s (picker)", id, label),
			Search:      searchKey("color colour picker swatch", base),
			Kind:        actionColorPicker,
			ColorTarget: id,
			SetColor:    func(c *config, spec string) { setSettingValue(c, id, sp, spec) },
		})
		for _, opt := range sp.Options {
			o := opt
			acts = append(acts, action{
				Title:  fmt.Sprintf("Set %s · %s → %s", id, label, o),
				Search: searchKey("color colour "+o, base),
				Apply:  func(c *config) { setSettingValue(c, id, sp, o) },
			})
		}
	}
	return acts
}

// globalActions emits the config-wide commands: themes, presets, reflow modes,
// separators, and reset/save markers (save is handled by the palette host but
// listed here so it is fuzzy-findable).
func globalActions(cfg config) []action {
	var acts []action

	for _, id := range themeIDs() {
		t := id
		acts = append(acts, action{
			Title:  "Theme → " + t,
			Search: searchKey("theme colors palette "+t, ""),
			Apply: func(c *config) {
				if t == "classic" {
					c.Theme = ""
				} else {
					c.Theme = t
				}
			},
		})
	}

	for _, p := range layoutPresets {
		preset := p
		acts = append(acts, action{
			Title:  "Apply preset → " + preset.ID,
			Search: searchKey("preset layout "+preset.ID+" "+preset.Desc, ""),
			Apply:  func(c *config) { applyPreset(c, preset) },
		})
	}

	for _, mode := range []struct{ id, label string }{
		{"", "off"}, {"cascade", "cascade"}, {"group", "group"},
	} {
		m := mode
		acts = append(acts, action{
			Title:  "Reflow → " + m.label,
			Search: searchKey("reflow wrap line "+m.label, ""),
			Apply:  func(c *config) { c.Reflow = m.id },
		})
	}

	for _, name := range separatorNames() {
		s := name
		acts = append(acts, action{
			Title:  "Separator → " + s,
			Search: searchKey("separator divider style "+s, ""),
			Apply:  func(c *config) { c.Style.Separator = s },
		})
	}

	acts = append(acts, action{
		Title:  "Reset to defaults",
		Search: searchKey("reset defaults clear start over", ""),
		Apply:  func(c *config) { *c = defaultConfig() },
	})

	return acts
}

// separatorNames lists the built-in separator glyphs in a stable order.
func separatorNames() []string {
	names := make([]string, 0, len(separators))
	for k := range separators {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ─── Apply primitives ────────────────────────────────────────────────
//
// These mirror the TUI's existing handlers (toggle/line/color/reorder/setting)
// but operate on a *config directly so both the palette UI and the unit tests
// reuse the exact same mutation logic.

func setSegmentEnabled(c *config, id string, on bool) {
	idx := -1
	for i, sid := range c.Segments {
		if sid == id {
			idx = i
			break
		}
	}
	if on {
		if idx < 0 {
			c.Segments = append(c.Segments, id)
		}
	} else if idx >= 0 {
		c.Segments = append(c.Segments[:idx], c.Segments[idx+1:]...)
	}
}

// setSegmentLine sets a per-segment line override, deleting it when the target
// equals the segment's natural line (mirrors effectiveLine semantics).
func setSegmentLine(c *config, id string, line int) {
	natural := 1
	if s, ok := segmentByID(id); ok {
		natural = s.line
	}
	if line == natural {
		delete(c.Lines, id)
		return
	}
	if c.Lines == nil {
		c.Lines = make(map[string]int)
	}
	c.Lines[id] = line
}

// setSegmentColor writes (or clears) a segment's primary color override.
func setSegmentColor(c *config, id, spec string) {
	if spec == "" || spec == "default" {
		delete(c.Colors, id)
		return
	}
	if c.Colors == nil {
		c.Colors = make(map[string]string)
	}
	c.Colors[id] = spec
	setSegmentEnabled(c, id, true)
}

// reorderSegment swaps a segment with its same-line neighbour in render order.
// delta -1 moves it earlier (left), +1 later (right); no-op at the boundary.
func reorderSegment(c *config, id string, delta int) {
	line := effectiveLine(id, *c)
	var peers []int
	for i, sid := range c.Segments {
		if effectiveLine(sid, *c) == line {
			peers = append(peers, i)
		}
	}
	pos := -1
	for i, pi := range peers {
		if c.Segments[pi] == id {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	target := pos + delta
	if target < 0 || target >= len(peers) {
		return
	}
	c.Segments[peers[pos]], c.Segments[peers[target]] = c.Segments[peers[target]], c.Segments[peers[pos]]
}

// intStep returns the coarse step for an int spec, defaulting to 1.
func intStep(sp settingSpec) int {
	if sp.Step > 1 {
		return sp.Step
	}
	return 1
}

// stepSetting toggles a bool or steps an int/enum setting by delta, reusing the
// flyout's prune-on-write logic. Enabling the segment first so a setting change
// is visible.
func stepSetting(c *config, id string, sp settingSpec, delta int) {
	seg, ok := segmentByID(id)
	if !ok {
		return
	}
	setSegmentEnabled(c, id, true)
	s := settingsFor(*c, seg)
	switch sp.Kind {
	case kindBool:
		s[sp.Key] = !s.Bool(sp.Key)
	case kindEnum, kindColor:
		s[sp.Key] = cycleOption(sp.Options, s.Str(sp.Key), delta)
	case kindInt:
		v := s.Int(sp.Key) + delta
		if v < sp.Min {
			v = sp.Min
		}
		if v > sp.Max {
			v = sp.Max
		}
		s[sp.Key] = v
	}
	setSegmentSettings(c, id, pruneSettings(seg, s))
}

// writeSegmentSetting enables the segment, writes one resolved key value, and
// persists the pruned settings. It is the shared tail of every "set one value"
// path (coerced enum/color, parsed int), so the enable+prune semantics live in
// exactly one place.
func writeSegmentSetting(c *config, id, key string, value any) {
	seg, ok := segmentByID(id)
	if !ok {
		return
	}
	setSegmentEnabled(c, id, true)
	s := settingsFor(*c, seg)
	s[key] = value
	setSegmentSettings(c, id, pruneSettings(seg, s))
}

// setSettingValue writes one setting to an explicit (coerced) value.
func setSettingValue(c *config, id string, sp settingSpec, value string) {
	writeSegmentSetting(c, id, sp.Key, sp.coerce(value))
}

// setSettingFromText parses a free-text int value, validates it against the
// spec bounds, and applies it. Returns an error the palette surfaces inline.
func setSettingFromText(c *config, id string, sp settingSpec, raw string) error {
	raw = strings.TrimSpace(raw)
	if sp.Kind != kindInt {
		setSettingValue(c, id, sp, raw)
		return nil
	}
	n, err := parseIntStrict(raw)
	if err != nil {
		return fmt.Errorf("not a number: %q", raw)
	}
	if n < sp.Min || n > sp.Max {
		return fmt.Errorf("must be %d..%d", sp.Min, sp.Max)
	}
	if _, ok := segmentByID(id); !ok {
		return fmt.Errorf("unknown segment %q", id)
	}
	writeSegmentSetting(c, id, sp.Key, n)
	return nil
}

func parseIntStrict(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	neg := false
	i := 0
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		i = 1
		if i == len(s) {
			return 0, fmt.Errorf("no digits")
		}
	}
	n := 0
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

// searchKey builds the lowercased fuzzy haystack from a set of parts.
func searchKey(parts ...string) string {
	return strings.ToLower(strings.Join(parts, " "))
}

// ─── Fuzzy matching ──────────────────────────────────────────────────

// scoredAction pairs an action with its match score for ranking.
type scoredAction struct {
	A     action
	Score int
}

// fuzzyScore scores how well query subsequence-matches the haystack. Returns
// (score, true) on a match; (0, false) when query's characters do not appear
// in order. Higher is better. An empty query matches everything with score 0.
//
// Scoring rewards: contiguous runs of matched characters, matches at the start
// of the haystack or right after a separator (word boundary), and a shorter
// haystack (so "cost" beats "cost-rate" for the query "cost").
func fuzzyScore(query, haystack string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0, true
	}
	h := strings.ToLower(haystack)
	score := 0
	hi := 0
	run := 0
	prevBoundary := true // start of string counts as a boundary
	for qi := 0; qi < len(query); qi++ {
		qc := query[qi]
		found := false
		for ; hi < len(h); hi++ {
			boundary := prevBoundary
			if hi > 0 {
				c := h[hi-1]
				boundary = c == ' ' || c == '-' || c == '_' || c == '·' || c == '/'
			}
			prevBoundary = false
			if h[hi] == qc {
				score += 1
				if hi == 0 || boundary {
					score += 8 // word-boundary / start bonus
				}
				run++
				score += run // contiguity bonus grows with run length
				hi++
				found = true
				break
			}
			run = 0
		}
		if !found {
			return 0, false
		}
	}
	// Prefer shorter haystacks (tighter matches) and exact-substring hits.
	score -= len(h) / 16
	if strings.Contains(h, query) {
		score += 5
	}
	return score, true
}

// rankActions filters and sorts actions by fuzzy score against query. With an
// empty query the original generation order is preserved. Ties break on title
// length then alphabetical, so the result is deterministic (testable).
func rankActions(acts []action, query string) []action {
	if strings.TrimSpace(query) == "" {
		out := make([]action, len(acts))
		copy(out, acts)
		return out
	}
	var scored []scoredAction
	for _, a := range acts {
		hay := a.Search
		if hay == "" {
			hay = strings.ToLower(a.Title)
		}
		// Also fold the visible title in, so query words that only appear in
		// the title (e.g. a segment id) still match.
		if sc, ok := fuzzyScore(query, a.Title+" "+hay); ok {
			scored = append(scored, scoredAction{A: a, Score: sc})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if len(scored[i].A.Title) != len(scored[j].A.Title) {
			return len(scored[i].A.Title) < len(scored[j].A.Title)
		}
		return scored[i].A.Title < scored[j].A.Title
	})
	out := make([]action, len(scored))
	for i, s := range scored {
		out[i] = s.A
	}
	return out
}
