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

// dslDirective is one `# key: value` top-level scalar, defined once so the
// serializer (get) and parser (set) can't drift: adding a directive is a single
// table entry that both halves loop over.
type dslDirective struct {
	key string
	// get returns the value to emit and whether to emit it at all (defaults and
	// empties are skipped so the buffer only shows what round-trips).
	get func(config) (string, bool)
	// set writes a parsed value into cfg, returning an error message if the
	// value is malformed (e.g. non-numeric padding). Empty msg = ok.
	set func(*config, string) string
}

// dslDirectives is the authoritative list of recognised directives, in the
// order configToDSL emits them.
var dslDirectives = []dslDirective{
	{"theme", func(c config) (string, bool) { return c.Theme, c.Theme != "" },
		func(c *config, v string) string { c.Theme = v; return "" }},
	{"reflow", func(c config) (string, bool) { return c.Reflow, c.Reflow != "" && c.Reflow != "off" },
		func(c *config, v string) string { c.Reflow = v; return "" }},
	{"color_depth", func(c config) (string, bool) { return c.ColorDepth, c.ColorDepth != "" },
		func(c *config, v string) string { c.ColorDepth = v; return "" }},
	{"separator", func(c config) (string, bool) { return c.Style.Separator, c.Style.Separator != "" },
		func(c *config, v string) string { c.Style.Separator = v; return "" }},
	{"separator_custom", func(c config) (string, bool) { return c.Style.SeparatorCustom, c.Style.SeparatorCustom != "" },
		func(c *config, v string) string { c.Style.SeparatorCustom = v; return "" }},
	{"padding",
		func(c config) (string, bool) {
			if c.Style.Padding == nil {
				return "", false
			}
			return strconv.Itoa(*c.Style.Padding), true
		},
		func(c *config, v string) string {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Sprintf("padding %q is not a number", v)
			}
			c.Style.Padding = &n
			return ""
		}},
}

