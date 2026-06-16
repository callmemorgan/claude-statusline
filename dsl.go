package main

// ─── Layout DSL ──────────────────────────────────────────────────────
//
// A small, line-oriented text language for describing a statusline layout.
// The buffer is the source of truth: it parses to a `config` and a `config`
// serializes back to a buffer (round-trip stable). It is *not* the on-disk
// format — that is always TOML via marshalConfigTOML. The DSL exists only as
// the editing surface for the `edit` mode.
//
// Grammar (informal):
//
//	buffer    := { line }
//	line      := directive | layout | blank | comment
//	directive := "#" key ":" value          // top-level config (theme, reflow…)
//	comment   := "#" ...                     // any other "#"-prefixed line, kept verbatim on re-serialize? No — comments are ignored on parse and regenerated on serialize.
//	layout    := token { WS token }          // one statusline render line, in order
//	token     := segment-id [ "[" overrides "]" ]
//	overrides := override { "," override }
//	override  := key "=" value               // key = a setting key, or "color"
//
// Each non-blank, non-"#" buffer line maps to ONE statusline render line, in
// top-to-bottom order. The first layout line is render line 1, the next is
// line 2, and so on (up to 9). Tokens on a line render left-to-right.
//
// Per-segment overrides go in [brackets]: setting keys from the segment's
// schema, plus the reserved pseudo-key `color` (the primary-color override).
// Values are bare words; bools accept true/false/on/off, ints are decimal,
// enum/color values are bare strings. Whitespace around "=" and "," is fine.
//
// Recognised directive keys: theme, reflow, separator, separator_custom,
// padding, color_depth. Unknown directives are reported as warnings.

import (
	"fmt"
	"strconv"
	"strings"
)

// dslDirectiveKeys is the set of top-level config keys settable via `# key: value`.
var dslDirectiveKeys = []string{"theme", "reflow", "separator", "separator_custom", "padding", "color_depth"}

// dslError is a single parse/lint diagnostic, anchored to a 1-based buffer line
// (0 = no specific line). Line is 1-based to match what an editor shows.
type dslError struct {
	Line int    // 1-based buffer line; 0 = global
	Col  int    // 1-based column within the line; 0 = whole line
	Msg  string // human-readable problem
}

func (e dslError) String() string {
	switch {
	case e.Line > 0 && e.Col > 0:
		return fmt.Sprintf("line %d:%d: %s", e.Line, e.Col, e.Msg)
	case e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	default:
		return e.Msg
	}
}

// ─── Serialize: config → buffer ──────────────────────────────────────

// configToDSL renders a config as an editable buffer. Top-level scalars become
// leading `# key: value` directives; segments are grouped by their effective
// render line (1..9) and emitted one render line per buffer line, in config
// order within each line. Per-segment color and non-default settings are
// emitted as [bracketed] overrides.
//
// Round-trip guarantee: parseDSL(configToDSL(cfg)) yields a config equal to cfg
// after both pass through validateConfig (the canonical normalization). The
// serializer only emits what the parser round-trips: it never writes a `line=`
// (line is positional) and never writes default-valued settings (pruned).
func configToDSL(cfg config) string {
	var b strings.Builder

	// Directives for the top-level scalars that the DSL owns.
	if cfg.Theme != "" {
		fmt.Fprintf(&b, "# theme: %s\n", cfg.Theme)
	}
	if r := cfg.Reflow; r != "" && r != "off" {
		fmt.Fprintf(&b, "# reflow: %s\n", r)
	}
	if cfg.ColorDepth != "" {
		fmt.Fprintf(&b, "# color_depth: %s\n", cfg.ColorDepth)
	}
	if cfg.Style.Separator != "" {
		fmt.Fprintf(&b, "# separator: %s\n", cfg.Style.Separator)
	}
	if cfg.Style.SeparatorCustom != "" {
		fmt.Fprintf(&b, "# separator_custom: %s\n", cfg.Style.SeparatorCustom)
	}
	if cfg.Style.Padding != nil {
		fmt.Fprintf(&b, "# padding: %d\n", *cfg.Style.Padding)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}

	// Group segment IDs by effective render line, preserving config order.
	// Lines with no segments still get an (empty) buffer line so positions
	// stay stable and the user can see the gap.
	byLine := map[int][]string{}
	maxLine := 0
	for _, id := range cfg.Segments {
		ln := effectiveLine(id, cfg)
		if ln < 1 {
			ln = 1
		}
		if ln > 9 {
			ln = 9
		}
		byLine[ln] = append(byLine[ln], id)
		if ln > maxLine {
			maxLine = ln
		}
	}

	for ln := 1; ln <= maxLine; ln++ {
		ids := byLine[ln]
		toks := make([]string, 0, len(ids))
		for _, id := range ids {
			toks = append(toks, serializeToken(id, cfg))
		}
		b.WriteString(strings.Join(toks, " "))
		b.WriteString("\n")
	}

	return b.String()
}

