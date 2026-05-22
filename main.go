package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	barWidth  = 20
	maxInput  = 1 << 20
	minObject = `{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}`
)

type payload struct {
	SessionID      string     `json:"session_id"`
	SessionName    string     `json:"session_name"`
	Cwd            string     `json:"cwd"`
	Version        string     `json:"version"`
	TranscriptPath string     `json:"transcript_path"`
	Exceeds200K    *bool      `json:"exceeds_200k_tokens"`
	Model          model      `json:"model"`
	Workspace      workspace  `json:"workspace"`
	Cost           cost       `json:"cost"`
	ContextWindow  contextWin `json:"context_window"`
	RateLimits rateLimits `json:"rate_limits"`
	Agent      agent      `json:"agent"`
	Worktree   worktree   `json:"worktree"`
	Vim        vim        `json:"vim"`
	Effort     effort     `json:"effort"`
}

type model struct {
	DisplayName string `json:"display_name"`
	ID          string `json:"id"`
}

type workspace struct {
	CurrentDir  string `json:"current_dir"`
	ProjectDir  string `json:"project_dir"`
	GitWorktree string `json:"git_worktree"`
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

type palette struct {
	Model   string
	Dir     string
	Git     string
	Chg     string
	Dur     string
	Cost    string
	Dim     string
	Rst     string
	ROK     string
	RWarn   string
	RCrit   string
	Agent   string
	Vim     string
	Purple  string
	Session string
}

func main() {
	start := time.Now()

	input := readInput()
	p := parsePayload(input)
	colors := currentPalette()
	lines := buildStatusline(p, colors)

	elapsedMS := float64(time.Since(start).Microseconds()) / 1000.0
	fmt.Printf("%s │ %s%.1fms\n%s\n%s\n", safeLine(lines, 0), colors.Dim, elapsedMS, safeLine(lines, 1), safeLine(lines, 2))
}

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
	return p
}

func currentPalette() palette {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return palette{}
	}
	return palette{
		Model:  "\x1b[35m",
		Dir:    "\x1b[36m",
		Git:    "\x1b[32m",
		Chg:    "\x1b[33m",
		Dur:    "\x1b[34m",
		Cost:   "\x1b[33m",
		Dim:    "\x1b[90m",
		Rst:    "\x1b[0m",
		ROK:    "\x1b[32m",
		RWarn:  "\x1b[33m",
		RCrit:  "\x1b[91m",
		Agent:   "\x1b[95m",
		Vim:     "\x1b[97m",
		Purple:  "\x1b[35m",
		Session: "\x1b[96m",
	}
}

