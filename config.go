package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// ─── Config ──────────────────────────────────────────────────────────

// currentSchemaVersion is written into saved configs; bump on breaking
// config-schema changes so future migrations have an anchor.
const currentSchemaVersion = 1

type pluginField struct {
	ID   string `json:"id" toml:"id"`
	Line int    `json:"line" toml:"line,omitempty"`
	Desc string `json:"desc" toml:"desc,omitempty"`
}

type pluginDef struct {
	ID        string        `json:"id" toml:"id,omitempty"`
	Command   string        `json:"command" toml:"command"`
	Line      int           `json:"line" toml:"line,omitempty"`
	Desc      string        `json:"desc" toml:"desc,omitempty"`
	TimeoutMS int           `json:"timeout_ms" toml:"timeout_ms,omitempty"`
	Async     bool          `json:"async" toml:"async,omitempty"`
	RefreshMS int           `json:"refresh_ms" toml:"refresh_ms,omitempty"`
	Fields    []pluginField `json:"fields" toml:"fields,omitempty"`
}

// config is the persisted configuration. Field order here is the key order
// in the saved TOML: scalars and arrays first, tables after.
type config struct {
	SchemaVersion int                       `toml:"schema_version,omitempty"`
	Theme         string                    `toml:"theme,omitempty"`
	ColorDepth    string                    `toml:"color_depth,omitempty"`
	Reflow        string                    `toml:"reflow,omitempty"`
	Preset        string                    `toml:"preset,omitempty"`
	Segments      []string                  `toml:"segments"`
	ThemeColors   map[string]string         `toml:"theme_colors,omitempty"`
	Lines         map[string]int            `toml:"lines,omitempty"`
	Colors        map[string]string         `toml:"colors,omitempty"`
	Settings      map[string]map[string]any `toml:"settings,omitempty"`
	Style         styleConfig               `toml:"style,omitempty"`
	State         stateConfig               `toml:"state,omitempty"`
	ReleaseNotes  releaseNotesConfig        `toml:"release_notes,omitempty"`
	Plugins       []pluginDef               `toml:"plugins,omitempty"`
	Update        updateConfig              `toml:"update,omitempty"`
}

// updateConfig is the [update] table in config.toml. Mode "" or unset means
// the default ("notify"); CheckHours nil means 24h. Validation lives in
// validateConfig and mirrors the [release_notes] warn-and-normalize style.
type updateConfig struct {
	Mode       string `toml:"mode,omitempty"`        // notify|auto|off
	CheckHours *int   `toml:"check_hours,omitempty"` // 1..168, default 24
}

func (u updateConfig) mode() string {
	if u.Mode == "" {
		return "notify"
	}
	return u.Mode
}

func (u updateConfig) checkEvery() time.Duration {
	if u.CheckHours == nil {
		return 24 * time.Hour
	}
	return time.Duration(*u.CheckHours) * time.Hour
}

// styleConfig is the [style] table: separator glyph and line padding.
type styleConfig struct {
	Separator       string `toml:"separator,omitempty"`        // bar|dot|slash|chevron|powerline|space|custom
	SeparatorCustom string `toml:"separator_custom,omitempty"` // used when separator = "custom"
	Padding         *int   `toml:"padding,omitempty"`          // leading spaces per line (default 1)
}

func defaultConfig() config {
	return config{
		Segments: []string{
			"vim-mode", "sandbox", "session-name", "agent-state", "directory",
			"added-dirs", "git-branch", "artifact-count", "lines-changed", "cache-percent", "cost", "update",
			"model", "output-style", "version", "duration", "cost-rate", "api-efficiency", "tokens",
			"context-window", "rate-limit-5h", "rate-limit-7d", "plan-tier",
		},
		Lines: nil,
	}
}

// configDirOverride redirects the config directory; set only by tests.
var configDirOverride string

func configDir() string {
	if configDirOverride != "" {
		return configDirOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".config", "claude-statusline")
}

func configPath() string {
	return filepath.Join(configDir(), "config.toml")
}

// configWarning is a non-fatal problem found while loading or validating the
// config. The renderer never fails on bad config — it normalizes and warns.
type configWarning struct {
	Path string // config location, e.g. "lines.cost"
	Msg  string
}

func (w configWarning) String() string {
	if w.Path == "" {
		return w.Msg
	}
	return w.Path + ": " + w.Msg
}

