package main

import (
	"strings"
	"testing"
	"time"
)

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"\x1b[35mabc\x1b[0m", 3},
		{"ctx ███░░ 65%", 13},
		{"\x1b[90m↑1.2M ↓89.0k\x1b[0m", 12},
	}
	for _, tc := range cases {
		if got := visibleWidth(tc.in); got != tc.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{45678, "45.6k"},
		{999999, "999.9k"},
		{1000000, "1.0M"},
		{1234567, "1.2M"},
	}
	for _, tc := range cases {
		if got := formatTokens(tc.in); got != tc.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatPath(t *testing.T) {
	cases := []struct {
		current, project, want string
	}{
		{"/Users/me/code/proj", "/Users/me/code/proj", "proj"},
		{"/Users/me/code/proj/sub", "/Users/me/code/proj", "proj→sub"},
		{"/Users/me/code/proj/a/b", "/Users/me/code/proj", "proj→a/b"},
		{"/Users/me/other", "/Users/me/code/proj", "other"},
		{"~", "", "~"},
	}
	for _, tc := range cases {
		if got := formatPath(tc.current, tc.project); got != tc.want {
			t.Errorf("formatPath(%q, %q) = %q, want %q", tc.current, tc.project, got, tc.want)
		}
	}
}

func TestFormatHHMMSS(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "00:00:00"},
		{-5, "00:00:00"},
		{3661000, "01:01:01"},
		{86399000, "23:59:59"},
	}
	for _, tc := range cases {
		if got := formatHHMMSS(tc.in); got != tc.want {
			t.Errorf("formatHHMMSS(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResetCountdown(t *testing.T) {
	now := time.Unix(1750000000, 0)
	cases := []struct {
		at   int64
		want string
	}{
		{now.Unix() - 10, "now"},
		{now.Unix() + 90, "1m"},
		{now.Unix() + 2*3600 + 30*60 + 30, "2h30m"},
		{now.Unix() + 3*86400 + 4*3600 + 60, "3d4h"},
	}
	for _, tc := range cases {
		if got := resetCountdown(tc.at, now); got != tc.want {
			t.Errorf("resetCountdown(now%+d) = %q, want %q", tc.at-now.Unix(), got, tc.want)
		}
	}
}

func TestEffortBadge(t *testing.T) {
	cases := map[string]string{
		"low": "⬇", "medium": "→", "high": "⬆", "xhigh": "⬆⬆", "max": "⬆⬆⬆",
		"HIGH": "⬆", "": "", "unknown": "",
	}
	for in, want := range cases {
		if got := effortBadge(in); got != want {
			t.Errorf("effortBadge(%q) = %q, want %q", in, got, want)
		}
	}
}

func ctxWindowSegment(t *testing.T) segmentInfo {
	t.Helper()
	initSegments(nil)
	seg, ok := segmentByID("context-window")
	if !ok {
		t.Fatal("context-window segment not registered")
	}
	return seg
}

func TestSettingsForDefaults(t *testing.T) {
	seg := ctxWindowSegment(t)
	s := settingsFor(config{}, seg)
	if !s.Bool("show_bar") || !s.Bool("show_warning") {
		t.Error("expected toggles to default to true")
	}
	if s.Int("bar_width") != 20 || s.Str("iconset") != "default" || s.Int("warn_at") != 60 || s.Int("crit_at") != 80 {
		t.Errorf("unexpected defaults: width=%d iconset=%q warn=%d crit=%d",
			s.Int("bar_width"), s.Str("iconset"), s.Int("warn_at"), s.Int("crit_at"))
	}
	if _, ok := s["show_countdown"]; ok {
		t.Error("context-window should not resolve a show_countdown setting")
	}
	if _, ok := s["stress_test"]; ok {
		t.Error("ephemeral specs must not appear in resolved settings")
	}
}

func TestSettingsForOverridesAndCoercion(t *testing.T) {
	seg := ctxWindowSegment(t)
	cfg := config{Settings: map[string]map[string]any{"context-window": {
		"bar_width": float64(35), // JSON numbers decode as float64
		"iconset":   "nonsense",  // invalid enum value → default
		"warn_at":   999,         // out of range → clamped
		"show_bar":  "yes",       // wrong type → default
	}}}
	s := settingsFor(cfg, seg)
	if s.Int("bar_width") != 35 {
		t.Errorf("float64 not coerced: %d", s.Int("bar_width"))
	}
	if s.Str("iconset") != "default" {
		t.Errorf("invalid enum should fall back to default: %q", s.Str("iconset"))
	}
	if s.Int("warn_at") != 100 {
		t.Errorf("out-of-range int should clamp: %d", s.Int("warn_at"))
	}
	if !s.Bool("show_bar") {
		t.Error("wrong-typed bool should fall back to default true")
	}
}

func TestPruneSettings(t *testing.T) {
	seg := ctxWindowSegment(t)
	s := settingsFor(config{}, seg)
	if got := pruneSettings(seg, s); got != nil {
		t.Errorf("all-default settings should prune to nil, got %v", got)
	}
	s["bar_width"] = 35
	got := pruneSettings(seg, s)
	if len(got) != 1 || got["bar_width"] != 35 {
		t.Errorf("expected only the changed key, got %v", got)
	}
}

func TestProgressBarFractional(t *testing.T) {
	// smooth at 25% of width 10: 20 of 80 units → 2 full cells + partial 4/8.
	got := progressBarWithIconset(25, "", "", palette{}, 10, "smooth")
	if got != "██▌       " {
		t.Errorf("smooth 25%%/10 = %q", got)
	}
	if got := progressBarWithIconset(0, "", "", palette{}, 10, "smooth"); got != "          " {
		t.Errorf("smooth 0%% = %q", got)
	}
	if got := progressBarWithIconset(100, "", "", palette{}, 10, "smooth"); got != "██████████" {
		t.Errorf("smooth 100%% = %q", got)
	}
	// Whole-cell sets are unchanged by the iconset refactor.
	if got := progressBarWithIconset(50, "", "", palette{}, 10, "blocks"); got != "█████░░░░░" {
		t.Errorf("blocks 50%% = %q", got)
	}
	// Unknown name falls back to default glyphs.
	if got := progressBarWithIconset(50, "", "", palette{}, 4, "nope"); got != "##--" {
		t.Errorf("fallback = %q", got)
	}
	// Every named set renders at the declared width.
	for _, name := range iconsetNames() {
		for _, pct := range []int{0, 33, 50, 99, 100} {
			if w := visibleWidth(progressBarWithIconset(pct, "", "", palette{}, 20, name)); w != 20 {
				t.Errorf("iconset %q at %d%% has width %d, want 20", name, pct, w)
			}
		}
	}
}

func TestFilterSegments(t *testing.T) {
	initSegments(nil)
	all := registeredSegments
	if got := filterSegments(all, ""); len(got) != len(all) {
		t.Errorf("empty query should return all, got %d/%d", len(got), len(all))
	}
	got := filterSegments(all, "rate")
	for _, s := range got {
		if !strings.Contains(s.id, "rate") && !strings.Contains(strings.ToLower(s.desc), "rate") {
			t.Errorf("unexpected match %q", s.id)
		}
	}
	if len(got) < 3 { // rate-limit-5h, rate-limit-7d, cost-rate
		t.Errorf("expected at least 3 'rate' matches, got %d", len(got))
	}
	if got := filterSegments(all, "GIT"); len(got) == 0 {
		t.Error("filter should be case-insensitive")
	}
	if got := filterSegments(all, "zzzznope"); len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}

func TestFooterRows(t *testing.T) {
	long := footerText("list")
	if got := footerRows(long, 0); got != 1 {
		t.Errorf("zero width = %d rows, want 1", got)
	}
	if got := footerRows(long, len(long)+10); got != 1 {
		t.Errorf("wide terminal = %d rows, want 1", got)
	}
	if got := footerRows(long, 100); got < 2 {
		t.Errorf("main footer at 100 cols = %d rows, want ≥2 (len %d)", got, len(long))
	}
	if got := footerRows(long, 10); got != 3 {
		t.Errorf("pathological width = %d rows, want clamp at 3", got)
	}
}

func TestAnsiToTview(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[32mok\x1b[0m", "[#00cd00]ok[-:-:-]"},
		{"\x1b[91mcrit\x1b[0m", "[#ff0000]crit[-:-:-]"},
		{"\x1b[38;5;208morange\x1b[0m", "[#ff8700]orange[-:-:-]"},
		{"\x1b[38;2;187;154;247mtokyo\x1b[0m", "[#bb9af7]tokyo[-:-:-]"},
		{"plain", "plain"},
	}
	for _, tc := range cases {
		if got := ansiToTview(tc.in); got != tc.want {
			t.Errorf("ansiToTview(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Literal brackets must be escaped, not parsed as tags.
	got := ansiToTview("[normal]")
	if got == "[normal]" {
		t.Errorf("literal bracket text should be escaped, got %q", got)
	}
	if !strings.Contains(got, "normal") {
		t.Errorf("escaped text lost content: %q", got)
	}
}
