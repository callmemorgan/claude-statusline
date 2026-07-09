package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacyJSON = `{
  "segments": ["model", "cost", "context-window"],
  "lines": {"cost": 2},
  "colors": {"model": "cyan"},
  "settings": {"context-window": {"bar_width": 30, "show_warning": false, "iconset": "blocks"}},
  "plugins": [{"id": "mem", "command": "~/p.sh", "line": 1, "desc": "RAM", "timeout_ms": 200}],
  "reflow": "group"
}`

func TestMigrateSchemaV2AddsFableAfter7d(t *testing.T) {
	dir := useTempConfigDir(t)
	// Explicit v1 layout with 7d but no Fable included bar.
	writeConfigFile(t, dir, `
schema_version = 1
segments = ["model", "rate-limit-5h", "rate-limit-7d", "cost"]
[lines]
"rate-limit-7d" = 3
[settings."rate-limit-7d"]
bar_width = 30
iconset = "smooth"
`)
	cfg, warns := LoadConfigWarn()

	// Fable inserted right after 7d.
	want := []string{"model", "rate-limit-5h", "rate-limit-7d", "rate-limit-fable", "cost"}
	if len(cfg.Segments) != len(want) {
		t.Fatalf("segments = %v, want %v", cfg.Segments, want)
	}
	for i, id := range want {
		if cfg.Segments[i] != id {
			t.Fatalf("segments = %v, want %v", cfg.Segments, want)
		}
	}
	if cfg.Lines["rate-limit-fable"] != 3 {
		t.Errorf("fable line = %d, want 3 (copied from 7d)", cfg.Lines["rate-limit-fable"])
	}
	s := SettingsFor(cfg, "rate-limit-fable", BarSettingSpecs(true, false, true, 20, []string{"default", "smooth"}, ProjectionSpecs(180)...))
	if s.Int("bar_width") != 30 || s.Str("iconset") != "smooth" {
		t.Errorf("fable settings bar_width=%d iconset=%q, want 30/smooth", s.Int("bar_width"), s.Str("iconset"))
	}
	if cfg.SchemaVersion != currentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", cfg.SchemaVersion, currentSchemaVersion)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w.String(), "rate-limit-fable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected migration warning, got %v", warns)
	}
}

func TestMigrateSchemaV2SkipsWhenFablePresentOrNo7d(t *testing.T) {
	dir := useTempConfigDir(t)

	// Already has fable — leave order alone, no insert warning.
	writeConfigFile(t, dir, `
schema_version = 1
segments = ["rate-limit-7d", "rate-limit-fable", "model"]
`)
	cfg, warns := LoadConfigWarn()
	if len(cfg.Segments) != 3 || cfg.Segments[0] != "rate-limit-7d" || cfg.Segments[1] != "rate-limit-fable" {
		t.Errorf("segments should be unchanged: %v", cfg.Segments)
	}
	for _, w := range warns {
		if strings.Contains(w.String(), "added rate-limit-fable") {
			t.Errorf("should not re-add fable when already present: %v", warns)
		}
	}

	// No 7d — do not add fable.
	writeConfigFile(t, dir, `
schema_version = 1
segments = ["model", "cost"]
`)
	cfg, warns = LoadConfigWarn()
	for _, id := range cfg.Segments {
		if id == "rate-limit-fable" {
			t.Errorf("should not add fable without 7d: %v", cfg.Segments)
		}
	}
	for _, w := range warns {
		if strings.Contains(w.String(), "added rate-limit-fable") {
			t.Errorf("unexpected fable migration: %v", warns)
		}
	}
}

func TestMigrateSchemaV2DoesNotReaddAfterSave(t *testing.T) {
	dir := useTempConfigDir(t)
	writeConfigFile(t, dir, `
schema_version = 1
segments = ["rate-limit-7d", "model"]
`)
	cfg := LoadConfig()
	// User removes fable and saves (schema bumps to current).
	filtered := cfg.Segments[:0]
	for _, id := range cfg.Segments {
		if id != "rate-limit-fable" {
			filtered = append(filtered, id)
		}
	}
	cfg.Segments = filtered
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg2 := LoadConfig()
	for _, id := range cfg2.Segments {
		if id == "rate-limit-fable" {
			t.Fatalf("fable re-added after user removed and saved: %v", cfg2.Segments)
		}
	}
	if cfg2.SchemaVersion != currentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", cfg2.SchemaVersion, currentSchemaVersion)
	}
}