// serializeToken renders one segment with its color + non-default settings.
func serializeToken(id string, cfg config) string {
	var overrides []string
	if c, ok := cfg.Colors[id]; ok && c != "" {
		overrides = append(overrides, "color="+c)
	}
	if seg, ok := segmentByID(id); ok {
		pruned := pruneSettings(seg, settingsFor(cfg, seg))
		// Emit in schema order for stability.
		for _, sp := range seg.settings {
			if sp.Ephemeral {
				continue
			}
			if v, ok := pruned[sp.Key]; ok {
				overrides = append(overrides, sp.Key+"="+formatSettingValue(v))
			}
		}
	}
	if len(overrides) == 0 {
		return id
	}
	return id + "[" + strings.Join(overrides, ", ") + "]"
}

func formatSettingValue(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.Itoa(int(t))
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ─── Parse: buffer → config ──────────────────────────────────────────

// parseDSL parses a layout buffer into a config. It starts from an empty
// (explicit) segment list — the buffer is authoritative, so an empty buffer
// means "hide everything" ([]string{}, not nil). Diagnostics are collected for
// unknown segments, unknown/invalid overrides, and malformed directives; the
// parser is lenient (it builds the best config it can and reports problems).
//
// The returned config is NOT yet normalized — callers should run
// validateConfig on it (which also catches anything the DSL didn't).
func parseDSL(text string) (config, []dslError) {
	cfg := config{Segments: []string{}}
	var errs []dslError

	seen := map[string]int{} // segment id → first buffer line (duplicate detection)
	renderLine := 0          // increments per non-directive layout line

	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)

		if trimmed == "" {
			// Blank line: skip, does not consume a render line.
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if e, ok := parseDirective(trimmed, lineNo, &cfg); !ok && e.Msg != "" {
				errs = append(errs, e)
			}
			continue
		}

		// A layout line. Each consumes the next render line slot.
		renderLine++
		if renderLine > 9 {
			errs = append(errs, dslError{Line: lineNo, Msg: fmt.Sprintf("more than 9 layout lines; line %d ignored (statuslines have at most 9 lines)", renderLine)})
			continue
		}

		toks, tokenErrs := tokenizeLayoutLine(raw, lineNo)
		errs = append(errs, tokenErrs...)
		for _, tok := range toks {
			e := applyToken(tok, renderLine, lineNo, seen, &cfg)
			errs = append(errs, e...)
		}
	}

	return cfg, errs
}

// parseDirective handles a `# key: value` line, writing into cfg. Returns
// ok=true if it was a recognised directive (and applied). A bare comment
// (`#` with no `key:`) returns ok=false with an empty message (silently
// ignored). An unknown key returns ok=false with a warning message.
func parseDirective(line string, lineNo int, cfg *config) (dslError, bool) {
	body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	if body == "" {
		return dslError{}, false // bare "#" — a blank comment
	}
	idx := strings.Index(body, ":")
	if idx < 0 {
		// A free-form comment with no "key: value" shape — ignore silently.
		return dslError{}, false
	}
	key := strings.TrimSpace(body[:idx])
	val := strings.TrimSpace(body[idx+1:])
	switch key {
	case "theme":
		cfg.Theme = val
	case "reflow":
		cfg.Reflow = val
	case "color_depth":
		cfg.ColorDepth = val
	case "separator":
		cfg.Style.Separator = val
	case "separator_custom":
		cfg.Style.SeparatorCustom = val
	case "padding":
		n, err := strconv.Atoi(val)
		if err != nil {
			return dslError{Line: lineNo, Msg: fmt.Sprintf("padding %q is not a number", val)}, false
		}
		cfg.Style.Padding = &n
	default:
		return dslError{Line: lineNo, Msg: fmt.Sprintf("unknown directive %q (known: %s)", key, strings.Join(dslDirectiveKeys, ", "))}, false
	}
	return dslError{}, true
}

