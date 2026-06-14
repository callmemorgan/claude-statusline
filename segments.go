package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─── Segment Renderers ───────────────────────────────────────────────

func renderVimMode(ctx renderCtx) (string, bool) {
	if ctx.P.Vim.Mode == "" {
		return "", false
	}
	return ctx.C.Vim + "[" + ctx.P.Vim.Mode + "]" + ctx.C.Rst, true
}

func renderSessionName(ctx renderCtx) (string, bool) {
	name := firstNonEmpty(ctx.P.SessionName, ctx.P.ConversationID)
	if name == "" {
		return "", false
	}
	if len(name) == 36 && strings.Count(name, "-") == 4 {
		name = name[:8]
	}
	return ctx.C.Session + name + ctx.C.Rst, true
}

func renderAgentName(ctx renderCtx) (string, bool) {
	if ctx.P.Agent.Name == "" {
		return "", false
	}
	return ctx.C.Agent + ctx.P.Agent.Name + ctx.C.Rst, true
}

func renderDirectory(ctx renderCtx) (string, bool) {
	currentDir := firstNonEmpty(ctx.P.Workspace.CurrentDir, ctx.P.Cwd, "~")
	projectDir := ctx.P.Workspace.ProjectDir
	return ctx.C.Dir + formatPath(currentDir, projectDir) + ctx.C.Rst, true
}

func renderGitBranch(ctx renderCtx) (string, bool) {
	currentDir := firstNonEmpty(ctx.P.Workspace.CurrentDir, ctx.P.Cwd, "~")
	branch := ctx.P.Worktree.Branch
	if branch == "" {
		branch = gitBranch(currentDir)
	}
	if branch == "" {
		return "", false
	}
	worktreeName := ctx.P.Worktree.Name
	if worktreeName == "" {
		worktreeName = ctx.P.Workspace.GitWorktree
	}
	display := branch
	if ctx.S.Bool("git_status") {
		ttl := time.Duration(ctx.S.Int("git_status_ttl_sec")) * time.Second
		timeout := time.Duration(ctx.S.Int("git_timeout_ms")) * time.Millisecond
		if info, ok := gitStatusFor(currentDir, ttl, timeout, ctx.Now); ok {
			if info.Dirty {
				display += ctx.C.Chg + "*" + ctx.C.Git
			}
			if info.Ahead > 0 || info.Behind > 0 {
				ab := ""
				if info.Ahead > 0 {
					ab += "↑" + strconv.Itoa(info.Ahead)
				}
				if info.Behind > 0 {
					ab += "↓" + strconv.Itoa(info.Behind)
				}
				display += " " + ctx.C.Dim + ab + ctx.C.Git
			}
		}
	}
	if worktreeName != "" && worktreeName != branch {
		display += " " + ctx.C.Dim + "(" + worktreeName + ")" + ctx.C.Rst
	}
	return ctx.C.Git + display + ctx.C.Rst, true
}

func renderLinesChanged(ctx renderCtx) (string, bool) {
	if ctx.P.Cost.TotalLinesAdded == 0 && ctx.P.Cost.TotalLinesRemoved == 0 {
		return "", false
	}
	return ctx.C.Chg + fmt.Sprintf("+%d/-%d", ctx.P.Cost.TotalLinesAdded, ctx.P.Cost.TotalLinesRemoved) + ctx.C.Rst, true
}

func renderCachePercent(ctx renderCtx) (string, bool) {
	cacheTotal := ctx.P.ContextWindow.CurrentUsage.InputTokens +
		ctx.P.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens
	if cacheTotal <= 0 || ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens <= 0 {
		return "", false
	}
	cacheBP := ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens * 10000 / cacheTotal
	return ctx.C.Dim + fmt.Sprintf("cache:%d.%02d%%", cacheBP/100, cacheBP%100) + ctx.C.Rst, true
}

func renderCost(ctx renderCtx) (string, bool) {
	if ctx.P.Cost.TotalCostUSD == 0 {
		return "", false
	}
	return ctx.C.Cost + "$" + formatCost(ctx.P.Cost.TotalCostUSD) + ctx.C.Rst, true
}

