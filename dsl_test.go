package main

import (
	"reflect"
	"strings"
	"testing"
)

// normalize runs validateConfig the same way the editor save path does, so
// round-trip comparisons use the canonical (post-normalization) form.
func normalize(c config) config {
	validateConfig(&c)
	return c
}

// roundTrip serializes a config to DSL, parses it back, and normalizes both
// sides, returning the parsed config and the DSL text for diagnostics.
func roundTrip(t *testing.T, c config) (config, string) {
	t.Helper()
	text := configToDSL(c)
	got, errs := parseDSL(text)
	if len(errs) != 0 {
		t.Fatalf("parseDSL reported errors on serialized config:\n%s\nDSL:\n%s", joinDSLErrs(errs), text)
	}
	return normalize(got), text
}

func joinDSLErrs(errs []dslError) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("  " + e.String() + "\n")
	}
	return b.String()
}

// layoutByLine returns, for each render line 1..9, the ordered segment IDs that
// land on it. This is the layout's true identity: a positional/line-grouped DSL
// preserves order *within* a line, so the flat Segments slice may interleave
// lines differently while the rendered output is identical. The render path
// groups by line too, so equal per-line ordering ⇒ byte-identical render.
func layoutByLine(c config) map[int][]string {
	out := map[int][]string{}
	for _, id := range c.Segments {
		ln := effectiveLine(id, c)
		out[ln] = append(out[ln], id)
	}
	return out
}

func TestDSLRoundTripDefault(t *testing.T) {
	initSegments(nil)
	in := normalize(defaultConfig())
	got, text := roundTrip(t, in)
	// Order within each render line is preserved; the flat slice may interleave
	// lines differently (the DSL is line-grouped), so compare per-line layout.
	if !reflect.DeepEqual(layoutByLine(got), layoutByLine(in)) {
		t.Errorf("per-line layout differs after round-trip\n  in:  %v\n  got: %v\nDSL:\n%s", layoutByLine(in), layoutByLine(got), text)
	}
}

func TestDSLRoundTripRich(t *testing.T) {
	initSegments(nil)
	in := config{
		Theme:  "gruvbox",
		Reflow: "cascade",
		Segments: []string{
			"directory", "git-branch", "cost", // line 1
			"model", "duration", // line 2
			"context-window", "rate-limit-5h", // line 3
		},
		Lines: map[string]int{
			// move cost from line 1 to line 2 to exercise line overrides
			"cost": 2,
		},
		Colors: map[string]string{
			"directory": "cyan",
			"cost":      "#ff8800",
		},
		Settings: map[string]map[string]any{
			"git-branch":     {"git_status": true},
			"rate-limit-5h":  {"bar_width": 25, "show_countdown": false},
			"context-window": {"iconset": "blocks"},
		},
		Style: styleConfig{Separator: "dot"},
	}
	in = normalize(in)

	got, text := roundTrip(t, in)

	if !reflect.DeepEqual(layoutByLine(got), layoutByLine(in)) {
		t.Errorf("per-line layout differs\n  in:  %v\n  got: %v\nDSL:\n%s", layoutByLine(in), layoutByLine(got), text)
	}
	if !reflect.DeepEqual(got.Lines, in.Lines) {
		t.Errorf("lines differ\n  in:  %v\n  got: %v\nDSL:\n%s", in.Lines, got.Lines, text)
	}
	if !reflect.DeepEqual(got.Colors, in.Colors) {
		t.Errorf("colors differ\n  in:  %v\n  got: %v\nDSL:\n%s", in.Colors, got.Colors, text)
	}
	if !reflect.DeepEqual(got.Settings, in.Settings) {
		t.Errorf("settings differ\n  in:  %v\n  got: %v\nDSL:\n%s", in.Settings, got.Settings, text)
	}
	if got.Theme != in.Theme {
		t.Errorf("theme: got %q want %q", got.Theme, in.Theme)
	}
	if got.Reflow != in.Reflow {
		t.Errorf("reflow: got %q want %q", got.Reflow, in.Reflow)
	}
	if got.Style.Separator != in.Style.Separator {
		t.Errorf("separator: got %q want %q", got.Style.Separator, in.Style.Separator)
	}
}

// TestDSLRoundTripRendersIdentically proves the real invariant: a config and
// its DSL round-trip render byte-identically through buildStatusline, even
// though the flat Segments slice may be reordered by line grouping.
func TestDSLRoundTripRendersIdentically(t *testing.T) {
	initSegments(nil)
	in := normalize(defaultConfig())
	got, _ := roundTrip(t, in)

	p := samplePayload()
	now := testNow
	bIn := buildStatusline(buildInput{P: p, C: palette{}, Cfg: in, Width: 0, Now: now})
	bGot := buildStatusline(buildInput{P: p, C: palette{}, Cfg: got, Width: 0, Now: now})
	if !reflect.DeepEqual(bIn, bGot) {
		t.Errorf("render differs after round-trip\n  in:  %q\n  got: %q", bIn, bGot)
	}
}

