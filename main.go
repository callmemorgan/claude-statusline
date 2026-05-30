package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

//go:embed README.md
var readmeContent string

const (
	barWidth  = 20
	maxInput  = 1 << 20
	minObject = `{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}`
)

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return reANSI.ReplaceAllString(s, "")
}

func visibleWidth(s string) int {
	return utf8.RuneCountInString(stripANSI(s))
}

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

// ─── Config ──────────────────────────────────────────────────────────

type pluginField struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
	Desc string `json:"desc"`
}

type pluginDef struct {
	ID        string        `json:"id"`
	Command   string        `json:"command"`
	Line      int           `json:"line"`
	Desc      string        `json:"desc"`
	TimeoutMS int           `json:"timeout_ms"`
	Fields    []pluginField `json:"fields"`
}

type config struct {
	Segments []string            `json:"segments"`
	Lines    map[string]int      `json:"lines"`
	Colors   map[string]string   `json:"colors"`
	Plugins  []pluginDef         `json:"plugins"`
	Reflow   string              `json:"reflow"`
}

func defaultConfig() config {
	return config{
		Segments: []string{
			"vim-mode", "sandbox", "session-name", "agent-state", "directory",
			"git-branch", "artifact-count", "lines-changed", "cache-percent", "cost",
			"model", "version", "duration", "api-efficiency", "tokens",
			"context-window", "rate-limit-5h", "rate-limit-7d", "plan-tier",
		},
		Lines: nil,
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
	cfg.Lines = loaded.Lines
	cfg.Colors = loaded.Colors
	cfg.Plugins = loaded.Plugins
	cfg.Reflow = loaded.Reflow
	// Auto-append plugin IDs only when the config file doesn't specify
	// segments at all (nil). If the user explicitly set segments — even to
	// an empty array — respect their choice and don't force plugins on.
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
	ConversationID string     `json:"conversation_id"`
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

// ─── Help ────────────────────────────────────────────────────────────

func printHelp() {
	fmt.Println(`claude-statusline — statusline renderer for Claude Code and Antigravity CLI

Usage:
  claude-statusline [--help|-h] [--configure] [--debug]

Modes:
  (none)       Read JSON payload from stdin and print the statusline.
  --configure  Launch an interactive TUI to toggle, order, and assign
               segments to lines. Saves to ~/.config/claude-statusline/config.json
  --debug      Read JSON from stdin and print a schema-comparison table
               showing which fields were detected for Claude Code vs agy.
  --help, -h   Show this help message.

Configuration file:
  ~/.config/claude-statusline/config.json

  Example:
    {
      "segments": ["session-name","directory","git-branch","cost","model","context-window"],
      "lines": {"cost": 2},
      "plugins": []
    }

  segments — which segments to show and in what order. Use [] to hide everything.
  lines    — override which line (1–9) a segment renders on. Omit for default.
  plugins  — custom executable segments. See README.md for plugin docs.

Environment:
  NO_COLOR=1   Disable ANSI colours.
  TERM=dumb    Disable ANSI colours.

Line 1 segments — Session & workspace:
  vim-mode      Vim mode indicator [normal] (Claude Code only)
  sandbox       [SANDBOX] when enabled (agy only)
  session-name  Session name, UUIDs truncated to 8 chars (both)
  agent-state   Agent working status [working] (agy only)
  agent-name    Agent name when using --agent (Claude Code only)
  directory     Current / project directory (both)
  git-branch    Git branch and worktree name (both)
  artifact-count  Number of artifacts (agy only)
  lines-changed   All lines added/removed by the agent in the session +N/-M (Claude Code only)
  cache-percent   Cache read percentage (Claude Code only)
  plan-tier     Subscription plan tier (agy only)
  cost          Estimated session cost $X.XX (Claude Code only)

Line 2 segments — Model & metrics:
  model         Model name with effort badge (both)
  version       Tool version (both)
  duration      Elapsed session time HH:MM:SS (Claude Code only)
  api-efficiency  API time / total time % (Claude Code only)
  tokens        Input/output token counts ↑N ↓M (both)

Line 3 segments — Usage bars:
  context-window  20-char progress bar with color-coded % (both)
  rate-limit-5h   5-hour quota bar with countdown (Claude Code Pro/Max only)
  rate-limit-7d   7-day quota bar with countdown (Claude Code Pro/Max only)

Segments hide automatically when their source data is missing or zero.

Examples:
  # Minimal smoke test
  echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | claude-statusline

  # Debug schema detection
  echo '{"product":"antigravity","model":{"display_name":"Gemini"}}' | claude-statusline --debug

  # Interactive configuration
  claude-statusline --configure`)
}

// ─── Main ────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		printHelp()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--configure" {
		runConfigure()
		return
	}
	debug := len(os.Args) > 1 && os.Args[1] == "--debug"

	start := time.Now()

	input := readInput()
	p := parsePayload(input)

	if debug {
		printDebugSchema(input, p)
		return
	}

	colors := currentPalette()
	cfg := loadConfig()
	initSegments(cfg.Plugins)
	lines := buildStatusline(p, colors, cfg, terminalWidth(p))

	elapsedMS := float64(time.Since(start).Microseconds()) / 1000.0
	if len(lines) > 0 {
		fmt.Printf("%s │ %s%.1fms%s\n", lines[0], colors.Dim, elapsedMS, colors.Rst)
		for _, l := range lines[1:] {
			fmt.Println(l)
		}
	} else {
		fmt.Printf("%s%.1fms%s\n", colors.Dim, elapsedMS, colors.Rst)
	}
}