func loadConfig() config {
	cfg, _ := loadConfigWarn()
	return cfg
}

// loadConfigWarn loads the TOML config (migrating a legacy config.json first
// if present), merges defaults, and normalizes invalid values. Warnings are
// surfaced by --debug and the TUI; the render path ignores them unless
// STATUSLINE_VERBOSE=1.
func loadConfigWarn() (config, []configWarning) {
	if migrated, ok := migrateLegacyJSON(); ok {
		cfg := mergeWithDefaults(migrated)
		return cfg, validateConfig(&cfg)
	}

	data, err := os.ReadFile(configPath())
	if err != nil {
		return defaultConfig(), nil
	}

	var warns []configWarning
	var loaded config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&loaded); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			// Unknown keys only — warn, then decode leniently.
			for _, e := range strict.Errors {
				warns = append(warns, configWarning{Path: strings.Join(e.Key(), "."), Msg: "unknown config key (ignored)"})
			}
			loaded = config{}
			if err := toml.Unmarshal(data, &loaded); err != nil {
				warns = append(warns, configWarning{Msg: fmt.Sprintf("config.toml unreadable, using defaults: %v", err)})
				return defaultConfig(), warns
			}
		} else {
			warns = append(warns, configWarning{Msg: fmt.Sprintf("config.toml unreadable, using defaults: %v", err)})
			return defaultConfig(), warns
		}
	}

	cfg := mergeWithDefaults(loaded)
	warns = append(warns, validateConfig(&cfg)...)
	return cfg, warns
}

// mergeWithDefaults applies the nil-vs-empty segments semantics: an explicit
// empty array means "hide everything"; an absent key means defaults plus
// auto-appended plugin segment IDs.
func mergeWithDefaults(loaded config) config {
	cfg := defaultConfig()
	cfg.SchemaVersion = loaded.SchemaVersion
	// A preset supplies the layout baseline — only when segments is absent
	// (an explicit segments list always wins over the preset). Plugin
	// auto-append below still runs, since loaded.Segments stays nil.
	if loaded.Preset != "" && loaded.Segments == nil {
		if p, ok := presetByID(loaded.Preset); ok {
			cfg.Segments = append([]string(nil), p.Segments...)
			if loaded.Lines == nil {
				loaded.Lines = p.Lines
			}
			if loaded.Settings == nil {
				loaded.Settings = p.Settings
			}
			if loaded.Theme == "" {
				loaded.Theme = p.Theme
			}
		}
	}
	cfg.Preset = loaded.Preset
	if loaded.Segments != nil {
		cfg.Segments = loaded.Segments
	}
	cfg.Theme = loaded.Theme
	cfg.ColorDepth = loaded.ColorDepth
	cfg.ThemeColors = loaded.ThemeColors
	cfg.Lines = loaded.Lines
	cfg.Colors = loaded.Colors
	cfg.Plugins = loaded.Plugins
	cfg.Reflow = loaded.Reflow
	cfg.Settings = loaded.Settings
	cfg.Style = loaded.Style
	cfg.State = loaded.State
	cfg.ReleaseNotes = loaded.ReleaseNotes
	cfg.Update = loaded.Update
	if loaded.Segments == nil {
		inSegments := make(map[string]bool, len(cfg.Segments))
		for _, id := range cfg.Segments {
			inSegments[id] = true
		}
		for _, p := range cfg.Plugins {
			if len(p.Fields) > 0 {
				for _, f := range p.Fields {
					if f.ID != "" && !inSegments[f.ID] {
						cfg.Segments = append(cfg.Segments, f.ID)
						inSegments[f.ID] = true
					}
				}
			} else if p.ID != "" && !inSegments[p.ID] {
				cfg.Segments = append(cfg.Segments, p.ID)
			}
		}
	}
	return cfg
}