func TestDSLRoundTripPadding(t *testing.T) {
	initSegments(nil)
	pad := 3
	in := normalize(config{
		Segments: []string{"directory", "cost"},
		Style:    styleConfig{Padding: &pad, Separator: "slash"},
	})
	got, _ := roundTrip(t, in)
	if got.Style.Padding == nil || *got.Style.Padding != 3 {
		t.Errorf("padding lost: got %v want 3", got.Style.Padding)
	}
	if got.Style.Separator != "slash" {
		t.Errorf("separator: got %q", got.Style.Separator)
	}
}

func TestDSLEmptyBufferHidesEverything(t *testing.T) {
	initSegments(nil)
	got, errs := parseDSL("\n\n   \n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %s", joinDSLErrs(errs))
	}
	if got.Segments == nil {
		t.Fatalf("empty buffer must yield explicit []string{} (hide all), got nil")
	}
	if len(got.Segments) != 0 {
		t.Errorf("expected 0 segments, got %v", got.Segments)
	}
}

func TestDSLLinePositions(t *testing.T) {
	initSegments(nil)
	// directory is naturally line 1, model naturally line 2; here we put them
	// on lines 1 and 2 respectively (no overrides), plus cost on line 3.
	got, errs := parseDSL("directory\nmodel\ncost\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %s", joinDSLErrs(errs))
	}
	got = normalize(got)
	if l := effectiveLine("directory", got); l != 1 {
		t.Errorf("directory line: got %d want 1", l)
	}
	if l := effectiveLine("model", got); l != 2 {
		t.Errorf("model line: got %d want 2", l)
	}
	if l := effectiveLine("cost", got); l != 3 {
		t.Errorf("cost line: got %d want 3", l)
	}
	// cost is naturally line 1, so a line-3 placement must be recorded.
	if got.Lines["cost"] != 3 {
		t.Errorf("expected cost line override 3, got %v", got.Lines)
	}
	// directory naturally line 1 → no override stored.
	if _, ok := got.Lines["directory"]; ok {
		t.Errorf("directory should not have a line override")
	}
}

func TestDSLUnknownSegment(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("directory nonsense-segment cost\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "unknown segment") && strings.Contains(e.Msg, "nonsense-segment") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unknown-segment diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLUnknownSetting(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("git-branch[nope=true]\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "no setting") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unknown-setting diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLInvalidSettingValue(t *testing.T) {
	initSegments(nil)
	// bar_width is an int 5..50; a non-number must be flagged.
	_, errs := parseDSL("rate-limit-5h[bar_width=huge]\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "bar_width") && strings.Contains(e.Msg, "not a number") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a non-numeric int diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLInvalidEnumValue(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("context-window[iconset=bogus]\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "iconset") && strings.Contains(e.Msg, "is not one of") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an invalid-enum diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLInvalidColor(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("cost[color=notacolor]\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "color") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an invalid-color diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLDuplicateSegment(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("cost\ncost\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "already used") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a duplicate-segment diagnostic, got: %s", joinDSLErrs(errs))
	}
}

// A repeated UNKNOWN id must also be flagged and de-duped (same as a known one),
// not silently appended twice.
func TestDSLDuplicateUnknownSegment(t *testing.T) {
	initSegments(nil)
	got, errs := parseDSL("nonsense-seg\nnonsense-seg\n")
	dup := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "already used") {
			dup = true
		}
	}
	if !dup {
		t.Errorf("expected a duplicate diagnostic for the repeated unknown id, got: %s", joinDSLErrs(errs))
	}
	count := 0
	for _, id := range got.Segments {
		if id == "nonsense-seg" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("unknown id should appear once, got %d copies: %v", count, got.Segments)
	}
}

// An unknown segment placed on a non-default line keeps that placement on
// round-trip (there is no natural line to fall back to, so it must be stored).
func TestDSLUnknownSegmentLinePreserved(t *testing.T) {
	initSegments(nil)
	got, _ := parseDSL("directory\nmodel\nnonsense-seg\n")
	if got.Lines["nonsense-seg"] != 3 {
		t.Errorf("expected unknown segment recorded on line 3, got %v", got.Lines)
	}
	if effectiveLine("nonsense-seg", got) != 3 {
		t.Errorf("unknown segment effectiveLine: got %d want 3", effectiveLine("nonsense-seg", got))
	}
	// And it survives a re-serialize → re-parse without drifting to line 1.
	got2, _ := parseDSL(configToDSL(got))
	if got2.Lines["nonsense-seg"] != 3 {
		t.Errorf("unknown segment line drifted on round-trip: %v\nDSL:\n%s", got2.Lines, configToDSL(got))
	}
}

