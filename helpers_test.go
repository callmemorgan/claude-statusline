package main

import (
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
	now := time.Now().Unix()
	cases := []struct {
		at   int64
		want string
	}{
		{now - 10, "now"},
		{now + 90, "1m"},
		{now + 2*3600 + 30*60 + 30, "2h30m"},
		{now + 3*86400 + 4*3600 + 60, "3d4h"},
	}
	for _, tc := range cases {
		if got := resetCountdown(tc.at); got != tc.want {
			t.Errorf("resetCountdown(now%+d) = %q, want %q", tc.at-now, got, tc.want)
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

func TestSettingsForDefaults(t *testing.T) {
	s := settingsFor(config{}, "context-window")
	if !*s.ShowBar || !*s.ShowCountdown || !*s.ShowWarning {
		t.Error("expected all toggles to default to true")
	}
	if *s.BarWidth != 20 || *s.Iconset != "default" || *s.WarnAt != 60 || *s.CritAt != 80 {
		t.Errorf("unexpected defaults: width=%d iconset=%q warn=%d crit=%d",
			*s.BarWidth, *s.Iconset, *s.WarnAt, *s.CritAt)
	}

	w := 35
	cfg := config{Settings: map[string]segmentSettings{"context-window": {BarWidth: &w}}}
	s = settingsFor(cfg, "context-window")
	if *s.BarWidth != 35 {
		t.Errorf("override not applied: %d", *s.BarWidth)
	}
	if *s.Iconset != "default" {
		t.Errorf("unset fields should still default: %q", *s.Iconset)
	}
}