func renderModel(ctx renderCtx) (string, bool) {
	modelName := firstNonEmpty(ctx.P.Model.DisplayName, ctx.P.Model.ID, "Claude")
	effortLevel := ctx.P.Effort.Level
	if effortLevel == "" {
		effortLevel = readEffortLevel()
	}
	modelLabel := modelName
	badge := effortBadge(effortLevel)
	if badge != "" {
		modelLabel += " " + badge
	}
	return ctx.C.Model + "[" + modelLabel + "]" + ctx.C.Rst, true
}

func renderVersion(ctx renderCtx) (string, bool) {
	if ctx.P.Version == "" {
		return "", false
	}
	return ctx.C.Dim + "v" + ctx.P.Version + ctx.C.Rst, true
}

// renderUpdate shows the available-release notice. Self-hides when
// [update].mode is off, when the cache is missing, when the latest is not
// strictly newer, or when the current version is not a clean release (dev,
// +dirty, or Go pseudo-version). Two forms: expanded for ~5 min after each
// check, compact the rest of the day.
func renderUpdate(ctx renderCtx) (string, bool) {
	if ctx.Cfg.Update.mode() == "off" {
		return "", false
	}
	cur, _, _ := versionString()
	// isReleaseVersion's ^N.N.N$ regex already rejects "dev", "+dirty", and
	// pseudo-versions, so this single guard covers every non-release shape.
	if !isReleaseVersion(cur) {
		return "", false
	}
	cache, ok := loadUpdateCheck()
	if !ok {
		return "", false
	}
	if cache.Latest == "" {
		return "", false
	}
	if compareVersions(cache.Latest, cur) <= 0 {
		return "", false
	}
	// Guard the lower bound like the spawn gate does: a future CheckedAt (clock
	// skew, restored backup, hand-edited cache) makes elapsed negative, which
	// without >= 0 would read as "always within the window" and pin the verbose
	// hint on screen forever.
	elapsed := ctx.Now.Unix() - cache.CheckedAt
	expanded := elapsed >= 0 && elapsed < int64(expandedWindow.Seconds())
	body := "⬆ v" + cache.Latest
	if expanded {
		hint := ctx.C.Dim + " · run: claude-statusline update · disable: [update] in config.toml" + ctx.C.Rst
		return ctx.C.Dim + body + ctx.C.Rst + hint, true
	}
	return ctx.C.Dim + body + ctx.C.Rst, true
}

func renderDuration(ctx renderCtx) (string, bool) {
	if ctx.P.Cost.TotalDurationMS == 0 {
		return "", false
	}
	elapsed := formatHHMMSS(ctx.P.Cost.TotalDurationMS)
	return ctx.C.Dur + elapsed + ctx.C.Rst, true
}

func renderAPIEfficiency(ctx renderCtx) (string, bool) {
	if ctx.P.Cost.TotalDurationMS <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s(API:%d%%)%s", ctx.C.Dim, ctx.P.Cost.TotalAPIDuration*100/ctx.P.Cost.TotalDurationMS, ctx.C.Rst), true
}

func renderTokens(ctx renderCtx) (string, bool) {
	inStr := formatTokens(ctx.P.ContextWindow.TotalInputTokens)
	outStr := formatTokens(ctx.P.ContextWindow.TotalOutputTokens)
	return ctx.C.Dim + "↑" + inStr + " ↓" + outStr + ctx.C.Rst, true
}

func renderContextWindow(ctx renderCtx) (string, bool) {
	ctxPct := 0
	if ctx.P.ContextWindow.UsedPercentage != nil {
		ctxPct = int(*ctx.P.ContextWindow.UsedPercentage)
	} else {
		usageTokens := ctx.P.ContextWindow.CurrentUsage.InputTokens +
			ctx.P.ContextWindow.CurrentUsage.CacheCreationInputTokens +
			ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens
		if usageTokens == 0 {
			usageTokens = ctx.P.ContextWindow.TotalInputTokens
		}
		if ctx.P.ContextWindow.ContextWindowSize > 0 && usageTokens > 0 {
			ctxPct = int(usageTokens * 100 / ctx.P.ContextWindow.ContextWindowSize)
		}
	}
	s := ctx.S
	ctxColor := pctColorWithSettings(ctxPct, ctx.C, s)
	result := ctx.C.Dim + "ctx "
	if s.Bool("show_bar") {
		result += progressBarWithIconset(ctxPct, ctxColor, ctx.C.Dim, ctx.C, s.Int("bar_width"), s.Str("iconset")) + " "
	}
	result += ctxColor + strconv.Itoa(ctxPct) + "%" + ctx.C.Rst
	if trend := contextTrend(ctx, ctxPct); trend != "" {
		result += " " + trend
	}
	if s.Bool("show_warning") && ctx.P.Exceeds200K != nil && *ctx.P.Exceeds200K {
		result += " " + ctx.C.RCrit + ">200k" + ctx.C.Rst
	}
	return result, true
}