// validateConfig normalizes invalid values in place and reports what changed.
// It never fails: bad values reset to safe ones. Checks that need the full
// segment registry (unknown segment IDs, per-segment setting keys) live in
// validateSegmentRefs, which runs after plugin registration.
func validateConfig(cfg *config) []configWarning {
	var warns []configWarning
	switch cfg.Reflow {
	case "", "off", "cascade", "group":
	default:
		warns = append(warns, configWarning{Path: "reflow", Msg: fmt.Sprintf("%q is not off, cascade, or group (ignored)", cfg.Reflow)})
		cfg.Reflow = ""
	}
	if cfg.Preset != "" {
		if _, ok := presetByID(cfg.Preset); !ok {
			names := make([]string, len(layoutPresets))
			for i, p := range layoutPresets {
				names[i] = p.ID
			}
			warns = append(warns, configWarning{Path: "preset", Msg: fmt.Sprintf("%q is not a preset (ignored); known: %s", cfg.Preset, strings.Join(names, ", "))})
			cfg.Preset = ""
		}
	}
	if cfg.Theme != "" {
		found := cfg.Theme == "original" // alias for classic
		for _, id := range themeIDs() {
			if id == cfg.Theme {
				found = true
				break
			}
		}
		if !found {
			warns = append(warns, configWarning{Path: "theme", Msg: fmt.Sprintf("%q is not a built-in theme (using classic); known: %s", cfg.Theme, strings.Join(themeIDs(), ", "))})
			cfg.Theme = ""
		}
	}
	switch strings.ToLower(cfg.ColorDepth) {
	case "", "auto", "truecolor", "24bit", "256", "16", "none":
	default:
		warns = append(warns, configWarning{Path: "color_depth", Msg: fmt.Sprintf("%q is not auto/truecolor/256/16/none (using auto)", cfg.ColorDepth)})
		cfg.ColorDepth = ""
	}
	for role, spec := range cfg.ThemeColors {
		knownRole := false
		for _, r := range themeRoles {
			if r == role {
				knownRole = true
				break
			}
		}
		if !knownRole {
			warns = append(warns, configWarning{Path: "theme_colors." + role, Msg: "unknown theme role (ignored)"})
			delete(cfg.ThemeColors, role)
			continue
		}
		if !validColorSpec(spec) {
			warns = append(warns, configWarning{Path: "theme_colors." + role, Msg: fmt.Sprintf("%q is not a hex value, 256 index, or color name (ignored)", spec)})
			delete(cfg.ThemeColors, role)
		}
	}
	for id, n := range cfg.Lines {
		if n < 1 || n > 9 {
			warns = append(warns, configWarning{Path: "lines." + id, Msg: fmt.Sprintf("line %d out of range 1-9 (ignored)", n)})
			delete(cfg.Lines, id)
		}
	}
	for id, name := range cfg.Colors {
		if !validColorSpec(name) {
			warns = append(warns, configWarning{Path: "colors." + id, Msg: fmt.Sprintf("%q is not a known color, theme role, hex value, or 256 index (ignored)", name)})
			delete(cfg.Colors, id)
		}
	}
	switch cfg.Style.Separator {
	case "", "bar", "dot", "slash", "chevron", "powerline", "space":
	case "custom":
		if cfg.Style.SeparatorCustom == "" {
			warns = append(warns, configWarning{Path: "style.separator", Msg: "custom separator selected but separator_custom is empty (using bar)"})
			cfg.Style.Separator = ""
		}
	default:
		warns = append(warns, configWarning{Path: "style.separator", Msg: fmt.Sprintf("%q is not bar/dot/slash/chevron/powerline/space/custom (using bar)", cfg.Style.Separator)})
		cfg.Style.Separator = ""
	}
	if p := cfg.Style.Padding; p != nil && (*p < 0 || *p > 8) {
		warns = append(warns, configWarning{Path: "style.padding", Msg: fmt.Sprintf("%d out of range 0-8 (using 1)", *p)})
		cfg.Style.Padding = nil
	}
	for i, p := range cfg.Plugins {
		path := fmt.Sprintf("plugins[%d]", i)
		if p.Command == "" {
			warns = append(warns, configWarning{Path: path, Msg: "missing command (plugin disabled)"})
		}
		if p.ID == "" && len(p.Fields) == 0 {
			warns = append(warns, configWarning{Path: path, Msg: "missing id and fields (plugin unreachable)"})
		}
		if p.Async {
			if p.RefreshMS == 0 {
				cfg.Plugins[i].RefreshMS = 5000
			} else if p.RefreshMS < 500 {
				warns = append(warns, configWarning{Path: path + ".refresh_ms", Msg: fmt.Sprintf("%d below minimum 500 (clamped)", p.RefreshMS)})
				cfg.Plugins[i].RefreshMS = 500
			}
			if p.TimeoutMS == 0 {
				cfg.Plugins[i].TimeoutMS = 10000
			} else if p.TimeoutMS > 60000 {
				warns = append(warns, configWarning{Path: path + ".timeout_ms", Msg: fmt.Sprintf("%d above maximum 60000 (clamped)", p.TimeoutMS)})
				cfg.Plugins[i].TimeoutMS = 60000
			}
		}
	}
	if d := cfg.ReleaseNotes.DurationSeconds; d != nil && (*d < 0 || *d > 600) {
		warns = append(warns, configWarning{Path: "release_notes.duration_seconds", Msg: fmt.Sprintf("%d out of range 0-600 (using %d)", *d, defaultAnnounceSeconds)})
		cfg.ReleaseNotes.DurationSeconds = nil
	}
	if cfg.ReleaseNotes.MaxLines != nil {
		switch v := cfg.ReleaseNotes.MaxLines.(type) {
		case int64:
			if v < 0 {
				warns = append(warns, configWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%d out of range (using %d)", v, defaultMaxLines)})
				cfg.ReleaseNotes.MaxLines = nil
			}
		case string:
			switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(v, "_", "-"), " ", "-")) {
			case "status-line", "same-as-status-line", "statusline", "same-as-statusline":
				// ok
			default:
				warns = append(warns, configWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%q is not an integer or status-line (using %d)", v, defaultMaxLines)})
				cfg.ReleaseNotes.MaxLines = nil
			}
		default:
			warns = append(warns, configWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%q is not an integer or status-line (using %d)", fmt.Sprintf("%v", v), defaultMaxLines)})
			cfg.ReleaseNotes.MaxLines = nil
		}
	}
	switch cfg.Update.Mode {
	case "", "notify", "auto", "off":
	default:
		warns = append(warns, configWarning{Path: "update.mode", Msg: fmt.Sprintf("%q is not notify/auto/off (using notify)", cfg.Update.Mode)})
		cfg.Update.Mode = ""
	}
	if h := cfg.Update.CheckHours; h != nil && (*h < 1 || *h > 168) {
		warns = append(warns, configWarning{Path: "update.check_hours", Msg: fmt.Sprintf("%d out of range 1-168 (using 24)", *h)})
		cfg.Update.CheckHours = nil
	}
	return warns
}

// validateSegmentRefs reports config references to segments or setting keys
// that don't exist. Requires initSegments to have run (so plugin segments are
// registered). Read-only: unknown IDs are kept (the renderer skips them).
func validateSegmentRefs(cfg config) []configWarning {
	var warns []configWarning
	known := func(id string) bool {
		_, ok := segmentByID(id)
		return ok
	}
	for _, id := range cfg.Segments {
		if !known(id) {
			warns = append(warns, configWarning{Path: "segments", Msg: fmt.Sprintf("unknown segment %q", id)})
		}
	}
	for id := range cfg.Lines {
		if !known(id) {
			warns = append(warns, configWarning{Path: "lines." + id, Msg: "unknown segment"})
		}
	}
	for id := range cfg.Colors {
		if !known(id) {
			warns = append(warns, configWarning{Path: "colors." + id, Msg: "unknown segment"})
		}
	}
	for id, vals := range cfg.Settings {
		seg, ok := segmentByID(id)
		if !ok {
			warns = append(warns, configWarning{Path: "settings." + id, Msg: "unknown segment"})
			continue
		}
		for key := range vals {
			found := false
			for _, sp := range seg.settings {
				if sp.Key == key && !sp.Ephemeral {
					found = true
					break
				}
			}
			if !found {
				warns = append(warns, configWarning{Path: "settings." + id + "." + key, Msg: "unknown setting key (ignored)"})
			}
		}
	}
	return warns
}

// marshalConfigTOML serializes the config, preserving the nil-vs-empty
// segments distinction: a nil Segments slice omits the key entirely so the
// "defaults + auto-append plugins" semantics survive a round-trip.
func marshalConfigTOML(cfg config) ([]byte, error) {
	cfg.SchemaVersion = currentSchemaVersion
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Segments == nil {
		data = bytes.Replace(data, []byte("segments = []\n"), nil, 1)
	}
	return data, nil
}

// writeFileAtomic writes via a temp file in the same directory + rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func saveConfig(cfg config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := marshalConfigTOML(cfg)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}