func buildStatusline(p payload, c palette) []string {
	modelName := firstNonEmpty(p.Model.DisplayName, p.Model.ID, "Claude")
	currentDir := firstNonEmpty(p.Workspace.CurrentDir, p.Cwd, "~")
	projectDir := p.Workspace.ProjectDir

	line1 := ""
	if p.Vim.Mode != "" {
		line1 = " " + component(c.Vim, "["+p.Vim.Mode+"]", "", true, c)
	}
	if p.SessionName != "" {
		line1 = appendSegment(line1, component(c.Session, p.SessionName, "", true, c), c)
	}
	if p.Agent.Name != "" {
		line1 = appendSegment(line1, component(c.Agent, p.Agent.Name, "", true, c), c)
	}
	line1 = appendSegment(line1, c.Dir+formatPath(currentDir, projectDir)+c.Rst, c)

	branch := p.Worktree.Branch
	if branch == "" {
		branch = gitBranch(currentDir)
	}
	worktreeName := p.Worktree.Name
	if worktreeName == "" {
		worktreeName = p.Workspace.GitWorktree
	}
	if branch != "" {
		display := branch
		if worktreeName != "" && worktreeName != branch {
			display = branch + " " + c.Dim + "(" + worktreeName + ")" + c.Rst
		}
		line1 = appendSegment(line1, component(c.Git, display, "", true, c), c)
	}
	if p.Cost.TotalLinesAdded != 0 || p.Cost.TotalLinesRemoved != 0 {
		line1 = appendSegment(line1, component(c.Chg, fmt.Sprintf("+%d/-%d", p.Cost.TotalLinesAdded, p.Cost.TotalLinesRemoved), "", true, c), c)
	}

	cacheTotal := p.ContextWindow.CurrentUsage.InputTokens + p.ContextWindow.CurrentUsage.CacheCreationInputTokens + p.ContextWindow.CurrentUsage.CacheReadInputTokens
	if cacheTotal > 0 && p.ContextWindow.CurrentUsage.CacheReadInputTokens > 0 {
		cacheBP := p.ContextWindow.CurrentUsage.CacheReadInputTokens * 10000 / cacheTotal
		line1 = appendSegment(line1, c.Dim+fmt.Sprintf("cache:%d.%02d%%", cacheBP/100, cacheBP%100)+c.Rst, c)
	}
	line1 = appendSegment(line1, component(c.Cost, "$"+formatCost(p.Cost.TotalCostUSD), "", true, c), c)

	effort := firstNonEmpty(p.Effort.Level, readEffortLevel())
	modelLabel := modelName
	badge := effortBadge(effort)
	if badge != "" {
		modelLabel += " " + badge
	}
	line2 := " " + component(c.Model, "["+modelLabel+"]", "", true, c)
	if p.Version != "" {
		line2 += " │ " + c.Dim + "v" + p.Version + c.Rst
	}
	elapsed := formatHHMMSS(p.Cost.TotalDurationMS)
	apiSuffix := apiEfficiency(p.Cost.TotalAPIDuration, p.Cost.TotalDurationMS, c)
	inStr := formatTokens(p.ContextWindow.TotalInputTokens)
	outStr := formatTokens(p.ContextWindow.TotalOutputTokens)
	line2 += " │ " + component(c.Dur, elapsed, apiSuffix+c.Dim+" │ ↑"+inStr+" ↓"+outStr+c.Rst, true, c)

	ctxPct := 0
	if p.ContextWindow.UsedPercentage != nil {
		ctxPct = int(*p.ContextWindow.UsedPercentage)
	} else {
		usageTokens := p.ContextWindow.CurrentUsage.InputTokens + p.ContextWindow.CurrentUsage.CacheCreationInputTokens + p.ContextWindow.CurrentUsage.CacheReadInputTokens
		if usageTokens == 0 {
			usageTokens = p.ContextWindow.TotalInputTokens // already includes cache reads+writes
		}
		if p.ContextWindow.ContextWindowSize > 0 && usageTokens > 0 {
			ctxPct = int(usageTokens * 100 / p.ContextWindow.ContextWindowSize)
		}
	}
	ctxColor := pctColor(ctxPct, c)
	ctxEntry := c.Dim + "ctx " + progressBar(ctxPct, ctxColor, c.Dim, c) + " " + ctxColor + strconv.Itoa(ctxPct) + "%" + c.Rst
	if p.Exceeds200K != nil && *p.Exceeds200K {
		ctxEntry += " " + c.RCrit + ">200k" + c.Rst
	}

	line3Parts := []string{ctxEntry}
	if seg, ok := rateLimitSegment("5h", p.RateLimits.FiveHour, 5*3600, c); ok {
		line3Parts = append(line3Parts, seg)
	}
	if seg, ok := rateLimitSegment("7d", p.RateLimits.SevenDay, 7*24*3600, c); ok {
		line3Parts = append(line3Parts, seg)
	}

	line3 := ""
	for i, part := range line3Parts {
		if i == 0 {
			line3 = " " + part
		} else {
			line3 += " " + c.Dim + "|" + c.Rst + " " + part
		}
	}

	return []string{line1, line2, line3}
}

func component(color, text, suffix string, show bool, c palette) string {
	if !show {
		return ""
	}
	return color + text + suffix + c.Rst
}

func appendSegment(line, segment string, c palette) string {
	if segment == "" {
		return line
	}
	if line == "" {
		return " " + segment
	}
	return line + " │ " + segment
}

func formatPath(current, project string) string {
	display := filepath.Base(current)
	if display == "." || display == string(filepath.Separator) || display == "" {
		display = current
	}
	if project != "" && current != project && strings.HasPrefix(current, project+"/") {
		return filepath.Base(project) + "→" + strings.TrimPrefix(current, project+"/")
	}
	return display
}