// ─── Debug Schema ────────────────────────────────────────────────────

func printDebugSchema(raw []byte, p payload) {
	tool := "claude-code"
	if p.Product == "antigravity" {
		tool = "agy"
	}
	fmt.Printf("=== payload source: %s ===\n\n", tool)

	// Parse raw JSON into a map to check which keys are actually present.
	var raw2 map[string]json.RawMessage
	_ = json.Unmarshal(raw, &raw2)
	present := func(key string) string {
		if _, ok := raw2[key]; ok {
			return "✓"
		}
		return "✗"
	}

	type row struct{ field, claude, agy, got string }
	rows := []row{
		// Claude Code fields
		{"session_name", "✓", "✗", present("session_name")},
		{"cost", "✓", "✗", present("cost")},
		{"rate_limits", "✓", "✗", present("rate_limits")},
		{"agent.name", "✓", "✗", func() string {
			if _, ok := raw2["agent"]; ok {
				return "✓"
			}
			return "✗"
		}()},
		{"worktree", "✓", "✗", present("worktree")},
		{"vim", "✓", "✗", present("vim")},
		{"effort", "✓", "✗", present("effort")},
		// agy fields
		{"conversation_id", "✗", "✓", present("conversation_id")},
		{"product", "✗", "✓", present("product")},
		{"agent_state", "✗", "✓", present("agent_state")},
		{"sandbox.enabled", "✗", "✓", present("sandbox")},
		{"artifact_count", "✗", "✓", present("artifact_count")},
		{"plan_tier", "✗", "✓", present("plan_tier")},
		// shared
		{"model", "✓", "✓", present("model")},
		{"workspace", "✓", "✓", present("workspace")},
		{"context_window", "✓", "✓", present("context_window")},
		{"version", "✓", "✓", present("version")},
	}

	fmt.Printf("%-22s  %-8s  %-8s  %-8s\n", "field", "claude", "agy", "got")
	fmt.Println(strings.Repeat("-", 54))
	for _, r := range rows {
		mismatch := ""
		if tool == "claude-code" && r.claude == "✓" && r.got == "✗" {
			mismatch = "  ← MISSING"
		}
		if tool == "agy" && r.agy == "✓" && r.got == "✗" {
			mismatch = "  ← MISSING"
		}
		fmt.Printf("%-22s  %-8s  %-8s  %-8s%s\n", r.field, r.claude, r.agy, r.got, mismatch)
	}

	fmt.Println()
	fmt.Printf("parsed values:\n")
	fmt.Printf("  product        = %q\n", p.Product)
	fmt.Printf("  session_name   = %q\n", p.SessionName)
	fmt.Printf("  conversation_id= %q\n", p.ConversationID)
	fmt.Printf("  model          = %q\n", p.Model.DisplayName)
	fmt.Printf("  workspace.cur  = %q\n", p.Workspace.CurrentDir)
	fmt.Printf("  workspace.proj = %q\n", p.Workspace.ProjectDir)
	fmt.Printf("  agent_state    = %q\n", p.AgentState)
	fmt.Printf("  plan_tier      = %q\n", p.PlanTier)
	fmt.Printf("  sandbox        = %v\n", p.Sandbox.Enabled)
	fmt.Printf("  artifact_count = %d\n", p.ArtifactCount)
	fmt.Printf("  cost_usd       = %.4f\n", p.Cost.TotalCostUSD)
	fmt.Printf("  vim_mode       = %q\n", p.Vim.Mode)
	fmt.Printf("  effort         = %q\n", p.Effort.Level)
}

// ─── Plugin System ───────────────────────────────────────────────────

var registeredSegments []segmentInfo

// pluginCache holds per-command parsed output within a single buildStatusline
// call so multi-field plugins run their command only once per turn.
var pluginCache map[string]map[string]string

func clearPluginCache() {
	pluginCache = map[string]map[string]string{}
}

