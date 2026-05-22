package main

import (
	"bufio"
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

// ─── Config ──────────────────────────────────────────────────────────

type config struct {
	Segments []string `json:"segments"`
}

func defaultConfig() config {
	return config{
		Segments: []string{
			"vim-mode", "session-name", "agent-name", "directory",
			"git-branch", "lines-changed", "cache-percent", "cost",
			"model", "version", "duration", "api-efficiency", "tokens",
			"context-window", "rate-limits",
		},
	}
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".config", "claude-statusline", "config.json")
}

func loadConfig() config {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	var loaded config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return cfg
	}
	// An explicit empty array means "hide everything"; only fall back to
	// defaults when the key is absent entirely (nil vs []).
	if loaded.Segments != nil {
		cfg.Segments = loaded.Segments
	}
	return cfg
}

func saveConfig(cfg config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// ─── Payload ─────────────────────────────────────────────────────────

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
	RateLimits     rateLimits `json:"rate_limits"`
	Agent          agent      `json:"agent"`
	Worktree       worktree   `json:"worktree"`
	Vim            vim        `json:"vim"`
	Effort         effort     `json:"effort"`
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

// ─── Main ────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--configure" {
		runConfigure()
		return
	}

	start := time.Now()

	input := readInput()
	p := parsePayload(input)
	colors := currentPalette()
	cfg := loadConfig()
	lines := buildStatusline(p, colors, cfg)

	elapsedMS := float64(time.Since(start).Microseconds()) / 1000.0
	fmt.Printf("%s │ %s%.1fms\n%s\n%s\n", safeLine(lines, 0), colors.Dim, elapsedMS, safeLine(lines, 1), safeLine(lines, 2))
}

// ─── Configure Mode ──────────────────────────────────────────────────

func runConfigure() {
	cfg := loadConfig()
	scanner := bufio.NewScanner(os.Stdin)
	segments := allSegmentInfos()

	for {
		clearScreen()
		printHeader()
		printPreview(cfg)
		fmt.Println()
		fmt.Println("Available segments:")
		for i, s := range segments {
			enabled := false
			for _, id := range cfg.Segments {
				if id == s.id {
					enabled = true
					break
				}
			}
			mark := "  "
			if enabled {
				mark = "• "
			}
			fmt.Printf("  %s%2d. %-20s %s\n", mark, i+1, s.id, s.desc)
		}
		fmt.Println()
		if len(cfg.Segments) == 0 {
			fmt.Println("Current order: (none — statusline will be hidden)")
		} else {
			fmt.Printf("Current order: %s\n", strings.Join(cfg.Segments, ", "))
		}
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  <n>,<m>,...   set order using numbers (e.g. 3,1,5,2)")
		fmt.Println("  <id>,<id>,... set order using IDs (e.g. model,directory,git-branch)")
		fmt.Println("  reset         restore defaults")
		fmt.Println("  done          save and exit")
		fmt.Println()
		fmt.Print("> ")

		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "done" || line == "" {
			break
		}
		if line == "reset" {
			cfg = defaultConfig()
			continue
		}

		parts := strings.Split(line, ",")
		newSegments := []string{}
		seen := map[string]bool{}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			// Try number first
			if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= len(segments) {
				id := segments[n-1].id
				if !seen[id] {
					newSegments = append(newSegments, id)
					seen[id] = true
				}
				continue
			}
			// Try ID
			for _, s := range segments {
				if s.id == part && !seen[part] {
					newSegments = append(newSegments, part)
					seen[part] = true
					break
				}
			}
		}
		if len(newSegments) > 0 {
			cfg.Segments = newSegments
		}
	}

	clearScreen()
	printHeader()
	printPreview(cfg)
	fmt.Println()
	if len(cfg.Segments) == 0 {
		fmt.Println("All segments disabled. Statusline will be hidden.")
	} else {
		fmt.Printf("Final order: %s\n", strings.Join(cfg.Segments, ", "))
	}
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Saved to %s\n", configPath())
}

func clearScreen() {
	fmt.Print("\x1b[2J\x1b[H")
}

func printHeader() {
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Println("          claude-statusline — configuration")
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Println()
}

func printPreview(cfg config) {
	colors := currentPalette()
	p := samplePayload()
	lines := buildStatusline(p, colors, cfg)
	fmt.Println("Preview:")
	fmt.Println()
	fmt.Println(lines[0])
	fmt.Println(lines[1])
	fmt.Println(lines[2])
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
	return p
}

// ─── Palette ─────────────────────────────────────────────────────────

