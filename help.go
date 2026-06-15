package main

import "fmt"

// ─── Help ────────────────────────────────────────────────────────────

func printHelp() {
	v, _, _ := versionString()
	fmt.Printf("claude-statusline v%s — statusline renderer for Claude Code and Antigravity CLI\n", v)
	fmt.Println(`
Usage:
  claude-statusline <command> [flags]

Commands:
  install      Wire this binary into ~/.claude/settings.json (or
               $CLAUDE_CONFIG_DIR/settings.json when set): backs up the
               original, splices the statusLine key without reformatting,
               and verifies with a sample render.
               Flags: --target claude|agy · --settings-path PATH · --force
                      --dry-run · --yes
  uninstall    Remove the statusline wiring (--restore swaps the backup back).
  configure    Interactive TUI: toggle/order segments, themes, presets,
               colors, per-segment settings, live width-aware preview,
               animated demo mode, render-in-terminal theme check.
  version      Show this binary's version. (The statusline's own "version"
               segment shows the calling tool's version, not this binary's.)
  debug        Read JSON from stdin and print a schema-comparison table plus
               any config warnings.
  release-notes  Show what changed in this version (also: vX.Y.Z, --all).
  update       Check for a new release and install it. Foreground,
               honors the same safety rails as the background worker.
               Flags: --check (resolve + report only, never install).
               Subcommand: verify (check the latest release's signature
               against the embedded key, then exit; installs nothing).
  help         Show this message.
  (none)       Read the JSON payload from stdin and print the statusline —
               this is how Claude Code invokes the binary.

  Legacy flag spellings (--configure, --debug, --version, --help) still work.

Configuration:
  ~/.config/claude-statusline/config.toml
  (a pre-1.0 config.json migrates automatically; the original is kept as
   config.json.bak)

  theme        classic | catppuccin-mocha | nord | dracula | gruvbox-dark |
               tokyo-night (truecolor, with 256/16-color fallback);
               classic is the pre-1.0 default look (alias: original)
  preset       named layout used when 'segments' is absent: classic, minimal,
               zen, cost-tracker, git-focus, vim-coder, quota-watch,
               full-dashboard
  segments     which segments to show, in order ([] hides everything)
  [lines]      per-segment line overrides (1-9)
  [colors]     per-segment colors: names, #rrggbb hex, 0-255 indexes, or
               theme roles
  [settings.*] per-segment settings — press o on a segment in the TUI to
               discover them
  [style]      separator = bar|dot|slash|chevron|powerline|space|custom,
               padding
  [state]      session history store powering burn rates and projections
               (enabled, retention_hours)
  [release_notes]  post-upgrade announcement: announce (default true),
               duration_seconds (default 25, 0 = off)
  [update]     background update check + segment: mode (notify|auto|off,
               default notify), check_hours (1-168, default 24)
  [[plugins]]  custom executable segments — see README.md

Line 1 segments — Session & workspace:
  vim-mode      Vim mode indicator [normal] (Claude Code only)
  sandbox       [SANDBOX] when enabled (agy only)
  session-name  Session name, UUIDs truncated to 8 chars (both)
  agent-state   Agent working status [working] (agy only)
  agent-name    Agent name when using --agent (Claude Code only)
  directory     Current / project directory (both)
  added-dirs    Count of /add-dir directories +2 dirs (Claude Code only)
  git-branch    Git branch and worktree; optional dirty marker and
                ahead/behind counts, e.g. main* ↑1↓2 (both)
  git-stash     Git stash count ⚑N, hidden at zero; off by default (both)
  artifact-count  Number of artifacts (agy only)
  lines-changed   Lines added/removed in the session +N/-M (Claude Code only)
  cache-percent   Cache read percentage (Claude Code only)
  plan-tier     Subscription plan tier (agy only)
  cost          Estimated session cost $X.XX (Claude Code only)

Line 2 segments — Model & metrics:
  model         Model name with effort badge (both)
  output-style  Output style ✎ Explanatory, hidden when default (Claude Code only)
  email         Account email, user part only — off by default (agy only)
  version       Tool version (both)
  duration      Elapsed session time HH:MM:SS (Claude Code only)
  cost-rate     Cost burn rate $1.84/h from session history (Claude Code only)
  api-efficiency  API time / total time % (Claude Code only)
  tokens        Input/output token counts ↑N ↓M (both)

Line 3 segments — Usage bars:
  context-window  Usage bar with growth trend and time-to-compact ↗ ~35m (both)
  rate-limit-5h   5-hour quota bar, countdown, burn-rate projection →58%
  rate-limit-7d   7-day quota bar, countdown, burn-rate projection
                  (rate limits: Claude Code Pro/Max only)

Segments hide automatically when their source data is missing or zero.
Burn rates, projections, and trends appear after ~5 minutes of session
history.

Environment:
  NO_COLOR=1            Disable colors (always wins).
  TERM=dumb             Disable colors.
  COLORTERM=truecolor   Force truecolor detection (or set color_depth).
  STATUSLINE_VERBOSE=1  Print config warnings to stderr during renders.

Examples:
  # One-command setup
  claude-statusline install

  # Minimal smoke test
  echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | claude-statusline

  # Interactive configuration
  claude-statusline configure`)
}