func initSegments(plugins []pluginDef) {
	registeredSegments = allSegmentInfos()
	for _, p := range plugins {
		def := p
		if len(def.Fields) > 0 {
			// Multi-field plugin: register one segment per field.
			for _, f := range def.Fields {
				field := f
				line := field.Line
				if line < 1 {
					line = 1
				}
				desc := field.Desc
				if desc == "" {
					desc = field.ID
				}
				registeredSegments = append(registeredSegments, segmentInfo{
					id:           field.ID,
					line:         line,
					desc:         desc + " [plugin]",
					primaryColor: "Dim",
					render: func(pay payload, c palette) (string, bool) {
						out := runPluginField(def, pay, field.ID)
						return out, out != ""
					},
				})
			}
		} else {
			// Single-field plugin: whole stdout is the segment value.
			line := def.Line
			if line < 1 {
				line = 1
			}
			desc := def.Desc
			if desc == "" {
				desc = def.ID
			}
			registeredSegments = append(registeredSegments, segmentInfo{
				id:           def.ID,
				line:         line,
				desc:         desc + " [plugin]",
				primaryColor: "Dim",
				render: func(pay payload, c palette) (string, bool) {
					out := runPluginRaw(def, pay)
					return out, out != ""
				},
			})
		}
	}
}

// runPluginField runs a multi-field plugin (cached per command) and returns
// the value for the requested field ID.
func runPluginField(def pluginDef, p payload, fieldID string) string {
	if pluginCache == nil {
		pluginCache = map[string]map[string]string{}
	}
	if _, ok := pluginCache[def.Command]; !ok {
		raw := runPluginRaw(def, p)
		pluginCache[def.Command] = parseKeyValueOutput(raw)
	}
	return pluginCache[def.Command][fieldID]
}

// parseKeyValueOutput parses "key:value" lines from plugin stdout.
func parseKeyValueOutput(raw string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if key != "" {
				result[key] = val
			}
		}
	}
	return result
}

// runPluginRaw executes the plugin command and returns the full trimmed stdout.
func runPluginRaw(def pluginDef, p payload) string {
	timeout := time.Duration(def.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	home, _ := os.UserHomeDir()
	cmd := strings.Replace(def.Command, "~", home, 1)
	c := exec.CommandContext(ctx, cmd)

	session := p.SessionName
	if session == "" {
		session = p.ConversationID
	}
	c.Env = append(os.Environ(),
		"STATUSLINE_MODEL="+p.Model.DisplayName,
		"STATUSLINE_DIR="+p.Workspace.CurrentDir,
		"STATUSLINE_BRANCH="+p.Worktree.Branch,
		"STATUSLINE_SESSION="+session,
		"STATUSLINE_PRODUCT="+p.Product,
		"STATUSLINE_COLUMNS="+strconv.Itoa(p.TerminalWidth),
		"STATUSLINE_LINES="+os.Getenv("LINES"),
	)
	if raw, err := json.Marshal(p); err == nil {
		c.Env = append(c.Env, "STATUSLINE_PAYLOAD="+string(raw))
	}

	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ─── Markdown → tview renderer ───────────────────────────────────────

var (
	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
)

func inlineMarkdown(s string) string {
	s = reBold.ReplaceAllString(s, "[::b]$1[::-]")
	s = reInlineCode.ReplaceAllString(s, "[green]$1[-]")
	return s
}

func markdownToTview(md string) string {
	var b strings.Builder
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		// Code fence open/close.
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			if inCode {
				b.WriteString("[gray]")
			} else {
				b.WriteString("[-]\n")
			}
			continue
		}
		if inCode {
			b.WriteString(tview.Escape(line) + "\n")
			continue
		}

		esc := tview.Escape(line)
		switch {
		case strings.HasPrefix(line, "# "):
			b.WriteString("\n[yellow::b]" + tview.Escape(strings.TrimPrefix(line, "# ")) + "[-::-]\n")
		case strings.HasPrefix(line, "## "):
			b.WriteString("\n[cyan::b]  " + tview.Escape(strings.TrimPrefix(line, "## ")) + "[-::-]\n")
		case strings.HasPrefix(line, "### "):
			b.WriteString("[green::b]    " + tview.Escape(strings.TrimPrefix(line, "### ")) + "[-::-]\n")
		case strings.HasPrefix(line, "|"):
			// Table rows and separators — dim.
			b.WriteString("[::d]" + esc + "[-::-]\n")
		case strings.HasPrefix(line, "---"):
			// Horizontal rule.
			b.WriteString("[::d]────────────────────────────────────────[-::-]\n")
		default:
			b.WriteString(inlineMarkdown(esc) + "\n")
		}
	}
	return b.String()
}

// ─── Configure Mode ──────────────────────────────────────────────────

func effectiveLine(id string, cfg config) int {
	if override, ok := cfg.Lines[id]; ok && override >= 1 {
		return override
	}
	if s, ok := segmentByID(id); ok {
		return s.line
	}
	return 1
}

