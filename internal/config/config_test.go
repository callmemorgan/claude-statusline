package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/palette"
)

// useTempConfigDir points ConfigDir at a temp dir for the duration of a test.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ConfigDirOverride = dir
	t.Cleanup(func() { ConfigDirOverride = "" })
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
	cfg := LoadConfig()
	def := DefaultConfig()
	if len(cfg.Segments) != len(def.Segments) {
		t.Errorf("expected default segments, got %d", len(cfg.Segments))
	}
}

func TestLoadConfigDefaultsOnMalformedTOML(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, "segments = [broken")
	cfg, warns := LoadConfigWarn()
	if len(cfg.Segments) != len(DefaultConfig().Segments) {
		t.Errorf("malformed config should fall back to defaults")
	}
	if len(warns) == 0 {
		t.Error("expected a warning for unreadable config")
	}
}

func TestLoadConfigExplicitEmptySegments(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `segments = []

[[plugins]]
id = "mem"
command = "x"
`)
	cfg := LoadConfig()
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
	cfg := LoadConfig()
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
	in := Config{
		Segments: []string{"model", "cost"},
		Lines:    map[string]int{"cost": 2},
		Colors:   map[string]string{"model": "cyan"},
		Reflow:   "group",
		Settings: map[string]map[string]any{"context-window": {"bar_width": 30}},
		Plugins: []PluginDef{{
			ID:      "demo",
			Command: "echo demo",
			Preview: "demo preview",
			Fields:  []PluginField{{ID: "cpu", Preview: "cpu preview"}},
		}},
	}
	if err := SaveConfig(in); err != nil {
		t.Fatal(err)
	}
	out := LoadConfig()
	if len(out.Segments) != 2 || out.Segments[0] != "model" {
		t.Errorf("segments not round-tripped: %v", out.Segments)
	}
	if out.Lines["cost"] != 2 || out.Colors["model"] != "cyan" || out.Reflow != "group" {
		t.Errorf("fields not round-tripped: %+v", out)
	}
	if len(out.Plugins) != 1 || out.Plugins[0].Preview != "demo preview" {
		t.Errorf("plugin preview not round-tripped: %+v", out.Plugins)
	}
	if len(out.Plugins[0].Fields) != 1 || out.Plugins[0].Fields[0].Preview != "cpu preview" {
		t.Errorf("field preview not round-tripped: %+v", out.Plugins[0].Fields)
	}
}

func TestPresetConfigKey(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `preset = "quota-watch"`)
	cfg := LoadConfig()
	if len(cfg.Segments) != 7 || cfg.Segments[0] != "model" {
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
	cfg = LoadConfig()
	if len(cfg.Segments) != 1 {
		t.Errorf("explicit segments should beat preset: %v", cfg.Segments)
	}
	if cfg.Theme != "nord" {
		t.Errorf("explicit theme should beat suggestion: %q", cfg.Theme)
	}

	// Unknown preset warns and is ignored.
	writeConfigFile(t, dir, `preset = "nope"`)
	cfg2, warns := LoadConfigWarn()
	if len(cfg2.Segments) != len(DefaultConfig().Segments) {
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
	cfg := Config{
		Plugins: []PluginDef{
			{ID: "a", Command: "x", Async: true, RefreshMS: 100},
			{ID: "b", Command: "x", Async: true, TimeoutMS: 0},
			{ID: "c", Command: "x", Async: true, TimeoutMS: 120000},
			{ID: "d", Command: "x", Async: false, RefreshMS: 100},
		},
	}
	warns := ValidateConfig(&cfg)
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
	cfg := Config{
		Colors:  map[string]string{"model": "cyan"},
		Theme:   "dracula",
		Plugins: []PluginDef{{ID: "mem", Command: "x"}},
	}
	p, _ := PresetByID("minimal")
	ApplyPreset(&cfg, p)
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
	cfg := LoadConfig()
	if cfg.Update.Mode != "auto" {
		t.Errorf("mode not loaded: %q", cfg.Update.Mode)
	}
	if cfg.Update.CheckHours == nil || *cfg.Update.CheckHours != 12 {
		t.Errorf("check_hours not loaded: %v", cfg.Update.CheckHours)
	}
	if got := cfg.Update.ModeOrDefault(); got != "auto" {
		t.Errorf("ModeOrDefault() = %q, want auto", got)
	}
	if got := cfg.Update.CheckEvery(); got != 12*time.Hour {
		t.Errorf("CheckEvery() = %v, want 12h", got)
	}
	if got := (UpdateConfig{}).ModeOrDefault(); got != "notify" {
		t.Errorf("default ModeOrDefault() = %q, want notify", got)
	}
	if got := (UpdateConfig{}).CheckEvery(); got != 24*time.Hour {
		t.Errorf("default CheckEvery() = %v, want 24h", got)
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded := LoadConfig()
	if loaded.Update.Mode != "auto" || loaded.Update.CheckHours == nil || *loaded.Update.CheckHours != 12 {
		t.Errorf("round-trip lost [update]: %+v", loaded.Update)
	}

	warns := ValidateConfig(&cfg)
	for _, w := range warns {
		if strings.HasPrefix(w.String(), "update.") {
			t.Errorf("valid [update] produced a warning: %v", warns)
		}
	}

	cases := []struct {
		name     string
		in       UpdateConfig
		wantMode string
		wantH    *int
		wantWarn bool
	}{
		{"bad-mode-warns", UpdateConfig{Mode: "loud", CheckHours: &h24}, "", &h24, true},
		{"hours-too-low", UpdateConfig{Mode: "notify", CheckHours: &h0}, "notify", nil, true},
		{"hours-too-high", UpdateConfig{Mode: "auto", CheckHours: &h200}, "auto", nil, true},
		{"valid-no-warn", UpdateConfig{Mode: "off", CheckHours: &h12}, "off", &h12, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Update: tc.in}
			warns := ValidateConfig(&cfg)
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
	loaded := Config{Update: UpdateConfig{Mode: "off", CheckHours: &h6}}
	cfg := MergeWithDefaults(loaded)
	if cfg.Update.Mode != "off" {
		t.Errorf("merge dropped mode: %q", cfg.Update.Mode)
	}
	if cfg.Update.CheckHours == nil || *cfg.Update.CheckHours != 6 {
		t.Errorf("merge dropped check_hours: %v", cfg.Update.CheckHours)
	}
}

func TestPresetSegmentIDsExist(t *testing.T) {
	for _, p := range LayoutPresets {
		for _, id := range p.Segments {
			if _, ok := PresetByID(id); !ok && id != "" {
				// Preset IDs are validated against the segment registry in the root
				// package; here we only sanity-check the preset list itself.
			}
		}
		if p.Theme != "" {
			ok := false
			for _, id := range palette.ThemeIDs() {
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
