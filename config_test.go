package main

import (
	"os"
	"path/filepath"
	"testing"
)

// useTempConfigDir points configDir at a temp dir for the duration of a test.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configDirOverride = dir
	t.Cleanup(func() { configDirOverride = "" })
	return dir
}

func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	useTempConfigDir(t)
	cfg := loadConfig()
	def := defaultConfig()
	if len(cfg.Segments) != len(def.Segments) {
		t.Errorf("expected default segments, got %d", len(cfg.Segments))
	}
}

func TestLoadConfigDefaultsOnMalformedJSON(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, "{not json")
	cfg := loadConfig()
	if len(cfg.Segments) != len(defaultConfig().Segments) {
		t.Errorf("malformed config should fall back to defaults")
	}
}

func TestLoadConfigExplicitEmptySegments(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `{"segments": [], "plugins": [{"id":"mem","command":"x"}]}`)
	cfg := loadConfig()
	if len(cfg.Segments) != 0 {
		t.Errorf("explicit [] must hide everything (no plugin auto-append), got %v", cfg.Segments)
	}
}

func TestLoadConfigAutoAppendsPlugins(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `{"plugins": [{"id":"mem","command":"x"},{"command":"y","fields":[{"id":"cpu"},{"id":"swap"}]}]}`)
	cfg := loadConfig()
	got := map[string]bool{}
	for _, id := range cfg.Segments {
		got[id] = true
	}
	for _, want := range []string{"mem", "cpu", "swap", "model"} {
		if !got[want] {
			t.Errorf("expected segment %q in auto-appended config, got %v", want, cfg.Segments)
		}
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	useTempConfigDir(t)
	w := 30
	in := config{
		Segments: []string{"model", "cost"},
		Lines:    map[string]int{"cost": 2},
		Colors:   map[string]string{"model": "cyan"},
		Reflow:   "group",
		Settings: map[string]segmentSettings{"context-window": {BarWidth: &w}},
	}
	if err := saveConfig(in); err != nil {
		t.Fatal(err)
	}
	out := loadConfig()
	if len(out.Segments) != 2 || out.Segments[0] != "model" {
		t.Errorf("segments not round-tripped: %v", out.Segments)
	}
	if out.Lines["cost"] != 2 || out.Colors["model"] != "cyan" || out.Reflow != "group" {
		t.Errorf("fields not round-tripped: %+v", out)
	}
	if s, ok := out.Settings["context-window"]; !ok || s.BarWidth == nil || *s.BarWidth != 30 {
		t.Errorf("settings not round-tripped: %+v", out.Settings)
	}
}