// dslToken is one parsed segment token: its id, raw override pairs, and the
// 1-based column where the id starts (for diagnostics/autocomplete).
type dslToken struct {
	id        string
	overrides []dslOverride
	col       int
}

type dslOverride struct {
	key string
	val string
	col int
}

// tokenizeLayoutLine splits a layout line into segment tokens. Tokens are
// whitespace-separated, except whitespace inside [brackets] is preserved so
// `seg[a=1, b=2]` stays one token. Unclosed brackets are reported.
func tokenizeLayoutLine(raw string, lineNo int) ([]dslToken, []dslError) {
	var toks []dslToken
	var errs []dslError
	runes := []rune(raw)
	i := 0
	for i < len(runes) {
		// Skip leading whitespace.
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		depth := 0
		for i < len(runes) {
			c := runes[i]
			if c == '[' {
				depth++
			} else if c == ']' {
				if depth > 0 {
					depth--
				}
			} else if (c == ' ' || c == '\t') && depth == 0 {
				break
			}
			i++
		}
		word := string(runes[start:i])
		if depth > 0 {
			errs = append(errs, dslError{Line: lineNo, Col: start + 1, Msg: fmt.Sprintf("unclosed '[' in token %q", word)})
		}
		tok, tokErrs := parseToken(word, lineNo, start+1)
		errs = append(errs, tokErrs...)
		toks = append(toks, tok)
	}
	return toks, errs
}

// parseToken parses one `id[overrides]` word into a dslToken. col is the
// 1-based column of the word's first rune.
func parseToken(word string, lineNo, col int) (dslToken, []dslError) {
	var errs []dslError
	tok := dslToken{col: col}
	br := strings.IndexByte(word, '[')
	if br < 0 {
		tok.id = strings.TrimSpace(word)
		return tok, errs
	}
	tok.id = strings.TrimSpace(word[:br])
	inner := word[br+1:]
	inner = strings.TrimSuffix(inner, "]")
	// Column where the bracket body begins.
	bodyCol := col + br + 1
	for _, part := range splitOverrides(inner) {
		if strings.TrimSpace(part.s) == "" {
			continue
		}
		eq := strings.IndexByte(part.s, '=')
		if eq < 0 {
			errs = append(errs, dslError{Line: lineNo, Col: bodyCol + part.off, Msg: fmt.Sprintf("override %q has no '=' (expected key=value)", strings.TrimSpace(part.s))})
			continue
		}
		k := strings.TrimSpace(part.s[:eq])
		v := strings.TrimSpace(part.s[eq+1:])
		tok.overrides = append(tok.overrides, dslOverride{key: k, val: v, col: bodyCol + part.off})
	}
	return tok, errs
}

type splitPart struct {
	s   string
	off int // offset of this part within the inner string
}

// splitOverrides splits a bracket body on commas, tracking each part's offset.
func splitOverrides(inner string) []splitPart {
	var parts []splitPart
	start := 0
	for i := 0; i < len(inner); i++ {
		if inner[i] == ',' {
			parts = append(parts, splitPart{s: inner[start:i], off: start})
			start = i + 1
		}
	}
	parts = append(parts, splitPart{s: inner[start:], off: start})
	return parts
}

