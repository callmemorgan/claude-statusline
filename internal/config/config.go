package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/sys"
)

// ─── Config ──────────────────────────────────────────────────────────

// currentSchemaVersion is written into saved configs; bump on breaking
// config-schema changes so future migrations have an anchor.
const currentSchemaVersion = 1

// DefaultAnnounceSeconds is the takeover window when release_notes.duration_seconds is
// unset (ValidateConfig also resets out-of-range values to this default).
const DefaultAnnounceSeconds = 25

// DefaultMaxLines is the takeover line budget when release_notes.max_lines is unset.
const DefaultMaxLines = 10

// SameAsStatusLineSentinel is returned by ReleaseNotesConfig.ResolvedMaxLines to mean "use the
// statusline's own line count".
const SameAsStatusLineSentinel = -1

type PluginField struct {
	ID      string `json:"id" toml:"id"`
	Line    int    `json:"line" toml:"line,omitempty"`
	Desc    string `json:"desc" toml:"desc,omitempty"`
	Preview string `json:"preview" toml:"preview,omitempty"`
}

type PluginDef struct {
	ID        string        `json:"id" toml:"id,omitempty"`
	Command   string        `json:"command" toml:"command"`
	Line      int           `json:"line" toml:"line,omitempty"`
	Desc      string        `json:"desc" toml:"desc,omitempty"`
	Preview   string        `json:"preview" toml:"preview,omitempty"`
	TimeoutMS int           `json:"timeout_ms" toml:"timeout_ms,omitempty"`
	Async     bool          `json:"async" toml:"async,omitempty"`
	RefreshMS int           `json:"refresh_ms" toml:"refresh_ms,omitempty"`
	Fields    []PluginField `json:"fields" toml:"fields,omitempty"`
}

// Config is the persisted configuration. Field order here is the key order
// in the saved TOML: scalars and arrays first, tables after.
type Config struct {
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
	Style         StyleConfig               `toml:"style,omitempty"`
	State         state.StateConfig         `toml:"state,omitempty"`
	ReleaseNotes  ReleaseNotesConfig        `toml:"release_notes,omitempty"`
	Plugins       []PluginDef               `toml:"plugins,omitempty"`
	Update        UpdateConfig              `toml:"update,omitempty"`
}

// UpdateConfig is the [update] table in config.toml. Mode "" or unset means
// the default ("notify"); CheckHours nil means 24h. Validation lives in
// ValidateConfig and mirrors the [release_notes] warn-and-normalize style.
type UpdateConfig struct {
	Mode       string `toml:"mode,omitempty"`        // notify|auto|off
	CheckHours *int   `toml:"check_hours,omitempty"` // 1..168, default 24
}

func (u UpdateConfig) ModeOrDefault() string {
	if u.Mode == "" {
		return "notify"
	}
	return u.Mode
}

func (u UpdateConfig) CheckEvery() time.Duration {
	if u.CheckHours == nil {
		return 24 * time.Hour
	}
	return time.Duration(*u.CheckHours) * time.Hour
}

// StyleConfig is the [style] table: separator glyph and line padding.
type StyleConfig struct {
	Separator       string `toml:"separator,omitempty"`        // bar|dot|slash|chevron|powerline|space|custom
	SeparatorCustom string `toml:"separator_custom,omitempty"` // used when separator = "custom"
	Padding         *int   `toml:"padding,omitempty"`          // leading spaces per line (default 1)
}

// ReleaseNotesConfig is the [release_notes] table in config.toml.
type ReleaseNotesConfig struct {
	Announce        *bool `toml:"announce,omitempty"`         // default true
	DurationSeconds *int  `toml:"duration_seconds,omitempty"` // default 25, 0 disables
	// MaxLines controls how many lines the post-upgrade takeover may occupy.
	// It can be an integer (0 = same as the statusline) or one of the symbolic
	// strings "status-line", "same-as-status-line", "statusline", or
	// "same-as-statusline". nil defaults to DefaultMaxLines.
	MaxLines any `toml:"max_lines,omitempty"`
}

