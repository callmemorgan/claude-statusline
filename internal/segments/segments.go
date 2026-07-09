package segments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/state"
)

const barWidth = 20

// ─── Segment Renderers ───────────────────────────────────────────────

func renderVimMode(ctx RenderCtx) (string, bool) {
	if ctx.P.Vim.Mode == "" {
		return "", false
	}
	return ctx.C.Vim + "[" + ctx.P.Vim.Mode + "]" + ctx.C.Rst, true
}

func renderSessionName(ctx RenderCtx) (string, bool) {
	name := FirstNonEmpty(ctx.P.SessionName, ctx.P.ConversationID)
	if name == "" {
		return "", false
	}
	if len(name) == 36 && strings.Count(name, "-") == 4 {
		name = name[:8]
	}
	return ctx.C.Session + name + ctx.C.Rst, true
}

func renderPromptID(ctx RenderCtx) (string, bool) {
	id := ctx.P.PromptID
	if id == "" {
		return "", false
	}
	if len(id) == 36 && strings.Count(id, "-") == 4 {
		id = id[:8]
	}
	return ctx.C.Dim + "prompt:" + id + ctx.C.Rst, true
}

func renderAgentName(ctx RenderCtx) (string, bool) {
	if ctx.P.Agent.Name == "" {
		return "", false
	}
	return ctx.C.Agent + ctx.P.Agent.Name + ctx.C.Rst, true
}

func renderDirectory(ctx RenderCtx) (string, bool) {
	currentDir := FirstNonEmpty(ctx.P.Workspace.CurrentDir, ctx.P.Cwd, "~")
	projectDir := ctx.P.Workspace.ProjectDir
	return ctx.C.Dir + ansi.FormatPath(currentDir, projectDir) + ctx.C.Rst, true
}

func renderGitBranch(ctx RenderCtx) (string, bool) {
	currentDir := FirstNonEmpty(ctx.P.Workspace.CurrentDir, ctx.P.Cwd, "~")
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
	if ctx.S.Bool("show_worktree_path") && ctx.P.Worktree.Path != "" {
		display += " " + ctx.C.Dim + "(" + ctx.P.Worktree.Path + ")" + ctx.C.Rst
	}
	if ctx.S.Bool("show_original_branch") && ctx.P.Worktree.OriginalBranch != "" {
		display += " " + ctx.C.Dim + "←" + ctx.P.Worktree.OriginalBranch + ctx.C.Rst
	}
	return ctx.C.Git + display + ctx.C.Rst, true
}

// renderGitStash shows the git stash count (⚑N). Self-hides when not in a repo,
// when there are no stashes, or on git error with no cached value. Runs a
// bounded, cached `git rev-list` like the rich git-branch status.
func renderGitStash(ctx RenderCtx) (string, bool) {
	currentDir := FirstNonEmpty(ctx.P.Workspace.CurrentDir, ctx.P.Cwd, "~")
	ttl := time.Duration(ctx.S.Int("git_stash_ttl_sec")) * time.Second
	timeout := time.Duration(ctx.S.Int("git_timeout_ms")) * time.Millisecond
	n, ok := gitStashFor(currentDir, ttl, timeout, ctx.Now)
	if !ok || n == 0 {
		return "", false
	}
	return ctx.C.Git + "⚑" + strconv.Itoa(n) + ctx.C.Rst, true
}

func renderLinesChanged(ctx RenderCtx) (string, bool) {
	if ctx.P.Cost.TotalLinesAdded == 0 && ctx.P.Cost.TotalLinesRemoved == 0 {
		return "", false
	}
	return ctx.C.Chg + fmt.Sprintf("+%d/-%d", ctx.P.Cost.TotalLinesAdded, ctx.P.Cost.TotalLinesRemoved) + ctx.C.Rst, true
}

func renderCachePercent(ctx RenderCtx) (string, bool) {
	cacheTotal := ctx.P.ContextWindow.CurrentUsage.InputTokens +
		ctx.P.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens
	if cacheTotal <= 0 || ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens <= 0 {
		return "", false
	}
	cacheBP := ctx.P.ContextWindow.CurrentUsage.CacheReadInputTokens * 10000 / cacheTotal
	return ctx.C.Dim + fmt.Sprintf("cache:%d.%02d%%", cacheBP/100, cacheBP%100) + ctx.C.Rst, true
}