func TestDSLUnclosedBracket(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("cost[color=cyan\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "unclosed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unclosed-bracket diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLDirectivesAndComments(t *testing.T) {
	initSegments(nil)
	in := "# theme: gruvbox\n# reflow: cascade\n# just a comment\n\ndirectory cost\n"
	got, errs := parseDSL(in)
	// "# just a comment" has no key:value shape → ignored, not an error.
	for _, e := range errs {
		t.Errorf("unexpected error: %s", e.String())
	}
	if got.Theme != "gruvbox" {
		t.Errorf("theme: got %q want gruvbox", got.Theme)
	}
	if got.Reflow != "cascade" {
		t.Errorf("reflow: got %q want cascade", got.Reflow)
	}
}

func TestDSLUnknownDirective(t *testing.T) {
	initSegments(nil)
	_, errs := parseDSL("# bogus: value\ndirectory\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "unknown directive") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unknown-directive diagnostic, got: %s", joinDSLErrs(errs))
	}
}

func TestDSLBoolSpellings(t *testing.T) {
	initSegments(nil)
	for _, spelling := range []string{"true", "on", "yes", "1"} {
		got, errs := parseDSL("git-branch[git_status=" + spelling + "]\n")
		if len(errs) != 0 {
			t.Fatalf("%q: unexpected errors: %s", spelling, joinDSLErrs(errs))
		}
		if got.Settings["git-branch"]["git_status"] != true {
			t.Errorf("%q: expected git_status=true, got %v", spelling, got.Settings["git-branch"])
		}
	}
}

func TestDSLCompletionsSegmentIDs(t *testing.T) {
	initSegments(nil)
	cs := dslCompletions("directory git-bra")
	found := false
	for _, c := range cs {
		if c.Text == "git-branch" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected git-branch in completions, got %v", cs)
	}
}

func TestDSLCompletionsSettingKeys(t *testing.T) {
	initSegments(nil)
	cs := dslCompletions("git-branch[git_st")
	found := false
	for _, c := range cs {
		if c.Text == "git_status" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected git_status key completion, got %v", cs)
	}
	// "color" pseudo-key should also be offered when the partial matches.
	cs = dslCompletions("cost[col")
	found = false
	for _, c := range cs {
		if c.Text == "color" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected color pseudo-key completion, got %v", cs)
	}
}

func TestDSLCompletionsValues(t *testing.T) {
	initSegments(nil)
	cs := dslCompletions("context-window[iconset=")
	found := false
	for _, c := range cs {
		if c.Text == "blocks" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected enum value completions for iconset, got %v", cs)
	}
	// bool value completions
	cs = dslCompletions("git-branch[git_status=")
	got := map[string]bool{}
	for _, c := range cs {
		got[c.Text] = true
	}
	if !got["true"] || !got["false"] {
		t.Errorf("expected true/false completions for bool, got %v", cs)
	}
}

func TestDSLTooManyLines(t *testing.T) {
	initSegments(nil)
	// 10 layout lines; the 10th must be flagged.
	lines := []string{
		"vim-mode", "sandbox", "session-name", "agent-state", "directory",
		"added-dirs", "git-branch", "artifact-count", "lines-changed", "cache-percent",
	}
	_, errs := parseDSL(strings.Join(lines, "\n") + "\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "more than 9") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a >9-lines diagnostic, got: %s", joinDSLErrs(errs))
	}
}

// TestDSLRoundTripPreservesLineGap checks that an empty render line (a gap
// between lines 1 and 3) survives a config → DSL → config round-trip instead
// of collapsing line 3 down to line 2.
func TestDSLRoundTripPreservesLineGap(t *testing.T) {
	initSegments(nil)
	in := normalize(config{
		Segments: []string{"directory", "cost"},
		Lines:    map[string]int{"cost": 3},
	})
	got, text := roundTrip(t, in)

	if effectiveLine("directory", got) != 1 {
		t.Errorf("directory effectiveLine: got %d want 1", effectiveLine("directory", got))
	}
	if effectiveLine("cost", got) != 3 {
		t.Errorf("cost effectiveLine: got %d want 3\nDSL:\n%s", effectiveLine("cost", got), text)
	}
	if got.Lines["cost"] != 3 {
		t.Errorf("cost line override lost: got %v\nDSL:\n%s", got.Lines, text)
	}
	// The DSL must contain an explicit blank line for the gap.
	if !strings.Contains(text, "\n\n") {
		t.Errorf("DSL should contain a blank line for the gap:\n%s", text)
	}
}

// TestDSLCRLFNoCorruption ensures trailing \r characters do not end up inside
// tokens or bracket values.
func TestDSLCRLFNoCorruption(t *testing.T) {
	initSegments(nil)
	got, errs := parseDSL("directory\r\ncost[color=cyan]\r\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %s", joinDSLErrs(errs))
	}
	if effectiveLine("directory", got) != 1 {
		t.Errorf("directory line: got %d want 1", effectiveLine("directory", got))
	}
	if got.Colors["cost"] != "cyan" {
		t.Errorf("cost color: got %q want cyan", got.Colors["cost"])
	}
}

// TestDSLTrailingJunkAfterBracket rejects text after a closing ']'.
func TestDSLTrailingJunkAfterBracket(t *testing.T) {
	initSegments(nil)
	got, errs := parseDSL("cost[color=cyan]junk\n")
	found := false
	for _, e := range errs {
		if strings.Contains(e.Msg, "trailing text after ']") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a trailing-junk diagnostic, got: %s", joinDSLErrs(errs))
	}
	// The leading segment id should still be parsed so the token is not lost.
	if !contains(got.Segments, "cost") {
		t.Errorf("expected segment %q to be parsed despite the trailing junk, got %v", "cost", got.Segments)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