func (r ReleaseNotesConfig) AnnounceOrDefault() bool {
	return r.Announce == nil || *r.Announce
}

func (r ReleaseNotesConfig) Duration() time.Duration {
	if r.DurationSeconds == nil {
		return DefaultAnnounceSeconds * time.Second
	}
	return time.Duration(*r.DurationSeconds) * time.Second
}

// ResolvedMaxLines returns the configured max_lines value normalized into a
// line count. A negative result means "same as the statusline".
func (r ReleaseNotesConfig) ResolvedMaxLines() int {
	if r.MaxLines == nil {
		return DefaultMaxLines
	}
	switch v := r.MaxLines.(type) {
	case int64:
		return int(v)
	case int:
		return v
	case string:
		switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(v, "_", "-"), " ", "-")) {
		case "status-line", "same-as-status-line", "statusline", "same-as-statusline":
			return SameAsStatusLineSentinel
		}
	}
	return DefaultMaxLines
}

func DefaultConfig() Config {
	return Config{
		Segments: []string{
			"vim-mode", "sandbox", "session-name", "agent-state", "directory",
			"added-dirs", "git-branch", "artifact-count", "lines-changed", "cache-percent", "cost", "update",
			"model", "output-style", "version", "duration", "cost-rate", "api-efficiency", "tokens",
			"context-window", "rate-limit-5h", "rate-limit-7d", "plan-tier",
		},
		Lines: nil,
	}
}

// ConfigDirOverride redirects the config directory; set only by tests.
var ConfigDirOverride string

func ConfigDir() string {
	if ConfigDirOverride != "" {
		return ConfigDirOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".config", "claude-statusline")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

// ConfigWarning is a non-fatal problem found while loading or validating the
// config. The renderer never fails on bad config — it normalizes and warns.
type ConfigWarning struct {
	Path string // config location, e.g. "lines.cost"
	Msg  string
}

func (w ConfigWarning) String() string {
	if w.Path == "" {
		return w.Msg
	}
	return w.Path + ": " + w.Msg
}

func LoadConfig() Config {
	cfg, _ := LoadConfigWarn()
	return cfg
}

// LoadConfigWarn loads the TOML config (migrating a legacy config.json first
// if present), merges defaults, and normalizes invalid values. Warnings are
// surfaced by --debug and the TUI; the render path ignores them unless
// STATUSLINE_VERBOSE=1.
func LoadConfigWarn() (Config, []ConfigWarning) {
	if migrated, ok := migrateLegacyJSON(); ok {
		cfg := MergeWithDefaults(migrated)
		return cfg, ValidateConfig(&cfg)
	}

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return DefaultConfig(), nil
	}

	var warns []ConfigWarning
	var loaded Config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&loaded); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			// Unknown keys only — warn, then decode leniently.
			for _, e := range strict.Errors {
				warns = append(warns, ConfigWarning{Path: strings.Join(e.Key(), "."), Msg: "unknown config key (ignored)"})
			}
			loaded = Config{}
			if err := toml.Unmarshal(data, &loaded); err != nil {
				warns = append(warns, ConfigWarning{Msg: fmt.Sprintf("config.toml unreadable, using defaults: %v", err)})
				return DefaultConfig(), warns
			}
		} else {
			warns = append(warns, ConfigWarning{Msg: fmt.Sprintf("config.toml unreadable, using defaults: %v", err)})
			return DefaultConfig(), warns
		}
	}

	cfg := MergeWithDefaults(loaded)
	warns = append(warns, ValidateConfig(&cfg)...)
	return cfg, warns
}