func renderCost(ctx RenderCtx) (string, bool) {
	if ctx.P.Cost.TotalCostUSD == 0 {
		return "", false
	}
	return ctx.C.Cost + "$" + formatCost(ctx.P.Cost.TotalCostUSD) + ctx.C.Rst, true
}

func renderModel(ctx RenderCtx) (string, bool) {
	modelName := FirstNonEmpty(ctx.P.Model.DisplayName, ctx.P.Model.ID, "Claude")
	effortLevel := ctx.P.Effort.Level
	if effortLevel == "" {
		effortLevel = readEffortLevel()
	}
	modelLabel := modelName
	badge := ansi.EffortBadge(effortLevel)
	if badge != "" {
		modelLabel += " " + badge
	}
	return ctx.C.Model + "[" + modelLabel + "]" + ctx.C.Rst, true
}

func renderVersion(ctx RenderCtx) (string, bool) {
	if ctx.P.Version == "" {
		return "", false
	}
	return ctx.C.Dim + "v" + ctx.P.Version + ctx.C.Rst, true
}

// UpdateRenderer is injected by the root package (which owns update checking)
// so the update segment can render without creating an import cycle.
var UpdateRenderer func(RenderCtx) (string, bool)

func renderUpdatePlaceholder(ctx RenderCtx) (string, bool) {
	if UpdateRenderer != nil {
		return UpdateRenderer(ctx)
	}
	return "", false
}

func renderDuration(ctx RenderCtx) (string, bool) {
	if ctx.P.Cost.TotalDurationMS == 0 {
		return "", false
	}
	elapsed := ansi.FormatHHMMSS(ctx.P.Cost.TotalDurationMS)
	return ctx.C.Dur + elapsed + ctx.C.Rst, true
}

func renderAPIEfficiency(ctx RenderCtx) (string, bool) {
	if ctx.P.Cost.TotalDurationMS <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s(API:%d%%)%s", ctx.C.Dim, ctx.P.Cost.TotalAPIDuration*100/ctx.P.Cost.TotalDurationMS, ctx.C.Rst), true
}

func renderTokens(ctx RenderCtx) (string, bool) {
	if ctx.P.ContextWindow.TotalInputTokens == 0 && ctx.P.ContextWindow.TotalOutputTokens == 0 {
		return "", false
	}
	inStr := ansi.FormatTokens(ctx.P.ContextWindow.TotalInputTokens)
	outStr := ansi.FormatTokens(ctx.P.ContextWindow.TotalOutputTokens)
	return ctx.C.Dim + "↑" + inStr + " ↓" + outStr + ctx.C.Rst, true
}

