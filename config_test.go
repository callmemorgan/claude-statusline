package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
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

func TestLoadConfigDefaultsOnMalformedTOML(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, "segments = [broken")
	cfg, warns := loadConfigWarn()
	if len(cfg.Segments) != len(defaultConfig().Segments) {
		t.Errorf("malformed config should fall back to defaults")
	}
	if len(warns) == 0 {
		t.Error("expected a warning for unreadable config")
	}
}

func TestLoadConfigExplicitEmptySegments(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, "segments = []\n\n[[plugins]]\nid = \"mem\"\ncommand = \"x\"\n")
	cfg := loadConfig()
	if len(cfg.Segments) != 0 {
		t.Errorf("explicit [] must hide everything (no plugin auto-append), got %v", cfg.Segments)
	}
}

func TestLoadConfigAutoAppendsPlugins(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `
[[plugins]]
id = "mem"
command = "x"

[[plugins]]
command = "y"

  [[plugins.fields]]
  id = "cpu"

  [[plugins.fields]]
  id = "swap"
`)
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
	in := config{
		Segments: []string{"model", "cost"},
		Lines:    map[string]int{"cost": 2},
		Colors:   map[string]string{"model": "cyan"},
		Reflow:   "group",
		Settings: map[string]map[string]any{"context-window": {"bar_width": 30}},
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
	initSegments(nil)
	seg, _ := segmentByID("context-window")
	if s := settingsFor(out, seg); s.Int("bar_width") != 30 {
		t.Errorf("settings not round-tripped through JSON: %v", out.Settings)
	}
}

func TestPresetConfigKey(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `preset = "quota-watch"`)
	cfg := loadConfig()
	if len(cfg.Segments) != 4 || cfg.Segments[0] != "model" {
		t.Errorf("preset segments not applied: %v", cfg.Segments)
	}
	if cfg.Theme != "tokyo-night" {
		t.Errorf("preset theme suggestion not applied: %q", cfg.Theme)
	}
	if cfg.Lines["rate-limit-5h"] != 2 {
		t.Errorf("preset lines not applied: %v", cfg.Lines)
	}

	// Explicit segments beat the preset; explicit theme beats the suggestion.
	writeConfigFile(t, dir, "preset = \"quota-watch\"\ntheme = \"nord\"\nsegments = [\"model\"]\n")
	cfg = loadConfig()
	if len(cfg.Segments) != 1 {
		t.Errorf("explicit segments should beat preset: %v", cfg.Segments)
	}
	if cfg.Theme != "nord" {
		t.Errorf("explicit theme should beat suggestion: %q", cfg.Theme)
	}

	// Unknown preset warns and is ignored.
	writeConfigFile(t, dir, `preset = "nope"`)
	cfg2, warns := loadConfigWarn()
	if len(cfg2.Segments) != len(defaultConfig().Segments) {
		t.Errorf("unknown preset should fall back to defaults")
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w.String(), "preset") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected preset warning, got %v", warns)
	}
}

func TestAsyncPluginConfigValidation(t *testing.T) {
	cfg := config{
		Plugins: []pluginDef{
			{ID: "a", Command: "x", Async: true, RefreshMS: 100},
			{ID: "b", Command: "x", Async: true, TimeoutMS: 0},
			{ID: "c", Command: "x", Async: true, TimeoutMS: 120000},
			{ID: "d", Command: "x", Async: false, RefreshMS: 100},
		},
	}
	warns := validateConfig(&cfg)
	if cfg.Plugins[0].RefreshMS != 500 {
		t.Errorf("refresh_ms 100 should clamp to 500, got %d", cfg.Plugins[0].RefreshMS)
	}
	if cfg.Plugins[1].TimeoutMS != 10000 {
		t.Errorf("async timeout 0 should default to 10000, got %d", cfg.Plugins[1].TimeoutMS)
	}
	if cfg.Plugins[2].TimeoutMS != 60000 {
		t.Errorf("timeout 120000 should clamp to 60000, got %d", cfg.Plugins[2].TimeoutMS)
	}
	if cfg.Plugins[3].RefreshMS != 100 {
		t.Errorf("sync refresh_ms should be ignored, got %d", cfg.Plugins[3].RefreshMS)
	}
	foundRefresh := false
	foundTimeout := false
	for _, w := range warns {
		if strings.Contains(w.String(), "refresh_ms") {
			foundRefresh = true
		}
		if strings.Contains(w.String(), "timeout_ms") {
			foundTimeout = true
		}
	}
	if !foundRefresh {
		t.Errorf("expected a refresh_ms clamp warning, got %v", warns)
	}
	if !foundTimeout {
		t.Errorf("expected a timeout_ms clamp warning, got %v", warns)
	}
}

func TestApplyPresetKeepsPluginsAndColors(t *testing.T) {
	cfg := config{
		Colors:  map[string]string{"model": "cyan"},
		Theme:   "dracula",
		Plugins: []pluginDef{{ID: "mem", Command: "x"}},
	}
	p, _ := presetByID("minimal")
	applyPreset(&cfg, p)
	if cfg.Colors["model"] != "cyan" {
		t.Error("preset must keep per-segment colors")
	}
	if cfg.Theme != "dracula" {
		t.Error("preset theme suggestion must not override a chosen theme")
	}
	if cfg.Segments[len(cfg.Segments)-1] != "mem" {
		t.Errorf("plugin segment dropped by preset: %v", cfg.Segments)
	}
}

