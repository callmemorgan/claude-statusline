package payload

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxInput  = 1 << 20
	minObject = `{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}`
)

func TerminalWidth(p Payload) int {
	if p.TerminalWidth > 0 {
		return p.TerminalWidth
	}
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// ─── Payload ─────────────────────────────────────────────────────────

type Payload struct {
	SessionID      string      `json:"session_id"`
	SessionName    string      `json:"session_name"`
	PromptID       string      `json:"prompt_id"`
	ConversationID string      `json:"conversation_id"`
	Cwd            string      `json:"cwd"`
	Version        string      `json:"version"`
	TranscriptPath string      `json:"transcript_path"`
	Exceeds200K    *bool       `json:"exceeds_200k_tokens"`
	Model          Model       `json:"model"`
	Workspace      Workspace   `json:"workspace"`
	Cost           Cost        `json:"cost"`
	ContextWindow  ContextWin  `json:"context_window"`
	RateLimits     RateLimits  `json:"rate_limits"`
	Agent          Agent       `json:"agent"`
	Worktree       Worktree    `json:"worktree"`
	Vim            Vim         `json:"vim"`
	Effort         Effort      `json:"effort"`
	Thinking       Thinking    `json:"thinking"`
	OutputStyle    OutputStyle `json:"output_style"`
	PR             PR          `json:"pr"`

	// agy additions
	Product       string  `json:"product"`
	AgentState    string  `json:"agent_state"`
	Sandbox       Sandbox `json:"sandbox"`
	ArtifactCount int     `json:"artifact_count"`
	PlanTier      string  `json:"plan_tier"`
	Email         string  `json:"email"`
	TerminalWidth int     `json:"terminal_width"`
}

type Sandbox struct {
	Enabled bool `json:"enabled"`
}

type Model struct {
	DisplayName string `json:"display_name"`
	ID          string `json:"id"`
}

type Workspace struct {
	CurrentDir  string   `json:"current_dir"`
	ProjectDir  string   `json:"project_dir"`
	GitWorktree string   `json:"git_worktree"`
	AddedDirs   []string `json:"added_dirs"`
	Repo        Repo     `json:"repo"`
}

type Repo struct {
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type OutputStyle struct {
	Name string `json:"name"`
}

type Effort struct {
	Level string `json:"level"`
}

type Thinking struct {
	Enabled *bool `json:"enabled"`
}

type Cost struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalLinesAdded   int64   `json:"total_lines_added"`
	TotalLinesRemoved int64   `json:"total_lines_removed"`
	TotalDurationMS   int64   `json:"total_duration_ms"`
	TotalAPIDuration  int64   `json:"total_api_duration_ms"`
}

type ContextWin struct {
	TotalInputTokens  int64        `json:"total_input_tokens"`
	TotalOutputTokens int64        `json:"total_output_tokens"`
	ContextWindowSize int64        `json:"context_window_size"`
	UsedPercentage    *float64     `json:"used_percentage"`
	CurrentUsage      CurrentUsage `json:"current_usage"`
}

type CurrentUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type RateLimits struct {
	FiveHour                LimitWindow        `json:"five_hour"`
	SevenDay                LimitWindow        `json:"seven_day"`
	SevenDaySonnet          LimitWindow        `json:"seven_day_sonnet"`
	SevenDayOpus            LimitWindow        `json:"seven_day_opus"`
	SevenDayOverageIncluded LimitWindow        `json:"seven_day_overage_included"` // Fable 5 included weekly quota
	ModelScoped             []ModelScopedLimit `json:"model_scoped,omitempty"`     // per-model buckets when server emits them
}

type LimitWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

// ModelScopedLimit is a per-model weekly window from Claude Code's session
// status (and a likely future statusline field). display_name is a
// server-supplied label such as "Fable", "Sonnet", or "Opus".
type ModelScopedLimit struct {
	DisplayName    string   `json:"display_name"`
	UsedPercentage *float64 `json:"used_percentage"` // 0–100 when present
	Utilization    *float64 `json:"utilization"`     // 0–1 alternative
	ResetsAt       *int64   `json:"-"`               // unix seconds; see UnmarshalJSON
}

// UnmarshalJSON accepts resets_at as unix seconds (number) or RFC3339 string.
func (m *ModelScopedLimit) UnmarshalJSON(data []byte) error {
	type alias struct {
		DisplayName    string          `json:"display_name"`
		UsedPercentage *float64        `json:"used_percentage"`
		Utilization    *float64        `json:"utilization"`
		ResetsAt       json.RawMessage `json:"resets_at"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	m.DisplayName = a.DisplayName
	m.UsedPercentage = a.UsedPercentage
	m.Utilization = a.Utilization
	m.ResetsAt = parseResetsAt(a.ResetsAt)
	return nil
}

func parseResetsAt(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	// Number (unix seconds, possibly float from JSON).
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		v := int64(n)
		return &v
	}
	// RFC3339 / RFC3339Nano string.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		v := t.Unix()
		return &v
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		v := t.Unix()
		return &v
	}
	return nil
}

// toLimitWindow prefers used_percentage; falls back to utilization×100.
func (m ModelScopedLimit) toLimitWindow() LimitWindow {
	var pct *float64
	switch {
	case m.UsedPercentage != nil:
		v := *m.UsedPercentage
		pct = &v
	case m.Utilization != nil:
		v := *m.Utilization * 100
		pct = &v
	}
	return LimitWindow{UsedPercentage: pct, ResetsAt: m.ResetsAt}
}

// windowOrScoped returns primary when it has usage data; otherwise the first
// model_scoped entry whose display_name contains needle (case-insensitive).
func (r RateLimits) windowOrScoped(primary LimitWindow, needle string) LimitWindow {
	if primary.UsedPercentage != nil {
		return primary
	}
	needle = strings.ToLower(needle)
	for _, m := range r.ModelScoped {
		if strings.Contains(strings.ToLower(m.DisplayName), needle) {
			w := m.toLimitWindow()
			if w.UsedPercentage != nil {
				return w
			}
		}
	}
	return LimitWindow{}
}

// Fable is the Fable 5 included weekly quota (seven_day_overage_included).
func (r RateLimits) Fable() LimitWindow {
	return r.windowOrScoped(r.SevenDayOverageIncluded, "fable")
}

// Sonnet is the weekly Sonnet-class quota.
func (r RateLimits) Sonnet() LimitWindow {
	return r.windowOrScoped(r.SevenDaySonnet, "sonnet")
}

// Opus is the weekly Opus-class quota.
func (r RateLimits) Opus() LimitWindow {
	return r.windowOrScoped(r.SevenDayOpus, "opus")
}

type Agent struct {
	Name string `json:"name"`
}

type Worktree struct {
	Name           string `json:"name"`
	Branch         string `json:"branch"`
	Path           string `json:"path"`
	OriginalCwd    string `json:"original_cwd"`
	OriginalBranch string `json:"original_branch"`
}

type Vim struct {
	Mode string `json:"mode"`
}

type PR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	ReviewState string `json:"review_state"`
}

func SamplePayload() Payload {
	trueVal := true
	now := time.Now().Unix()
	reset5h := now + 3600*2 + 1800
	reset7d := now + 86400*3 + 3600*4
	pct50 := 50.0
	pct30 := 30.0
	pct65 := 65.0
	pct20 := 20.0
	pct15 := 15.0
	pct40 := 40.0
	return Payload{
		SessionName: "my-project",
		PromptID:    "550e8400-e29b-41d4-a716-446655440000",
		Cwd:         "/Users/me/code/my-project",
		Version:     "0.1.0",
		Exceeds200K: &trueVal,
		Model:       Model{DisplayName: "Claude 3.7 Sonnet"},
		Workspace: Workspace{
			CurrentDir:  "/Users/me/code/my-project",
			ProjectDir:  "/Users/me/code/my-project",
			GitWorktree: "my-project",
			AddedDirs:   []string{"/Users/me/code/shared-lib"},
			Repo:        Repo{Host: "github.com", Owner: "callmemorgan", Name: "claude-statusline"},
		},
		OutputStyle: OutputStyle{Name: "Explanatory"},
		Email:       "you@example.com",
		Thinking:    Thinking{Enabled: &trueVal},
		PR:          PR{Number: 42, URL: "https://github.com/callmemorgan/claude-statusline/pull/42", ReviewState: "pending"},
		Cost: Cost{
			TotalCostUSD:      0.42,
			TotalLinesAdded:   128,
			TotalLinesRemoved: 45,
			TotalDurationMS:   1234567,
			TotalAPIDuration:  890123,
		},
		ContextWindow: ContextWin{
			TotalInputTokens:  45678,
			TotalOutputTokens: 1234,
			ContextWindowSize: 200000,
			UsedPercentage:    &pct65,
			CurrentUsage: CurrentUsage{
				InputTokens:              40000,
				OutputTokens:             1234,
				CacheCreationInputTokens: 2000,
				CacheReadInputTokens:     3000,
			},
		},
		RateLimits: RateLimits{
			FiveHour:                LimitWindow{UsedPercentage: &pct50, ResetsAt: &reset5h},
			SevenDay:                LimitWindow{UsedPercentage: &pct30, ResetsAt: &reset7d},
			SevenDaySonnet:          LimitWindow{UsedPercentage: &pct20, ResetsAt: &reset7d},
			SevenDayOpus:            LimitWindow{UsedPercentage: &pct15, ResetsAt: &reset7d},
			SevenDayOverageIncluded: LimitWindow{UsedPercentage: &pct40, ResetsAt: &reset7d},
		},
		Agent: Agent{Name: "CodeReview"},
		Worktree: Worktree{
			Name:           "my-project",
			Branch:         "feature/config",
			Path:           "/Users/me/code/my-project",
			OriginalCwd:    "/Users/me/code",
			OriginalBranch: "main",
		},
		Vim:    Vim{Mode: "normal"},
		Effort: Effort{Level: "high"},
	}
}

// ─── Input ───────────────────────────────────────────────────────────

func ReadInput() []byte {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxInput))
	if err != nil {
		return []byte(minObject)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || data[len(data)-1] != '}' {
		return []byte(minObject)
	}
	return data
}

func ParsePayload(data []byte) Payload {
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		_ = json.Unmarshal([]byte(minObject), &p)
	}
	p.Workspace.ProjectDir = strings.TrimPrefix(p.Workspace.ProjectDir, "file://")
	return p
}
