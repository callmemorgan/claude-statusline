# claude-statusline

A fast statusline renderer for [Claude Code](https://claude.ai/code) and [Antigravity CLI](https://antigravity.dev) (`agy`).

Both tools pipe a JSON payload to this binary on every turn. It renders a colored, multi-line summary in your terminal. The number of lines is fully configurable — segments are assigned to lines 1–9 and empty lines collapse automatically.

The core renderer has no dependencies. The interactive `--configure` TUI uses [tview](https://github.com/rivo/tview).

---

## Install

**macOS — Homebrew (recommended):**

```bash
brew tap callmemorgan/tap
brew install claude-statusline
```

Upgrade later with `brew upgrade claude-statusline`.

---

**Any platform — `go install`:**

```bash
go install github.com/callmemorgan/claude-statusline@latest
```

Requires Go 1.22+. Make sure `$(go env GOPATH)/bin` is on your `$PATH`.

---

**Prebuilt binaries:**

Download a signed binary from the [releases page](https://github.com/callmemorgan/claude-statusline/releases). Each asset includes a cosign certificate and signature for verification.

Verify with [cosign](https://sigstore.dev):

```bash
cosign verify-blob \
  --certificate claude-statusline_Darwin_arm64.tar.gz.cert \
  --signature claude-statusline_Darwin_arm64.tar.gz.sig \
  --certificate-identity-regexp="^https://github.com/callmemorgan/claude-statusline/.github/workflows/release.yml@" \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  claude-statusline_Darwin_arm64.tar.gz
```

> **macOS note:** Downloaded binaries are not notarized. If Gatekeeper blocks the binary on first run, run `xattr -d com.apple.quarantine /path/to/claude-statusline`, or use Homebrew/`go install` instead.

---

**Build from source:**

```bash
git clone https://github.com/callmemorgan/claude-statusline.git
cd claude-statusline
go build -o claude-statusline .
```

---

## Wiring it up

### Claude Code

Add to your Claude Code settings (`~/.claude/settings.json`):

```json
{
  "statusLine": {
    "type": "command",
    "command": "claude-statusline"
  }
}
```

If you installed via Homebrew or `go install`, the binary is already on your `$PATH`. If you built from source or downloaded a binary manually, use the full path instead of `claude-statusline`.

### Antigravity CLI (agy)

Add to your agy config:

```json
{
  "statusline": "claude-statusline"
}
```

If the binary isn't on your `$PATH`, use the full path instead.

The binary auto-detects which tool is calling it via the `product` field in the payload and hides segments that aren't applicable (e.g. rate limits are hidden under agy, plan tier is hidden under Claude Code).

---

## What it looks like

**Claude Code (default config):**

```
 my-project /Users/me/code/my-project feature/test +128/-45 cache:12.34% $1.23 │ 0.3ms
 [Claude Sonnet 4.6 ⬆] v2.1.90 01:00:41 (API:65%) ↑1.2M ↓89k
 ctx [##############------] 72% >200k
 5h [########------------] 45% (2h30m) │ 7d [###-----------------] 12% (3d4h)
```

**agy (default config):**

```
 fbce29fe /Users/me/code/my-project feature/test artifacts:2 Google AI Pro
 [Gemini 3.5 Flash (High)] v1.0.2 ↑116.7k ↓35.4k
 ctx [#-------------------] 11%
```

Segments that receive no data from the active tool hide themselves automatically — no configuration needed.

---

## Segments

| Segment | Default line | Source | Description |
|---------|-------------|--------|-------------|
| `vim-mode` | 1 | Claude Code | Vim mode indicator, e.g. `[normal]` or `[INSERT]` |
| `sandbox` | 1 | agy | `[SANDBOX]` indicator when sandbox mode is enabled |
| `session-name` | 1 | both | Session name (Claude Code) or conversation ID (agy). UUIDs are truncated to 8 chars |
| `agent-state` | 1 | agy | Agent working status, e.g. `[working]` — green when active |
| `agent-name` | 1 | Claude Code | Agent name when running with `--agent` |
| `directory` | 1 | both | Current / project directory. Shows `project→subdir` when inside a project subdirectory |
| `git-branch` | 1 | both | Git branch and worktree name. Falls back to reading `.git/HEAD` if not in payload |
| `artifact-count` | 1 | agy | Number of generated artifacts |
| `lines-changed` | 1 | Claude Code | Session cumulative lines added/removed, e.g. `+128/-45` |
| `cache-percent` | 1 | Claude Code | Cache read percentage from `context_window.current_usage` |
| `plan-tier` | 1 | agy | Subscription plan tier |
| `cost` | 1 | Claude Code | Estimated session cost in USD, e.g. `$1.23` |
| `model` | 2 | both | Model name with effort badge (⬇ → ⬆ ⬆⬆ ⬆⬆⬆) |
| `version` | 2 | both | Tool version |
| `duration` | 2 | Claude Code | Elapsed session wall-clock time in `HH:MM:SS` |
| `api-efficiency` | 2 | Claude Code | Percentage of time spent in API calls vs. total elapsed |
| `tokens` | 2 | both | Input/output token counts in compact notation (`↑1.2M ↓89k`) |
| `context-window` | 3 | both | 20-char progress bar with color-coded context usage % |
| `rate-limit-5h` | 3 | Claude Code | 5-hour rate limit bar with countdown timer (Pro/Max only) |
| `rate-limit-7d` | 3 | Claude Code | 7-day rate limit bar with countdown timer (Pro/Max only) |

### Color coding

- **Model**: magenta
- **Directory**: cyan
- **Git**: green
- **Changes / Cost**: yellow
- **Duration**: blue
- **Context / Rate limits**: green (< 60%), yellow (60–80%), red (> 80%)
- **Agent**: bright magenta
- **Vim**: bright white
- **Session**: bright cyan

---

## Configuration

```bash
claude-statusline --configure
```

Opens an interactive TUI: a scrollable segment list on the left, a live description panel on the right, and a statusline preview below.

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate segments |
| `Space` | Toggle segment on/off |
| `1`–`9` | Move segment to that line (enables it if disabled) |
| `c` | Cycle segment color (enables it if disabled) |
| `←` / `→` | Reorder segment within its current line |
| `Shift+↑` / `Shift+↓` | Swap all segments on the current line with the adjacent line |
| `r` | Reset to defaults |
| `s` | Save and exit |
| `q` | Quit without saving |
| `h` | Open help (README); `q`/`Esc` to close |

### Manual config

Config lives at `~/.config/claude-statusline/config.json`. An annotated example is provided at [`config.json.example`](config.json.example):

```bash
cp config.json.example ~/.config/claude-statusline/config.json
```

```json
{
  "segments": [
    "session-name",
    "directory",
    "git-branch",
    "cost",
    "model",
    "version",
    "context-window",
    "rate-limit-5h",
    "rate-limit-7d"
  ],
  "lines": {
    "cost": 2
  },
  "colors": {
    "model": "cyan",
    "cost": "green"
  },
  "reflow": "group"
}
```

- `segments` — which segments to show and in what order. Omit to use defaults.
- `lines` — override which line a segment renders on (1–9). Omit a segment to use its natural line.
- `colors` — override the display color of a segment. Supported names: `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`, and `bright-*` variants. Set to `"default"` or omit to use the segment's natural color.
- `reflow` — how segments wrap when the terminal is too narrow:
  - `"cascade"` (default) — segments spill greedily across line boundaries.
  - `"group"` — each logical line wraps independently, preserving the boundaries set in `lines`.
- Empty array `[]` — hides the statusline entirely.
- Blank lines (no active segments) are collapsed automatically.
- When the terminal is too narrow for a line to fit, segments automatically spill to the next line.

### Common configurations

**Minimal — model + context only:**

```json
{
  "segments": ["model", "context-window"]
}
```

**Git-focused — directory + branch on line 1, model + tokens on line 2:**

```json
{
  "segments": ["directory", "git-branch", "model", "tokens", "context-window"],
  "lines": {
    "model": 2,
    "tokens": 2,
    "context-window": 2
  }
}
```

**Cost tracking — cost + duration on line 1:**

```json
{
  "segments": ["session-name", "directory", "cost", "duration", "model", "tokens", "context-window"],
  "lines": {
    "cost": 1,
    "duration": 1,
    "model": 2,
    "tokens": 2,
    "context-window": 3
  }
}
```

---

## Plugins

Add your own segments with any executable — a shell script, Python script, or binary. Each plugin runs on every turn, and its stdout becomes the segment content. Empty output hides the segment automatically.

### Single-field plugin

One segment, whole stdout is the value:

```json
{
  "plugins": [
    {
      "id": "memory",
      "command": "~/.config/claude-statusline/plugins/memory.sh",
      "line": 1,
      "desc": "RAM usage",
      "timeout_ms": 200
    }
  ]
}
```

### Multi-field plugin

One command, multiple independent segments. The command runs **once** per turn; each field reads its value from a `key:value` line in stdout:

```json
{
  "plugins": [
    {
      "command": "~/.config/claude-statusline/plugins/memory.sh",
      "timeout_ms": 200,
      "fields": [
        {"id": "mem-used", "line": 1, "desc": "RAM used"},
        {"id": "mem-swap", "line": 1, "desc": "Swap used"},
        {"id": "mem-free", "line": 3, "desc": "Free RAM"}
      ]
    }
  ]
}
```

Each field ID is an independent segment — independently togglable, positionable, and reorderable in the TUI.

- `id` — segment identifier (used in `segments` list and TUI)
- `command` — path to the executable; `~` is expanded
- `line` — default line (1–9); overridable via TUI or `lines` config
- `desc` — shown in the TUI description panel
- `timeout_ms` — kill the process after this many ms (default: 200); hidden if it times out or exits non-zero
- `fields` — multi-field mode; output must use `key:value` lines; mutually exclusive with top-level `id`

Plugin IDs are **auto-appended** to `segments` if not already present, so they appear immediately without editing the list manually.

### Environment variables

The binary exposes these to every plugin:

| Variable | Value |
|----------|-------|
| `STATUSLINE_MODEL` | Model display name |
| `STATUSLINE_DIR` | Current working directory |
| `STATUSLINE_BRANCH` | Git branch |
| `STATUSLINE_SESSION` | Session name or conversation ID |
| `STATUSLINE_PRODUCT` | `antigravity` or empty for Claude Code |
| `STATUSLINE_COLUMNS` | Terminal width (`COLUMNS` or `terminal_width`) |
| `STATUSLINE_LINES` | Terminal height (`LINES`) |
| `STATUSLINE_PAYLOAD` | Full JSON payload (for advanced use) |

### Example: memory + swap (cross-platform, multi-field)

A full working example lives at [`examples/plugins/memory.sh`](examples/plugins/memory.sh). It reports `mem-used`, `swap-used`, and `%-mem-used`, and works on both macOS (`vm_stat`/`sysctl`) and Linux (`/proc/meminfo`).

```sh
cp examples/plugins/memory.sh ~/.config/claude-statusline/plugins/memory.sh
chmod +x ~/.config/claude-statusline/plugins/memory.sh
```

Add to your config:

```json
{
  "plugins": [
    {
      "command": "~/.config/claude-statusline/plugins/memory.sh",
      "timeout_ms": 200,
      "fields": [
        {"id": "mem-used",   "line": 1, "desc": "RAM used"},
        {"id": "swap-used",  "line": 1, "desc": "Swap used"},
        {"id": "%-mem-used", "line": 1, "desc": "RAM % used"}
      ]
    }
  ]
}
```

Plugin segments appear in `--configure` with a `[plugin]` label alongside built-in segments — same toggle, line assignment, and reorder controls.

---

## JSON Payload Reference

### Claude Code fields

Claude Code sends this JSON structure via stdin:

```json
{
  "cwd": "/current/working/directory",
  "session_id": "abc123...",
  "session_name": "my-session",
  "transcript_path": "/path/to/transcript.jsonl",
  "version": "2.1.90",
  "model": {
    "id": "claude-opus-4-7",
    "display_name": "Opus"
  },
  "workspace": {
    "current_dir": "/current/working/directory",
    "project_dir": "/original/project/directory",
    "added_dirs": [],
    "git_worktree": "feature-xyz"
  },
  "cost": {
    "total_cost_usd": 0.01234,
    "total_duration_ms": 45000,
    "total_api_duration_ms": 2300,
    "total_lines_added": 156,
    "total_lines_removed": 23
  },
  "context_window": {
    "total_input_tokens": 15500,
    "total_output_tokens": 1200,
    "context_window_size": 200000,
    "used_percentage": 8,
    "remaining_percentage": 92,
    "current_usage": {
      "input_tokens": 8500,
      "output_tokens": 1200,
      "cache_creation_input_tokens": 5000,
      "cache_read_input_tokens": 2000
    }
  },
  "exceeds_200k_tokens": false,
  "effort": { "level": "high" },
  "thinking": { "enabled": true },
  "rate_limits": {
    "five_hour": { "used_percentage": 23.5, "resets_at": 1738425600 },
    "seven_day": { "used_percentage": 41.2, "resets_at": 1738857600 }
  },
  "vim": { "mode": "NORMAL" },
  "agent": { "name": "security-reviewer" },
  "worktree": { "name": "my-feature", "branch": "worktree-my-feature" }
}
```

**Fields that may be absent:**
- `session_name` — only when set via `--name` or `/rename`
- `workspace.git_worktree` — only inside a linked git worktree
- `effort` — only when the model supports reasoning effort
- `vim` — only when vim mode is enabled
- `agent` — only when running with `--agent`
- `worktree` — only during `--worktree` sessions
- `rate_limits` — only for Claude Pro/Max subscribers after the first API response

**Fields that may be `null`:**
- `context_window.current_usage` — before the first API call and after `/compact`
- `context_window.used_percentage` / `context_window.remaining_percentage` — early in the session

### agy fields

agy sends a similar payload with these additional fields:

```json
{
  "product": "antigravity",
  "conversation_id": "fbce29fe-...",
  "agent_state": "working",
  "sandbox": { "enabled": false },
  "artifact_count": 3,
  "plan_tier": "Google AI Pro"
}
```

The binary detects agy by the `product: "antigravity"` field and automatically hides Claude Code-specific segments.

---

## Debug

```bash
echo '{"product":"antigravity",...}' | claude-statusline --debug
```

Prints a field presence table comparing the received payload against the Claude Code and agy schemas, plus all parsed values. Useful for diagnosing missing segments or unexpected payload shapes.

---

## Troubleshooting

**Status line not appearing**

- Verify your binary is executable and on your `$PATH`
- Check that the tool is actually piping JSON (test with `--debug`)
- Claude Code: run `claude --debug` to log exit code and stderr from statusline invocations
- Ensure workspace trust is accepted (statusline requires the same trust as hooks)

**Segments are hidden unexpectedly**

- Check `--debug` output to see if the fields are present in the payload
- Remember: zero values hide `cost`, `duration`, `lines-changed`, etc.
- `rate_limits` only appears for Claude Pro/Max after the first API call
- `agent-name` only appears when running with `--agent`
- `vim-mode` only appears when vim mode is enabled

**Colors not showing**

- Set `NO_COLOR=1` or `TERM=dumb` to disable colors intentionally
- If colors appear garbled, your terminal may not support the ANSI sequences used

**Context percentage looks wrong**

- `used_percentage` is calculated from input tokens only (not output tokens)
- It may differ slightly from `/context` output due to timing of calculation

## License

MIT

## AI use

This project was developed primarily with [Moonshot AI's Kimi Code](https://www.moonshot.cn/), with contributions from [Warp.dev GLM 5.1](https://www.warp.dev/) and [Claude Code](https://claude.ai/code) for code review.
