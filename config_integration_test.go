package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// useTempConfigDir points ConfigDir at a temp dir for the duration of a test.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.ConfigDirOverride = dir
	t.Cleanup(func() { config.ConfigDirOverride = "" })
	return dir
}

func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSaveConfigRoundTripWithSegments(t *testing.T) {
	useTempConfigDir(t)
	in := config.Config{
		Segments: []string{"model", "cost"},
		Lines:    map[string]int{"cost": 2},
		Colors:   map[string]string{"model": "cyan"},
		Reflow:   "group",
		Settings: map[string]map[string]any{"context-window": {"bar_width": 30}},
	}
	if err := config.SaveConfig(in); err != nil {
		t.Fatal(err)
	}
	out := config.LoadConfig()
	initSegments(nil)
	seg, _ := segments.ByID("context-window")
	if s := config.SettingsFor(out, seg.ID, seg.Settings); s.Int("bar_width") != 30 {
		t.Errorf("settings not round-tripped through JSON: %v", out.Settings)
	}
}

func TestPresetSegmentIDsExist(t *testing.T) {
	initSegments(nil)
	for _, p := range config.LayoutPresets {
		for _, id := range p.Segments {
			if _, ok := segments.ByID(id); !ok {
				t.Errorf("preset %q references unknown segment %q", p.ID, id)
			}
		}
		for id := range p.Settings {
			seg, ok := segments.ByID(id)
			if !ok {
				t.Errorf("preset %q settings reference unknown segment %q", p.ID, id)
				continue
			}
			for key := range p.Settings[id] {
				found := false
				for _, sp := range seg.Settings {
					if sp.Key == key {
						found = true
					}
				}
				if !found {
					t.Errorf("preset %q has unknown setting %s.%s", p.ID, id, key)
				}
			}
		}
	}
}

const legacyJSON = `{
  "segments": ["model", "cost", "context-window"],
  "lines": {"cost": 2},
  "colors": {"model": "cyan"},
  "settings": {"context-window": {"bar_width": 30, "show_warning": false, "iconset": "blocks"}},
  "plugins": [{"id": "mem", "command": "~/p.sh", "line": 1, "desc": "RAM", "timeout_ms": 200}],
  "reflow": "group"
}`

func TestMigrateLegacyJSONWithSegments(t *testing.T) {
	dir := useTempConfigDir(t)
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.LoadConfig()

	initSegments(nil)
	seg, _ := segments.ByID("context-window")
	s := config.SettingsFor(cfg, seg.ID, seg.Settings)
	if s.Int("bar_width") != 30 || s.Bool("show_warning") || s.Str("iconset") != "blocks" {
		t.Errorf("settings not migrated: %v", cfg.Settings)
	}
}

func TestValidateSegmentRefs(t *testing.T) {
	initSegments(nil)
	cfg := config.Config{
		Segments: []string{"model", "no-such-segment"},
		Settings: map[string]map[string]any{
			"context-window": {"bar_width": 30, "bogus_key": 1},
			"ghost":          {"x": 1},
		},
	}
	warns := validateSegmentRefs(cfg)
	want := []string{"no-such-segment", "bogus_key", "settings.ghost"}
	for _, w := range want {
		found := false
		for _, got := range warns {
			if strings.Contains(got.String(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning about %q, got %v", w, warns)
		}
	}
}
