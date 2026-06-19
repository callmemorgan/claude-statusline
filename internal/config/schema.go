package config

// ─── Segment Settings Schema ─────────────────────────────────────────
//
// Each segment declares its configurable settings as a list of SettingSpec.
// The TUI flyout, settings resolution, validation, and config persistence all
// derive from this single declaration — there is no parallel feature map.

import (
	"strconv"

	"github.com/callmemorgan/claude-statusline/internal/palette"
)

type SettingKind int

const (
	KindBool SettingKind = iota
	KindInt
	KindEnum
	KindColor // like KindEnum for cycling, but accepts any color spec (hex, 256 index, theme role)
)

type SettingSpec struct {
	Key       string // config key + flyout id, e.g. "bar_width"
	Name      string // flyout label, e.g. "Bar width"
	Desc      string // flyout description panel text
	Kind      SettingKind
	Default   any      // bool | int | string, matching Kind
	Min, Max  int      // KindInt bounds
	Step      int      // KindInt coarse step (Shift+←/→); 0 or 1 = fine only
	Options   []string // KindEnum ordered values
	Ephemeral bool     // TUI-only (stress_test, sync_to_all); never persisted
}

// Coerce validates a raw config value against the spec, clamping integers and
// falling back to the default on type or domain mismatch. JSON decodes numbers
// as float64 and TOML as int64; both are accepted for KindInt.
func (sp SettingSpec) Coerce(raw any) any {
	switch sp.Kind {
	case KindBool:
		if v, ok := raw.(bool); ok {
			return v
		}
	case KindInt:
		var n int
		switch v := raw.(type) {
		case int:
			n = v
		case int64:
			n = int(v)
		case float64:
			n = int(v)
		default:
			return sp.Default
		}
		if n < sp.Min {
			n = sp.Min
		}
		if n > sp.Max {
			n = sp.Max
		}
		return n
	case KindEnum:
		if v, ok := raw.(string); ok {
			for _, o := range sp.Options {
				if o == v {
					return v
				}
			}
		}
	case KindColor:
		if v, ok := raw.(string); ok && palette.ValidColorSpec(v) {
			return v
		}
	}
	return sp.Default
}

// Settings is a fully-resolved per-segment settings map: every non-ephemeral
// key from the segment's schema is present with a validated value.
type Settings map[string]any

func (s Settings) Bool(key string) bool {
	v, _ := s[key].(bool)
	return v
}

func (s Settings) Int(key string) int {
	v, _ := s[key].(int)
	return v
}

func (s Settings) Str(key string) string {
	v, _ := s[key].(string)
	return v
}

func (s Settings) ValueString(sp SettingSpec) string {
	switch sp.Kind {
	case KindBool:
		if s.Bool(sp.Key) {
			return "on"
		}
		return "off"
	case KindEnum, KindColor:
		return s.Str(sp.Key)
	case KindInt:
		return strconv.Itoa(s.Int(sp.Key))
	}
	return ""
}

// SettingsFor merges the segment's schema defaults with the raw values stored
// in cfg.Settings, validating each one. Segments without a schema get an
// empty map.
func SettingsFor(cfg Config, segID string, specs []SettingSpec) Settings {
	out := Settings{}
	raw := cfg.Settings[segID]
	for _, sp := range specs {
		if sp.Ephemeral {
			continue
		}
		rv, ok := raw[sp.Key]
		if !ok {
			out[sp.Key] = sp.Default
			continue
		}
		out[sp.Key] = sp.Coerce(rv)
	}
	return out
}

// PruneSettings returns only the keys that differ from the segment's schema
// defaults — what gets persisted. Returns nil when everything is default.
func PruneSettings(specs []SettingSpec, s Settings) map[string]any {
	var out map[string]any
	for _, sp := range specs {
		if sp.Ephemeral {
			continue
		}
		if v, ok := s[sp.Key]; ok && v != sp.Default {
			if out == nil {
				out = map[string]any{}
			}
			out[sp.Key] = v
		}
	}
	return out
}

// SetSegmentSettings stores pruned values for a segment, removing the entry
// entirely when nothing differs from defaults.
func SetSegmentSettings(cfg *Config, segID string, vals map[string]any) {
	if len(vals) == 0 {
		delete(cfg.Settings, segID)
		return
	}
	if cfg.Settings == nil {
		cfg.Settings = map[string]map[string]any{}
	}
	cfg.Settings[segID] = vals
}

// GitBranchSettingSpecs declares the opt-in rich git status settings.
func GitBranchSettingSpecs() []SettingSpec {
	return []SettingSpec{
		{Key: "git_status", Name: "Rich status", Desc: "Run git status (cached, bounded) to show a dirty marker and ahead/behind counts, e.g. main* ↑1↓2", Kind: KindBool, Default: false},
		{Key: "git_status_ttl_sec", Name: "Cache TTL (s)", Desc: "Seconds a git status result is reused before running git again", Kind: KindInt, Default: 10, Min: 1, Max: 300, Step: 5},
		{Key: "git_timeout_ms", Name: "Timeout (ms)", Desc: "Hard limit on a single git status run; on timeout the last cached value is shown", Kind: KindInt, Default: 150, Min: 50, Max: 2000, Step: 50},
	}
}

