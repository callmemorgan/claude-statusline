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
	OutputStyle    OutputStyle `json:"output_style"`

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
}

type OutputStyle struct {
	Name string `json:"name"`
}

type Effort struct {
	Level string `json:"level"`
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
	FiveHour LimitWindow `json:"five_hour"`
	SevenDay LimitWindow `json:"seven_day"`
}

type LimitWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

type Agent struct {
	Name string `json:"name"`
}

type Worktree struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

type Vim struct {
	Mode string `json:"mode"`
}

func SamplePayload() Payload {
	trueVal := true
	now := time.Now().Unix()
	reset5h := now + 3600*2 + 1800
	reset7d := now + 86400*3 + 3600*4
	pct50 := 50.0
	pct30 := 30.0
	pct65 := 65.0
	return Payload{
		SessionName: "my-project",
		Cwd:         "/Users/me/code/my-project",
		Version:     "0.1.0",
		Exceeds200K: &trueVal,
		Model:       Model{DisplayName: "Claude 3.7 Sonnet"},
		Workspace:   Workspace{CurrentDir: "/Users/me/code/my-project", ProjectDir: "/Users/me/code/my-project", GitWorktree: "my-project", AddedDirs: []string{"/Users/me/code/shared-lib"}},
		OutputStyle: OutputStyle{Name: "Explanatory"},
		Email:       "you@example.com",
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
			FiveHour: LimitWindow{UsedPercentage: &pct50, ResetsAt: &reset5h},
			SevenDay: LimitWindow{UsedPercentage: &pct30, ResetsAt: &reset7d},
		},
		Agent:    Agent{Name: "CodeReview"},
		Worktree: Worktree{Name: "my-project", Branch: "feature/config"},
		Vim:      Vim{Mode: "normal"},
		Effort:   Effort{Level: "high"},
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
