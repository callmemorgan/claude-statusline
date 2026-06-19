package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testNow is the fixed clock used for golden rendering; payload fixtures use
// resets_at values relative to this instant so countdowns are deterministic.
var testNow = time.Unix(1750000000, 0)

// loadPayload reads and parses a fixture from testdata/payloads.
func loadPayload(t *testing.T, name string) payload {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "payloads", name))
	if err != nil {
		t.Fatalf("read payload fixture: %v", err)
	}
	return parsePayload(data)
}

// checkGolden compares got against testdata/golden/<name>.txt, rewriting the
// file when -update is set.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test -run Golden -update): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestBuildStatuslineGolden renders payload × config combinations and compares
// against golden files. Goldens are color-free by construction: an empty
// palette is passed in, equivalent to NO_COLOR.
func TestBuildStatuslineGolden(t *testing.T) {
	// Neutralize machine state: readEffortLevel and gitBranch fall back to
	// reads under $HOME when payload fields are absent.
	t.Setenv("HOME", t.TempDir())

	cases := []struct {
		name    string
		payload string
		cfg     config
		columns int
	}{
		{"claude-full__default", "claude-full.json", defaultConfig(), 0},
		{"agy-full__default", "agy-full.json", defaultConfig(), 0},
		{"pi-full__default", "pi-full.json", defaultConfig(), 0},
		{"minimal__default", "minimal.json", defaultConfig(), 0},
		{"claude-full__default-60", "claude-full.json", defaultConfig(), 60},
		{"claude-full__cascade-60", "claude-full.json", func() config {
			c := defaultConfig()
			c.Reflow = "cascade"
			return c
		}(), 60},
		{"claude-full__group-60", "claude-full.json", func() config {
			c := defaultConfig()
			c.Reflow = "group"
			return c
		}(), 60},
		{"claude-full__custom-lines", "claude-full.json", config{
			Segments: []string{"directory", "git-branch", "cost", "model", "context-window"},
			Lines:    map[string]int{"cost": 2, "context-window": 1},
		}, 0},
		{"claude-full__bar-settings", "claude-full.json", config{
			Segments: []string{"context-window", "rate-limit-5h"},
			Settings: map[string]map[string]any{
				"context-window": {"bar_width": 30, "iconset": "blocks", "show_warning": false},
			},
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := loadPayload(t, tc.payload)
			initSegments(tc.cfg.Plugins)
			lines := buildStatusline(buildInput{P: p, Cfg: tc.cfg, Width: tc.columns, Now: testNow})
			checkGolden(t, tc.name, strings.Join(lines, "\n")+"\n")
		})
	}
}

// TestBuildStatuslineEmptySegments verifies an explicit empty segment list
// renders nothing.
func TestBuildStatuslineEmptySegments(t *testing.T) {
	p := loadPayload(t, "claude-full.json")
	cfg := config{Segments: []string{}}
	initSegments(nil)
	if lines := buildStatusline(buildInput{P: p, Cfg: cfg, Now: testNow}); len(lines) != 0 {
		t.Errorf("expected no lines, got %q", lines)
	}
}

// TestBuildStatuslineColorCodes verifies ANSI escapes appear with a real
// palette and that visibleWidth ignores them.
func TestBuildStatuslineColorCodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := loadPayload(t, "claude-full.json")
	cfg := config{Segments: []string{"directory"}}
	initSegments(nil)

	colored := buildStatusline(buildInput{P: p, C: palette{Dir: "\x1b[36m", Rst: "\x1b[0m"}, Cfg: cfg, Now: testNow})
	plain := buildStatusline(buildInput{P: p, Cfg: cfg, Now: testNow})
	if len(colored) != 1 || len(plain) != 1 {
		t.Fatalf("expected 1 line, got %d / %d", len(colored), len(plain))
	}
	if !strings.Contains(colored[0], "\x1b[36m") {
		t.Errorf("expected ANSI escape in colored output: %q", colored[0])
	}
	if strings.Contains(plain[0], "\x1b[") {
		t.Errorf("unexpected ANSI escape in plain output: %q", plain[0])
	}
	if visibleWidth(colored[0]) != visibleWidth(plain[0]) {
		t.Errorf("visibleWidth differs: %d vs %d", visibleWidth(colored[0]), visibleWidth(plain[0]))
	}
}

// TestNewPayloadSegments covers output-style, added-dirs, and email.
func TestNewPayloadSegments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	initSegments(nil)
	render := func(p payload, id string) (string, bool) {
		seg, ok := segmentByID(id)
		if !ok {
			t.Fatalf("segment %q not registered", id)
		}
		return seg.render(renderCtx{P: p, Now: testNow})
	}

	var p payload
	if _, show := render(p, "output-style"); show {
		t.Error("output-style should hide with no payload data")
	}
	p.OutputStyle.Name = "default"
	if _, show := render(p, "output-style"); show {
		t.Error("output-style should hide when style is default")
	}
	p.OutputStyle.Name = "Explanatory"
	if got, show := render(p, "output-style"); !show || got != "✎ Explanatory" {
		t.Errorf("output-style = %q, %v", got, show)
	}

	if _, show := render(p, "added-dirs"); show {
		t.Error("added-dirs should hide when empty")
	}
	p.Workspace.AddedDirs = []string{"/a"}
	if got, _ := render(p, "added-dirs"); got != "+1 dir" {
		t.Errorf("added-dirs singular = %q", got)
	}
	p.Workspace.AddedDirs = []string{"/a", "/b"}
	if got, _ := render(p, "added-dirs"); got != "+2 dirs" {
		t.Errorf("added-dirs plural = %q", got)
	}

	if _, show := render(p, "email"); show {
		t.Error("email should hide when empty")
	}
	p.Email = "morgan@skyslope.com"
	if got, _ := render(p, "email"); got != "morgan@…" {
		t.Errorf("email = %q", got)
	}
}