func GitStashSettingSpecs() []SettingSpec {
	return []SettingSpec{
		{Key: "git_stash_ttl_sec", Name: "Cache TTL (s)", Desc: "Seconds a stash count is reused before running git again", Kind: KindInt, Default: 10, Min: 1, Max: 300, Step: 5},
		{Key: "git_timeout_ms", Name: "Timeout (ms)", Desc: "Hard limit on a single stash-count run; on timeout the last cached value is shown", Kind: KindInt, Default: 150, Min: 50, Max: 2000, Step: 50},
	}
}

// BarSettingSpecs generates the shared schema for progress-bar segments.
// countdown/warning toggle the segment-specific extras; extra specs slot in
// before the ephemeral rows; syncToAll appends the "copy to all bars" action.
// barWidth and iconsets come from the render layer so this package stays
// independent of rendering details.
func BarSettingSpecs(countdown, warning, syncToAll bool, barWidth int, iconsets []string, extra ...SettingSpec) []SettingSpec {
	specs := []SettingSpec{
		{Key: "show_bar", Name: "Show bar", Desc: "Render the progress bar", Kind: KindBool, Default: true},
	}
	if countdown {
		specs = append(specs, SettingSpec{Key: "show_countdown", Name: "Show countdown", Desc: "Append the reset countdown timer, e.g. (2h30m)", Kind: KindBool, Default: true})
	}
	if warning {
		specs = append(specs, SettingSpec{Key: "show_warning", Name: "Show >200k warning", Desc: "Append red >200k when context exceeds 200k tokens", Kind: KindBool, Default: true})
	}
	specs = append(specs,
		SettingSpec{Key: "bar_width", Name: "Bar width", Desc: "Number of characters in the progress bar", Kind: KindInt, Default: barWidth, Min: 5, Max: 50, Step: 1},
		SettingSpec{Key: "iconset", Name: "Iconset", Desc: "Visual style of the progress bar", Kind: KindEnum, Default: "default", Options: iconsets},
		SettingSpec{Key: "warn_at", Name: "Warn at", Desc: "Percentage threshold for the warning color", Kind: KindInt, Default: 60, Min: 0, Max: 100, Step: 5},
		SettingSpec{Key: "crit_at", Name: "Critical at", Desc: "Percentage threshold for the critical color", Kind: KindInt, Default: 80, Min: 0, Max: 100, Step: 5},
		SettingSpec{Key: "ok_color", Name: "OK color", Desc: "Color below the warning threshold (space cycles, enter opens the picker)", Kind: KindColor, Default: "green", Options: palette.ColorCycle},
		SettingSpec{Key: "warn_color", Name: "Warn color", Desc: "Color between the warn and critical thresholds (space cycles, enter opens the picker)", Kind: KindColor, Default: "yellow", Options: palette.ColorCycle},
		SettingSpec{Key: "crit_color", Name: "Critical color", Desc: "Color above the critical threshold (space cycles, enter opens the picker)", Kind: KindColor, Default: "bright-red", Options: palette.ColorCycle},
	)
	specs = append(specs, extra...)
	specs = append(specs, SettingSpec{Key: "stress_test", Name: "Stress test preview", Desc: "Animate the preview from 0% to 100% to see all colors", Kind: KindBool, Default: false, Ephemeral: true})
	if syncToAll {
		specs = append(specs, SettingSpec{Key: "sync_to_all", Name: "Sync to all bars", Desc: "Copy these settings to the other progress bar segments", Kind: KindBool, Default: false, Ephemeral: true})
	}
	return specs
}

// CostRateSpecs declares the cost-rate segment's settings.
func CostRateSpecs() []SettingSpec {
	return []SettingSpec{
		{Key: "window_min", Name: "Window (min)", Desc: "Trailing minutes of history the $/h rate is computed over (falls back to the whole session when shorter)", Kind: KindInt, Default: 60, Min: 5, Max: 480, Step: 15},
	}
}

// ProjectionSpecs are the burn-rate projection settings on rate-limit bars.
func ProjectionSpecs(defaultWindowMin int) []SettingSpec {
	return []SettingSpec{
		{Key: "show_projection", Name: "Show projection", Desc: "Project usage at reset from the recent burn rate, e.g. →58% (needs a few minutes of session history)", Kind: KindBool, Default: true},
		{Key: "projection_window_min", Name: "Projection window (min)", Desc: "Trailing minutes of history the burn rate is computed over", Kind: KindInt, Default: defaultWindowMin, Min: 5, Max: 24 * 60, Step: 15},
	}
}

// TrendSpecs are the context growth trend settings on context-window.
func TrendSpecs() []SettingSpec {
	return []SettingSpec{
		{Key: "show_trend", Name: "Show trend", Desc: "Append a growth arrow and time-to-compact estimate, e.g. ↗ ~35m (needs a few minutes of session history)", Kind: KindBool, Default: true},
		{Key: "compact_at", Name: "Compact at %", Desc: "Context percentage where auto-compact is expected; the ETA counts down to this", Kind: KindInt, Default: 80, Min: 0, Max: 100, Step: 5},
	}
}