func runConfigure() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "claude-statusline --configure requires an interactive terminal.")
		fmt.Fprintf(os.Stderr, "Edit %s directly, or run from a terminal.\n", configPath())
		os.Exit(1)
	}

	cfg := loadConfig()
	initSegments(cfg.Plugins)
	segments := registeredSegments

	app := tview.NewApplication()

	// Scrollable list of all segments with toggle state.
	list := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	list.SetBorder(true)

	// Description panel — shows the description of the currently selected segment.
	descView := tview.NewTextView().SetWrap(true)
	descView.SetBorder(true).SetTitle(" Description ")

	// Live preview of the statusline (plain text — no ANSI / tview colour tags).
	// Fixed at 12 rows (10 content + 2 border) — max 9 statusline lines plus padding.
	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)

	previewBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(preview, 0, 1, false)
	previewBox.SetBorder(true).SetTitle(" Preview ")

	// Fixed-height help bar.
	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText(" space toggle • 1-9 line • c color • ←/→ reorder • ↑/↓ nav • ⇧↑/↓ move row • h help • r reset • s save • q quit")

	// Help page — full README rendered with markdown formatting.
	helpView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetText(markdownToTview(readmeContent))
	helpView.SetBorder(true).SetTitle(" Help — README (↑/↓ scroll • q/Esc close) ")

	// Update list items and preview from current cfg.
	updateUI := func() {
		currentIdx := list.GetCurrentItem()

		list.Clear()
		for _, s := range segments {
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

			line := s.line
			if override, ok := cfg.Lines[s.id]; ok && override >= 1 {
				line = override
			}
			lineStr := ""
			if line != s.line {
				lineStr = fmt.Sprintf(" [L%d]", line)
			}

			colorStr := ""
			if colorName := cfg.Colors[s.id]; colorName != "" && colorName != "default" {
				colorStr = fmt.Sprintf("[%s]", colorName)
			}

			mainText := fmt.Sprintf("%s%s%s%s", mark, s.id, lineStr, colorStr)
			list.AddItem(mainText, "", 0, nil)
		}

		if currentIdx >= 0 && currentIdx < len(segments) {
			list.SetCurrentItem(currentIdx)
		}
		list.SetTitle(fmt.Sprintf(" Segments (%d/%d) ", len(cfg.Segments), len(segments)))

		// Refresh preview with colours converted to tview tags.
		p := samplePayload()
		lines := buildStatusline(p, currentPalette(), cfg, 0)
		for i, l := range lines {
			lines[i] = strings.TrimLeft(l, " ")
		}
		previewText := strings.TrimSpace(strings.Join(lines, "\n"))
		if previewText == "" {
			previewText = "(statusline hidden — no segments enabled)"
		} else {
			previewText = ansiToTview(previewText)
		}
		preview.SetText(previewText)
	}

	updateUI()

	list.SetChangedFunc(func(idx int, _, _ string, _ rune) {
		if idx >= 0 && idx < len(segments) {
			descView.SetText(segments[idx].desc)
		} else {
			descView.SetText("")
		}
	})
	// Seed the description for the initial selection.
	if len(segments) > 0 {
		descView.SetText(segments[0].desc)
	}

	// pages holds both the configure view and the help view.
	pages := tview.NewPages()

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// When the help page is visible, only intercept close keys; let
		// everything else pass through so the TextView handles scrolling.
		if name, _ := pages.GetFrontPage(); name == "help" {
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage("configure")
				app.SetFocus(list)
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' || event.Rune() == 'Q' {
					pages.SwitchToPage("configure")
					app.SetFocus(list)
					return nil
				}
			}
			return event
		}

		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case 'h', 'H':
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case ' ':
				idx := list.GetCurrentItem()
				if idx < 0 || idx >= len(segments) {
					return nil
				}
				id := segments[idx].id
				found := -1
				for i, segID := range cfg.Segments {
					if segID == id {
						found = i
						break
					}
				}
				if found >= 0 {
					cfg.Segments = append(cfg.Segments[:found], cfg.Segments[found+1:]...)
				} else {
					cfg.Segments = append(cfg.Segments, id)
				}
				updateUI()
				return nil
			case 'c', 'C':
				idx := list.GetCurrentItem()
				if idx < 0 || idx >= len(segments) {
					return nil
				}
				id := segments[idx].id
				if cfg.Colors == nil {
					cfg.Colors = make(map[string]string)
				}
				currentColor := cfg.Colors[id]
				if currentColor == "" {
					currentColor = "default"
				}
				nextColor := "default"
				for i, name := range colorCycle {
					if name == currentColor {
						nextColor = colorCycle[(i+1)%len(colorCycle)]
						break
					}
				}
				if nextColor == "default" {
					delete(cfg.Colors, id)
				} else {
					cfg.Colors[id] = nextColor
				}
				// Ensure the segment is enabled when assigning a color.
				enabled := false
				for _, segID := range cfg.Segments {
					if segID == id {
						enabled = true
						break
					}
				}
				if !enabled {
					cfg.Segments = append(cfg.Segments, id)
				}
				updateUI()
				return nil
			default:
				r := event.Rune()
				if r >= '1' && r <= '9' {
					idx := list.GetCurrentItem()
					if idx < 0 || idx >= len(segments) {
						return nil
					}
					id := segments[idx].id
					n := int(r - '0')
					if cfg.Lines == nil {
						cfg.Lines = make(map[string]int)
					}
					if segments[idx].line == n {
						delete(cfg.Lines, id)
					} else {
						cfg.Lines[id] = n
					}
					// Ensure the segment is enabled when assigning a line.
					enabled := false
					for _, segID := range cfg.Segments {
						if segID == id {
							enabled = true
							break
						}
					}
					if !enabled {
						cfg.Segments = append(cfg.Segments, id)
					}
					updateUI()
					return nil
				}
			case 'r', 'R':
				cfg = defaultConfig()
				updateUI()
				return nil
			case 's', 'S':
				if err := saveConfig(cfg); err != nil {
					preview.SetText(fmt.Sprintf("Error saving: %v", err))
					return nil
				}
				app.Stop()
				fmt.Printf("Saved to %s\n", configPath())
				return nil
			case 'q', 'Q':
				app.Stop()
				return nil
			}
		case tcell.KeyEscape:
			app.Stop()
			return nil
		case tcell.KeyUp, tcell.KeyDown:
			// Unmodified Up/Down: pass through for list navigation.
			if event.Modifiers()&tcell.ModShift == 0 {
				return event
			}
			// Shift+Up / Shift+Down: swap the entire row with the adjacent row.
			idx := list.GetCurrentItem()
			if idx < 0 || idx >= len(segments) {
				return nil
			}
			id := segments[idx].id
			myLine := effectiveLine(id, cfg)
			targetLine := myLine - 1
			if event.Key() == tcell.KeyDown {
				targetLine = myLine + 1
			}
			if targetLine < 1 || targetLine > 9 {
				return nil
			}
			if cfg.Lines == nil {
				cfg.Lines = make(map[string]int)
			}
			// Snapshot which segments are on each line before reassigning.
			var onMyLine, onTargetLine []string
			for _, sid := range cfg.Segments {
				el := effectiveLine(sid, cfg)
				if el == myLine {
					onMyLine = append(onMyLine, sid)
				} else if el == targetLine {
					onTargetLine = append(onTargetLine, sid)
				}
			}
			assignLine := func(sid string, line int) {
				naturalLine := 1
				if s, ok := segmentByID(sid); ok {
					naturalLine = s.line
				}
				if line == naturalLine {
					delete(cfg.Lines, sid)
				} else {
					cfg.Lines[sid] = line
				}
			}
			for _, sid := range onMyLine {
				assignLine(sid, targetLine)
			}
			for _, sid := range onTargetLine {
				assignLine(sid, myLine)
			}
			updateUI()
			return nil
		case tcell.KeyLeft, tcell.KeyRight:
			idx := list.GetCurrentItem()
			if idx < 0 || idx >= len(segments) {
				return event
			}
			id := segments[idx].id
			myLine := effectiveLine(id, cfg)
			// Collect indices in cfg.Segments that share the same line, in order.
			var peers []int
			for i, sid := range cfg.Segments {
				if effectiveLine(sid, cfg) == myLine {
					peers = append(peers, i)
				}
			}
			// Find this segment's position within peers.
			pos := -1
			for i, pi := range peers {
				if cfg.Segments[pi] == id {
					pos = i
					break
				}
			}
			if event.Key() == tcell.KeyLeft && pos > 0 {
				cfg.Segments[peers[pos]], cfg.Segments[peers[pos-1]] =
					cfg.Segments[peers[pos-1]], cfg.Segments[peers[pos]]
				updateUI()
				return nil
			} else if event.Key() == tcell.KeyRight && pos >= 0 && pos < len(peers)-1 {
				cfg.Segments[peers[pos]], cfg.Segments[peers[pos+1]] =
					cfg.Segments[peers[pos+1]], cfg.Segments[peers[pos]]
				updateUI()
				return nil
			}
		}
		return event
	})

	topRow := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(list, 0, 1, true).
		AddItem(descView, 0, 3, false)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(topRow, 0, 1, true).
		AddItem(previewBox, 12, 0, false).
		AddItem(help, 1, 0, false)

	pages.AddPage("configure", flex, true, true)
	pages.AddPage("help", helpView, true, false)

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
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

