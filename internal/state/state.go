package state

// ─── Session State Store ─────────────────────────────────────────────
//
// The renderer is invoked once per turn with no memory between calls. To
// power burn-rate, projection, and trend features, each render appends a
// small timestamped sample to a per-session JSON file under the XDG state
// directory. One file per session means concurrent statuslines from
// different sessions never contend; writes are atomic temp+rename, so the
// worst concurrency outcome is a lost sample.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/sys"
)

const (
	stateSampleDebounce = 5 * time.Second
	stateMaxSamples     = 500
	stateMaxFiles       = 200
)

// stateConfig is the [state] table in config.toml.
type StateConfig struct {
	Enabled        *bool `toml:"enabled,omitempty"`
	RetentionHours int   `toml:"retention_hours,omitempty"`
}

func (s StateConfig) enabled() bool {
	return s.Enabled == nil || *s.Enabled
}

func (s StateConfig) retention() time.Duration {
	if s.RetentionHours > 0 {
		return time.Duration(s.RetentionHours) * time.Hour
	}
	return 48 * time.Hour
}

// sample is one observation of the session's counters.
type Sample struct {
	T      int64    `json:"t"` // unix seconds
	Cost   float64  `json:"cost,omitempty"`
	CtxPct float64  `json:"ctx,omitempty"` // context window used %
	InTok  int64    `json:"in,omitempty"`
	OutTok int64    `json:"out,omitempty"`
	RL5h   *float64 `json:"rl5h,omitempty"` // rate-limit used %
	RL7d   *float64 `json:"rl7d,omitempty"`
}

type SessionState struct {
	SessionID string        `json:"session_id"`
	Samples   []Sample      `json:"samples"`
	Retention time.Duration `json:"-"`

	path  string
	dirty bool
}

func StateBaseDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "claude-statusline")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".local", "state", "claude-statusline")
}

func StateDir() string {
	return filepath.Join(StateBaseDir(), "sessions")
}

func PluginCacheDir() string {
	return filepath.Join(StateBaseDir(), "plugins")
}

// sanitizeSessionID keeps [A-Za-z0-9._-]; anything else becomes '-'.
func sanitizeSessionID(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, id)
}

