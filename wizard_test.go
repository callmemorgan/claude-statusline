package main

import (
	"reflect"
	"sort"
	"testing"
)

// testRegistry returns the built-in segment registry for deterministic
// assembly tests (no plugins).
func testRegistry() []segmentInfo {
	return allSegmentInfos()
}

func enabledSet(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestAssembleWizardConfig_DefaultChoices(t *testing.T) {
	cfg := assembleWizardConfig(defaultWizardChoices(), testRegistry())

	if len(cfg.Segments) == 0 {
		t.Fatal("default wizard choices produced an empty statusline")
	}
	// Every emitted segment must be a real registry ID.
	for _, id := range cfg.Segments {
		if _, ok := segByIDIn(testRegistry(), id); !ok {
			t.Errorf("segment %q is not in the registry", id)
		}
	}
	// Default-on categories include directory, git-branch, model, cost,
	// context-window. Spot-check a representative set is present.
	for _, want := range []string{"directory", "git-branch", "model", "cost", "context-window"} {
		if !contains(cfg.Segments, want) {
			t.Errorf("expected default config to contain %q; got %v", want, cfg.Segments)
		}
	}
	// Default density is balanced → no rate-limit segments pushed to line 4.
	for id, line := range cfg.Lines {
		if line == 4 {
			t.Errorf("balanced density should not place %q on line 4", id)
		}
	}
	// Rich git status is on by default → git-branch carries git_status=true.
	if v, ok := cfg.Settings["git-branch"]["git_status"]; !ok || v != true {
		t.Errorf("expected git-branch git_status=true; got %v (present=%v)", v, ok)
	}
}

func TestAssembleWizardConfig_EmptySelectionHidesEverything(t *testing.T) {
	choices := wizardChoices{Categories: map[string]bool{}, Density: densityBalanced}
	cfg := assembleWizardConfig(choices, testRegistry())

	// Empty selection means "hide everything": a non-nil empty slice, NOT nil
	// (nil would mean "defaults" on the render path).
	if cfg.Segments == nil {
		t.Fatal("empty selection produced nil Segments (would mean defaults); want []")
	}
	if len(cfg.Segments) != 0 {
		t.Errorf("empty selection should yield no segments; got %v", cfg.Segments)
	}
}

func TestAssembleWizardConfig_SegmentOrderFollowsCategoryOrder(t *testing.T) {
	// Enable git and project; project comes first in wizardCategories, so its
	// segments must precede git's.
	choices := wizardChoices{
		Categories: enabledSet("git", "project"),
		Density:    densityBalanced,
	}
	cfg := assembleWizardConfig(choices, testRegistry())

	idxDir := indexOf(cfg.Segments, "directory")
	idxBranch := indexOf(cfg.Segments, "git-branch")
	if idxDir < 0 || idxBranch < 0 {
		t.Fatalf("expected directory and git-branch present; got %v", cfg.Segments)
	}
	if idxDir > idxBranch {
		t.Errorf("project segments should precede git segments: %v", cfg.Segments)
	}
}

func TestAssembleWizardConfig_CompactCollapsesToOneLine(t *testing.T) {
	initSegments(nil)
	choices := wizardChoices{
		Categories: enabledSet("context"), // naturally line 3 segments
		Density:    densityCompact,
	}
	cfg := assembleWizardConfig(choices, testRegistry())

	for _, id := range cfg.Segments {
		if got := effectiveLine(id, cfg); got != 1 {
			t.Errorf("compact density: %q on line %d, want 1", id, got)
		}
	}
}

func TestAssembleWizardConfig_SpaciousPushesRateLimitsToLine4(t *testing.T) {
	// effectiveLine resolves natural lines through the global registry, so make
	// sure it's populated (built-ins, no plugins).
	initSegments(nil)
	choices := wizardChoices{
		Categories: enabledSet("context"),
		Density:    densitySpacious,
	}
	cfg := assembleWizardConfig(choices, testRegistry())

	for _, id := range []string{"rate-limit-5h", "rate-limit-7d"} {
		if !contains(cfg.Segments, id) {
			t.Fatalf("expected %q present in spacious context layout", id)
		}
		if got := effectiveLine(id, cfg); got != 4 {
			t.Errorf("spacious density: %q on line %d, want 4", id, got)
		}
	}
	// context-window keeps its natural line (3) under spacious.
	if got := effectiveLine("context-window", cfg); got != 3 {
		t.Errorf("spacious density: context-window on line %d, want 3", got)
	}
}

func TestAssembleWizardConfig_NoRedundantLineOverrides(t *testing.T) {
	// Balanced keeps natural lines, so cfg.Lines should hold no entry that
	// equals a segment's natural line.
	cfg := assembleWizardConfig(defaultWizardChoices(), testRegistry())
	for id, line := range cfg.Lines {
		nat := 1
		if s, ok := segByIDIn(testRegistry(), id); ok {
			nat = s.line
		}
		if line == nat {
			t.Errorf("redundant line override stored for %q (line==natural==%d)", id, line)
		}
	}
}

func TestAssembleWizardConfig_GitStatusOffOmitsSetting(t *testing.T) {
	choices := defaultWizardChoices()
	choices.GitStatus = false
	cfg := assembleWizardConfig(choices, testRegistry())

	if s, ok := cfg.Settings["git-branch"]; ok {
		if _, has := s["git_status"]; has {
			t.Errorf("git status off should not persist git_status key; got %v", s)
		}
	}
}

func TestAssembleWizardConfig_GitStatusIgnoredWhenGitNotSelected(t *testing.T) {
	choices := wizardChoices{
		Categories: enabledSet("model"),
		Density:    densityBalanced,
		GitStatus:  true, // but git category is off
	}
	cfg := assembleWizardConfig(choices, testRegistry())

	if contains(cfg.Segments, "git-branch") {
		t.Fatal("git-branch should not be present without the git category")
	}
	if _, ok := cfg.Settings["git-branch"]; ok {
		t.Errorf("no git-branch settings should be written when git is off; got %v", cfg.Settings["git-branch"])
	}
}

func TestAssembleWizardConfig_ThemeNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"classic", ""},
		{"original", ""},
		{"", ""},
		{"nord", "nord"},
	} {
		choices := defaultWizardChoices()
		choices.Theme = tc.in
		cfg := assembleWizardConfig(choices, testRegistry())
		if cfg.Theme != tc.want {
			t.Errorf("theme %q normalized to %q, want %q", tc.in, cfg.Theme, tc.want)
		}
	}
}