func gitBranch(dir string) string {
	searchDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		gitEntry := filepath.Join(searchDir, ".git")
		info, err := os.Stat(gitEntry)
		if err == nil {
			gitDir := gitEntry
			if !info.IsDir() {
				data, readErr := os.ReadFile(gitEntry)
				if readErr != nil {
					return ""
				}
				ref := strings.TrimSpace(string(data))
				if !strings.HasPrefix(ref, "gitdir: ") {
					return ""
				}
				gitDir = strings.TrimPrefix(ref, "gitdir: ")
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Clean(filepath.Join(searchDir, gitDir))
				}
			}
			head, readErr := os.ReadFile(filepath.Join(gitDir, "HEAD"))
			if readErr != nil {
				return ""
			}
			content := strings.TrimSpace(string(head))
			if strings.HasPrefix(content, "ref: refs/heads/") {
				return strings.TrimPrefix(content, "ref: refs/heads/")
			}
			return "detached"
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			return ""
		}
		searchDir = parent
	}
}

func formatHHMMSS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	totalSeconds := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", totalSeconds/3600, (totalSeconds%3600)/60, totalSeconds%60)
}

func apiEfficiency(apiMS, totalMS int64, c palette) string {
	if apiMS < 0 {
		apiMS = 0
	}
	if totalMS <= 0 {
		return " " + c.Dim + "(API:0%)"
	}
	return fmt.Sprintf(" %s(API:%d%%)", c.Dim, apiMS*100/totalMS)
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%d.%dM", n/1_000_000, (n%1_000_000)/100_000)
	case n >= 1_000:
		return fmt.Sprintf("%d.%dk", n/1_000, (n%1_000)/100)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func progressBar(pct int, fillColor, emptyColor string, c palette) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * barWidth / 100
	empty := barWidth - filled
	return fillColor + strings.Repeat("#", filled) + emptyColor + strings.Repeat("-", empty) + c.Rst
}

// progressBarWithTime renders a bar like progressBar but overlays a purple "|"
// at the timePct position so you can compare quota usage vs. time elapsed.
// timePct < 0 means unknown — falls back to a plain bar.
func progressBarWithTime(pct, timePct int, fillColor, emptyColor string, c palette) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * barWidth / 100

	timeSlot := -1
	if timePct >= 0 && timePct <= 100 {
		timeSlot = timePct * barWidth / 100
		if timeSlot >= barWidth {
			timeSlot = barWidth - 1
		}
	}

	var b strings.Builder
	for i := 0; i < barWidth; i++ {
		switch {
		case i == timeSlot:
			b.WriteString(c.Purple + "|")
		case i < filled:
			b.WriteString(fillColor + "#")
		default:
			b.WriteString(emptyColor + "-")
		}
	}
	b.WriteString(c.Rst)
	return b.String()
}

func pctColor(pct int, c palette) string {
	switch {
	case pct > 80:
		return c.RCrit
	case pct >= 60:
		return c.RWarn
	default:
		return c.ROK
	}
}

func rateLimitSegment(label string, window limitWindow, windowSecs int64, c palette) (string, bool) {
	if window.UsedPercentage == nil {
		return "", false
	}
	pct := int(*window.UsedPercentage)
	color := pctColor(pct, c)
	countdown := "?"
	timePct := -1
	if window.ResetsAt != nil {
		countdown = resetCountdown(*window.ResetsAt)
		if windowSecs > 0 {
			remaining := *window.ResetsAt - time.Now().Unix()
			if remaining >= 0 && remaining <= windowSecs {
				timePct = int((windowSecs - remaining) * 100 / windowSecs)
			}
		}
	}
	return fmt.Sprintf("%s%s %s %s%d%%%s (%s)%s", c.Dim, label, progressBarWithTime(pct, timePct, color, c.Dim, c), color, pct, c.Dim, countdown, c.Rst), true
}

func resetCountdown(resetUnix int64) string {
	remaining := resetUnix - time.Now().Unix()
	if remaining <= 0 {
		return "now"
	}
	days := remaining / 86400
	hours := (remaining % 86400) / 3600
	minutes := (remaining % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func formatCost(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func effortBadge(effort string) string {
	switch strings.ToLower(effort) {
	case "low":
		return "⬇"
	case "medium":
		return "→"
	case "high":
		return "⬆"
	case "xhigh":
		return "⬆⬆"
	case "max":
		return "⬆⬆⬆"
	default:
		return ""
	}
}

func readEffortLevel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		EffortLevel string `json:"effortLevel"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return ""
	}
	return s.EffortLevel
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func safeLine(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}
