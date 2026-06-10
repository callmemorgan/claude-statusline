package main

import "fmt"

// ─── Help ────────────────────────────────────────────────────────────

func printHelp() {
	fmt.Println(`claude-statusline — statusline renderer for Claude Code and Antigravity CLI

Usage:
  claude-statusline [--help|-h] [--version|-v] [--configure] [--debug]

Modes:
  (none)       Read JSON payload from stdin and print the statusline.
  --configure  Launch an interactive TUI to toggle, order, and assign
               segments to lines. Saves to ~/.config/claude-statusline/config.toml
  --debug      Read JSON from stdin and print a schema-comparison table
               showing which fields were detected for Claude Code vs agy.
  --version    Show this binary's version. (The statusline's "version"
               segment shows the calling tool's version, not this binary's.)
  --help, -h   Show this help message.

Configuration file:
  ~/.config/claude-statusline/config.toml
  (a pre-1.0 config.json is migrated automatically; the original is kept
   as config.json.bak)

  Example:
    segments = ["session-name", "directory", "git-branch", "cost", "model", "context-window"]

    [lines]
    cost = 2

  segments — which segments to show and in what order. Use [] to hide everything.
  lines    — override which line (1–9) a segment renders on. Omit for default.
  plugins  — custom executable segments ([[plugins]] tables). See README.md.

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
  added-dirs    Count of /add-dir directories +2 dirs (Claude Code only)
  git-branch    Git branch and worktree name (both)
  artifact-count  Number of artifacts (agy only)
  lines-changed   All lines added/removed by the agent in the session +N/-M (Claude Code only)
  cache-percent   Cache read percentage (Claude Code only)
  plan-tier     Subscription plan tier (agy only)
  cost          Estimated session cost $X.XX (Claude Code only)

Line 2 segments — Model & metrics:
  model         Model name with effort badge (both)
  output-style  Output style name ✎ Explanatory, hidden when default (Claude Code only)
  email         Account email, user part only — off by default (agy only)
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