func TestAssembleWizardConfig_UpdateNoticeAppended(t *testing.T) {
	// Any non-empty selection appends the self-hiding update notice.
	cfg := assembleWizardConfig(defaultWizardChoices(), testRegistry())
	if !contains(cfg.Segments, "update") {
		t.Errorf("expected update notice appended to a non-empty layout; got %v", cfg.Segments)
	}
	// But not to an empty selection.
	empty := assembleWizardConfig(wizardChoices{Categories: map[string]bool{}}, testRegistry())
	if contains(empty.Segments, "update") {
		t.Error("update notice should not be appended to an empty layout")
	}
}

func TestAssembleWizardConfig_UnknownIDsDropped(t *testing.T) {
	// A registry missing git-stash should silently drop it from the git
	// category's contribution without erroring.
	var trimmed []segmentInfo
	for _, s := range allSegmentInfos() {
		if s.id == "git-stash" {
			continue
		}
		trimmed = append(trimmed, s)
	}
	choices := wizardChoices{Categories: enabledSet("git"), Density: densityBalanced}
	cfg := assembleWizardConfig(choices, trimmed)

	if contains(cfg.Segments, "git-stash") {
		t.Error("git-stash should be dropped when absent from the registry")
	}
	if !contains(cfg.Segments, "git-branch") {
		t.Errorf("git-branch should still be present; got %v", cfg.Segments)
	}
}