func currentPalette() palette {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return palette{}
	}
	return palette{
		Model:   "\x1b[35m",
		Dir:     "\x1b[36m",
		Git:     "\x1b[32m",
		Chg:     "\x1b[33m",
		Dur:     "\x1b[34m",
		Cost:    "\x1b[33m",
		Dim:     "\x1b[90m",
		Rst:     "\x1b[0m",
		ROK:     "\x1b[32m",
		RWarn:   "\x1b[33m",
		RCrit:   "\x1b[91m",
		Agent:   "\x1b[95m",
		Vim:     "\x1b[97m",
		Purple:  "\x1b[35m",
		Session: "\x1b[96m",
	}
}

// ─── Segment Renderers ───────────────────────────────────────────────

func renderVimMode(p payload, c palette) (string, bool) {
	if p.Vim.Mode == "" {
		return "", false
	}
	return c.Vim + "[" + p.Vim.Mode + "]" + c.Rst, true
}

func renderSessionName(p payload, c palette) (string, bool) {
	if p.SessionName == "" {
		return "", false
	}
	return c.Session + p.SessionName + c.Rst, true
}

func renderAgentName(p payload, c palette) (string, bool) {
	if p.Agent.Name == "" {
		return "", false
	}
	return c.Agent + p.Agent.Name + c.Rst, true
}

func renderDirectory(p payload, c palette) (string, bool) {
	currentDir := firstNonEmpty(p.Workspace.CurrentDir, p.Cwd, "~")
	projectDir := p.Workspace.ProjectDir
	return c.Dir + formatPath(currentDir, projectDir) + c.Rst, true
}

func renderGitBranch(p payload, c palette) (string, bool) {
	currentDir := firstNonEmpty(p.Workspace.CurrentDir, p.Cwd, "~")
	branch := p.Worktree.Branch
	if branch == "" {
		branch = gitBranch(currentDir)
	}
	if branch == "" {
		return "", false
	}
	worktreeName := p.Worktree.Name
	if worktreeName == "" {
		worktreeName = p.Workspace.GitWorktree
	}
	display := branch
	if worktreeName != "" && worktreeName != branch {
		display = branch + " " + c.Dim + "(" + worktreeName + ")" + c.Rst
	}
	return c.Git + display + c.Rst, true
}

func renderLinesChanged(p payload, c palette) (string, bool) {
	if p.Cost.TotalLinesAdded == 0 && p.Cost.TotalLinesRemoved == 0 {
		return "", false
	}
	return c.Chg + fmt.Sprintf("+%d/-%d", p.Cost.TotalLinesAdded, p.Cost.TotalLinesRemoved) + c.Rst, true
}

func renderCachePercent(p payload, c palette) (string, bool) {
	cacheTotal := p.ContextWindow.CurrentUsage.InputTokens +
		p.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		p.ContextWindow.CurrentUsage.CacheReadInputTokens
	if cacheTotal <= 0 || p.ContextWindow.CurrentUsage.CacheReadInputTokens <= 0 {
		return "", false
	}
	cacheBP := p.ContextWindow.CurrentUsage.CacheReadInputTokens * 10000 / cacheTotal
	return c.Dim + fmt.Sprintf("cache:%d.%02d%%", cacheBP/100, cacheBP%100) + c.Rst, true
}

func renderCost(p payload, c palette) (string, bool) {
	return c.Cost + "$" + formatCost(p.Cost.TotalCostUSD) + c.Rst, true
}

func renderModel(p payload, c palette) (string, bool) {
	modelName := firstNonEmpty(p.Model.DisplayName, p.Model.ID, "Claude")
	effort := firstNonEmpty(p.Effort.Level, readEffortLevel())
	modelLabel := modelName
	badge := effortBadge(effort)
	if badge != "" {
		modelLabel += " " + badge
	}
	return c.Model + "[" + modelLabel + "]" + c.Rst, true
}

func renderVersion(p payload, c palette) (string, bool) {
	if p.Version == "" {
		return "", false
	}
	return c.Dim + "v" + p.Version + c.Rst, true
}

func renderDuration(p payload, c palette) (string, bool) {
	elapsed := formatHHMMSS(p.Cost.TotalDurationMS)
	return c.Dur + elapsed + c.Rst, true
}

func renderAPIEfficiency(p payload, c palette) (string, bool) {
	if p.Cost.TotalDurationMS <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s(API:%d%%)", c.Dim, p.Cost.TotalAPIDuration*100/p.Cost.TotalDurationMS), true
}

