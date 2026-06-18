package main

import (
	"strings"
	"testing"
)

// findAction returns the first action whose Title equals title.
func findAction(acts []action, title string) (action, bool) {
	for _, a := range acts {
		if a.Title == title {
			return a, true
		}
	}
	return action{}, false
}

func containsTitle(acts []action, title string) bool {
	_, ok := findAction(acts, title)
	return ok
}

func TestGenerateActionsCoversRegistry(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)

	if len(acts) == 0 {
		t.Fatal("generateActions returned no actions")
	}

	// Every registered segment must contribute an enable-or-disable command.
	for _, seg := range registeredSegments {
		hasToggle := containsTitle(acts, "Enable "+seg.id) || containsTitle(acts, "Disable "+seg.id)
		if !hasToggle {
			t.Errorf("no enable/disable action for segment %q", seg.id)
		}
		// A "move to line N" must exist for at least one line != natural.
		hasMove := false
		hasColorPicker := false
		for _, a := range acts {
			if strings.HasPrefix(a.Title, "Move "+seg.id+" to line ") {
				hasMove = true
			}
			if a.Title == "Set "+seg.id+" color (picker)" {
				hasColorPicker = true
			}
		}
		if !hasMove {
			t.Errorf("no move-to-line action for segment %q", seg.id)
		}
		if !hasColorPicker {
			t.Errorf("no color-picker action for segment %q", seg.id)
		}
	}
}

func TestGenerateActionsTogglesVerbByState(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig() // "cost" is enabled by default

	acts := generateActions(cfg)
	if !containsTitle(acts, "Disable cost") {
		t.Error("enabled segment should offer Disable")
	}
	if containsTitle(acts, "Enable cost") {
		t.Error("enabled segment should not also offer Enable")
	}

	// Disable cost, regenerate, and the verb must flip.
	a, ok := findAction(acts, "Disable cost")
	if !ok {
		t.Fatal("missing Disable cost")
	}
	a.Apply(&cfg)
	acts2 := generateActions(cfg)
	if !containsTitle(acts2, "Enable cost") {
		t.Error("disabled segment should offer Enable")
	}
	if containsTitle(acts2, "Disable cost") {
		t.Error("disabled segment should not also offer Disable")
	}
}

func TestGenerateActionsSettingCommands(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)

	// context-window has a bool (show_bar), an int (bar_width), an enum
	// (iconset), and color settings — each should produce commands.
	if !containsTitle(acts, "Toggle context-window · Show bar") {
		t.Error("missing bool toggle for context-window show_bar")
	}
	if !containsTitle(acts, "Increase context-window · Bar width") {
		t.Error("missing int increase for context-window bar_width")
	}
	if !containsTitle(acts, "Decrease context-window · Bar width") {
		t.Error("missing int decrease for context-window bar_width")
	}
	// Enum: one command per iconset option.
	foundEnum := false
	for _, a := range acts {
		if strings.HasPrefix(a.Title, "Set context-window · Iconset → ") {
			foundEnum = true
			break
		}
	}
	if !foundEnum {
		t.Error("missing enum set commands for context-window iconset")
	}
}

func TestSettingCommandActuallyMutates(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)

	a, ok := findAction(acts, "Increase context-window · Bar width")
	if !ok {
		t.Fatal("missing increase action")
	}
	before := settingsFor(cfg, mustSeg(t, "context-window")).Int("bar_width")
	a.Apply(&cfg)
	after := settingsFor(cfg, mustSeg(t, "context-window")).Int("bar_width")
	if after <= before {
		t.Errorf("bar_width should increase: before=%d after=%d", before, after)
	}

	// Persisted value must be pruned (only non-default keys present).
	if _, ok := cfg.Settings["context-window"]; !ok {
		t.Error("expected context-window settings to be recorded")
	}
}

func TestEphemeralSettingsExcluded(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)
	for _, a := range acts {
		if strings.Contains(a.Title, "Stress test") || strings.Contains(a.Title, "Sync to all") {
			t.Errorf("ephemeral setting leaked into palette: %q", a.Title)
		}
	}
}

func TestMoveLineExcludesCurrent(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	// context-window's natural line is 3.
	acts := generateActions(cfg)
	if containsTitle(acts, "Move context-window to line 3") {
		t.Error("should not offer to move a segment to the line it's already on")
	}
	if !containsTitle(acts, "Move context-window to line 1") {
		t.Error("should offer to move context-window to line 1")
	}

	a, _ := findAction(acts, "Move context-window to line 1")
	a.Apply(&cfg)
	if effectiveLine("context-window", cfg) != 1 {
		t.Errorf("expected line 1, got %d", effectiveLine("context-window", cfg))
	}
	// Moving back to natural line clears the override.
	acts2 := generateActions(cfg)
	a2, ok := findAction(acts2, "Move context-window to line 3")
	if !ok {
		t.Fatal("after moving away, should offer to move back to natural line 3")
	}
	a2.Apply(&cfg)
	if _, present := cfg.Lines["context-window"]; present {
		t.Error("moving to natural line should delete the override")
	}
}