func TestMigrateLegacyJSON(t *testing.T) {
	dir := useTempConfigDir(t)
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	// Converted values survived.
	if len(cfg.Segments) != 3 || cfg.Segments[0] != "model" {
		t.Errorf("segments not migrated: %v", cfg.Segments)
	}
	if cfg.Lines["cost"] != 2 || cfg.Colors["model"] != "cyan" || cfg.Reflow != "group" {
		t.Errorf("scalar fields not migrated: %+v", cfg)
	}
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].ID != "mem" || cfg.Plugins[0].TimeoutMS != 200 {
		t.Errorf("plugins not migrated: %+v", cfg.Plugins)
	}
	if s := SettingsFor(cfg, "context-window", BarSettingSpecs(false, true, true, 20, []string{"default", "blocks", "smooth"}, TrendSpecs()...)); s.Int("bar_width") != 30 || s.Bool("show_warning") || s.Str("iconset") != "blocks" {
		t.Errorf("settings not migrated: %v", cfg.Settings)
	}

	// config.toml written, original renamed to .bak.
	tomlData, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	if !strings.Contains(string(tomlData), "# migrated from config.json") {
		t.Error("migration header missing")
	}
	if !strings.Contains(string(tomlData), "bar_width = 30") {
		t.Errorf("whole-number settings should be ints in TOML, got:\n%s", tomlData)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Error("config.json should be renamed away")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Error("config.json.bak missing")
	}

	// Idempotent: a second load reads TOML and never re-migrates, even if a
	// new config.json appears.
	if err := os.WriteFile(jsonPath, []byte(`{"segments": ["vim-mode"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2 := LoadConfig()
	if len(cfg2.Segments) != 3 {
		t.Errorf("TOML must win over a reappearing config.json: %v", cfg2.Segments)
	}
}

func TestMigrateMalformedJSONLeftAlone(t *testing.T) {
	dir := useTempConfigDir(t)
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	if len(cfg.Segments) != len(DefaultConfig().Segments) {
		t.Error("malformed legacy config should fall back to defaults")
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Error("malformed config.json must be left untouched")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
		t.Error("no config.toml should be written for malformed input")
	}
}

func TestMigratePreservesNilSegments(t *testing.T) {
	dir := useTempConfigDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"plugins": [{"id": "mem", "command": "x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	// nil segments → defaults + auto-appended plugin.
	if cfg.Segments[len(cfg.Segments)-1] != "mem" {
		t.Errorf("plugin not auto-appended after migration: %v", cfg.Segments)
	}
	tomlData, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tomlData), "segments") {
		t.Errorf("nil segments must stay omitted in TOML so auto-append survives:\n%s", tomlData)
	}
	// And the next plain TOML load behaves the same.
	cfg2 := LoadConfig()
	if cfg2.Segments[len(cfg2.Segments)-1] != "mem" {
		t.Errorf("auto-append broken after TOML round-trip: %v", cfg2.Segments)
	}
}

func TestConfigValidationClampsAndWarns(t *testing.T) {
	dir := useTempConfigDir(t)
	bad := `
reflow = "diagonal"
segments = ["model", "directory"]

[lines]
model = 12

[colors]
model = "mauve"

[settings.context-window]
bar_width = 30

[unknown_table]
x = 1
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warns := LoadConfigWarn()
	if cfg.Reflow != "" {
		t.Errorf("bad reflow should reset, got %q", cfg.Reflow)
	}
	if _, ok := cfg.Lines["model"]; ok {
		t.Error("out-of-range line should be dropped")
	}
	if _, ok := cfg.Colors["model"]; ok {
		t.Error("unknown color should be dropped")
	}
	wantSubstrings := []string{"reflow", "lines.model", "colors.model", "unknown_table"}
	for _, want := range wantSubstrings {
		found := false
		for _, w := range warns {
			if strings.Contains(w.String(), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a warning mentioning %q, got %v", want, warns)
		}
	}
	// Valid parts still load.
	if len(cfg.Segments) != 2 {
		t.Errorf("valid keys should still apply: %v", cfg.Segments)
	}
}