func renderTokens(p payload, c palette) (string, bool) {
	inStr := formatTokens(p.ContextWindow.TotalInputTokens)
	outStr := formatTokens(p.ContextWindow.TotalOutputTokens)
	return c.Dim + "↑" + inStr + " ↓" + outStr + c.Rst, true
}

func renderContextWindow(p payload, c palette) (string, bool) {
	ctxPct := 0
	if p.ContextWindow.UsedPercentage != nil {
		ctxPct = int(*p.ContextWindow.UsedPercentage)
	} else {
		usageTokens := p.ContextWindow.CurrentUsage.InputTokens +
			p.ContextWindow.CurrentUsage.CacheCreationInputTokens +
			p.ContextWindow.CurrentUsage.CacheReadInputTokens
		if usageTokens == 0 {
			usageTokens = p.ContextWindow.TotalInputTokens
		}
		if p.ContextWindow.ContextWindowSize > 0 && usageTokens > 0 {
			ctxPct = int(usageTokens * 100 / p.ContextWindow.ContextWindowSize)
		}
	}
	ctxColor := pctColor(ctxPct, c)
	result := c.Dim + "ctx " + progressBar(ctxPct, ctxColor, c.Dim, c) + " " + ctxColor + strconv.Itoa(ctxPct) + "%" + c.Rst
	if p.Exceeds200K != nil && *p.Exceeds200K {
		result += " " + c.RCrit + ">200k" + c.Rst
	}
	return result, true
}

func renderRateLimits(p payload, c palette) (string, bool) {
	parts := []string{}
	if seg, ok := rateLimitSegment("5h", p.RateLimits.FiveHour, 5*3600, c); ok {
		parts = append(parts, seg)
	}
	if seg, ok := rateLimitSegment("7d", p.RateLimits.SevenDay, 7*24*3600, c); ok {
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "+c.Dim+"│"+c.Rst+" "), true
}

// ─── Segment Registry ────────────────────────────────────────────────

type segmentInfo struct {
	id     string
	line   int
	desc   string
	render func(p payload, c palette) (string, bool)
}

func allSegmentInfos() []segmentInfo {
	return []segmentInfo{
		{id: "vim-mode", line: 1, desc: "Vim mode indicator (e.g. [normal])", render: renderVimMode},
		{id: "session-name", line: 1, desc: "Session name label", render: renderSessionName},
		{id: "agent-name", line: 1, desc: "Agent name", render: renderAgentName},
		{id: "directory", line: 1, desc: "Current / project directory", render: renderDirectory},
		{id: "git-branch", line: 1, desc: "Git branch and worktree name", render: renderGitBranch},
		{id: "lines-changed", line: 1, desc: "Lines added / removed", render: renderLinesChanged},
		{id: "cache-percent", line: 1, desc: "Cache read percentage", render: renderCachePercent},
		{id: "cost", line: 1, desc: "Total session cost", render: renderCost},
		{id: "model", line: 2, desc: "Model name and effort badge", render: renderModel},
		{id: "version", line: 2, desc: "Claude Code version", render: renderVersion},
		{id: "duration", line: 2, desc: "Elapsed session duration", render: renderDuration},
		{id: "api-efficiency", line: 2, desc: "API efficiency percentage", render: renderAPIEfficiency},
		{id: "tokens", line: 2, desc: "Input / output token counts", render: renderTokens},
		{id: "context-window", line: 3, desc: "Context window usage bar", render: renderContextWindow},
		{id: "rate-limits", line: 3, desc: "5-hour and 7-day quota bars", render: renderRateLimits},
	}
}

func segmentByID(id string) (segmentInfo, bool) {
	for _, s := range allSegmentInfos() {
		if s.id == id {
			return s, true
		}
	}
	return segmentInfo{}, false
}

// ─── Statusline Builder ──────────────────────────────────────────────

func buildStatusline(p payload, c palette, cfg config) []string {
	line1Parts := []string{}
	line2Parts := []string{}
	line3Parts := []string{}

	for _, id := range cfg.Segments {
		if s, ok := segmentByID(id); ok {
			if rendered, show := s.render(p, c); show {
				switch s.line {
				case 1:
					line1Parts = append(line1Parts, rendered)
				case 2:
					line2Parts = append(line2Parts, rendered)
				case 3:
					line3Parts = append(line3Parts, rendered)
				}
			}
		}
	}

	line1 := joinParts(line1Parts)
	line2 := joinParts(line2Parts)
	line3 := joinParts(line3Parts)

	return []string{line1, line2, line3}
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " │ ")
}

// ─── Helpers ─────────────────────────────────────────────────────────

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