// applyToken validates a token and writes it into cfg at the given render line.
// Unknown segments, duplicates, unknown override keys, and invalid values are
// reported; valid pieces are still applied where possible.
func applyToken(tok dslToken, renderLine, lineNo int, seen map[string]int, cfg *config) []dslError {
	var errs []dslError
	id := tok.id
	if id == "" {
		return errs
	}

	seg, known := segmentByID(id)
	if !known {
		errs = append(errs, dslError{Line: lineNo, Col: tok.col, Msg: fmt.Sprintf("unknown segment %q", id)})
		// Still add it so the buffer round-trips; validateConfig will warn and
		// the renderer skips it. But skip override resolution we can't validate.
		cfg.Segments = append(cfg.Segments, id)
		assignLine(cfg, id, renderLine)
		return errs
	}
	if first, dup := seen[id]; dup {
		errs = append(errs, dslError{Line: lineNo, Col: tok.col, Msg: fmt.Sprintf("segment %q already used on line %d (each segment may appear once)", id, first)})
		return errs
	}
	seen[id] = lineNo

	cfg.Segments = append(cfg.Segments, id)
	assignLine(cfg, id, renderLine)

	// Apply overrides: `color` is the reserved primary-color pseudo-key; every
	// other key must match a non-ephemeral settingSpec.
	settingVals := map[string]any{}
	for _, ov := range tok.overrides {
		if ov.key == "color" {
			if !validColorSpec(ov.val) {
				errs = append(errs, dslError{Line: lineNo, Col: ov.col, Msg: fmt.Sprintf("color %q is not a known color, theme role, hex value, or 256 index", ov.val)})
				continue
			}
			if ov.val == "" || ov.val == "default" {
				delete(cfg.Colors, id)
			} else {
				if cfg.Colors == nil {
					cfg.Colors = map[string]string{}
				}
				cfg.Colors[id] = ov.val
			}
			continue
		}
		sp, ok := specByKey(seg, ov.key)
		if !ok {
			errs = append(errs, dslError{Line: lineNo, Col: ov.col, Msg: fmt.Sprintf("segment %q has no setting %q", id, ov.key)})
			continue
		}
		parsed, perr := parseSettingValue(sp, ov.val)
		if perr != "" {
			errs = append(errs, dslError{Line: lineNo, Col: ov.col, Msg: fmt.Sprintf("setting %q: %s", ov.key, perr)})
			continue
		}
		settingVals[sp.Key] = sp.coerce(parsed)
	}

	// Merge resolved values with schema defaults, prune, and store.
	if len(settingVals) > 0 {
		full := settingsFor(*cfg, seg)
		for k, v := range settingVals {
			full[k] = v
		}
		setSegmentSettings(cfg, id, pruneSettings(seg, full))
	}
	return errs
}

// assignLine records a per-segment line override when it differs from the
// segment's natural line, mirroring the TUI (a line equal to natural is
// *deleted*, never stored).
func assignLine(cfg *config, id string, renderLine int) {
	natural := renderLine
	if seg, ok := segmentByID(id); ok {
		natural = seg.line
	}
	if renderLine == natural {
		delete(cfg.Lines, id)
		return
	}
	if cfg.Lines == nil {
		cfg.Lines = map[string]int{}
	}
	cfg.Lines[id] = renderLine
}

// specByKey finds a non-ephemeral settingSpec by key on a segment.
func specByKey(seg segmentInfo, key string) (settingSpec, bool) {
	for _, sp := range seg.settings {
		if sp.Ephemeral {
			continue
		}
		if sp.Key == key {
			return sp, true
		}
	}
	return settingSpec{}, false
}

// parseSettingValue converts a bare DSL value string to a typed value suitable
// for sp.coerce. Returns a non-empty error string on a clearly-wrong value
// (e.g. a non-numeric int, a non-option enum). The empty string means OK.
func parseSettingValue(sp settingSpec, val string) (any, string) {
	switch sp.Kind {
	case kindBool:
		switch strings.ToLower(val) {
		case "true", "on", "yes", "1":
			return true, ""
		case "false", "off", "no", "0":
			return false, ""
		default:
			return nil, fmt.Sprintf("%q is not a boolean (use true/false)", val)
		}
	case kindInt:
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Sprintf("%q is not a number", val)
		}
		return n, ""
	case kindEnum:
		for _, o := range sp.Options {
			if o == val {
				return val, ""
			}
		}
		return nil, fmt.Sprintf("%q is not one of: %s", val, strings.Join(sp.Options, ", "))
	case kindColor:
		if !validColorSpec(val) {
			return nil, fmt.Sprintf("%q is not a known color, theme role, hex value, or 256 index", val)
		}
		return val, ""
	}
	return val, ""
}

// ─── Autocomplete ────────────────────────────────────────────────────

// dslCompletion is one suggestion: the text to insert and a short label.
type dslCompletion struct {
	Text  string // the completion text (segment id, setting key, etc.)
	Label string // display hint (e.g. the description or kind)
}