// dslDirectiveKeys lists the recognised directive keys (for diagnostics).
func dslDirectiveKeys() []string {
	keys := make([]string, len(dslDirectives))
	for i, d := range dslDirectives {
		keys[i] = d.key
	}
	return keys
}

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
	for _, d := range dslDirectives {
		if v, ok := d.get(cfg); ok {
			fmt.Fprintf(&b, "# %s: %s\n", d.key, v)
		}
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
	layoutStarted := false   // true once we've seen the first layout line

	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		lineNo := i + 1
		// Trim trailing carriage returns so CRLF does not corrupt bracket parsing.
		raw = strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(raw)

		if trimmed == "" {
			// A blank line before the first layout line is just a directive
			// separator; once layout lines have started, blanks are explicit
			// empty render lines and must consume a line slot to keep gaps
			// round-trip stable.
			if layoutStarted {
				renderLine++
				if renderLine > 9 {
					errs = append(errs, dslError{Line: lineNo, Msg: fmt.Sprintf("more than 9 layout lines; line %d ignored (statuslines have at most 9 lines)", renderLine)})
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if e, ok := parseDirective(trimmed, lineNo, &cfg); !ok && e.Msg != "" {
				errs = append(errs, e)
			}
			continue
		}

		// A layout line. Each consumes the next render line slot.
		layoutStarted = true
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
	for _, d := range dslDirectives {
		if d.key != key {
			continue
		}
		if msg := d.set(cfg, val); msg != "" {
			return dslError{Line: lineNo, Msg: msg}, false
		}
		return dslError{}, true
	}
	return dslError{Line: lineNo, Msg: fmt.Sprintf("unknown directive %q (known: %s)", key, strings.Join(dslDirectiveKeys(), ", "))}, false
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
// 1-based column of the word's first rune. All offsets are tracked in runes so
// reported columns line up with the (rune-based) columns tokenizeLayoutLine
// produces, even when the line contains multibyte runes.
func parseToken(word string, lineNo, col int) (dslToken, []dslError) {
	var errs []dslError
	tok := dslToken{col: col}
	wr := []rune(word)
	br := runeIndex(wr, '[')
	if br < 0 {
		tok.id = strings.TrimSpace(word)
		return tok, errs
	}
	tok.id = strings.TrimSpace(string(wr[:br]))
	closeIdx := runeIndex(wr[br+1:], ']')
	closeIdxAbs := len(wr) // default: parse everything after '[' if unclosed
	if closeIdx >= 0 {
		closeIdxAbs = br + 1 + closeIdx
		if closeIdxAbs != len(wr)-1 {
			errs = append(errs, dslError{Line: lineNo, Col: col + closeIdxAbs, Msg: fmt.Sprintf("trailing text after ']' in token %q", word)})
		}
	}
	inner := wr[br+1 : closeIdxAbs]
	// Column where the bracket body begins (rune-based).
	bodyCol := col + br + 1
	for _, part := range splitOverrides(inner) {
		s := strings.TrimSpace(string(part.s))
		if s == "" {
			continue
		}
		eq := runeIndex(part.s, '=')
		if eq < 0 {
			errs = append(errs, dslError{Line: lineNo, Col: bodyCol + part.off, Msg: fmt.Sprintf("override %q has no '=' (expected key=value)", s)})
			continue
		}
		k := strings.TrimSpace(string(part.s[:eq]))
		v := strings.TrimSpace(string(part.s[eq+1:]))
		tok.overrides = append(tok.overrides, dslOverride{key: k, val: v, col: bodyCol + part.off})
	}
	return tok, errs
}

// runeIndex returns the index of the first occurrence of r in rs, or -1.
func runeIndex(rs []rune, r rune) int {
	for i, c := range rs {
		if c == r {
			return i
		}
	}
	return -1
}

type splitPart struct {
	s   []rune
	off int // rune offset of this part within the inner slice
}

// splitOverrides splits a bracket body on commas, tracking each part's rune
// offset (so column diagnostics are correct for multibyte input).
func splitOverrides(inner []rune) []splitPart {
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

	// Duplicate detection covers unknown ids too: appending the same id twice
	// would round-trip as two identical tokens (and validateConfig keeps unknown
	// ids), so flag the repeat once and drop it.
	if first, dup := seen[id]; dup {
		errs = append(errs, dslError{Line: lineNo, Col: tok.col, Msg: fmt.Sprintf("segment %q already used on line %d (each segment may appear once)", id, first)})
		return errs
	}
	seen[id] = lineNo

	seg, known := segmentByID(id)
	if !known {
		errs = append(errs, dslError{Line: lineNo, Col: tok.col, Msg: fmt.Sprintf("unknown segment %q", id)})
		// Still add it so the buffer round-trips; validateConfig will warn and
		// the renderer skips it. But skip override resolution we can't validate.
		cfg.Segments = append(cfg.Segments, id)
		assignLine(cfg, id, renderLine)
		return errs
	}

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
// *deleted*, never stored). For an unknown segment there is no natural line to
// compare against, so the explicit placement is always stored — otherwise the
// buffer position would be lost on re-serialize (an unknown id on line 3 would
// drift back to line 1).
func assignLine(cfg *config, id string, renderLine int) {
	seg, known := segmentByID(id)
	if known && renderLine == seg.line {
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
	ctx := bracketContext(prefix)
	if !ctx.inBracket {
		// Segment-id completion: take the trailing word.
		return segmentCompletions(ctx.partial)
	}
	seg, ok := segmentByID(ctx.segID)
	if !ok {
		return nil
	}
	if ctx.key != "" {
		// Completing a value for a known key.
		return valueCompletions(seg, ctx.key, ctx.partial)
	}
	// Completing a setting key.
	return keyCompletions(seg, ctx.partial)
}

// bracketCtx is the cursor context derived from a line prefix: whether the
// cursor sits inside an open `[bracket]`, and what it's completing.
type bracketCtx struct {
	inBracket bool   // cursor is inside an unclosed '['
	segID     string // segment id that opened the bracket (in-bracket only)
	key       string // setting key being valued, when past a '=' (else "")
	partial   string // the partial word/value under the cursor
}

// bracketContext is the single source of truth for "where is the cursor". Both
// dslCompletions (which suggestions to offer) and the editor's insert path
// (which partial word to replace) consume it, so the two stay in sync.
//
// `partial` is only left-trimmed: the insert path replaces exactly `partial`
// before the cursor, so any whitespace the user typed up to the cursor must be
// part of it. Completion matchers lower/compare and tolerate a stray trailing
// space, so this is safe for both consumers.
func bracketContext(prefix string) bracketCtx {
	openBr := strings.LastIndexByte(prefix, '[')
	closeBr := strings.LastIndexByte(prefix, ']')
	if openBr <= closeBr {
		// Outside any bracket: completing a segment id (trailing word).
		return bracketCtx{partial: trailingWord(prefix)}
	}

	ctx := bracketCtx{inBracket: true, segID: trailingWord(prefix[:openBr])}
	inner := prefix[openBr+1:]
	// The current override is everything after the last comma.
	if c := strings.LastIndexByte(inner, ','); c >= 0 {
		inner = inner[c+1:]
	}
	if eq := strings.IndexByte(inner, '='); eq >= 0 {
		ctx.key = strings.TrimSpace(inner[:eq])
		ctx.partial = strings.TrimLeft(inner[eq+1:], " \t")
	} else {
		ctx.partial = strings.TrimLeft(inner, " \t")
	}
	return ctx
}

func keyCompletions(seg segmentInfo, partial string) []dslCompletion {
	p := strings.ToLower(strings.TrimSpace(partial))
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
	p := strings.ToLower(strings.TrimSpace(partial))
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
		// Default for a kindInt spec is an int by convention, but don't panic if
		// a future spec ships a differently-typed default; coerce normalizes it.
		def, _ := sp.coerce(sp.Default).(int)
		out = append(out, dslCompletion{Text: strconv.Itoa(def), Label: fmt.Sprintf("%d..%d (default)", sp.Min, sp.Max)})
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
