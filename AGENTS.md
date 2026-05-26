<!-- From: /Users/morgan/.claude/statusline/AGENTS.md -->
# AGENTS.md — claude-statusline

## Project Overview

`claude-statusline` is a Go implementation of a statusline renderer for [Claude Code](https://claude.ai/code) and [Antigravity CLI](https://antigravity.dev) (`agy`). Both tools pipe a JSON payload to the binary on every turn, and the tool renders a configurable multi-line colored summary showing session metadata, model information, cost, token usage, rate limits, and more.

The binary auto-detects which tool is calling it via the `product` field in the payload and hides segments that aren't applicable (e.g., rate limits are hidden under agy; plan tier is hidden under Claude Code).

## Technology Stack

- **Go 1.26** — Single-file implementation (`main.go`).
- **Runtime: standard library only** — The JSON-to-statusline renderer has zero external dependencies.
- **Configure mode: tview** — `claude-statusline --configure` uses `github.com/rivo/tview` for panes, scrollable lists, live preview, and help.
- **Embedded README** — `README.md` is embedded via `//go:embed` and rendered as formatted help inside the TUI.

## Project Structure

```
.
├── main.go                  # Go implementation (single file, ~1400 lines)
├── go.mod                   # Go module: github.com/callmemorgan/claude-statusline
├── go.sum                   # Dependency checksums
├── .gitignore               # Ignores built binary
├── README.md                # Human-facing documentation
├── TESTING.md               # Manual test cases
├── AGENTS.md                # This file
└── examples/
    └── plugins/
        └── memory.sh        # Cross-platform memory monitoring plugin example
```

There are no sub-packages and no generated code. The only external dependencies are `tview`, `tcell`, and `golang.org/x/term`, used exclusively by `--configure`.

## Build and Run

```bash
# Easiest: ask Claude Code to install it for you
claude "please install https://github.com/callmemorgan/claude-statusline as my statusline"

# Or install via go install
go install github.com/callmemorgan/claude-statusline@latest

# Or build the Go binary locally
go build -o claude-statusline .

# Quick smoke test with minimal JSON
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline

# Debug schema comparison (Claude Code vs agy)
echo '{"product":"antigravity","model":{"display_name":"Gemini"}}' | ./claude-statusline --debug

# Launch interactive configuration TUI
./claude-statusline --configure

# Clean build artifacts
rm -f claude-statusline
```

## Runtime Architecture

1. **Read stdin** up to 1 MiB (`maxInput = 1 << 20`) and validate that input starts with `{` and ends with `}`.
2. **Parse JSON** into the Claude Code / agy payload schema.
3. **Fall back** to a minimal object if input is missing or malformed.
4. **Resolve colors** based on `NO_COLOR` and `TERM=dumb` environment variables.
5. **Load configuration** from `~/.config/claude-statusline/config.json`.
6. **Initialize segments** (built-in + any plugins defined in config).
7. **Build output lines** by iterating enabled segments, rendering each, and grouping by assigned line number (1–9). Blank lines collapse automatically.
8. **Print** the statusline lines plus an elapsed-timing suffix on the first line.

### JSON Payload Schema (Go structs)

The Go source defines the expected payload shape in the `payload` struct. Key fields:

- `session_id`, `session_name`, `conversation_id` — session identifiers (agy uses `conversation_id`; UUIDs are truncated to 8 chars for display)
- `cwd`, `version`, `transcript_path` — top-level metadata
- `product` — `"antigravity"` for agy, empty/omitted for Claude Code
- `exceeds_200k_tokens` — boolean flag for context window warning
- `model` — `display_name`, `id`
- `workspace` — `current_dir`, `project_dir` (`file://` prefix is stripped), `git_worktree`
- `cost` — `total_cost_usd`, `total_lines_added`, `total_lines_removed`, `total_duration_ms`, `total_api_duration_ms`
- `context_window` — token counts, `context_window_size`, `used_percentage`, `current_usage` (with cache creation/read tokens)
- `rate_limits` — `five_hour` and `seven_day` windows with `used_percentage` and `resets_at`
- `agent` / `agent_state` — agent name (Claude Code) or working state (agy)
- `worktree` — `name`, `branch`
- `vim` — `mode`
- `effort` — `level`
- `sandbox` — `enabled` (agy)
- `artifact_count` — number of artifacts (agy)
- `plan_tier` — subscription tier (agy)

Fields are optional; missing or zero values cause segments to hide themselves automatically.

## Configuration

Statusline segments are controlled by `~/.config/claude-statusline/config.json`. An annotated example is provided at [`config.json.example`](config.json.example):

```bash
cp config.json.example ~/.config/claude-statusline/config.json
```

```json
{
  "segments": [
    "vim-mode", "sandbox", "session-name", "agent-state", "directory",
    "git-branch", "artifact-count", "lines-changed", "cache-percent", "cost",
    "model", "version", "duration", "api-efficiency", "tokens",
    "context-window", "rate-limit-5h", "rate-limit-7d", "plan-tier"
  ],
  "lines": {
    "cost": 2,
    "model": 1
  },
  "plugins": []
}
```

**Behavior:**
- `segments` controls visibility and order. An explicit empty array `[]` hides the statusline entirely.
- `lines` maps segment IDs to line numbers (1–9). Segments not listed use their natural line.
- Blank lines (lines with no active segments) collapse automatically.
- Invalid line numbers or unknown segment IDs in `lines` are silently ignored.
- Missing config file = all default segments in default order with no line overrides.

### Built-in segments

| ID | Natural line | Description | Tool source |
|----|-------------|-------------|-------------|
| `vim-mode` | 1 | Vim mode indicator (e.g., `[normal]`) | Claude Code |
| `sandbox` | 1 | `[SANDBOX]` indicator when enabled | agy |
| `session-name` | 1 | Session name (UUIDs truncated to 8 chars) | both |
| `agent-state` | 1 | Agent working status (e.g., `[working]`) | agy |
| `agent-name` | 1 | Agent name | Claude Code |
| `directory` | 1 | Current / project directory | both |
| `git-branch` | 1 | Git branch and worktree name | both |
| `artifact-count` | 1 | Artifact count | agy |
| `lines-changed` | 1 | Lines added / removed | Claude Code |
| `cache-percent` | 1 | Cache read percentage | Claude Code |
| `plan-tier` | 1 | Subscription plan tier | agy |
| `cost` | 1 | Total session cost | Claude Code |
| `model` | 2 | Model name and effort badge | both |
| `version` | 2 | Claude Code / agy version | both |
| `duration` | 2 | Elapsed session duration (HH:MM:SS) | Claude Code |
| `api-efficiency` | 2 | API efficiency percentage | Claude Code |
| `tokens` | 2 | Input / output token counts | both |
| `context-window` | 3 | Context window usage bar | both |
| `rate-limit-5h` | 3 | 5-hour quota bar with countdown | Claude Code |
| `rate-limit-7d` | 3 | 7-day quota bar with countdown | Claude Code |

Segments that receive no data from the active tool hide themselves automatically.

### Segment rendering details

#### `vim-mode`
Renders `[mode]` in bright white. Source: `vim.mode`. Hidden when empty.

#### `sandbox`
Renders `[SANDBOX]` in bright red. Source: `sandbox.enabled`. Hidden when false.

#### `session-name`
Renders the session identifier in bright cyan. Uses `session_name` (Claude Code) or `conversation_id` (agy). If the value is a 36-character UUID with 4 dashes, truncated to the first 8 chars. Hidden when empty.

#### `agent-state`
Renders `[state]` in green when `"working"`, dim otherwise. Source: `agent_state`. Hidden when empty.

#### `agent-name`
Renders the agent name in bright magenta. Source: `agent.name`. Hidden when empty.

#### `directory`
Renders the current directory in cyan. Uses `workspace.current_dir` → `cwd` → `~`. If inside a project subdirectory, formats as `project→subdir`. `file://` prefixes are stripped from `project_dir`.

#### `git-branch`
Renders the git branch in green. Uses `worktree.branch`; falls back to reading `.git/HEAD` by walking up from `current_dir`. Handles git worktrees (`.git` as file with `gitdir:`). If a worktree name differs from the branch, shows `branch (worktree)`. Hidden outside a git repo.

#### `artifact-count`
Renders `artifacts:N` in yellow. Source: `artifact_count`. Hidden when ≤ 0.

#### `lines-changed`
Renders `+added/-removed` in yellow. Source: `cost.total_lines_added`, `cost.total_lines_removed`. Hidden when both are zero.

#### `cache-percent`
Renders `cache:XX.XX%` in dim. Calculated from `context_window.current_usage`: `cache_read / (input + cache_creation + cache_read) * 100`. Hidden when cache total is zero.

#### `plan-tier`
Renders the plan tier in purple. Source: `plan_tier`. Hidden when empty.

#### `cost`
Renders `$X.XX` in yellow. Source: `cost.total_cost_usd`. Always formatted to 2 decimal places. Hidden when zero.

#### `model`
Renders `[Model Name badge]` in magenta. Model name from `model.display_name` → `model.id` → `"Claude"`. Effort badge from `effort.level` or `~/.claude/settings.json` (`effortLevel`). Badges: `low`→⬇, `medium`→→, `high`→⬆, `xhigh`→⬆⬆, `max`→⬆⬆⬆.

#### `version`
Renders `vX.Y.Z` in dim. Source: `version`. Hidden when empty.

#### `duration`
Renders `HH:MM:SS` in blue. Source: `cost.total_duration_ms`. Hidden when zero.

#### `api-efficiency`
Renders `(API:NN%)` in dim. Calculated as `total_api_duration * 100 / total_duration`. Hidden when total duration is ≤ 0.

#### `tokens`
Renders `↑input ↓output` in dim. Source: `context_window.total_input_tokens`, `total_output_tokens`. Compacted: `≥1M`→`X.YM`, `≥1k`→`X.Yk`, else raw integer.

#### `context-window`
Renders `ctx [bar] NN%` with color-coded percentage. Uses `used_percentage` if present, else calculates from `current_usage` / `context_window_size`. Bar: `#` = used, `-` = empty. Color: green (< 60%), yellow (60–80%), red (> 80%). Appends `>200k` in red if `exceeds_200k_tokens` is true.

#### `rate-limit-5h` / `rate-limit-7d`
Renders `label [bar] NN% (countdown)`. Uses `rate_limits.five_hour` / `seven_day`. Bar overlays a purple `|` at time-elapsed position. Countdown from `resets_at` Unix timestamp. Color thresholds same as context window. Hidden when `used_percentage` is absent (non-Pro/Max users, or before first API call).

### Plugin system

Custom segments can be provided by any executable. Plugins are defined in the `plugins` array in config:

**Single-field plugin** — whole stdout becomes the segment value:
```json
{
  "id": "memory",
  "command": "~/.config/claude-statusline/plugins/memory.sh",
  "line": 1,
  "desc": "RAM usage",
  "timeout_ms": 200
}
```

**Multi-field plugin** — one command produces multiple segments via `key:value` lines:
```json
{
  "command": "~/.config/claude-statusline/plugins/memory.sh",
  "timeout_ms": 200,
  "fields": [
    {"id": "mem-used",   "line": 1, "desc": "RAM used"},
    {"id": "swap-used",  "line": 1, "desc": "Swap used"},
    {"id": "%-mem-used", "line": 1, "desc": "RAM % used"}
  ]
}
```

A full cross-platform example is provided at `examples/plugins/memory.sh` (macOS + Linux).

Plugin behavior:
- `~` in `command` is expanded to the user's home directory.
- Default timeout is 200 ms; hidden on timeout or non-zero exit.
- Empty stdout hides the segment automatically.
- Plugin IDs are auto-appended to `segments` if not already present.
- Plugin segments appear in `--configure` with a `[plugin]` label.

**Environment variables exposed to plugins:**
| Variable | Value |
|----------|-------|
| `STATUSLINE_MODEL` | Model display name |
| `STATUSLINE_DIR` | Current working directory |
| `STATUSLINE_BRANCH` | Git branch |
| `STATUSLINE_SESSION` | Session name or conversation ID |
| `STATUSLINE_PRODUCT` | `"antigravity"` or empty |
| `STATUSLINE_PAYLOAD` | Full JSON payload |

### Interactive setup

`claude-statusline --configure` opens a TUI with:

1. **Segment list** — all built-in and plugin segments with `•` toggle indicators and `[Ln]` line overrides.
2. **Description panel** — shows the description of the currently selected segment.
3. **Preview pane** — live-rendered statusline (no colour) that updates as you change segments.
4. **Help page** — full README rendered with markdown formatting (press `h`).

Keys:
- `↑/↓` — navigate segments
- `Space` — toggle on/off
- `1`–`9` — assign segment to that line (enables it if disabled)
- `←/→` — reorder segment within its current line
- `Shift+↑/↓` — swap all segments on the current line with the adjacent line
- `r` — reset to defaults
- `s` — save and exit
- `q` — quit without saving
- `h` — open help (README); `q`/`Esc` to close

Requires an interactive terminal (`golang.org/x/term` check).

## Color Palette

Colors are ANSI escape codes gated by `NO_COLOR` and `TERM=dumb`. When disabled, all color strings are empty.

| Semantic | ANSI Code |
|----------|-----------|
| Model | `\x1b[35m` (magenta) |
| Directory | `\x1b[36m` (cyan) |
| Git | `\x1b[32m` (green) |
| Changes | `\x1b[33m` (yellow) |
| Duration | `\x1b[34m` (blue) |
| Cost | `\x1b[33m` (yellow) |
| Dim | `\x1b[90m` (bright black) |
| OK rate/context | `\x1b[32m` (green) |
| Warning | `\x1b[33m` (yellow) |
| Critical | `\x1b[91m` (bright red) |
| Agent | `\x1b[95m` (bright magenta) |
| Vim | `\x1b[97m` (bright white) |
| Session | `\x1b[96m` (bright cyan) |
| Purple | `\x1b[35m` (magenta) — used for time-elapsed overlay in rate-limit bars |

## Progress Bars

- `barWidth` is hard-coded to `20` characters.
- Context bar uses `#` for used portion and `-` for empty.
- Rate-limit bars overlay a purple `|` at the time-elapsed position within the window.
- Percentage color thresholds: `> 80%` = critical (red), `>= 60%` = warning (yellow), else OK (green).

## Token Formatting

Token counts are compacted:
- `>= 1_000_000` → `X.YM`
- `>= 1_000` → `X.Yk`
- otherwise → raw integer

## Cost Formatting

Always formatted to two decimal places (`%.2f`).

## Effort Level Badge

Read from the JSON payload (`effort.level`) or from `~/.claude/settings.json` (`effortLevel` field). Mapped to arrows:
- `low` → `⬇`
- `medium` → `→`
- `high` → `⬆`
- `xhigh` → `⬆⬆`
- `max` → `⬆⬆⬆`

## Git Branch Detection

Detects the git branch by walking up the directory tree from `current_dir`, reading `.git/HEAD` directly, and parsing `ref: refs/heads/<branch>` or returning `detached`. Also handles git worktrees (`.git` as a file pointing to an external `gitdir` via `gitdir: …`).

## Code Style Guidelines

- **Go**: Keep everything in `package main`. Use plain structs with JSON tags. Prefer explicit error handling with early returns. Minimise external dependencies; new runtime code should stay stdlib-only.
- **Naming**: Go uses camelCase. Segment renderers are named `render<SegmentName>`.
- **Comments**: Explain *why* for non-obvious logic. Section dividers use `// ─── Section ──────────────────────────────────────────────────────────`.

## Testing

There are **no automated tests** in this repository. Because the tool is a pure function from stdin JSON to stdout text, manual validation is the current workflow. See `TESTING.md` for exhaustive manual test cases covering:

- Minimal / full Claude Code / full agy payloads
- Config behavior (defaults, custom segments, hiding everything, line overrides, blank-line collapse)
- Edge cases (malformed input, color disable, outside git repo, zero values, `file://` URI stripping, UUID truncation)
- `--configure` TUI interactions (toggle, line assignment, reorder, reset)

Quick smoke test:
```bash
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline
```

## Deployment / Distribution

There is no CI/CD, packaging, or release automation in this repo. The project is intended to be built locally:

```bash
go build -o claude-statusline .
```

Users can place the resulting binary anywhere on their `$PATH` and configure Claude Code or agy to invoke it.

## Security Considerations

- **Input size capped at 1 MiB** (`maxInput = 1 << 20`) to prevent memory exhaustion from malicious stdin.
- **JSON is parsed, not evaluated** — no `eval` or shell interpolation of payload content.
- **File system reads are limited** to:
  - Walking up the directory tree to find `.git/HEAD`
  - Reading `~/.claude/settings.json` for effort level
  - Reading `~/.config/claude-statusline/config.json` for configuration
- **Plugin commands execute with a timeout** (default 200 ms) and receive sanitized environment variables. The full JSON payload is passed via `STATUSLINE_PAYLOAD` for advanced use.
- **No network access** in the core renderer.
- **No secrets or credentials** are handled.

## Conventions for Contributors

- Keep the runtime renderer dependency-free (standard library only). The `--configure` TUI may use `tview`.
- Respect `NO_COLOR` and `TERM=dumb` for any new color output.
- Support lines 1–9; empty lines collapse automatically.
- New built-in segments should follow the pattern: `render<Name>` function, entry in `allSegmentInfos()`, natural line assignment, auto-hide on missing/zero data.
- If a segment is tool-specific (Claude Code vs agy), implement auto-hide based on payload field presence rather than explicit tool checks.
- Plugin IDs should be auto-appended to the segments list if not already present.