// dslCompletions returns context-aware completions for the token under the
// cursor. The cursor context is derived from `prefix` — the text on the
// current line up to the cursor. It distinguishes three positions:
//
//   - inside a [bracket] before any '=' → complete setting keys (+ "color")
//     for the segment whose id opens the bracket
//   - inside a [bracket] right after a key's '=' → complete that setting's
//     enum/color options (or true/false for bools)
//   - otherwise → complete segment ids from the registry
//
// Matching is case-insensitive substring against the partial word.
func dslCompletions(prefix string) []dslCompletion {
	// Find the last unbracketed token boundary to locate the active word.
	openBr := strings.LastIndexByte(prefix, '[')
	closeBr := strings.LastIndexByte(prefix, ']')
	inBracket := openBr > closeBr

	if inBracket {
		return bracketCompletions(prefix, openBr)
	}

	// Segment-id completion: take the trailing word.
	word := trailingWord(prefix)
	return segmentCompletions(word)
}

// bracketCompletions completes setting keys or values inside a [bracket].
func bracketCompletions(prefix string, openBr int) []dslCompletion {
	// Resolve the segment id: the word immediately before '['.
	idPart := prefix[:openBr]
	segID := trailingWord(idPart)
	seg, ok := segmentByID(segID)
	if !ok {
		return nil
	}

	inner := prefix[openBr+1:]
	// The current override is everything after the last comma.
	if c := strings.LastIndexByte(inner, ','); c >= 0 {
		inner = inner[c+1:]
	}
	if eq := strings.IndexByte(inner, '='); eq >= 0 {
		// Completing a value for a known key.
		key := strings.TrimSpace(inner[:eq])
		partial := strings.TrimSpace(inner[eq+1:])
		return valueCompletions(seg, key, partial)
	}
	// Completing a key.
	partial := strings.TrimSpace(inner)
	return keyCompletions(seg, partial)
}

func keyCompletions(seg segmentInfo, partial string) []dslCompletion {
	p := strings.ToLower(partial)
	var out []dslCompletion
	if strings.Contains("color", p) {
		out = append(out, dslCompletion{Text: "color", Label: "primary color override"})
	}
	for _, sp := range seg.settings {
		if sp.Ephemeral {
			continue
		}
		if p == "" || strings.Contains(strings.ToLower(sp.Key), p) {
			out = append(out, dslCompletion{Text: sp.Key, Label: sp.Name})
		}
	}
	return out
}

func valueCompletions(seg segmentInfo, key, partial string) []dslCompletion {
	p := strings.ToLower(partial)
	match := func(s string) bool { return p == "" || strings.Contains(strings.ToLower(s), p) }
	if key == "color" {
		var out []dslCompletion
		for _, c := range colorCycle {
			if match(c) {
				out = append(out, dslCompletion{Text: c, Label: "color"})
			}
		}
		return out
	}
	sp, ok := specByKey(seg, key)
	if !ok {
		return nil
	}
	var out []dslCompletion
	switch sp.Kind {
	case kindBool:
		for _, v := range []string{"true", "false"} {
			if match(v) {
				out = append(out, dslCompletion{Text: v, Label: "bool"})
			}
		}
	case kindEnum, kindColor:
		for _, o := range sp.Options {
			if match(o) {
				out = append(out, dslCompletion{Text: o, Label: sp.Name})
			}
		}
	case kindInt:
		out = append(out, dslCompletion{Text: strconv.Itoa(sp.Default.(int)), Label: fmt.Sprintf("%d..%d (default)", sp.Min, sp.Max)})
	}
	return out
}

// segmentCompletions matches registered segment ids by substring.
func segmentCompletions(word string) []dslCompletion {
	w := strings.ToLower(strings.TrimSpace(word))
	var out []dslCompletion
	for _, s := range registeredSegments {
		if w == "" || strings.Contains(strings.ToLower(s.id), w) || strings.Contains(strings.ToLower(s.desc), w) {
			out = append(out, dslCompletion{Text: s.id, Label: s.desc})
		}
	}
	return out
}

// trailingWord returns the final whitespace-delimited word of s (possibly "").
func trailingWord(s string) string {
	i := len(s)
	for i > 0 {
		c := s[i-1]
		if c == ' ' || c == '\t' {
			break
		}
		i--
	}
	return s[i:]
}