// loadState reads (or initializes) the state for a session, pruning samples
// older than the retention window. Returns nil when state is disabled or no
// session ID is available — renderers must tolerate a nil state.
func LoadState(cfg StateConfig, sessionID string, now time.Time) *SessionState {
	if !cfg.enabled() || sessionID == "" {
		return nil
	}
	st := &SessionState{
		SessionID: sessionID,
		path:      filepath.Join(StateDir(), sanitizeSessionID(sessionID)+".json"),
		Retention: cfg.retention(),
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil {
		// Corrupt state never breaks a render; start fresh.
		st.Samples = nil
		return st
	}
	cutoff := now.Add(-st.Retention).Unix()
	i := 0
	for i < len(st.Samples) && st.Samples[i].T < cutoff {
		i++
	}
	if i > 0 {
		st.Samples = st.Samples[i:]
		st.dirty = true
	}
	if len(st.Samples) > stateMaxSamples {
		st.Samples = st.Samples[len(st.Samples)-stateMaxSamples:]
		st.dirty = true
	}
	return st
}

// Record appends a sample built from the payload. Samples closer than the
// debounce interval to the previous one are skipped — Claude Code renders
// the statusline very frequently during activity.
func (st *SessionState) Record(p payload.Payload, now time.Time) {
	if st == nil {
		return
	}
	if n := len(st.Samples); n > 0 && now.Unix()-st.Samples[n-1].T < int64(stateSampleDebounce/time.Second) {
		return
	}
	s := Sample{
		T:      now.Unix(),
		Cost:   p.Cost.TotalCostUSD,
		InTok:  p.ContextWindow.TotalInputTokens,
		OutTok: p.ContextWindow.TotalOutputTokens,
	}
	if p.ContextWindow.UsedPercentage != nil {
		s.CtxPct = *p.ContextWindow.UsedPercentage
	}
	if v := p.RateLimits.FiveHour.UsedPercentage; v != nil {
		f := *v
		s.RL5h = &f
	}
	if v := p.RateLimits.SevenDay.UsedPercentage; v != nil {
		f := *v
		s.RL7d = &f
	}
	st.Samples = append(st.Samples, s)
	st.dirty = true
}

// Save persists the state (when changed) and opportunistically prunes
// abandoned session files. Called after the statusline is printed so disk
// I/O never delays output.
func (st *SessionState) Save() error {
	if st == nil || !st.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := sys.WriteFileAtomic(st.path, data); err != nil {
		return err
	}
	st.dirty = false
	pruneStateDir(filepath.Dir(st.path), st.Retention)
	return nil
}

// pruneStateDir deletes session files whose mtime is older than the
// retention window, and the oldest files beyond the hard cap.
func pruneStateDir(dir string, retention time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileAge struct {
		path string
		mod  time.Time
	}
	var files []fileAge
	cutoff := time.Now().Add(-retention)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		files = append(files, fileAge{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(files) > stateMaxFiles {
		sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
		for _, f := range files[:len(files)-stateMaxFiles] {
			os.Remove(f.path)
		}
	}
}

// ─── Series Math ─────────────────────────────────────────────────────

type Point struct {
	T int64
	V float64
}

type Series []Point

// Series extracts one field's history. Field names: cost, ctx, in, out,
// rl5h, rl7d. Pointer fields skip samples where the value was absent.
func (st *SessionState) Series(field string) Series {
	if st == nil {
		return nil
	}
	out := make(Series, 0, len(st.Samples))
	for _, s := range st.Samples {
		switch field {
		case "cost":
			out = append(out, Point{s.T, s.Cost})
		case "ctx":
			out = append(out, Point{s.T, s.CtxPct})
		case "in":
			out = append(out, Point{s.T, float64(s.InTok)})
		case "out":
			out = append(out, Point{s.T, float64(s.OutTok)})
		case "rl5h":
			if s.RL5h != nil {
				out = append(out, Point{s.T, *s.RL5h})
			}
		case "rl7d":
			if s.RL7d != nil {
				out = append(out, Point{s.T, *s.RL7d})
			}
		}
	}
	return out
}

// trailing returns the points within the trailing window before now.
func (s Series) trailing(window time.Duration, now time.Time) Series {
	cutoff := now.Add(-window).Unix()
	i := 0
	for i < len(s) && s[i].T < cutoff {
		i++
	}
	return s[i:]
}

func (s Series) Last() (float64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)-1].V, true
}

// Span is the time covered between the first and last points of the trailing
// window — callers use it to suppress trends built on too little history.
func (s Series) Span(window time.Duration, now time.Time) time.Duration {
	t := s.trailing(window, now)
	if len(t) < 2 {
		return 0
	}
	return time.Duration(t[len(t)-1].T-t[0].T) * time.Second
}

// Rate returns the endpoint slope, in units per hour, over the trailing
// window. ok is false with fewer than two points or zero elapsed time.
func (s Series) Rate(window time.Duration, now time.Time) (perHour float64, ok bool) {
	t := s.trailing(window, now)
	if len(t) < 2 {
		return 0, false
	}
	first, last := t[0], t[len(t)-1]
	elapsed := last.T - first.T
	if elapsed <= 0 {
		return 0, false
	}
	return (last.V - first.V) / (float64(elapsed) / 3600.0), true
}

// Delta returns the value change over the trailing window.
func (s Series) Delta(window time.Duration, now time.Time) (float64, bool) {
	t := s.trailing(window, now)
	if len(t) < 2 {
		return 0, false
	}
	return t[len(t)-1].V - t[0].V, true
}

// ProjectWhen estimates when the series reaches target at the current rate.
// ok is false when the rate is non-positive or the target is already passed.
func (s Series) ProjectWhen(target float64, window time.Duration, now time.Time) (time.Time, bool) {
	rate, ok := s.Rate(window, now)
	if !ok || rate <= 0 {
		return time.Time{}, false
	}
	last, _ := s.Last()
	if last >= target {
		return time.Time{}, false
	}
	hours := (target - last) / rate
	return now.Add(time.Duration(hours * float64(time.Hour))), true
}

// ProjectAt estimates the series value at a future instant given the current
// rate over the trailing window.
func (s Series) ProjectAt(at time.Time, window time.Duration, now time.Time) (float64, bool) {
	rate, ok := s.Rate(window, now)
	if !ok {
		return 0, false
	}
	last, lok := s.Last()
	if !lok {
		return 0, false
	}
	hours := at.Sub(now).Hours()
	if hours < 0 {
		return 0, false
	}
	return last + rate*hours, true
}