func TestUpdateConfigRoundTripAndValidation(t *testing.T) {
	dir := useTempConfigDir(t)
	h24 := 24
	h12 := 12
	h200 := 200
	h0 := 0
	writeConfigFile(t, dir, `
[update]
mode = "auto"
check_hours = 12
`)
	cfg := loadConfig()
	if cfg.Update.Mode != "auto" {
		t.Errorf("mode not loaded: %q", cfg.Update.Mode)
	}
	if cfg.Update.CheckHours == nil || *cfg.Update.CheckHours != 12 {
		t.Errorf("check_hours not loaded: %v", cfg.Update.CheckHours)
	}
	if got := cfg.Update.mode(); got != "auto" {
		t.Errorf("mode() = %q, want auto", got)
	}
	if got := cfg.Update.checkEvery(); got != 12*time.Hour {
		t.Errorf("checkEvery() = %v, want 12h", got)
	}
	if got := (updateConfig{}).mode(); got != "notify" {
		t.Errorf("default mode() = %q, want notify", got)
	}
	if got := (updateConfig{}).checkEvery(); got != 24*time.Hour {
		t.Errorf("default checkEvery() = %v, want 24h", got)
	}

	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded := loadConfig()
	if loaded.Update.Mode != "auto" || loaded.Update.CheckHours == nil || *loaded.Update.CheckHours != 12 {
		t.Errorf("round-trip lost [update]: %+v", loaded.Update)
	}

	warns := validateConfig(&cfg)
	for _, w := range warns {
		if strings.HasPrefix(w.String(), "update.") {
			t.Errorf("valid [update] produced a warning: %v", warns)
		}
	}

	cases := []struct {
		name     string
		in       updateConfig
		wantMode string
		wantH    *int
		wantWarn bool
	}{
		{"bad-mode-warns", updateConfig{Mode: "loud", CheckHours: &h24}, "", &h24, true},
		{"hours-too-low", updateConfig{Mode: "notify", CheckHours: &h0}, "notify", nil, true},
		{"hours-too-high", updateConfig{Mode: "auto", CheckHours: &h200}, "auto", nil, true},
		{"valid-no-warn", updateConfig{Mode: "off", CheckHours: &h12}, "off", &h12, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config{Update: tc.in}
			warns := validateConfig(&cfg)
			if cfg.Update.Mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", cfg.Update.Mode, tc.wantMode)
			}
			if (cfg.Update.CheckHours == nil) != (tc.wantH == nil) {
				t.Errorf("CheckHours nil=%v, want nil=%v", cfg.Update.CheckHours == nil, tc.wantH == nil)
			}
			if tc.wantH != nil && cfg.Update.CheckHours != nil && *cfg.Update.CheckHours != *tc.wantH {
				t.Errorf("CheckHours = %d, want %d", *cfg.Update.CheckHours, *tc.wantH)
			}
			found := false
			for _, w := range warns {
				if strings.HasPrefix(w.String(), "update.") {
					found = true
				}
			}
			if found != tc.wantWarn {
				t.Errorf("[update] warning present=%v, want=%v (warns=%v)", found, tc.wantWarn, warns)
			}
		})
	}
}

func TestUpdateConfigMergePreserves(t *testing.T) {
	h6 := 6
	loaded := config{Update: updateConfig{Mode: "off", CheckHours: &h6}}
	cfg := mergeWithDefaults(loaded)
	if cfg.Update.Mode != "off" {
		t.Errorf("merge dropped mode: %q", cfg.Update.Mode)
	}
	if cfg.Update.CheckHours == nil || *cfg.Update.CheckHours != 6 {
		t.Errorf("merge dropped check_hours: %v", cfg.Update.CheckHours)
	}
}

func TestPresetSegmentIDsExist(t *testing.T) {
	initSegments(nil)
	for _, p := range layoutPresets {
		for _, id := range p.Segments {
			if _, ok := segmentByID(id); !ok {
				t.Errorf("preset %q references unknown segment %q", p.ID, id)
			}
		}
		for id := range p.Settings {
			seg, ok := segmentByID(id)
			if !ok {
				t.Errorf("preset %q settings reference unknown segment %q", p.ID, id)
				continue
			}
			for key := range p.Settings[id] {
				found := false
				for _, sp := range seg.settings {
					if sp.Key == key {
						found = true
					}
				}
				if !found {
					t.Errorf("preset %q has unknown setting %s.%s", p.ID, id, key)
				}
			}
		}
		if p.Theme != "" {
			if _, ok := presetByID(p.ID); !ok {
				t.Errorf("presetByID broken for %q", p.ID)
			}
			ok := false
			for _, id := range themeIDs() {
				if id == p.Theme {
					ok = true
				}
			}
			if !ok {
				t.Errorf("preset %q suggests unknown theme %q", p.ID, p.Theme)
			}
		}
	}
}