func TestGlobalActions(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)

	if !containsTitle(acts, "Theme → nord") {
		t.Error("missing theme action")
	}
	if !containsTitle(acts, "Apply preset → zen") {
		t.Error("missing preset action")
	}
	if !containsTitle(acts, "Reflow → group") {
		t.Error("missing reflow action")
	}
	if !containsTitle(acts, "Reset to defaults") {
		t.Error("missing reset action")
	}
	if !containsTitle(acts, "Save configuration") {
		t.Error("missing save action")
	}

	resetAct, ok := findAction(acts, "Reset to defaults")
	if !ok || resetAct.Kind != actionConfirm {
		t.Error("reset action should require confirmation")
	}
	saveAct, ok := findAction(acts, "Save configuration")
	if !ok || saveAct.Kind != actionSave {
		t.Error("save action should be actionSave")
	}

	// Theme classic clears cfg.Theme.
	a, _ := findAction(acts, "Theme → classic")
	cfg.Theme = "nord"
	a.Apply(&cfg)
	if cfg.Theme != "" {
		t.Errorf("classic theme should clear cfg.Theme, got %q", cfg.Theme)
	}

	// Preset action applies the preset's segments.
	cfg2 := defaultConfig()
	pa, _ := findAction(generateActions(cfg2), "Apply preset → minimal")
	pa.Apply(&cfg2)
	if len(cfg2.Segments) == 0 || cfg2.Segments[0] != "directory" {
		t.Errorf("preset minimal not applied: %v", cfg2.Segments)
	}
}

func TestSetSettingFromTextValidation(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	seg := mustSeg(t, "context-window")

	var intSp settingSpec
	var enumSp settingSpec
	var colorSp settingSpec
	for _, s := range seg.settings {
		switch s.Key {
		case "bar_width":
			intSp = s
		case "iconset":
			enumSp = s
		case "ok_color":
			colorSp = s
		}
	}
	if intSp.Key == "" {
		t.Fatal("bar_width spec not found")
	}
	if enumSp.Key == "" {
		t.Fatal("iconset spec not found")
	}
	if colorSp.Key == "" {
		t.Fatal("ok_color spec not found")
	}

	if err := setSettingFromText(&cfg, "context-window", intSp, "abc"); err == nil {
		t.Error("expected error for non-numeric input")
	}
	if err := setSettingFromText(&cfg, "context-window", intSp, "9999"); err == nil {
		t.Error("expected out-of-bounds error")
	}
	if err := setSettingFromText(&cfg, "context-window", intSp, "  20 "); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
	if got := settingsFor(cfg, seg).Int("bar_width"); got != 20 {
		t.Errorf("bar_width = %d, want 20", got)
	}

	// Enum: invalid option must surface an error, not silently fall back.
	if err := setSettingFromText(&cfg, "context-window", enumSp, "not-an-iconset"); err == nil {
		t.Error("expected error for invalid enum value")
	}
	if err := setSettingFromText(&cfg, "context-window", enumSp, "ascii"); err != nil {
		t.Errorf("valid enum rejected: %v", err)
	}

	// Color: invalid spec must surface an error, not silently fall back.
	if err := setSettingFromText(&cfg, "context-window", colorSp, "not-a-color"); err == nil {
		t.Error("expected error for invalid color spec")
	}
	if err := setSettingFromText(&cfg, "context-window", colorSp, "#ff0000"); err != nil {
		t.Errorf("valid color rejected: %v", err)
	}
}

