package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func terminalWidth(p payload) int {
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

type payload struct {
	SessionID      string      `json:"session_id"`
	SessionName    string      `json:"session_name"`
	ConversationID string      `json:"conversation_id"`
	Cwd            string      `json:"cwd"`
	Version        string      `json:"version"`
	TranscriptPath string      `json:"transcript_path"`
	Exceeds200K    *bool       `json:"exceeds_200k_tokens"`
	Model          model       `json:"model"`
	Workspace      workspace   `json:"workspace"`
	Cost           cost        `json:"cost"`
	ContextWindow  contextWin  `json:"context_window"`
	RateLimits     rateLimits  `json:"rate_limits"`
	Agent          agent       `json:"agent"`
	Worktree       worktree    `json:"worktree"`
	Vim            vim         `json:"vim"`
	Effort         effort      `json:"effort"`
	OutputStyle    outputStyle `json:"output_style"`

	// agy additions
	Product       string  `json:"product"`
	AgentState    string  `json:"agent_state"`
	Sandbox       sandbox `json:"sandbox"`
	ArtifactCount int     `json:"artifact_count"`
	PlanTier      string  `json:"plan_tier"`
	Email         string  `json:"email"`
	TerminalWidth int     `json:"terminal_width"`
}

type sandbox struct {
	Enabled bool `json:"enabled"`
}

type model struct {
	DisplayName string `json:"display_name"`
	ID          string `json:"id"`
}

type workspace struct {
	CurrentDir  string   `json:"current_dir"`
	ProjectDir  string   `json:"project_dir"`
	GitWorktree string   `json:"git_worktree"`
	AddedDirs   []string `json:"added_dirs"`
}

type outputStyle struct {
	Name string `json:"name"`
}

type effort struct {
	Level string `json:"level"`
}

type cost struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalLinesAdded   int64   `json:"total_lines_added"`
	TotalLinesRemoved int64   `json:"total_lines_removed"`
	TotalDurationMS   int64   `json:"total_duration_ms"`
	TotalAPIDuration  int64   `json:"total_api_duration_ms"`
}

type contextWin struct {
	TotalInputTokens  int64        `json:"total_input_tokens"`
	TotalOutputTokens int64        `json:"total_output_tokens"`
	ContextWindowSize int64        `json:"context_window_size"`
	UsedPercentage    *float64     `json:"used_percentage"`
	CurrentUsage      currentUsage `json:"current_usage"`
}

type currentUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type rateLimits struct {
	FiveHour limitWindow `json:"five_hour"`
	SevenDay limitWindow `json:"seven_day"`
}

type limitWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

type agent struct {
	Name string `json:"name"`
}

type worktree struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

type vim struct {
	Mode string `json:"mode"`
}

func samplePayload() payload {
	trueVal := true
	now := time.Now().Unix()
	reset5h := now + 3600*2 + 1800
	reset7d := now + 86400*3 + 3600*4
	pct50 := 50.0
	pct30 := 30.0
	pct65 := 65.0
	return payload{
		SessionName: "my-project",
		Cwd:         "/Users/me/code/my-project",
		Version:     "0.1.0",
		Exceeds200K: &trueVal,
		Model:       model{DisplayName: "Claude 3.7 Sonnet"},
		Workspace:   workspace{CurrentDir: "/Users/me/code/my-project", ProjectDir: "/Users/me/code/my-project", GitWorktree: "my-project"},
		Cost: cost{
			TotalCostUSD:      0.42,
			TotalLinesAdded:   128,
			TotalLinesRemoved: 45,
			TotalDurationMS:   1234567,
			TotalAPIDuration:  890123,
		},
		ContextWindow: contextWin{
			TotalInputTokens:  45678,
			TotalOutputTokens: 1234,
			ContextWindowSize: 200000,
			UsedPercentage:    &pct65,
			CurrentUsage: currentUsage{
				InputTokens:              40000,
				OutputTokens:             1234,
				CacheCreationInputTokens: 2000,
				CacheReadInputTokens:     3000,
			},
		},
		RateLimits: rateLimits{
			FiveHour: limitWindow{UsedPercentage: &pct50, ResetsAt: &reset5h},
			SevenDay: limitWindow{UsedPercentage: &pct30, ResetsAt: &reset7d},
		},
		Agent:    agent{Name: "CodeReview"},
		Worktree: worktree{Name: "my-project", Branch: "feature/config"},
		Vim:      vim{Mode: "normal"},
		Effort:   effort{Level: "high"},
	}
}

// ─── Input ───────────────────────────────────────────────────────────

func readInput() []byte {
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

func parsePayload(data []byte) payload {
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		_ = json.Unmarshal([]byte(minObject), &p)
	}
	p.Workspace.ProjectDir = strings.TrimPrefix(p.Workspace.ProjectDir, "file://")
	return p
}
