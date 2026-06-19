package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testNow is the fixed clock used for golden rendering; payload fixtures use
// resets_at values relative to this instant so countdowns are deterministic.
var testNow = time.Unix(1750000000, 0)

// loadPayload reads and parses a fixture from testdata/payloads.
func loadPayload(t *testing.T, name string) payload.Payload {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "payloads", name))
	if err != nil {
		t.Fatalf("read payload fixture: %v", err)
	}
	return payload.ParsePayload(data)
}

// checkGolden compares got against testdata/golden/<name>.txt, rewriting the
// file when -update is set.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name+".txt")
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
		Payload string
		cfg     config.Config
		columns int
	}{
		{"claude-full__default", "claude-full.json", config.DefaultConfig(), 0},
		{"agy-full__default", "agy-full.json", config.DefaultConfig(), 0},
		{"pi-full__default", "pi-full.json", config.DefaultConfig(), 0},
		{"minimal__default", "minimal.json", config.DefaultConfig(), 0},
		{"claude-full__default-60", "claude-full.json", config.DefaultConfig(), 60},
		{"claude-full__cascade-60", "claude-full.json", func() config.Config {
			c := config.DefaultConfig()
			c.Reflow = "cascade"
			return c
		}(), 60},
		{"claude-full__group-60", "claude-full.json", func() config.Config {
			c := config.DefaultConfig()
			c.Reflow = "group"
			return c
		}(), 60},
		{"claude-full__custom-lines", "claude-full.json", config.Config{
			Segments: []string{"directory", "git-branch", "cost", "model", "context-window"},
			Lines:    map[string]int{"cost": 2, "context-window": 1},
		}, 0},
		{"claude-full__bar-settings", "claude-full.json", config.Config{
			Segments: []string{"context-window", "rate-limit-5h"},
			Settings: map[string]map[string]any{
				"context-window": {"bar_width": 30, "iconset": "blocks", "show_warning": false},
			},
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := loadPayload(t, tc.Payload)
			segments.Init()
			lines := Statusline(Input{P: p, Cfg: tc.cfg, Width: tc.columns, Now: testNow})
			checkGolden(t, tc.name, strings.Join(lines, "\n")+"\n")
		})
	}
}

// TestBuildStatuslineEmptySegments verifies an explicit empty segment list
// renders nothing.
func TestBuildStatuslineEmptySegments(t *testing.T) {
	p := loadPayload(t, "claude-full.json")
	cfg := config.Config{Segments: []string{}}
	segments.Init()
	if lines := Statusline(Input{P: p, Cfg: cfg, Now: testNow}); len(lines) != 0 {
		t.Errorf("expected no lines, got %q", lines)
	}
}

// TestBuildStatuslineColorCodes verifies ANSI escapes appear with a real
// palette and that visibleWidth ignores them.
func TestBuildStatuslineColorCodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := loadPayload(t, "claude-full.json")
	cfg := config.Config{Segments: []string{"directory"}}
	segments.Init()

	colored := Statusline(Input{P: p, C: palette.Palette{Dir: "\x1b[36m", Rst: "\x1b[0m"}, Cfg: cfg, Now: testNow})
	plain := Statusline(Input{P: p, Cfg: cfg, Now: testNow})
	if len(colored) != 1 || len(plain) != 1 {
		t.Fatalf("expected 1 line, got %d / %d", len(colored), len(plain))
	}
	if !strings.Contains(colored[0], "\x1b[36m") {
		t.Errorf("expected ANSI escape in colored output: %q", colored[0])
	}
	if strings.Contains(plain[0], "\x1b[") {
		t.Errorf("unexpected ANSI escape in plain output: %q", plain[0])
	}
	if ansi.VisibleWidth(colored[0]) != ansi.VisibleWidth(plain[0]) {
		t.Errorf("visibleWidth differs: %d vs %d", ansi.VisibleWidth(colored[0]), ansi.VisibleWidth(plain[0]))
	}
}