func TestParseIntStrict(t *testing.T) {
	cases := []struct {
		in   string
		want int
		err  bool
	}{
		{"0", 0, false},
		{"42", 42, false},
		{"+7", 7, false},
		{"-3", -3, false},
		{"", 0, true},
		{"12a", 0, true},
		{"a", 0, true},
		{"+", 0, true},
		{"1.5", 0, true},
	}
	for _, c := range cases {
		got, err := parseIntStrict(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseIntStrict(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseIntStrict(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseIntStrict(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReorderSegmentBoundaries(t *testing.T) {
	initSegments(nil)

	// Line 1: directory, git-branch (in that order).
	cfg := config{Segments: []string{"directory", "git-branch"}}

	// directory is already first; moving left is a no-op.
	reorderSegment(&cfg, "directory", -1)
	if cfg.Segments[0] != "directory" || cfg.Segments[1] != "git-branch" {
		t.Errorf("left boundary reorder should be no-op: %v", cfg.Segments)
	}

	// git-branch is already last; moving right is a no-op.
	reorderSegment(&cfg, "git-branch", +1)
	if cfg.Segments[0] != "directory" || cfg.Segments[1] != "git-branch" {
		t.Errorf("right boundary reorder should be no-op: %v", cfg.Segments)
	}

	// A valid reorder still works.
	reorderSegment(&cfg, "git-branch", -1)
	if cfg.Segments[0] != "git-branch" || cfg.Segments[1] != "directory" {
		t.Errorf("expected swap, got %v", cfg.Segments)
	}
}

func TestSetSegmentColorEnablesSegment(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	setSegmentEnabled(&cfg, "cost", false)
	if contains(cfg.Segments, "cost") {
		t.Fatal("cost should be disabled before test")
	}

	setSegmentColor(&cfg, "cost", "red")
	if !contains(cfg.Segments, "cost") {
		t.Error("setSegmentColor should enable the segment so the color is visible")
	}
	if cfg.Colors["cost"] != "red" {
		t.Errorf("expected color override red, got %q", cfg.Colors["cost"])
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

// ─── Fuzzy matching ──────────────────────────────────────────────────

func TestFuzzyScoreSubsequence(t *testing.T) {
	if _, ok := fuzzyScore("dscst", "disable cost"); !ok {
		t.Error("expected 'dscst' to subsequence-match 'disable cost'")
	}
	if _, ok := fuzzyScore("xyz", "disable cost"); ok {
		t.Error("expected 'xyz' to NOT match 'disable cost'")
	}
	if _, ok := fuzzyScore("", "anything"); !ok {
		t.Error("empty query should match")
	}
}

func TestFuzzyScoreMultibyte(t *testing.T) {
	// Segment ids/descriptions may contain multibyte runes; matching must index
	// by rune, not byte, or it will skip/misalign characters.
	if _, ok := fuzzyScore("中文", "中文测试"); !ok {
		t.Error("expected '中文' to match '中文测试'")
	}
	if _, ok := fuzzyScore("éco", "économie"); !ok {
		t.Error("expected 'éco' to match 'économie'")
	}
	if _, ok := fuzzyScore("禁用", "enable 禁用 cost"); !ok {
		t.Error("expected multibyte query to match mixed haystack")
	}
	if _, ok := fuzzyScore("中不", "中文测试"); ok {
		t.Error("expected non-subsequence '中不' not to match '中文测试'")
	}
}

func TestFuzzyScoreBoundaryBonus(t *testing.T) {
	// "cost" should score higher against "disable cost" (word-boundary start)
	// than against "lines-changed cost-detail" where the 'c' is mid-word? Use a
	// clearer pair: boundary match beats interior match.
	boundary, ok1 := fuzzyScore("cw", "context window")
	interior, ok2 := fuzzyScore("cw", "scwx blob")
	if !ok1 || !ok2 {
		t.Fatal("both should match")
	}
	if boundary <= interior {
		t.Errorf("word-boundary match (%d) should outscore interior (%d)", boundary, interior)
	}
}

func TestFuzzyScoreShorterWins(t *testing.T) {
	short, _ := fuzzyScore("cost", "cost")
	long, _ := fuzzyScore("cost", "cost rate burn over recent session history window")
	if short <= long {
		t.Errorf("shorter exact haystack (%d) should beat longer (%d)", short, long)
	}
}

func TestRankActionsOrdersByIntent(t *testing.T) {
	initSegments(nil)
	cfg := defaultConfig()
	acts := generateActions(cfg)

	ranked := rankActions(acts, "disable cost")
	if len(ranked) == 0 {
		t.Fatal("no ranked results")
	}
	// "Disable cost" should be at or very near the top for that query.
	topN := ranked
	if len(topN) > 5 {
		topN = topN[:5]
	}
	found := false
	for _, a := range topN {
		if a.Title == "Disable cost" {
			found = true
			break
		}
	}
	if !found {
		var titles []string
		for _, a := range topN {
			titles = append(titles, a.Title)
		}
		t.Errorf("'Disable cost' not in top 5 for query 'disable cost'; got %v", titles)
	}
}

func TestRankActionsThemeQuery(t *testing.T) {
	initSegments(nil)
	acts := generateActions(defaultConfig())
	ranked := rankActions(acts, "theme nord")
	if len(ranked) == 0 {
		t.Fatal("no results")
	}
	if ranked[0].Title != "Theme → nord" {
		t.Errorf("top result for 'theme nord' = %q, want 'Theme → nord'", ranked[0].Title)
	}
}

func TestRankActionsEmptyQueryPreservesOrder(t *testing.T) {
	initSegments(nil)
	acts := generateActions(defaultConfig())
	ranked := rankActions(acts, "")
	if len(ranked) != len(acts) {
		t.Fatalf("empty query dropped actions: %d vs %d", len(ranked), len(acts))
	}
	for i := range acts {
		if ranked[i].Title != acts[i].Title {
			t.Errorf("empty query reordered at %d: %q vs %q", i, ranked[i].Title, acts[i].Title)
		}
	}
}

func TestRankActionsDeterministic(t *testing.T) {
	initSegments(nil)
	acts := generateActions(defaultConfig())
	a := rankActions(acts, "color")
	b := rankActions(acts, "color")
	if len(a) != len(b) {
		t.Fatal("nondeterministic length")
	}
	for i := range a {
		if a[i].Title != b[i].Title {
			t.Errorf("nondeterministic order at %d: %q vs %q", i, a[i].Title, b[i].Title)
		}
	}
}

func mustSeg(t *testing.T, id string) segmentInfo {
	t.Helper()
	s, ok := segmentByID(id)
	if !ok {
		t.Fatalf("segment %q not registered", id)
	}
	return s
}