// stateMinTrendSpan is the minimum recorded history before any trend,
// projection, or rate renders — slopes over less are noise.
const stateMinTrendSpan = 5 * time.Minute

// contextTrend renders the context growth arrow and time-to-compact ETA from
// session history: ↗ ~35m while growing (warn-colored inside 15 minutes),
// ↘ after shrinking (compaction), nothing when flat or data-starved.
func contextTrend(ctx renderCtx, ctxPct int) string {
	if !ctx.S.Bool("show_trend") || ctx.State == nil {
		return ""
	}
	const window = 15 * time.Minute
	const flatPerHour = 6.0 // ±1% per 10 minutes
	series := ctx.State.Series("ctx")
	if series.Span(window, ctx.Now) < stateMinTrendSpan {
		return ""
	}
	rate, ok := series.Rate(window, ctx.Now)
	if !ok {
		return ""
	}
	switch {
	case rate >= flatPerHour:
		compactAt := ctx.S.Int("compact_at")
		arrowColor := ctx.C.Dim
		eta := ""
		if ctxPct < compactAt {
			if when, ok := series.ProjectWhen(float64(compactAt), window, ctx.Now); ok {
				if d := when.Sub(ctx.Now); d < 2*time.Hour {
					eta = " ~" + shortDuration(d)
					if d < 15*time.Minute {
						arrowColor = ctx.C.RWarn
					}
				}
			}
		}
		return arrowColor + "↗" + eta + ctx.C.Rst
	case rate <= -flatPerHour:
		return ctx.C.Dim + "↘" + ctx.C.Rst
	}
	return ""
}

// shortDuration formats an ETA compactly: 35m, 1h20m.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		d = time.Minute
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func renderRateLimit5h(ctx renderCtx) (string, bool) {
	return rateLimitSegment("5h", ctx.P.RateLimits.FiveHour, 5*3600, "rl5h", ctx)
}

func renderRateLimit7d(ctx renderCtx) (string, bool) {
	return rateLimitSegment("7d", ctx.P.RateLimits.SevenDay, 7*24*3600, "rl7d", ctx)
}

func renderCostRate(ctx renderCtx) (string, bool) {
	if ctx.State == nil {
		return "", false
	}
	window := time.Duration(ctx.S.Int("window_min")) * time.Minute
	series := ctx.State.Series("cost")
	if series.Span(window, ctx.Now) < stateMinTrendSpan {
		return "", false
	}
	rate, ok := series.Rate(window, ctx.Now)
	if !ok || rate < 0.01 {
		return "", false
	}
	return ctx.C.Cost + "$" + formatCost(rate) + "/h" + ctx.C.Rst, true
}

func renderAgentState(ctx renderCtx) (string, bool) {
	if ctx.P.AgentState == "" {
		return "", false
	}
	stateColor := ctx.C.Dim
	if ctx.P.AgentState == "working" {
		stateColor = ctx.C.Git
	}
	return stateColor + "[" + ctx.P.AgentState + "]" + ctx.C.Rst, true
}

func renderSandbox(ctx renderCtx) (string, bool) {
	if !ctx.P.Sandbox.Enabled {
		return "", false
	}
	return ctx.C.RCrit + "[SANDBOX]" + ctx.C.Rst, true
}

func renderArtifactCount(ctx renderCtx) (string, bool) {
	if ctx.P.ArtifactCount <= 0 {
		return "", false
	}
	return ctx.C.Chg + fmt.Sprintf("artifacts:%d", ctx.P.ArtifactCount) + ctx.C.Rst, true
}