// colorCycle is the ordered list of color names cycled by the TUI.
var colorCycle = []string{"default", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// colorCodes maps color names to ANSI escape codes.
var colorCodes = map[string]string{
	"default":       "",
	"red":           "\x1b[31m",
	"green":         "\x1b[32m",
	"yellow":        "\x1b[33m",
	"blue":          "\x1b[34m",
	"magenta":       "\x1b[35m",
	"cyan":          "\x1b[36m",
	"white":         "\x1b[37m",
	"bright-red":    "\x1b[91m",
	"bright-green":  "\x1b[92m",
	"bright-yellow": "\x1b[93m",
	"bright-blue":   "\x1b[94m",
	"bright-magenta": "\x1b[95m",
	"bright-cyan":   "\x1b[96m",
	"bright-white":  "\x1b[97m",
}

// paletteWithOverride returns a copy of c with the named primary color field
// replaced by the ANSI code for colorName.
func paletteWithOverride(c palette, primaryColor, colorName string) palette {
	code := colorCodes[colorName]
	if code == "" {
		return c
	}
	p := c
	switch primaryColor {
	case "Model":
		p.Model = code
	case "Dir":
		p.Dir = code
	case "Git":
		p.Git = code
	case "Chg":
		p.Chg = code
	case "Dur":
		p.Dur = code
	case "Cost":
		p.Cost = code
	case "Dim":
		p.Dim = code
	case "ROK":
		p.ROK = code
	case "RWarn":
		p.RWarn = code
	case "RCrit":
		p.RCrit = code
	case "Agent":
		p.Agent = code
	case "Vim":
		p.Vim = code
	case "Purple":
		p.Purple = code
	case "Session":
		p.Session = code
	}
	return p
}

// ansiToTview converts ANSI color codes in s to tview color tags.
// It escapes literal '[' characters so they are not interpreted as tags.
func ansiToTview(s string) string {
	// Step 1: replace ANSI codes with Unicode private-use placeholders.
	s = strings.ReplaceAll(s, "\x1b[0m", "\uE000")
	s = strings.ReplaceAll(s, "\x1b[30m", "\uE010")
	s = strings.ReplaceAll(s, "\x1b[31m", "\uE011")
	s = strings.ReplaceAll(s, "\x1b[32m", "\uE012")
	s = strings.ReplaceAll(s, "\x1b[33m", "\uE013")
	s = strings.ReplaceAll(s, "\x1b[34m", "\uE014")
	s = strings.ReplaceAll(s, "\x1b[35m", "\uE015")
	s = strings.ReplaceAll(s, "\x1b[36m", "\uE016")
	s = strings.ReplaceAll(s, "\x1b[37m", "\uE017")
	s = strings.ReplaceAll(s, "\x1b[90m", "\uE020")
	s = strings.ReplaceAll(s, "\x1b[91m", "\uE021")
	s = strings.ReplaceAll(s, "\x1b[92m", "\uE022")
	s = strings.ReplaceAll(s, "\x1b[93m", "\uE023")
	s = strings.ReplaceAll(s, "\x1b[94m", "\uE024")
	s = strings.ReplaceAll(s, "\x1b[95m", "\uE025")
	s = strings.ReplaceAll(s, "\x1b[96m", "\uE026")
	s = strings.ReplaceAll(s, "\x1b[97m", "\uE027")

	// Step 2: escape literal '[' for tview.
	s = tview.Escape(s)

	// Step 3: replace placeholders with tview color tags.
	s = strings.ReplaceAll(s, "\uE000", "[-]")
	s = strings.ReplaceAll(s, "\uE010", "[black]")
	s = strings.ReplaceAll(s, "\uE011", "[red]")
	s = strings.ReplaceAll(s, "\uE012", "[green]")
	s = strings.ReplaceAll(s, "\uE013", "[yellow]")
	s = strings.ReplaceAll(s, "\uE014", "[blue]")
	s = strings.ReplaceAll(s, "\uE015", "[magenta]")
	s = strings.ReplaceAll(s, "\uE016", "[cyan]")
	s = strings.ReplaceAll(s, "\uE017", "[white]")
	s = strings.ReplaceAll(s, "\uE020", "[gray]")
	s = strings.ReplaceAll(s, "\uE021", "[red::b]")
	s = strings.ReplaceAll(s, "\uE022", "[green::b]")
	s = strings.ReplaceAll(s, "\uE023", "[yellow::b]")
	s = strings.ReplaceAll(s, "\uE024", "[blue::b]")
	s = strings.ReplaceAll(s, "\uE025", "[magenta::b]")
	s = strings.ReplaceAll(s, "\uE026", "[cyan::b]")
	s = strings.ReplaceAll(s, "\uE027", "[white::b]")

	return s
}

// ─── Segment Renderers ───────────────────────────────────────────────

func renderVimMode(p payload, c palette) (string, bool) {
	if p.Vim.Mode == "" {
		return "", false
	}
	return c.Vim + "[" + p.Vim.Mode + "]" + c.Rst, true
}

func renderSessionName(p payload, c palette) (string, bool) {
	name := firstNonEmpty(p.SessionName, p.ConversationID)
	if name == "" {
		return "", false
	}
	if len(name) == 36 && strings.Count(name, "-") == 4 {
		name = name[:8]
	}
	return c.Session + name + c.Rst, true
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
	if p.Cost.TotalCostUSD == 0 {
		return "", false
	}
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
	if p.Cost.TotalDurationMS == 0 {
		return "", false
	}
	elapsed := formatHHMMSS(p.Cost.TotalDurationMS)
	return c.Dur + elapsed + c.Rst, true
}

func renderAPIEfficiency(p payload, c palette) (string, bool) {
	if p.Cost.TotalDurationMS <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s(API:%d%%)%s", c.Dim, p.Cost.TotalAPIDuration*100/p.Cost.TotalDurationMS, c.Rst), true
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

func renderRateLimit5h(p payload, c palette) (string, bool) {
	return rateLimitSegment("5h", p.RateLimits.FiveHour, 5*3600, c)
}

func renderRateLimit7d(p payload, c palette) (string, bool) {
	return rateLimitSegment("7d", p.RateLimits.SevenDay, 7*24*3600, c)
}

func renderAgentState(p payload, c palette) (string, bool) {
	if p.AgentState == "" {
		return "", false
	}
	stateColor := c.Dim
	if p.AgentState == "working" {
		stateColor = c.Git
	}
	return stateColor + "[" + p.AgentState + "]" + c.Rst, true
}

func renderSandbox(p payload, c palette) (string, bool) {
	if !p.Sandbox.Enabled {
		return "", false
	}
	return c.RCrit + "[SANDBOX]" + c.Rst, true
}

func renderArtifactCount(p payload, c palette) (string, bool) {
	if p.ArtifactCount <= 0 {
		return "", false
	}
	return c.Chg + fmt.Sprintf("artifacts:%d", p.ArtifactCount) + c.Rst, true
}

func renderPlanTier(p payload, c palette) (string, bool) {
	if p.PlanTier == "" {
		return "", false
	}
	return c.Purple + p.PlanTier + c.Rst, true
}

// ─── Segment Registry ────────────────────────────────────────────────

type segmentInfo struct {
	id           string
	line         int
	desc         string
	primaryColor string
	render       func(p payload, c palette) (string, bool)
}

func allSegmentInfos() []segmentInfo {
	return []segmentInfo{
		{id: "vim-mode", line: 1, desc: "Vim mode indicator (e.g. [normal])", primaryColor: "Vim", render: renderVimMode},
		{id: "sandbox", line: 1, desc: "Sandbox status indicator", primaryColor: "RCrit", render: renderSandbox},
		{id: "session-name", line: 1, desc: "Session name label", primaryColor: "Session", render: renderSessionName},
		{id: "agent-state", line: 1, desc: "Agent working status", primaryColor: "Git", render: renderAgentState},
		{id: "agent-name", line: 1, desc: "Agent name", primaryColor: "Agent", render: renderAgentName},
		{id: "directory", line: 1, desc: "Current / project directory", primaryColor: "Dir", render: renderDirectory},
		{id: "git-branch", line: 1, desc: "Git branch and worktree name", primaryColor: "Git", render: renderGitBranch},
		{id: "artifact-count", line: 1, desc: "Artifact count", primaryColor: "Chg", render: renderArtifactCount},
		{id: "lines-changed", line: 1, desc: "All lines added / removed by the agent in the session", primaryColor: "Chg", render: renderLinesChanged},
		{id: "cache-percent", line: 1, desc: "Cache read percentage", primaryColor: "Dim", render: renderCachePercent},
		{id: "plan-tier", line: 1, desc: "Subscription plan tier", primaryColor: "Purple", render: renderPlanTier},
		{id: "cost", line: 1, desc: "Total session cost", primaryColor: "Cost", render: renderCost},
		{id: "model", line: 2, desc: "Model name and effort badge", primaryColor: "Model", render: renderModel},
		{id: "version", line: 2, desc: "Claude Code version", primaryColor: "Dim", render: renderVersion},
		{id: "duration", line: 2, desc: "Elapsed session duration", primaryColor: "Dur", render: renderDuration},
		{id: "api-efficiency", line: 2, desc: "API efficiency percentage", primaryColor: "Dim", render: renderAPIEfficiency},
		{id: "tokens", line: 2, desc: "Input / output token counts", primaryColor: "Dim", render: renderTokens},
		{id: "context-window", line: 3, desc: "Context window usage bar", primaryColor: "Dim", render: renderContextWindow},
		{id: "rate-limit-5h", line: 3, desc: "5-hour quota bar", primaryColor: "Dim", render: renderRateLimit5h},
		{id: "rate-limit-7d", line: 3, desc: "7-day quota bar", primaryColor: "Dim", render: renderRateLimit7d},
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

// ─── Statusline Builder ──────────────────────────────────────────────

func buildStatusline(p payload, c palette, cfg config, columns int) []string {
	clearPluginCache()
	parts := map[int][]string{}
	for _, id := range cfg.Segments {
		if s, ok := segmentByID(id); ok {
			segPalette := c
			if c.Rst != "" {
				if colorName := cfg.Colors[id]; colorName != "" && colorName != "default" {
					segPalette = paletteWithOverride(c, s.primaryColor, colorName)
				}
			}
			if rendered, show := s.render(p, segPalette); show {
				line := s.line
				if override, ok := cfg.Lines[id]; ok && override >= 1 {
					line = override
				}
				parts[line] = append(parts[line], rendered)
			}
		}
	}
	if len(parts) == 0 {
		return []string{}
	}

	if columns > 0 && cfg.Reflow == "group" {
		return buildStatuslineGroup(parts, columns)
	}

	return buildStatuslineCascade(parts, columns)
}

// buildStatuslineCascade is the original reflow behaviour: segments spill
// greedily from line 1 → 2 → 3 regardless of which logical line they belong to.
func buildStatuslineCascade(parts map[int][]string, columns int) []string {
	maxLine := 0
	originalLines := map[int]bool{}
	for k := range parts {
		if k > maxLine {
			maxLine = k
		}
		originalLines[k] = true
	}

	// Track which lines received overflow from a previous line.
	receivedOverflow := map[int]bool{}

	// Auto-reflow: spill trailing segments to the next line when a line
	// exceeds the available terminal width.
	if columns > 0 {
		const timingSuffixReserve = 15
		const safetyMargin = 5
		lineNum := 1
		for lineNum <= maxLine {
			budget := columns - safetyMargin
			if lineNum == 1 {
				budget = columns - timingSuffixReserve - safetyMargin
				if budget < 10 {
					budget = 10
				}
			}
			for {
				segs := parts[lineNum]
				if len(segs) <= 1 {
					break
				}
				width := 1 // leading space in joinParts
				for i, seg := range segs {
					if i > 0 {
						width += 3 // " │ "
					}
					width += visibleWidth(seg)
				}
				if width <= budget {
					break
				}
				// Move last segment to the next line.
				moved := segs[len(segs)-1]
				parts[lineNum] = segs[:len(segs)-1]
				parts[lineNum+1] = append([]string{moved}, parts[lineNum+1]...)
				receivedOverflow[lineNum+1] = true
				if lineNum+1 > maxLine {
					maxLine = lineNum + 1
				}
			}
			lineNum++
		}
	}

	out := []string{}
	for i := 1; i <= maxLine; i++ {
		line := joinParts(parts[i])
		if receivedOverflow[i] && originalLines[i] && i > 1 && (len(out) == 0 || out[len(out)-1] != "") {
			out = append(out, "")
		}
		out = append(out, line)
	}
	return out
}

// buildStatuslineGroup wraps each logical line independently. Segments from
// different logical lines never share a physical line, preserving the line
// boundaries defined in the configuration.
func buildStatuslineGroup(parts map[int][]string, columns int) []string {
	const timingSuffixReserve = 15
	const safetyMargin = 5

	var lineNums []int
	for k := range parts {
		lineNums = append(lineNums, k)
	}
	sort.Ints(lineNums)

	var out []string
	firstPhysicalLine := true

	for _, lineNum := range lineNums {
		segs := parts[lineNum]
		if len(segs) == 0 {
			continue
		}

		var current []string
		currentWidth := 0

		for _, seg := range segs {
			segWidth := visibleWidth(seg)
			sep := 1 // leading space
			if len(current) > 0 {
				sep = 3 // " │ "
			}

			budget := columns - safetyMargin
			if firstPhysicalLine && len(out) == 0 {
				budget = columns - timingSuffixReserve - safetyMargin
				if budget < 10 {
					budget = 10
				}
			}

			if len(current) == 0 || currentWidth+sep+segWidth <= budget {
				current = append(current, seg)
				currentWidth += sep + segWidth
			} else {
				out = append(out, joinParts(current))
				current = []string{seg}
				currentWidth = 1 + segWidth
				firstPhysicalLine = false
			}
		}

		if len(current) > 0 {
			out = append(out, joinParts(current))
			firstPhysicalLine = false
		}
	}

	return out
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