func TestAssembleWizardConfig_NoDuplicateSegments(t *testing.T) {
	// Enable every category; assert the emitted list has no duplicates.
	all := map[string]bool{}
	for _, c := range wizardCategories() {
		all[c.ID] = true
	}
	cfg := assembleWizardConfig(wizardChoices{Categories: all, Density: densityBalanced}, testRegistry())

	seen := map[string]bool{}
	for _, id := range cfg.Segments {
		if seen[id] {
			t.Errorf("duplicate segment %q in %v", id, cfg.Segments)
		}
		seen[id] = true
	}
}

func TestAssembledConfigRendersThroughPipeline(t *testing.T) {
	// The assembled config must drive buildStatusline without panicking and
	// produce a non-empty render with the sample payload (sanity that the
	// wizard output is render-valid).
	initSegments(nil)
	cfg := assembleWizardConfig(defaultWizardChoices(), registeredSegments)
	lines := buildStatusline(buildInput{
		P:     samplePayload(),
		C:     palette{}, // empty palette: color-free, like the golden tests
		Cfg:   cfg,
		Now:   testNow,
		State: previewState(testNow),
	})
	if len(lines) == 0 {
		t.Fatal("assembled config rendered no lines")
	}
	joined := ""
	for _, l := range lines {
		joined += l
	}
	if joined == "" {
		t.Fatal("assembled config rendered only blank lines")
	}
}

func TestDeriveWizardChoices_NilSegmentsGivesDefaults(t *testing.T) {
	got := deriveWizardChoices(config{Segments: nil})
	want := defaultWizardChoices()
	if !reflect.DeepEqual(sortedKeys(got.Categories), sortedKeys(want.Categories)) {
		t.Errorf("nil segments should give default categories; got %v want %v",
			sortedKeys(got.Categories), sortedKeys(want.Categories))
	}
	if got.Density != want.Density || got.GitStatus != want.GitStatus {
		t.Errorf("nil segments should give default density/git; got %+v want %+v", got, want)
	}
}

func TestDeriveWizardChoices_EmptySegmentsStaysEmpty(t *testing.T) {
	got := deriveWizardChoices(config{Segments: []string{}})
	if len(got.Categories) != 0 {
		t.Errorf("empty segments should yield empty categories; got %v", got.Categories)
	}
}

func TestDeriveWizardChoices_RoundTripsCustomConfig(t *testing.T) {
	initSegments(nil)
	// Build a config via the wizard, derive choices back, re-assemble, and
	// expect the same segment set (round-trip stability for re-running the
	// wizard over its own output).
	orig := assembleWizardConfig(wizardChoices{
		// Include context so the spacious line-4 spread is recoverable from
		// the line spread (density inference is best-effort and needs the
		// rate-limit segments present to detect line 4).
		Categories: enabledSet("git", "model", "cost", "context"),
		Density:    densitySpacious,
		Theme:      "nord",
		GitStatus:  true,
	}, registeredSegments)

	derived := deriveWizardChoices(orig)
	if derived.Theme != "nord" {
		t.Errorf("theme not recovered: %q", derived.Theme)
	}
	if derived.Density != densitySpacious {
		t.Errorf("density not recovered: %v", derived.Density)
	}
	if !derived.GitStatus {
		t.Error("git status not recovered")
	}
	reassembled := assembleWizardConfig(derived, registeredSegments)
	if !reflect.DeepEqual(sortedCopy(orig.Segments), sortedCopy(reassembled.Segments)) {
		t.Errorf("segment set drifted on round-trip:\n orig: %v\n re:   %v",
			sortedCopy(orig.Segments), sortedCopy(reassembled.Segments))
	}
}

// ─── helpers ─────────────────────────────────────────────────────────

func segByIDIn(reg []segmentInfo, id string) (segmentInfo, bool) {
	for _, s := range reg {
		if s.id == id {
			return s, true
		}
	}
	return segmentInfo{}, false
}

func contains(s []string, want string) bool { return indexOf(s, want) >= 0 }

func indexOf(s []string, want string) int {
	for i, v := range s {
		if v == want {
			return i
		}
	}
	return -1
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