func renderPlanTier(ctx renderCtx) (string, bool) {
	if ctx.P.PlanTier == "" {
		return "", false
	}
	return ctx.C.Purple + ctx.P.PlanTier + ctx.C.Rst, true
}

func renderOutputStyle(ctx renderCtx) (string, bool) {
	name := ctx.P.OutputStyle.Name
	if name == "" || strings.EqualFold(name, "default") {
		return "", false
	}
	return ctx.C.Purple + "✎ " + name + ctx.C.Rst, true
}

func renderAddedDirs(ctx renderCtx) (string, bool) {
	n := len(ctx.P.Workspace.AddedDirs)
	if n == 0 {
		return "", false
	}
	label := "dirs"
	if n == 1 {
		label = "dir"
	}
	return ctx.C.Dim + fmt.Sprintf("+%d %s", n, label) + ctx.C.Rst, true
}

func renderEmail(ctx renderCtx) (string, bool) {
	e := ctx.P.Email
	if e == "" {
		return "", false
	}
	if i := strings.IndexByte(e, '@'); i >= 0 {
		e = e[:i+1] + "…"
	}
	return ctx.C.Dim + e + ctx.C.Rst, true
}

// ─── Segment Registry ────────────────────────────────────────────────

type segmentInfo struct {
	id           string
	line         int
	desc         string
	primaryColor string
	settings     []settingSpec // nil → no flyout, no settings entry
	needsState   bool          // renderer reads the session state store
	render       func(ctx renderCtx) (string, bool)
}

func allSegmentInfos() []segmentInfo {
	return []segmentInfo{
		{id: "vim-mode", line: 1, desc: "Vim mode indicator (e.g. [normal])", primaryColor: "Vim", render: renderVimMode},
		{id: "sandbox", line: 1, desc: "Sandbox status indicator", primaryColor: "RCrit", render: renderSandbox},
		{id: "session-name", line: 1, desc: "Session name label", primaryColor: "Session", render: renderSessionName},
		{id: "agent-state", line: 1, desc: "Agent working status", primaryColor: "Git", render: renderAgentState},
		{id: "agent-name", line: 1, desc: "Agent name", primaryColor: "Agent", render: renderAgentName},
		{id: "directory", line: 1, desc: "Current / project directory", primaryColor: "Dir", render: renderDirectory},
		{id: "added-dirs", line: 1, desc: "Number of extra directories added with /add-dir", primaryColor: "Dim", render: renderAddedDirs},
		{id: "git-branch", line: 1, desc: "Git branch and worktree name, with optional dirty marker and ahead/behind counts", primaryColor: "Git", settings: gitBranchSettingSpecs(), render: renderGitBranch},
		{id: "artifact-count", line: 1, desc: "Artifact count", primaryColor: "Chg", render: renderArtifactCount},
		{id: "lines-changed", line: 1, desc: "All lines added / removed by the agent in the session", primaryColor: "Chg", render: renderLinesChanged},
		{id: "cache-percent", line: 1, desc: "Cache read percentage", primaryColor: "Dim", render: renderCachePercent},
		{id: "plan-tier", line: 1, desc: "Subscription plan tier", primaryColor: "Purple", render: renderPlanTier},
		{id: "cost", line: 1, desc: "Total session cost", primaryColor: "Cost", render: renderCost},
		{id: "model", line: 2, desc: "Model name and effort badge", primaryColor: "Model", render: renderModel},
		{id: "output-style", line: 2, desc: "Output style name (hidden when default)", primaryColor: "Purple", render: renderOutputStyle},
		{id: "email", line: 2, desc: "Account email, user part only (off by default)", primaryColor: "Dim", render: renderEmail},
		{id: "version", line: 2, desc: "Claude Code version", primaryColor: "Dim", render: renderVersion},
		{id: "update", line: 1, desc: "Update available notice (self-hides when current, dev, or [update].mode = off)", primaryColor: "Dim", render: renderUpdate},
		{id: "duration", line: 2, desc: "Elapsed session duration", primaryColor: "Dur", render: renderDuration},
		{id: "cost-rate", line: 2, desc: "Cost burn rate $/h over recent session history", primaryColor: "Cost", settings: costRateSpecs(), needsState: true, render: renderCostRate},
		{id: "api-efficiency", line: 2, desc: "API efficiency percentage", primaryColor: "Dim", render: renderAPIEfficiency},
		{id: "tokens", line: 2, desc: "Input / output token counts", primaryColor: "Dim", render: renderTokens},
		{id: "context-window", line: 3, desc: "Context window usage bar with growth trend and time-to-compact estimate", primaryColor: "Dim", settings: barSettingSpecs(false, true, true, trendSpecs()...), needsState: true, render: renderContextWindow},
		{id: "rate-limit-5h", line: 3, desc: "5-hour quota bar with reset countdown and burn-rate projection", primaryColor: "Dim", settings: barSettingSpecs(true, false, true, projectionSpecs(30)...), needsState: true, render: renderRateLimit5h},
		{id: "rate-limit-7d", line: 3, desc: "7-day quota bar with reset countdown and burn-rate projection", primaryColor: "Dim", settings: barSettingSpecs(true, false, true, projectionSpecs(180)...), needsState: true, render: renderRateLimit7d},
	}
}

