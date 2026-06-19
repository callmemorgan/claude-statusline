package main

// ─── Legacy Config Migration ─────────────────────────────────────────
//
// Pre-1.0 configs were JSON at ~/.config/claude-statusline/config.json.
// On first load after upgrading, the JSON is converted to config.toml once
// and the original is kept as config.json.bak. config.toml always wins:
// if it exists, the JSON is never looked at again.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/sys"
)

// legacyConfig matches the pre-1.0 JSON schema. The old fixed-field
// segmentSettings struct deserializes losslessly into maps because the JSON
// keys are identical to today's schema-driven setting keys.
type legacyConfig struct {
	Segments []string                  `json:"segments"`
	Lines    map[string]int            `json:"lines"`
	Colors   map[string]string         `json:"colors"`
	Plugins  []pluginDef               `json:"plugins"`
	Reflow   string                    `json:"reflow"`
	Settings map[string]map[string]any `json:"settings"`
}

// migrateLegacyJSON converts config.json to config.toml. Returns the
// converted config and true when a legacy config was parsed this run — the
// caller uses it directly so a failed TOML write still renders correctly
// (migration retries next invocation). Idempotent: a present config.toml
// short-circuits immediately.
func migrateLegacyJSON() (config, bool) {
	dir := configDir()
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		return config{}, false
	}
	jsonPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return config{}, false
	}

	var legacy legacyConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		fmt.Fprintf(os.Stderr, "claude-statusline: cannot migrate config.json (%v); using defaults\n", err)
		return config{}, false
	}

	cfg := config{
		SchemaVersion: currentSchemaVersion,
		Reflow:        legacy.Reflow,
		Segments:      legacy.Segments,
		Lines:         legacy.Lines,
		Colors:        legacy.Colors,
		Settings:      normalizeSettingsNumbers(legacy.Settings),
		Plugins:       legacy.Plugins,
	}

	tomlData, err := marshalConfigTOML(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude-statusline: cannot convert config.json to TOML (%v)\n", err)
		return cfg, true
	}
	header := fmt.Sprintf("# migrated from config.json on %s\n", time.Now().Format("2006-01-02"))
	if err := sys.WriteFileAtomic(filepath.Join(dir, "config.toml"), append([]byte(header), tomlData...)); err != nil {
		// Keep config.json in place; migration retries on the next run and
		// this run uses the in-memory conversion.
		fmt.Fprintf(os.Stderr, "claude-statusline: cannot write config.toml (%v); will retry\n", err)
		return cfg, true
	}
	if err := os.Rename(jsonPath, jsonPath+".bak"); err == nil {
		fmt.Fprintf(os.Stderr, "claude-statusline: migrated config to %s (old config saved as config.json.bak)\n", filepath.Join(dir, "config.toml"))
	}
	return cfg, true
}

// normalizeSettingsNumbers converts whole-number float64s (how JSON decodes
// every number) to ints so the written TOML reads bar_width = 30, not 30.0.
func normalizeSettingsNumbers(settings map[string]map[string]any) map[string]map[string]any {
	for _, vals := range settings {
		for k, v := range vals {
			if f, ok := v.(float64); ok && f == math.Trunc(f) {
				vals[k] = int(f)
			}
		}
	}
	return settings
}
