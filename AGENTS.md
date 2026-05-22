# AGENTS.md — claude-statusline

## Project Overview

`claude-statusline` is a Go implementation of a statusline renderer for [Claude Code](https://claude.ai/code). Claude Code pipes a JSON payload to the statusline on every turn, and the tool renders a three-line colored summary showing:

- Model name, version, and effort level
- Current directory and git branch/worktree
- Session cost, duration, and API efficiency
- Token usage (input/output) with compact formatting
- Context window usage as an ASCII progress bar
- Rate-limit usage (5-hour and 7-day windows) with countdown timers
- Vim mode indicator, agent name, and lines added/removed

## Technology Stack

- **Go 1.26** — Single-file implementation.
- **Standard library only** — Zero external dependencies.

## Project Structure

```
.
├── main.go                  # Go implementation (single file, ~520 lines)
├── go.mod                   # Go module: github.com/callmemorgan/claude-statusline
├── .gitignore               # Ignores built binary
└── AGENTS.md                # This file
```

There are no sub-packages, no vendored dependencies, and no generated code.

## Build and Run

```bash
# Build the Go binary
go build -o claude-statusline main.go

# Quick smoke test with minimal JSON
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline

# Clean build artifacts
rm -f claude-statusline
```

## Runtime Architecture

1. **Read stdin** up to 1 MiB and validate that input starts with `{` and ends with `}`.
2. **Parse JSON** into the Claude Code payload schema.
3. **Fall back** to a minimal object if input is missing or malformed.
4. **Resolve colors** based on `NO_COLOR` and `TERM` environment variables.
5. **Build three lines** of output by extracting fields, formatting numbers, and computing percentages.
6. **Print** the three lines plus an elapsed-timing suffix on line 1.

### JSON Payload Schema (Go structs)

The Go source defines the expected payload shape. Key structs:

- `payload` — root object containing `model`, `workspace`, `cost`, `context_window`, `rate_limits`, `agent`, `worktree`, `vim`, `effort`, plus top-level strings like `session_id`, `session_name`, `cwd`, `version`, `transcript_path`.
- `model` — `display_name`, `id`
- `workspace` — `current_dir`, `project_dir`, `git_worktree`
- `cost` — `total_cost_usd`, `total_lines_added`, `total_lines_removed`, `total_duration_ms`, `total_api_duration_ms`
- `contextWin` / `currentUsage` — token counts and context size
- `rateLimits` / `limitWindow` — `used_percentage`, `resets_at`

Fields are optional; missing values are replaced with sensible defaults.

### Git Branch Detection

Detects the git branch by walking up the directory tree, reading `.git/HEAD` directly, and parsing `ref: refs/heads/<branch>` or returning `detached`. Also handles git worktrees (`.git` as a file pointing to an external `gitdir`).

### Configuration

Statusline segments are controlled by an **ordered array** of segment IDs in `~/.config/claude-statusline/config.json`. The array determines both *which* segments appear and *in what order*:

```json
{
  "segments": [
    "vim-mode",
    "session-name",
    "agent-name",
    "directory",
    "git-branch",
    "lines-changed",
    "cache-percent",
    "cost",
    "model",
    "version",
    "duration",
    "api-efficiency",
    "tokens",
    "context-window",
    "rate-limits"
  ]
}
```

**Behavior:**
- Segments render on their natural line (line 1 = workspace meta, line 2 = model/duration, line 3 = progress bars).
- Missing config file = all segments in default order.
- Empty array `"segments": []` = hide the statusline entirely.
- Omitted segments = hidden.

**Available segments:**

| ID | Line | Description |
|----|------|-------------|
| `vim-mode` | 1 | Vim mode indicator (e.g. `[normal]`) |
| `session-name` | 1 | Session name label |
| `agent-name` | 1 | Agent name |
| `directory` | 1 | Current / project directory |
| `git-branch` | 1 | Git branch and worktree name |
| `lines-changed` | 1 | Lines added / removed |
| `cache-percent` | 1 | Cache read percentage |
| `cost` | 1 | Total session cost |
| `model` | 2 | Model name and effort badge |
| `version` | 2 | Claude Code version |
| `duration` | 2 | Elapsed session duration |
| `api-efficiency` | 2 | API efficiency percentage |
| `tokens` | 2 | Input / output token counts |
| `context-window` | 3 | Context window usage bar |
| `rate-limits` | 3 | 5-hour and 7-day quota bars |

**Interactive setup:** `claude-statusline --configure` opens a live-preview editor where you can set the segment order by typing numbers or IDs.

### Color Palette

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

### Progress Bars

- `barWidth` is hard-coded to `20` characters.
- Context bar uses `#` for used portion and `-` for empty.
- Rate-limit bars overlay a purple `|` at the time-elapsed position within the window.

### Token Formatting

Token counts are compacted:
- `>= 1_000_000` → `X.YM`
- `>= 1_000` → `X.Yk`
- otherwise → raw integer

### Cost Formatting

Always formatted to two decimal places (`%.2f`).

### Effort Level Badge

Read from `~/.claude/settings.json` (`effortLevel` field) or from the JSON payload. Mapped to arrows:
- `low` → `⬇`
- `medium` → `→`
- `high` → `⬆`
- `xhigh` → `⬆⬆`
- `max` → `⬆⬆⬆`

## Code Style Guidelines

- **Go**: Keep everything in `package main`. Use plain structs with JSON tags. Prefer explicit error handling with early returns. No external dependencies.
- **Naming**: Go uses camelCase.
- **Comments**: Explain *why* for non-obvious logic.

## Testing

There are **no automated tests** in this repository. Because the tool is a pure function from stdin JSON to stdout text, manual validation is the current workflow:

```bash
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline
```

If adding tests, the simplest approach would be table-driven tests feeding JSON strings to `buildStatusline` and asserting on output substrings.

## Deployment / Distribution

There is no CI/CD, packaging, or release automation. The project is intended to be built locally:

```bash
go build -o claude-statusline main.go
```

Users can place the resulting binary anywhere on their `$PATH` and configure Claude Code to invoke it.

## Security Considerations

- **Input size capped at 1 MiB** (`maxInput = 1 << 20`) to prevent memory exhaustion from malicious stdin.
- **JSON is parsed, not evaluated** — no `eval` or shell interpolation of payload content.
- **File system reads are limited** to:
  - Walking up the directory tree to find `.git/HEAD`
  - Reading `~/.claude/settings.json` for effort level
- **No network access**.
- **No secrets or credentials** are handled.

## Conventions for Contributors

- Keep the binary dependency-free (standard library only).
- Respect `NO_COLOR` and `TERM=dumb` for any new color output.
- Keep the three-line output contract: line 1 = location/meta, line 2 = model/duration/tokens, line 3 = progress bars.