// MergeWithDefaults applies the nil-vs-empty segments semantics: an explicit
// empty array means "hide everything"; an absent key means defaults plus
// auto-appended plugin segment IDs.
func MergeWithDefaults(loaded Config) Config {
	cfg := DefaultConfig()
	cfg.SchemaVersion = loaded.SchemaVersion
	// A preset supplies the layout baseline — only when segments is absent
	// (an explicit segments list always wins over the preset). Plugin
	// auto-append below still runs, since loaded.Segments stays nil.
	if loaded.Preset != "" && loaded.Segments == nil {
		if p, ok := PresetByID(loaded.Preset); ok {
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

// ValidateConfig normalizes invalid values in place and reports what changed.
// It never fails: bad values reset to safe ones. Checks that need the full
// segment registry (unknown segment IDs, per-segment setting keys) live in
// the caller, which runs after plugin registration.
func ValidateConfig(cfg *Config) []ConfigWarning {
	var warns []ConfigWarning
	switch cfg.Reflow {
	case "", "off", "cascade", "group":
	default:
		warns = append(warns, ConfigWarning{Path: "reflow", Msg: fmt.Sprintf("%q is not off, cascade, or group (ignored)", cfg.Reflow)})
		cfg.Reflow = ""
	}
	if cfg.Preset != "" {
		if _, ok := PresetByID(cfg.Preset); !ok {
			names := make([]string, len(LayoutPresets))
			for i, p := range LayoutPresets {
				names[i] = p.ID
			}
			warns = append(warns, ConfigWarning{Path: "preset", Msg: fmt.Sprintf("%q is not a preset (ignored); known: %s", cfg.Preset, strings.Join(names, ", "))})
			cfg.Preset = ""
		}
	}
	if cfg.Theme != "" {
		found := cfg.Theme == "original" // alias for classic
		for _, id := range palette.ThemeIDs() {
			if id == cfg.Theme {
				found = true
				break
			}
		}
		if !found {
			warns = append(warns, ConfigWarning{Path: "theme", Msg: fmt.Sprintf("%q is not a built-in theme (using classic); known: %s", cfg.Theme, strings.Join(palette.ThemeIDs(), ", "))})
			cfg.Theme = ""
		}
	}
	switch strings.ToLower(cfg.ColorDepth) {
	case "", "auto", "truecolor", "24bit", "256", "16", "none":
	default:
		warns = append(warns, ConfigWarning{Path: "color_depth", Msg: fmt.Sprintf("%q is not auto/truecolor/256/16/none (using auto)", cfg.ColorDepth)})
		cfg.ColorDepth = ""
	}
	for role, spec := range cfg.ThemeColors {
		knownRole := false
		for _, r := range palette.ThemeRoles {
			if r == role {
				knownRole = true
				break
			}
		}
		if !knownRole {
			warns = append(warns, ConfigWarning{Path: "theme_colors." + role, Msg: "unknown theme role (ignored)"})
			delete(cfg.ThemeColors, role)
			continue
		}
		if !palette.ValidColorSpec(spec) {
			warns = append(warns, ConfigWarning{Path: "theme_colors." + role, Msg: fmt.Sprintf("%q is not a hex value, 256 index, or color name (ignored)", spec)})
			delete(cfg.ThemeColors, role)
		}
	}
	for id, n := range cfg.Lines {
		if n < 1 || n > 9 {
			warns = append(warns, ConfigWarning{Path: "lines." + id, Msg: fmt.Sprintf("line %d out of range 1-9 (ignored)", n)})
			delete(cfg.Lines, id)
		}
	}
	for id, name := range cfg.Colors {
		if !palette.ValidColorSpec(name) {
			warns = append(warns, ConfigWarning{Path: "colors." + id, Msg: fmt.Sprintf("%q is not a known color, theme role, hex value, or 256 index (ignored)", name)})
			delete(cfg.Colors, id)
		}
	}
	switch cfg.Style.Separator {
	case "", "bar", "dot", "slash", "chevron", "powerline", "space":
	case "custom":
		if cfg.Style.SeparatorCustom == "" {
			warns = append(warns, ConfigWarning{Path: "style.separator", Msg: "custom separator selected but separator_custom is empty (using bar)"})
			cfg.Style.Separator = ""
		}
	default:
		warns = append(warns, ConfigWarning{Path: "style.separator", Msg: fmt.Sprintf("%q is not bar/dot/slash/chevron/powerline/space/custom (using bar)", cfg.Style.Separator)})
		cfg.Style.Separator = ""
	}
	if p := cfg.Style.Padding; p != nil && (*p < 0 || *p > 8) {
		warns = append(warns, ConfigWarning{Path: "style.padding", Msg: fmt.Sprintf("%d out of range 0-8 (using 1)", *p)})
		cfg.Style.Padding = nil
	}
	for i, p := range cfg.Plugins {
		path := fmt.Sprintf("plugins[%d]", i)
		if p.Command == "" {
			warns = append(warns, ConfigWarning{Path: path, Msg: "missing command (plugin disabled)"})
		}
		if p.ID == "" && len(p.Fields) == 0 {
			warns = append(warns, ConfigWarning{Path: path, Msg: "missing id and fields (plugin unreachable)"})
		}
		if p.Async {
			if p.RefreshMS == 0 {
				cfg.Plugins[i].RefreshMS = 5000
			} else if p.RefreshMS < 500 {
				warns = append(warns, ConfigWarning{Path: path + ".refresh_ms", Msg: fmt.Sprintf("%d below minimum 500 (clamped)", p.RefreshMS)})
				cfg.Plugins[i].RefreshMS = 500
			}
			if p.TimeoutMS == 0 {
				cfg.Plugins[i].TimeoutMS = 10000
			} else if p.TimeoutMS > 60000 {
				warns = append(warns, ConfigWarning{Path: path + ".timeout_ms", Msg: fmt.Sprintf("%d above maximum 60000 (clamped)", p.TimeoutMS)})
				cfg.Plugins[i].TimeoutMS = 60000
			}
		}
	}
	if d := cfg.ReleaseNotes.DurationSeconds; d != nil && (*d < 0 || *d > 600) {
		warns = append(warns, ConfigWarning{Path: "release_notes.duration_seconds", Msg: fmt.Sprintf("%d out of range 0-600 (using %d)", *d, DefaultAnnounceSeconds)})
		cfg.ReleaseNotes.DurationSeconds = nil
	}
	if cfg.ReleaseNotes.MaxLines != nil {
		switch v := cfg.ReleaseNotes.MaxLines.(type) {
		case int64:
			if v < 0 {
				warns = append(warns, ConfigWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%d out of range (using %d)", v, DefaultMaxLines)})
				cfg.ReleaseNotes.MaxLines = nil
			}
		case string:
			switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(v, "_", "-"), " ", "-")) {
			case "status-line", "same-as-status-line", "statusline", "same-as-statusline":
				// ok
			default:
				warns = append(warns, ConfigWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%q is not an integer or status-line (using %d)", v, DefaultMaxLines)})
				cfg.ReleaseNotes.MaxLines = nil
			}
		default:
			warns = append(warns, ConfigWarning{Path: "release_notes.max_lines", Msg: fmt.Sprintf("%q is not an integer or status-line (using %d)", fmt.Sprintf("%v", v), DefaultMaxLines)})
			cfg.ReleaseNotes.MaxLines = nil
		}
	}
	switch cfg.Update.Mode {
	case "", "notify", "auto", "off":
	default:
		warns = append(warns, ConfigWarning{Path: "update.mode", Msg: fmt.Sprintf("%q is not notify/auto/off (using notify)", cfg.Update.Mode)})
		cfg.Update.Mode = ""
	}
	if h := cfg.Update.CheckHours; h != nil && (*h < 1 || *h > 168) {
		warns = append(warns, ConfigWarning{Path: "update.check_hours", Msg: fmt.Sprintf("%d out of range 1-168 (using 24)", *h)})
		cfg.Update.CheckHours = nil
	}
	return warns
}

// MarshalConfigTOML serializes the config, preserving the nil-vs-empty
// segments distinction: a nil Segments slice omits the key entirely so the
// "defaults + auto-append plugins" semantics survive a round-trip.
func MarshalConfigTOML(cfg Config) ([]byte, error) {
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

func SaveConfig(cfg Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := MarshalConfigTOML(cfg)
	if err != nil {
		return err
	}
	return sys.WriteFileAtomic(path, data)
}
