package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

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

	barWidth30 := 30
	showWarnOff := false
	iconBlocks := "blocks"

	cases := []struct {
		name    string
		payload string
		cfg     config
		columns int
	}{
		{"claude-full__default", "claude-full.json", defaultConfig(), 0},
		{"agy-full__default", "agy-full.json", defaultConfig(), 0},
		{"minimal__default", "minimal.json", defaultConfig(), 0},
		{"claude-full__cascade-60", "claude-full.json", defaultConfig(), 60},
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
			Settings: map[string]segmentSettings{
				"context-window": {BarWidth: &barWidth30, Iconset: &iconBlocks, ShowWarning: &showWarnOff},
			},
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := loadPayload(t, tc.payload)
			initSegments(tc.cfg.Plugins)
			lines := buildStatusline(p, palette{}, tc.cfg, tc.columns)
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
	if lines := buildStatusline(p, palette{}, cfg, 0); len(lines) != 0 {
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

	colored := buildStatusline(p, palette{Dir: "\x1b[36m", Rst: "\x1b[0m"}, cfg, 0)
	plain := buildStatusline(p, palette{}, cfg, 0)
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
