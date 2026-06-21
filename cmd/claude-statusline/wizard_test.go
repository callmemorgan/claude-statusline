package main

import (
	"reflect"
	"testing"
)

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"\x1b[1;36mhello\x1b[0m", 5},
		{"日本語", 3},
	}
	for _, c := range cases {
		got := visibleWidth(c.in)
		if got != c.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  []string
	}{
		{"hello world", 11, []string{"hello world"}},
		{"hello world", 10, []string{"hello", "world"}},
		{"hello world", 5, []string{"hello", "world"}},
		{"a bb ccc dddd", 6, []string{"a bb", "ccc", "dddd"}},
		{"line one\nline two", 20, []string{"line one", "line two"}},
	}
	for _, c := range cases {
		got := wrapText(c.text, c.width)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("wrapText(%q, %d) = %v, want %v", c.text, c.width, got, c.want)
		}
	}
}

func TestWrapTextIndent(t *testing.T) {
	got := wrapTextIndent("Decide whether to wire into Claude Code", 20, 4)
	want := []string{
		"Decide whether",
		"    to wire into",
		"    Claude Code",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapTextIndent(...) = %v, want %v", got, want)
	}
}