func renderContextWindow(ctx RenderCtx) (string, bool) {
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
		result += ProgressBarWithIconset(ctxPct, ctxColor, ctx.C.Dim, ctx.C, s.Int("bar_width"), s.Str("iconset")) + " "
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
func contextTrend(ctx RenderCtx, ctxPct int) string {
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

func renderRateLimit5h(ctx RenderCtx) (string, bool) {
	return rateLimitSegment("5h", ctx.P.RateLimits.FiveHour, 5*3600, "rl5h", ctx)
}

func renderRateLimit7d(ctx RenderCtx) (string, bool) {
	return rateLimitSegment("7d", ctx.P.RateLimits.SevenDay, 7*24*3600, "rl7d", ctx)
}

func renderRateLimitFable(ctx RenderCtx) (string, bool) {
	return rateLimitSegment("Fable", ctx.P.RateLimits.Fable(), 7*24*3600, "rl_fable", ctx)
}

func renderRateLimitSonnet(ctx RenderCtx) (string, bool) {
	return rateLimitSegment("Sonnet", ctx.P.RateLimits.Sonnet(), 7*24*3600, "rl_sonnet", ctx)
}

func renderRateLimitOpus(ctx RenderCtx) (string, bool) {
	return rateLimitSegment("Opus", ctx.P.RateLimits.Opus(), 7*24*3600, "rl_opus", ctx)
}

func renderCostRate(ctx RenderCtx) (string, bool) {
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

func renderAgentState(ctx RenderCtx) (string, bool) {
	if ctx.P.AgentState == "" {
		return "", false
	}
	stateColor := ctx.C.Dim
	if ctx.P.AgentState == "working" {
		stateColor = ctx.C.Git
	}
	return stateColor + "[" + ctx.P.AgentState + "]" + ctx.C.Rst, true
}

func renderSandbox(ctx RenderCtx) (string, bool) {
	if !ctx.P.Sandbox.Enabled {
		return "", false
	}
	return ctx.C.RCrit + "[SANDBOX]" + ctx.C.Rst, true
}

func renderArtifactCount(ctx RenderCtx) (string, bool) {
	if ctx.P.ArtifactCount <= 0 {
		return "", false
	}
	return ctx.C.Chg + fmt.Sprintf("artifacts:%d", ctx.P.ArtifactCount) + ctx.C.Rst, true
}

func renderPlanTier(ctx RenderCtx) (string, bool) {
	if ctx.P.PlanTier == "" {
		return "", false
	}
	return ctx.C.Purple + ctx.P.PlanTier + ctx.C.Rst, true
}

func renderOutputStyle(ctx RenderCtx) (string, bool) {
	name := ctx.P.OutputStyle.Name
	if name == "" || strings.EqualFold(name, "default") {
		return "", false
	}
	return ctx.C.Purple + "✎ " + name + ctx.C.Rst, true
}

func renderPR(ctx RenderCtx) (string, bool) {
	pr := ctx.P.PR
	if pr.Number <= 0 {
		return "", false
	}
	if ctx.S.Bool("show_url") {
		url := pr.URL
		if url == "" && ctx.P.Workspace.Repo.Name != "" {
			repo := ctx.P.Workspace.Repo
			url = fmt.Sprintf("https://%s/%s/%s/pull/%d", repo.Host, repo.Owner, repo.Name, pr.Number)
		}
		if url == "" {
			url = fmt.Sprintf("#%d", pr.Number)
		}
		return ctx.C.Git + url + ctx.C.Rst, true
	}
	out := "#" + strconv.Itoa(pr.Number)
	if ctx.S.Bool("show_review_state") && pr.ReviewState != "" {
		out += " " + ctx.C.Dim + "(" + pr.ReviewState + ")" + ctx.C.Rst
	}
	return ctx.C.Git + out + ctx.C.Rst, true
}

func renderRepo(ctx RenderCtx) (string, bool) {
	repo := ctx.P.Workspace.Repo
	if repo.Name == "" {
		return "", false
	}
	out := repo.Owner + "/" + repo.Name
	if ctx.S.Bool("show_host") && repo.Host != "" {
		out = repo.Host + ":" + out
	}
	return ctx.C.Dim + out + ctx.C.Rst, true
}

func renderThinking(ctx RenderCtx) (string, bool) {
	if ctx.P.Thinking.Enabled == nil || !*ctx.P.Thinking.Enabled {
		return "", false
	}
	icon := "🗘 thinking"
	if ctx.S.Str("icon") == "text" {
		icon = "[thinking]"
	}
	return ctx.C.Model + icon + ctx.C.Rst, true
}

func renderAddedDirs(ctx RenderCtx) (string, bool) {
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

func renderEmail(ctx RenderCtx) (string, bool) {
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

// Info describes one segment: its ID, default line, description, primary
// color role, optional settings schema, and renderer.
type Info struct {
	ID           string
	Line         int
	Desc         string
	PrimaryColor string
	Settings     []config.SettingSpec // nil → no flyout, no settings entry
	NeedsState   bool                 // renderer reads the session state store
	Plugin       bool                 // registered from a [[plugins]] entry
	Preview      string               // sample value used in the TUI assembler
	Render       func(ctx RenderCtx) (string, bool)
}

var registry []Info

func allSegmentInfos() []Info {
	return []Info{
		{ID: "vim-mode", Line: 1, Desc: "Vim mode indicator (e.g. [normal])", PrimaryColor: "Vim", Render: renderVimMode},
		{ID: "sandbox", Line: 1, Desc: "Sandbox status indicator", PrimaryColor: "RCrit", Render: renderSandbox},
		{ID: "session-name", Line: 1, Desc: "Session name label", PrimaryColor: "Session", Render: renderSessionName},
		{ID: "prompt-id", Line: 2, Desc: "Prompt ID, truncated to 8 chars when it looks like a UUID", PrimaryColor: "Dim", Render: renderPromptID},
		{ID: "agent-state", Line: 1, Desc: "Agent working status", PrimaryColor: "Git", Render: renderAgentState},
		{ID: "agent-name", Line: 1, Desc: "Agent name", PrimaryColor: "Agent", Render: renderAgentName},
		{ID: "directory", Line: 1, Desc: "Current / project directory", PrimaryColor: "Dir", Render: renderDirectory},
		{ID: "added-dirs", Line: 1, Desc: "Number of extra directories added with /add-dir", PrimaryColor: "Dim", Render: renderAddedDirs},
		{ID: "repo", Line: 1, Desc: "Repository owner/name from the payload", PrimaryColor: "Dim", Settings: config.RepoSettingSpecs(), Render: renderRepo},
		{ID: "pr", Line: 1, Desc: "Pull request number and optional review state or URL", PrimaryColor: "Git", Settings: config.PRSettingSpecs(), Render: renderPR},
		{ID: "git-branch", Line: 1, Desc: "Git branch and worktree name, with optional dirty marker, ahead/behind counts, worktree path, and original branch", PrimaryColor: "Git", Settings: config.GitBranchSettingSpecs(), Render: renderGitBranch},
		{ID: "git-stash", Line: 1, Desc: "Git stash count (⚑N), hidden when there are no stashes", PrimaryColor: "Git", Settings: config.GitStashSettingSpecs(), Render: renderGitStash},
		{ID: "artifact-count", Line: 1, Desc: "Artifact count", PrimaryColor: "Chg", Render: renderArtifactCount},
		{ID: "lines-changed", Line: 1, Desc: "All lines added / removed by the agent in the session", PrimaryColor: "Chg", Render: renderLinesChanged},
		{ID: "cache-percent", Line: 1, Desc: "Cache read percentage", PrimaryColor: "Dim", Render: renderCachePercent},
		{ID: "plan-tier", Line: 1, Desc: "Subscription plan tier", PrimaryColor: "Purple", Render: renderPlanTier},
		{ID: "cost", Line: 1, Desc: "Total session cost", PrimaryColor: "Cost", Render: renderCost},
		{ID: "model", Line: 2, Desc: "Model name and effort badge", PrimaryColor: "Model", Render: renderModel},
		{ID: "output-style", Line: 2, Desc: "Output style name (hidden when default)", PrimaryColor: "Purple", Render: renderOutputStyle},
		{ID: "thinking", Line: 2, Desc: "Thinking indicator when reasoning is enabled", PrimaryColor: "Model", Settings: config.ThinkingSettingSpecs(), Render: renderThinking},
		{ID: "email", Line: 2, Desc: "Account email, user part only (off by default)", PrimaryColor: "Dim", Render: renderEmail},
		{ID: "version", Line: 2, Desc: "Claude Code version", PrimaryColor: "Dim", Render: renderVersion},
		{ID: "update", Line: 1, Desc: "Update available notice (self-hides when current, dev, or [update].mode = off)", PrimaryColor: "Dim", Render: renderUpdatePlaceholder},
		{ID: "duration", Line: 2, Desc: "Elapsed session duration", PrimaryColor: "Dur", Render: renderDuration},
		{ID: "cost-rate", Line: 2, Desc: "Cost burn rate $/h over recent session history", PrimaryColor: "Cost", Settings: config.CostRateSpecs(), NeedsState: true, Render: renderCostRate},
		{ID: "api-efficiency", Line: 2, Desc: "API efficiency percentage", PrimaryColor: "Dim", Render: renderAPIEfficiency},
		{ID: "tokens", Line: 2, Desc: "Input / output token counts", PrimaryColor: "Dim", Render: renderTokens},
		{ID: "context-window", Line: 3, Desc: "Context window usage bar with growth trend and time-to-compact estimate", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(false, true, true, barWidth, IconsetNames(), config.TrendSpecs()...), NeedsState: true, Render: renderContextWindow},
		{ID: "rate-limit-5h", Line: 3, Desc: "5-hour quota bar with reset countdown and burn-rate projection", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(true, false, true, barWidth, IconsetNames(), config.ProjectionSpecs(30)...), NeedsState: true, Render: renderRateLimit5h},
		{ID: "rate-limit-7d", Line: 3, Desc: "7-day quota bar with reset countdown and burn-rate projection", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(true, false, true, barWidth, IconsetNames(), config.ProjectionSpecs(180)...), NeedsState: true, Render: renderRateLimit7d},
		{ID: "rate-limit-fable", Line: 3, Desc: "Fable 5 weekly included-quota bar with countdown and projection", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(true, false, true, barWidth, IconsetNames(), config.ProjectionSpecs(180)...), NeedsState: true, Render: renderRateLimitFable},
		{ID: "rate-limit-sonnet", Line: 3, Desc: "Sonnet weekly quota bar with countdown and projection", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(true, false, true, barWidth, IconsetNames(), config.ProjectionSpecs(180)...), NeedsState: true, Render: renderRateLimitSonnet},
		{ID: "rate-limit-opus", Line: 3, Desc: "Opus weekly quota bar with countdown and projection", PrimaryColor: "Dim", Settings: config.BarSettingSpecs(true, false, true, barWidth, IconsetNames(), config.ProjectionSpecs(180)...), NeedsState: true, Render: renderRateLimitOpus},
	}
}

// Init initializes the segment registry with the built-in segments.
func Init() {
	registry = allSegmentInfos()
}

// Register appends a segment (typically a plugin segment) to the registry.
func Register(seg Info) {
	registry = append(registry, seg)
}

// ByID returns the segment with the given ID, if present.
func ByID(id string) (Info, bool) {
	for _, s := range registry {
		if s.ID == id {
			return s, true
		}
	}
	return Info{}, false
}

// All returns the current segment registry.
func All() []Info {
	return registry
}

// Filter returns the segments whose ID or description contains the query
// (case-insensitive). An empty query returns everything.
func Filter(all []Info, query string) []Info {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all
	}
	var out []Info
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.ID), q) || strings.Contains(strings.ToLower(s.Desc), q) {
			out = append(out, s)
		}
	}
	return out
}

// ─── Helpers ─────────────────────────────────────────────────────────

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

func rateLimitSegment(label string, window payload.LimitWindow, windowSecs int64, seriesKey string, ctx RenderCtx) (string, bool) {
	if window.UsedPercentage == nil {
		return "", false
	}
	c, s := ctx.C, ctx.S
	pct := int(*window.UsedPercentage)
	color := pctColorWithSettings(pct, c, s)
	countdown := "?"
	timePct := -1
	if window.ResetsAt != nil {
		countdown = ansi.ResetCountdown(*window.ResetsAt, ctx.Now)
		if windowSecs > 0 {
			remaining := *window.ResetsAt - ctx.Now.Unix()
			if remaining >= 0 && remaining <= windowSecs {
				timePct = int((windowSecs - remaining) * 100 / windowSecs)
			}
		}
	}
	result := c.Dim + label + " "
	if s.Bool("show_bar") {
		result += ProgressBarWithTimeAndIconset(pct, timePct, color, c.Dim, c, s.Int("bar_width"), s.Str("iconset")) + " "
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
func rateLimitProjection(window payload.LimitWindow, seriesKey string, ctx RenderCtx) string {
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

func formatCost(v float64) string {
	return fmt.Sprintf("%.2f", v)
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

// FirstNonEmpty returns the first non-empty string argument.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ─── Progress Bars ───────────────────────────────────────────────────

// iconset defines the glyphs of one progress-bar style. All glyphs are a
// single terminal cell wide. Partials, when present, are fractional-fill
// glyphs ordered low→high that multiply the bar's effective resolution.
type iconset struct {
	Filled, Empty string
	Partials      []string
}

var iconsets = map[string]iconset{
	"default":      {Filled: "#", Empty: "-"},
	"blocks":       {Filled: "█", Empty: "░"},
	"dots":         {Filled: "●", Empty: "○"},
	"ascii":        {Filled: "=", Empty: " "},
	"minimal":      {Filled: "|", Empty: " "},
	"braille":      {Filled: "⣿", Empty: "⣀"},
	"braille-fine": {Filled: "⣿", Empty: "⠀", Partials: []string{"⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷"}},
	"shade":        {Filled: "▓", Empty: "░"},
	"smooth":       {Filled: "█", Empty: " ", Partials: []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}},
	"line":         {Filled: "━", Empty: "─"},
	"slim":         {Filled: "▰", Empty: "▱"},
	"vertical":     {Filled: "▮", Empty: "▯"},
}

// iconsetOrder is the cycle order offered in the TUI (map iteration order is
// random, so the list is explicit).
var iconsetOrder = []string{
	"default", "blocks", "dots", "ascii", "minimal",
	"smooth", "braille", "braille-fine", "shade", "line", "slim", "vertical",
}

// IconsetNames returns the ordered list of supported progress-bar iconsets.
func IconsetNames() []string {
	return iconsetOrder
}

func iconsetByName(name string) iconset {
	if is, ok := iconsets[name]; ok {
		return is
	}
	return iconsets["default"]
}

func iconsetPair(name string) (string, string) {
	is := iconsetByName(name)
	return is.Filled, is.Empty
}

// ProgressBarWithIconset renders a percentage bar using the named iconset.
func ProgressBarWithIconset(pct int, fillColor, emptyColor string, c palette.Palette, width int, name string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	is := iconsetByName(name)

	if len(is.Partials) == 0 {
		filled := pct * width / 100
		return fillColor + strings.Repeat(is.Filled, filled) +
			emptyColor + strings.Repeat(is.Empty, width-filled) + c.Rst
	}

	// Fractional fill: each cell subdivides into len(Partials)+1 units; the
	// remainder renders as one partial glyph in the fill color.
	n := len(is.Partials) + 1
	units := pct * width * n / 100
	full := units / n
	rem := units % n
	var b strings.Builder
	b.WriteString(fillColor)
	b.WriteString(strings.Repeat(is.Filled, full))
	empty := width - full
	if rem > 0 && full < width {
		b.WriteString(is.Partials[rem-1])
		empty--
	}
	b.WriteString(emptyColor)
	b.WriteString(strings.Repeat(is.Empty, empty))
	b.WriteString(c.Rst)
	return b.String()
}

// ProgressBarWithTimeAndIconset renders a percentage bar with a time marker.
func ProgressBarWithTimeAndIconset(pct, timePct int, fillColor, emptyColor string, c palette.Palette, width int, iconset string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	filledChar, emptyChar := iconsetPair(iconset)

	timeSlot := -1
	if timePct >= 0 && timePct <= 100 {
		timeSlot = timePct * width / 100
		if timeSlot >= width {
			timeSlot = width - 1
		}
	}

	var b strings.Builder
	for i := 0; i < width; i++ {
		switch {
		case i == timeSlot:
			b.WriteString(c.Purple + "|")
		case i < filled:
			b.WriteString(fillColor + filledChar)
		default:
			b.WriteString(emptyColor + emptyChar)
		}
	}
	b.WriteString(c.Rst)
	return b.String()
}

func pctColorWithSettings(pct int, c palette.Palette, s config.Settings) string {
	warnAt, critAt := s.Int("warn_at"), s.Int("crit_at")
	var colorName, natural string
	switch {
	case pct > critAt:
		colorName, natural = s.Str("crit_color"), "bright-red"
	case pct >= warnAt:
		colorName, natural = s.Str("warn_color"), "yellow"
	default:
		colorName, natural = s.Str("ok_color"), "green"
	}
	// "" or "default" both mean "use the natural color for this state".
	if colorName == "" || colorName == "default" {
		colorName = natural
	}
	return palette.ResolveColor(colorName, c)
}

// ─── Render Context ──────────────────────────────────────────────────

// RenderCtx carries everything a segment renderer needs. The palette already
// has the per-segment color override applied, and S holds the segment's own
// resolved settings. Now is injected so countdowns and rates are testable.
type RenderCtx struct {
	P       payload.Payload
	C       palette.Palette
	S       config.Settings
	State   *state.SessionState // nil unless the segment declares NeedsState
	Cfg     config.Config       // resolved config, rarely needed (e.g. update segment)
	Width   int
	Now     time.Time
	Preview bool // true when rendering for the TUI assembler/preview
}