func segmentByID(id string) (segmentInfo, bool) {
	for _, s := range registeredSegments {
		if s.id == id {
			return s, true
		}
	}
	return segmentInfo{}, false
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

func rateLimitSegment(label string, window limitWindow, windowSecs int64, seriesKey string, ctx renderCtx) (string, bool) {
	if window.UsedPercentage == nil {
		return "", false
	}
	c, s := ctx.C, ctx.S
	pct := int(*window.UsedPercentage)
	color := pctColorWithSettings(pct, c, s)
	countdown := "?"
	timePct := -1
	if window.ResetsAt != nil {
		countdown = resetCountdown(*window.ResetsAt, ctx.Now)
		if windowSecs > 0 {
			remaining := *window.ResetsAt - ctx.Now.Unix()
			if remaining >= 0 && remaining <= windowSecs {
				timePct = int((windowSecs - remaining) * 100 / windowSecs)
			}
		}
	}
	result := c.Dim + label + " "
	if s.Bool("show_bar") {
		result += progressBarWithTimeAndIconset(pct, timePct, color, c.Dim, c, s.Int("bar_width"), s.Str("iconset")) + " "
	}
	result += color + strconv.Itoa(pct) + "%" + c.Dim
	if s.Bool("show_countdown") {
		result += " (" + countdown + ")"
	}
	result += c.Rst
	if proj := rateLimitProjection(window, seriesKey, ctx); proj != "" {
		result += " " + proj
	}
	return result, true
}

// rateLimitProjection extrapolates the recent burn rate to the reset instant:
// →58% in dim, warn-colored past crit_at, crit-colored at or beyond 100%.
// Renders nothing when usage is flat or falling, history is too short, or no
// reset time is known.
func rateLimitProjection(window limitWindow, seriesKey string, ctx renderCtx) string {
	if !ctx.S.Bool("show_projection") || ctx.State == nil || window.ResetsAt == nil {
		return ""
	}
	win := time.Duration(ctx.S.Int("projection_window_min")) * time.Minute
	series := ctx.State.Series(seriesKey)
	// Demand history proportional to the projection window — extrapolating a
	// short burst across a long window (7d especially) is noise, not signal.
	minSpan := win / 4
	if minSpan < stateMinTrendSpan {
		minSpan = stateMinTrendSpan
	}
	if series.Span(win, ctx.Now) < minSpan {
		return ""
	}
	rate, ok := series.Rate(win, ctx.Now)
	if !ok || rate <= 0 {
		return ""
	}
	proj, ok := series.ProjectAt(time.Unix(*window.ResetsAt, 0), win, ctx.Now)
	if !ok {
		return ""
	}
	pct := int(proj)
	color := ctx.C.Dim
	switch {
	case pct >= 100:
		color = pctColorWithSettings(101, ctx.C, ctx.S) // crit
	case pct >= ctx.S.Int("crit_at"):
		color = pctColorWithSettings(ctx.S.Int("warn_at"), ctx.C, ctx.S) // warn
	}
	return color + "→" + strconv.Itoa(pct) + "%" + ctx.C.Rst
}

func resetCountdown(resetUnix int64, now time.Time) string {
	remaining := resetUnix - now.Unix()
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
