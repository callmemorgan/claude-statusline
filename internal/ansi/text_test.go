package ansi

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
		if got := VisibleWidth(tc.in); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
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
		if got := FormatTokens(tc.in); got != tc.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", tc.in, got, tc.want)
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
		if got := FormatPath(tc.current, tc.project); got != tc.want {
			t.Errorf("FormatPath(%q, %q) = %q, want %q", tc.current, tc.project, got, tc.want)
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
		if got := FormatHHMMSS(tc.in); got != tc.want {
			t.Errorf("FormatHHMMSS(%d) = %q, want %q", tc.in, got, tc.want)
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
		if got := ResetCountdown(tc.at, now); got != tc.want {
			t.Errorf("ResetCountdown(now%+d) = %q, want %q", tc.at-now.Unix(), got, tc.want)
		}
	}
}

func TestEffortBadge(t *testing.T) {
	cases := map[string]string{
		"low": "⬇", "medium": "→", "high": "⬆", "xhigh": "⬆⬆", "max": "⬆⬆⬆",
		"HIGH": "⬆", "": "", "unknown": "",
	}
	for in, want := range cases {
		if got := EffortBadge(in); got != want {
			t.Errorf("EffortBadge(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFooterRows(t *testing.T) {
	long := strings.Repeat("a", 200)
	if got := FooterRows(long, 0); got != 1 {
		t.Errorf("zero width = %d rows, want 1", got)
	}
	if got := FooterRows(long, len(long)+10); got != 1 {
		t.Errorf("wide terminal = %d rows, want 1", got)
	}
	if got := FooterRows(long, 100); got < 2 {
		t.Errorf("long text at 100 cols = %d rows, want ≥2 (len %d)", got, len(long))
	}
	if got := FooterRows(long, 10); got != 3 {
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
		if got := AnsiToTview(tc.in); got != tc.want {
			t.Errorf("AnsiToTview(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Literal brackets must be escaped, not parsed as tags.
	got := AnsiToTview("[normal]")
	if got == "[normal]" {
		t.Errorf("literal bracket text should be escaped, got %q", got)
	}
	if !strings.Contains(got, "normal") {
		t.Errorf("escaped text lost content: %q", got)
	}
}
