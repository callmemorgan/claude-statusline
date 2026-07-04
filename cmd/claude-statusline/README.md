# claude-statusline

A fast, themeable statusline for [Claude Code](https://claude.ai/code), [Antigravity](https://antigravity.google/product/antigravity-cli) (`agy`), and [Pi](https://pi.dev) — your session's cost, context, and limits at a glance.

![claude-statusline rendering a live session with the Tokyo Night theme — git branch, lines changed, cost, burn rate, context-window trend, and rate-limit projections](assets/claude-tokyo-night.png)

Each tool pipes a JSON payload to the binary on every turn; it renders a compact, color-coded readout in your terminal:

- **Ten built-in themes** — classic, Catppuccin Mocha, Nord, Dracula, Gruvbox Dark, Tokyo Night, Newsprint, Paper, Solarized Light, and Monochrome — in truecolor with automatic 256/16-color fallback. Paper, Solarized Light, and Monochrome are tuned for light terminal backgrounds.
- **Burn-rate intelligence** — rate-limit projections (`→58%` at reset), cost per hour (`$1.84/h`), and time-to-compact estimates (`↗ ~35m`), computed from your session's own history.
- **One-command setup** — `claude-statusline install` wires everything up and verifies it.
- **A real configuration TUI** — live width-aware preview, theme and preset pickers, a color swatch picker, per-segment settings, search, an animated demo mode, and a render-in-your-terminal view for honest theme checking.
- **31 built-in segments + plugins** — assigned to lines 1–9; empty lines collapse, and segments hide automatically when their data is missing.

The core renderer is a single static binary (one TOML dependency); the interactive TUI uses [tview](https://github.com/rivo/tview).

---

## Install

**macOS — Homebrew (recommended):**

```bash
brew tap callmemorgan/tap
brew install claude-statusline
claude-statusline install
```

Upgrade later with `brew upgrade claude-statusline`.

**Any platform — `go install`:**

```bash
go install github.com/callmemorgan/claude-statusline@latest
claude-statusline install
```

Requires Go 1.22+. Make sure `$(go env GOPATH)/bin` is on your `$PATH`.

**Any platform — npm:**

```bash
npm i -g @morgan.rebrand/claude-statusline
claude-statusline install
```

Requires Node 14+. npm installs a small JS shim that `spawnSync`s the correct per-platform binary from `optionalDependencies`; Homebrew and manual installs remain the low-latency recommendation because each render pays the cost of one Node process spawn. If you already have another `claude-statusline` on your `$PATH` (for example a Go install at `$(go env GOPATH)/bin/claude-statusline` or `~/.local/bin/claude-statusline`), npm may not be the one executed — check with `which claude-statusline`.

> npm packages are built and published automatically with each GitHub release.

**Pi:**

```bash
pi install npm:@morgan.rebrand/claude-statusline
```

This installs the package as a Pi extension. The extension wires the renderer into Pi's footer and refreshes on session/turn/model events. No separate `claude-statusline install` step is needed inside Pi.

> The Pi extension resolves the Go binary from the same `@morgan.rebrand/claude-statusline` npm package (via its per-platform `optionalDependencies`). Because Pi owns the files, the binary's own self-swapper leaves Pi installs alone and points you at `pi update` instead. If you want to use a local build while developing, set `CLAUDE_STATUSLINE_BIN` to the absolute path of a `claude-statusline` binary before launching Pi.

Upgrade later with Pi's own package manager:

```bash
# update this package only
pi update --extension npm:@morgan.rebrand/claude-statusline
# update Pi and all packages (skips pinned specs)
pi update
# update packages only, without touching pi itself
pi update --extensions
```

**Prebuilt binaries:**

Download a binary from the [releases page](https://github.com/callmemorgan/claude-statusline/releases). Each release ships a `checksums.txt` signed with a key-based cosign bundle (`checksums.txt.bundle`); the public key is [`release/cosign.pub`](release/cosign.pub) in this repo. Verify the signature, then the asset's checksum:

```bash
cosign verify-blob \
  --key release/cosign.pub \
  --bundle checksums.txt.bundle \
  --insecure-ignore-tlog \
  checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

`--insecure-ignore-tlog` skips the online transparency-log inclusion check so verification works offline against just the embedded key — it does not weaken the signature check itself. The self-update path performs this same key-based verification in-process — no `cosign` needed at runtime.

> **macOS note:** Downloaded binaries are not notarized. If Gatekeeper blocks the binary on first run, run `xattr -d com.apple.quarantine /path/to/claude-statusline`, or use Homebrew/`go install` instead.

**Build from source:**

```bash
git clone https://github.com/callmemorgan/claude-statusline.git
cd claude-statusline
go build -o claude-statusline ./cmd/claude-statusline
```

---

## Wiring it up

```bash
claude-statusline install
```

This backs up `~/.claude/settings.json` (honoring `$CLAUDE_CONFIG_DIR` when set), splices in the `statusLine` key **without reformatting the rest of the file**, and verifies the wiring by rendering a sample payload through the exact command Claude Code will run. Flags: `--dry-run` to preview, `--force` to overwrite an existing entry, `--target agy` for Antigravity, `--settings-path` for non-standard locations. `claude-statusline uninstall` removes the wiring (`--restore` swaps the backup back).

Optional Claude Code `statusLine` fields can be set at install time and are written only when their flag is passed:

- `--refresh-interval N` — re-run the command every N seconds (N >= 1).
- `--hide-vim-mode-indicator` — suppress Claude Code's built-in `-- INSERT --` indicator.
- `--statusline-padding N` — add N extra cells of horizontal padding (N >= 0).

For example:

```bash
claude-statusline install --refresh-interval 30 --hide-vim-mode-indicator --statusline-padding 2
```

### Subagent status line

Claude Code can also render a per-task status line for subagents and long-running tools via the `subagentStatusLine` hook. To wire it up alongside the main status line:

```bash
claude-statusline install --subagent-statusline
```

This adds a `subagentStatusLine` key to `settings.json` that invokes `claude-statusline subagent-statusline`. The hook receives JSON with base payload fields plus `columns` and a `tasks` array; the command emits one `{"id":"<task id>","content":"<ANSI string>"}` JSON line per task. Empty `id`s keep the default rendering, and empty `content` hides the row. Pass `--dry-run` to preview the change.

<details>
<summary>Manual wiring (fallback)</summary>

Claude Code — add to `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "claude-statusline"
  }
}
```

Antigravity — add to your `agy` config:

```json
{
  "statusline": "claude-statusline"
}
```

If the binary isn't on your `$PATH`, use the full path instead.
</details>

The binary auto-detects which tool is calling it via the `product` field in the payload and hides segments that don't apply — rate limits under Antigravity, say, or plan tier under Claude Code.

### Pi configuration

After `pi install npm:@morgan.rebrand/claude-statusline`, the extension is active immediately. It uses the same config file as the other tools:

```bash
claude-statusline configure   # edit themes, segments, and settings
```

Changes are picked up on the next Pi render. The extension builds a Claude-compatible payload from Pi's live context — session id, working directory, model, and context-window usage — so a focused set of segments shows up under Pi: `session-name`, `directory`, `model`, the `context-window` bar, `git-branch`, `git-stash`, and the `update` notice. The Claude- and Antigravity-specific segments don't render, because Pi doesn't send their data. See the [harness support](#harness-support) matrix for the exact per-segment breakdown.

To remove the statusline from Pi:

```bash
pi remove npm:@morgan.rebrand/claude-statusline
```

---

## What it looks like

**Claude Code (default `classic` theme, after an hour of session history):**

![Claude Code statusline with the classic theme: session name, directory, git branch, lines changed, cache percentage, cost, model with effort badge, output style, duration, cost burn rate, API efficiency, tokens, context-window bar with trend, and both rate-limit bars with projections](assets/claude-classic.png)

**Antigravity (`agy`) — default config:**

![Antigravity statusline: conversation ID, agent state, directory, artifact count, plan tier, model, version, tokens, and context-window bar](assets/agy-classic.png)

**Pi (Tokyo Night):**

![Pi statusline with the Tokyo Night theme: session name, project directory, git branch, cache percentage, model, output style, duration, tokens, and context-window bar](assets/pi-tokyo-night.png)

Segments that receive no data from the active tool hide themselves automatically — no configuration needed. The burn rate (`$1.44/h`), context trend (`↗ ~13m`), and rate-limit projections (`→79%`, `→125%`) above are computed from the session's own history — see [Burn rates, projections, and trends](#burn-rates-projections-and-trends).

> Screenshots are generated from real renderer output by `scripts/screenshots.py`.

---

## Segments

| Segment | Default line | Source | Description |
|---------|-------------|--------|-------------|
| `vim-mode` | 1 | Claude Code | Vim mode indicator, e.g. `[normal]` or `[INSERT]` |
| `sandbox` | 1 | Antigravity | `[SANDBOX]` indicator when sandbox mode is enabled |
| `session-name` | 1 | all three | Session name (Claude Code) or conversation ID (Antigravity / Pi). UUIDs are truncated to 8 chars |
| `agent-state` | 1 | Antigravity | Agent working status, e.g. `[working]` — green when active |
| `agent-name` | 1 | Claude Code | Agent name when running with `--agent` |
| `directory` | 1 | all three | Current / project directory. Shows `project→subdir` when inside a project subdirectory |
| `added-dirs` | 1 | Claude Code | Count of extra directories added with `/add-dir`, e.g. `+2 dirs` |
| `git-branch` | 1 | all three | Git branch and worktree name. Optional rich status (settings): dirty marker, ahead/behind counts, worktree path, original branch, e.g. `main* ↑1↓2` |
| `git-stash` | 1 | all three | Git stash count (`⚑N`), hidden when there are no stashes. Off by default (runs a cached, bounded `git`); add it in `configure` |
| `repo` | 1 | Claude Code | Repository `owner/name` from the payload. Optional host prefix |
| `pr` | 1 | Claude Code | Pull request `#N` with optional review state, or full URL when `show_url` is set |
| `artifact-count` | 1 | Antigravity | Number of generated artifacts |
| `lines-changed` | 1 | Claude Code | Session cumulative lines added/removed, e.g. `+128/-45` |
| `cache-percent` | 1 | Claude Code | Cache read percentage from `context_window.current_usage` |
| `plan-tier` | 1 | Antigravity | Subscription plan tier |
| `cost` | 1 | Claude Code | Estimated session cost in USD, e.g. `$1.23` |
| `model` | 2 | all three | Model name with effort badge (⬇ → ⬆ ⬆⬆ ⬆⬆⬆) |
| `output-style` | 2 | Claude Code | Output style, e.g. `✎ Explanatory` — hidden when default |
| `thinking` | 2 | Claude Code | Thinking indicator, `🗘 thinking` or `[thinking]` when reasoning is enabled |
| `email` | 2 | Antigravity | Account email, user part only (`morgan@…`) — **off by default** |
| `version` | 2 | Claude Code, Antigravity | Tool version |
| `update` | 1 | all three | `⬆ vX.Y.Z` when behind, hides when current. Self-hides on dev builds. |
| `duration` | 2 | Claude Code | Elapsed session wall-clock time in `HH:MM:SS` |
| `cost-rate` | 2 | Claude Code | Cost burn rate over recent history, e.g. `$1.84/h` |
| `api-efficiency` | 2 | Claude Code | Percentage of time spent in API calls vs. total elapsed |
| `tokens` | 2 | Claude Code, Antigravity | Input/output token counts in compact notation (`↑1.2M ↓89k`) |
| `prompt-id` | 2 | Claude Code | Prompt ID, UUIDs truncated to 8 chars |
| `context-window` | 3 | all three | Usage bar with color-coded %, growth trend arrow, and time-to-compact estimate (`↗ ~35m`) |
| `rate-limit-5h` | 3 | Claude Code | 5-hour rate limit bar with countdown and burn-rate projection (`→58%`) (Pro/Max only) |
| `rate-limit-7d` | 3 | Claude Code | 7-day rate limit bar with countdown and burn-rate projection (Pro/Max only) |

### Harness support

No segment is gated by tool name — each one renders when the active harness sends its data and self-hides otherwise, so the grid below is really "which tool provides the field." (`git-branch` and `git-stash` fall back to live `git`, so they work wherever you have a repo; plugins run under any harness.)

| Segment | Claude Code | Antigravity | Pi |
|---------|:---:|:---:|:---:|
| `vim-mode` | ✓ | ✗ | ✗ |
| `sandbox` | ✗ | ✓ | ✗ |
| `session-name` | ✓ | ✓ | ✓ |
| `agent-state` | ✗ | ✓ | ✗ |
| `agent-name` | ✓ | ✗ | ✗ |
| `directory` | ✓ | ✓ | ✓ |
| `added-dirs` | ✓ | ✗ | ✗ |
| `git-branch` | ✓ | ✓ | ✓ |
| `git-stash` | ✓ | ✓ | ✓ |
| `repo` | ✓ | ✗ | ✗ |
| `pr` | ✓ | ✗ | ✗ |
| `artifact-count` | ✗ | ✓ | ✗ |
| `lines-changed` | ✓ | ✗ | ✗ |
| `cache-percent` | ✓ | ✗ | ✗ |
| `plan-tier` | ✗ | ✓ | ✗ |
| `cost` | ✓ | ✗ | ✗ |
| `model` | ✓ | ✓ | ✓ |
| `output-style` | ✓ | ✗ | ✗ |
| `thinking` | ✓ | ✗ | ✗ |
| `email` | ✗ | ✓ | ✗ |
| `version` | ✓ | ✓ | ✗ |
| `update` | ✓ | ✓ | ✓ |
| `duration` | ✓ | ✗ | ✗ |
| `cost-rate` | ✓ | ✗ | ✗ |
| `api-efficiency` | ✓ | ✗ | ✗ |
| `tokens` | ✓ | ✓ | ✗ |
| `prompt-id` | ✓ | ✗ | ✗ |
| `context-window` | ✓ | ✓ | ✓ |
| `rate-limit-5h` | ✓ | ✗ | ✗ |
| `rate-limit-7d` | ✓ | ✗ | ✗ |

✓ renders · ✗ no data, stays hidden.

### Burn rates, projections, and trends

`cost-rate`, the rate-limit `→58%` projections, and the context `↗ ~35m` trend are computed from a small per-session history file the renderer maintains at `~/.local/state/claude-statusline/sessions/` (`$XDG_STATE_HOME` respected). They appear after ~5 minutes of session history, never extrapolate a short burst across a long window, and stay hidden when usage is flat or falling. Disable or tune via the `[state]` config table.

---

## Themes

```toml
theme = "tokyo-night"   # classic | catppuccin-mocha | nord | dracula | gruvbox-dark |
                        # tokyo-night | newsprint | paper | solarized-light | monochrome
```

Themes map fifteen semantic roles (model, dir, git, ok/warn/crit, accent, sep, …) to colors. On truecolor terminals you get the real hex palette; 256-color and 16-color terminals get automatic nearest-match fallbacks. `classic` (the default — `original` is an accepted alias) reproduces the pre-1.0 ANSI look exactly, so existing installs keep their colors unless they opt into a theme. The in-TUI preview approximates colors; press `v` in the configurator to render against your real terminal.

<details>
<summary><strong>Theme gallery</strong> — the same session in every theme</summary>
<br>

**classic**

![classic theme](assets/claude-classic.png)

**catppuccin-mocha**

![catppuccin-mocha theme](assets/claude-catppuccin-mocha.png)

**nord**

![nord theme](assets/claude-nord.png)

**dracula**

![dracula theme](assets/claude-dracula.png)

**gruvbox-dark**

![gruvbox-dark theme](assets/claude-gruvbox-dark.png)

**tokyo-night**

![tokyo-night theme](assets/claude-tokyo-night.png)

**newsprint**

![newsprint theme](assets/claude-newsprint.png)

**paper**

![paper theme](assets/claude-paper.png)

**solarized-light**

![solarized-light theme](assets/claude-solarized-light.png)

**monochrome**

![monochrome theme](assets/claude-monochrome.png)

</details>

- **Color depth** is auto-detected from `COLORTERM`/`TERM`/terminal program; override with `color_depth = "truecolor" | "256" | "16" | "none"`. `NO_COLOR=1` always wins.
- **Per-role overrides** layer on top of any theme:

```toml
[theme_colors]
git = "#a3be8c"   # hex
cost = "yellow"   # 16-color name
dim = "245"       # xterm-256 index
```

- **Per-segment colors** (`[colors]` or the TUI) accept the same grammar plus theme role names: `model = "accent"`.

---

## Configuration

```bash
claude-statusline configure
```

An interactive TUI: segment list (left), description panel (right), a **live preview at your real terminal width**, and a status strip showing the active theme/preset and unsaved-changes marker. The preview is fed synthetic session history and git status, so every feature — burn rates, projections, trends, rich git — is visible while you configure it. Nothing touches disk until you save.

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate segments |
| `Space` | Toggle segment on/off |
| `1`–`9` | Move segment to that line (enables it if disabled) |
| `c` | Cycle segment color |
| `C` | Open the color picker — theme roles, ANSI names, recents; hover live-previews |
| `←` / `→` | Reorder segment within its current line |
| `Shift+↑` / `Shift+↓` | Swap all segments on the current line with the adjacent line |
| `o` | Open per-segment settings (bar width, iconsets, thresholds, projections, git status…) |
| `t` | Theme picker with live preview |
| `p` | Preset picker with live preview |
| `/` | Filter the segment list |
| `w` | Cycle preview width (auto → 80 → 60 → 40) to check the layout |
| `d` | Demo mode — animate the whole preview: bars sweep, countdowns tick, cost grows |
| `v` | Hide the TUI and render directly in your terminal — check the theme against your real colors and background |
| `r` | Reset to defaults (asks first) |
| `s` | Save and keep editing (`✓ Saved` flash) |
| `q` / `Esc` | Quit — asks if there are unsaved changes |
| `h` / `?` | Help overlay (`r` inside it opens the full README) |

In the flyout (`o`): `space`/`enter` toggles or cycles, `←`/`→` adjusts numbers (`Shift` for coarse steps), and `enter` on a color row opens the swatch picker.

### Presets

Eight named layouts, applied from the TUI (`p`) or used as your config baseline:

`classic` · `minimal` · `zen` · `cost-tracker` · `git-focus` · `vim-coder` · `quota-watch` · `full-dashboard`

```toml
preset = "cost-tracker"   # used when `segments` is absent; your lines/settings/theme win over it
```

### Manual config

Config lives at `~/.config/claude-statusline/config.toml` — a pre-1.0 `config.json` is **migrated automatically** on first run (the original is kept as `config.json.bak`). An annotated example is at [`configs/config.toml.example`](configs/config.toml.example).

```toml
theme = "nord"
reflow = "group"
segments = ["session-name", "directory", "git-branch", "cost", "model", "context-window", "rate-limit-5h"]

[style]
separator = "chevron"   # bar | dot | slash | chevron | powerline | space | custom
padding = 1

[lines]
cost = 2

[colors]
model = "#cba6f7"       # names, hex, 256 indexes, or theme roles

[settings.context-window]
bar_width = 30
iconset = "smooth"
warn_at = 70
crit_at = 90

[settings.git-branch]
git_status = true       # dirty marker + ahead/behind (cached git exec)

[state]
enabled = true          # session history for burn rates / projections
retention_hours = 48
```

- `segments` — which segments to show and in what order. Omit for defaults (plugins auto-append); `[]` hides everything.
- `[lines]` — override which line a segment renders on (1–9).
- `[colors]` — per-segment color: names (`red`…`bright-white`), `#rrggbb` hex, `0`–`255` xterm indexes, or theme roles (`accent`, `dim`, …).
- `[settings.<segment>]` — per-segment settings. Press `o` on a segment in the TUI to discover its settings interactively; highlights:
  - bars (`context-window`, `rate-limit-*`): `show_bar`, `bar_width` (5–50), `iconset` (`default`, `blocks`, `dots`, `ascii`, `minimal`, `smooth`, `braille`, `braille-fine`, `shade`, `line`, `slim`, `vertical`), `warn_at`/`crit_at`, `ok_color`/`warn_color`/`crit_color`, `show_countdown`, `show_warning`
  - projections (`rate-limit-*`): `show_projection`, `projection_window_min`
  - context trend: `show_trend`, `compact_at`
  - `cost-rate`: `window_min`
  - `git-branch`: `git_status` (off by default), `git_status_ttl_sec`, `git_timeout_ms`, `show_worktree_path`, `show_original_branch`
  - `git-stash`: `git_stash_ttl_sec`, `git_timeout_ms`
  - `pr`: `show_url`, `show_review_state`
  - `repo`: `show_host`
  - `thinking`: `icon` (`emoji` or `text`)
- `reflow` — line wrapping on narrow terminals is **opt-in**. `"off"` (default: no wrapping; a too-wide line is left for the terminal to soft-wrap), `"cascade"` (segments spill greedily across line boundaries), or `"group"` (each logical line wraps independently).
- Invalid values never break rendering — they're normalized with warnings, visible in `debug` output and the TUI.

---

## Plugins

Add your own segments with any executable — a shell script, Python script, or binary. Each plugin runs on every turn, and its stdout becomes the segment content. Empty output hides the segment automatically.

### Single-field plugin

One segment, whole stdout is the value:

```toml
[[plugins]]
id = "memory"
command = "~/.config/claude-statusline/plugins/memory.sh"
line = 1
desc = "RAM usage"
timeout_ms = 200
```

### Multi-field plugin

One command, multiple independent segments. The command runs **once** per turn; each field reads its value from a `key:value` line in stdout:

```toml
[[plugins]]
command = "~/.config/claude-statusline/plugins/memory.sh"
timeout_ms = 200

  [[plugins.fields]]
  id = "mem-used"
  line = 1
  desc = "RAM used"

  [[plugins.fields]]
  id = "swap-used"
  line = 1
  desc = "Swap used"
```

Each field ID is an independent segment — independently togglable, positionable, and reorderable in the TUI.

- `id` — segment identifier (used in `segments` list and TUI)
- `command` — path to the executable; `~` is expanded
- `line` — default line (1–9); overridable via TUI or `[lines]`
- `desc` — shown in the TUI description panel
- `timeout_ms` — kill the process after this many ms (default: 200 sync, 10000 async); hidden if it times out or exits non-zero
- `async` — opt into stale-while-revalidate caching (default: `false`)
- `refresh_ms` — how stale the cache may get before a background refresh (default: 5000; minimum: 500); ignored when `async = false`
- `fields` — multi-field mode; output must use `key:value` lines; mutually exclusive with top-level `id`

Plugin IDs are **auto-appended** to `segments` if not already present, so they appear immediately without editing the list manually.

### Async plugins

Plugins that talk to slow external services (`kubectl`, `gh api`, a weather fetch, …) can opt into **stale-while-revalidate** mode so they never delay a render:

```toml
[[plugins]]
id = "k8s-context"
command = "~/.config/claude-statusline/plugins/k8s.sh"
async = true
refresh_ms = 10000   # consider cache stale after 10s
timeout_ms = 8000    # kill the background run after 8s
```

When `async = true`, the renderer immediately shows the last cached value and spawns a detached background refresher only when the cache is older than `refresh_ms`. The next render picks up the fresh output. Trade-offs:

- The value is **one refresh cycle behind** the live state.
- The cache is **shared across sessions** (keyed by `command`), which is intentional for environment-level data.
- The segment hides until the first refresh completes and writes a cache file.

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

Plugin segments appear in `configure` with a `[plugin]` label alongside built-in segments — same toggle, line assignment, and reorder controls.

---

## JSON Payload Reference

### Claude Code fields

Claude Code sends this JSON structure via stdin:

```json
{
  "cwd": "/current/working/directory",
  "session_id": "abc123...",
  "session_name": "my-session",
  "prompt_id": "550e8400-e29b-41d4-a716-446655440000",
  "transcript_path": "/path/to/transcript.jsonl",
  "version": "2.1.90",
  "model": {
    "id": "claude-opus-4-7",
    "display_name": "Opus"
  },
  "output_style": { "name": "Explanatory" },
  "workspace": {
    "current_dir": "/current/working/directory",
    "project_dir": "/original/project/directory",
    "added_dirs": [],
    "git_worktree": "feature-xyz",
    "repo": {
      "host": "github.com",
      "owner": "anthropics",
      "name": "claude-code"
    }
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
  "pr": {
    "number": 1234,
    "url": "https://github.com/anthropics/claude-code/pull/1234",
    "review_state": "pending"
  },
  "worktree": {
    "name": "my-feature",
    "path": "/path/to/.claude/worktrees/my-feature",
    "branch": "worktree-my-feature",
    "original_cwd": "/path/to/project",
    "original_branch": "main"
  }
}
```

**Fields that may be absent:**
- `session_name` — only when set via `--name` or `/rename`
- `prompt_id` — only after the first user input
- `workspace.git_worktree` — only inside a linked git worktree
- `workspace.repo` — only inside a git repository with an origin remote configured
- `effort` — only when the model supports reasoning effort
- `thinking` — only when the model supports extended thinking
- `output_style` — only when an output style is set
- `vim` — only when vim mode is enabled
- `agent` — only when running with `--agent`
- `pr` — only while an open PR is found for the current branch
- `worktree` — only during `--worktree` sessions
- `rate_limits` — only for Claude Pro/Max subscribers after the first API response

**Fields that may be `null`:**
- `context_window.current_usage` — before the first API call and after `/compact`
- `context_window.used_percentage` / `context_window.remaining_percentage` — early in the session

### Antigravity fields

Antigravity sends a similar payload with these additional fields:

```json
{
  "product": "antigravity",
  "conversation_id": "fbce29fe-...",
  "agent_state": "working",
  "sandbox": { "enabled": false },
  "artifact_count": 3,
  "plan_tier": "Google AI Pro",
  "email": "user@example.com"
}
```

The binary detects Antigravity by the `product: "antigravity"` field and automatically hides Claude Code-specific segments.

---

## Debug

```bash
echo '{"product":"antigravity",...}' | claude-statusline debug
```

Prints a field presence table comparing the received payload against the Claude Code and Antigravity schemas, all parsed values, and any config validation warnings. Useful for diagnosing missing segments or unexpected payload shapes. Set `STATUSLINE_VERBOSE=1` to also print config warnings to stderr during normal renders.

---

## Release notes

```bash
claude-statusline release-notes                 # current version
claude-statusline release-notes v1.0.2          # any past version
claude-statusline release-notes v1.0.0..v1.2.0  # cross-version summary
claude-statusline release-notes --all           # every version, newest first
claude-statusline --release-notes               # flag form also works
```

Prints notes sourced from the embedded `CHANGELOG.md` (no network). Each version's section is the same data that ships with the binary, so the on-disk content can't get out of sync with what you installed. Bullets are sorted by importance: CHANGELOG.md entries can use a leading `[N]` marker (ordinary items 0–5, critical/pinned items can use e.g. 99999), and bullets without a marker default to importance 0.

### What's new announcement

The first time the binary renders under a new version, the statusline briefly replaces itself with a short release-notes announcement, then goes back to normal on the next refresh. If you jump across several versions (e.g. v1.0.0 → v1.5.0), the announcement surfaces the highest-importance bullets from the whole upgrade span, sorted so you see the biggest changes first. The window is 25 seconds by default and is configurable:

```toml
[release_notes]
announce = true
duration_seconds = 25   # 0 disables the takeover entirely
max_lines = 10          # 0 or "status-line" keeps the statusline's own height
```

`announce = false` and `duration_seconds = 0` both fully disable it. `max_lines` controls how tall the announcement can grow: the default is 10, so most minor-version updates fit entirely; set it to `0` or `"status-line"` to keep the announcement at the same height as your normal statusline. Source builds (`version = "dev"`) never announce and never write the version state file. An unwritable state directory degrades silently — your render is unaffected, and nothing is printed to stderr.

---

## Updates

The binary checks GitHub for new releases in the background. Default is `notify` — a small `⬆ vX.Y.Z` segment appears on line 1 when you're behind, and the next render (or `claude-statusline update`) installs it.

```bash
claude-statusline update          # check + install
claude-statusline update --check  # check + report only, never install
claude-statusline update verify   # verify the latest release's signature, then exit (installs nothing)
```

`update verify` runs the same in-process signature check the self-swap path uses — it fetches the latest release's `checksums.txt` and its cosign bundle, checks them against the embedded public key, and prints the key fingerprint. It installs nothing and fails closed (non-zero exit) on any error.

**The render path never touches the network.** The check is a detached worker spawned *after* the print loop, identical in shape to the async plugin refresh. It writes its result to a tiny cache file (`update.json` under the state dir) and the next render reads it. One `os.ReadFile` on the happy path, one detached `exec.Command` spawn at most once per check interval.

The notify segment has two forms:

- **Compact** (`⬆ v1.2.0`) the rest of the day.
- **Expanded** for ~5 minutes after each check: `⬆ v1.2.0 · run: claude-statusline update · disable: [update] in config.toml`. The disclosure window is derived from the cache's `checked_at`, so no extra state is needed.

After a self-update lands, the same segment shows `✓ updated to vX.Y.Z` for ~5 minutes (read from `update-result.json`, written by the install path). It self-hides once the window passes or when the running binary's version doesn't match the recorded target, so a no-op `brew upgrade` or a stale record never confirms.

Modes:

```toml
[update]
mode = "notify"   # default: show segment only
# mode = "auto"   # also upgrade in the background (manual + Homebrew; npm excluded)
# mode = "off"    # no checks, no segment, no network ever
check_hours = 24  # 1..168, default 24
```

`auto` mode **crosses MAJOR versions** — it's a one-way door that downloads, verifies the cosign signature on `checksums.txt` against the embedded public key, sha256-verifies the asset against it, smoke-tests the staged binary, and atomically swaps the on-disk exe. Homebrew installs run `brew upgrade claude-statusline` instead of touching the binary directly (package-manager bookkeeping fights self-swap); npm installs are ignored entirely by `auto` because npm owns the file (update with `npm update -g @morgan.rebrand/claude-statusline` instead). Pi installs are also ignored by the binary's self-swapper because Pi's package manager owns the files — update them with `pi update --extension npm:@morgan.rebrand/claude-statusline` or `pi update`. The tap's git checkout is refreshed first (a targeted `git pull`, not a global `brew update`) so brew sees the newly-published formula despite `HOMEBREW_NO_AUTO_UPDATE`. Failures are silent on the next interval retries; an invalid signature, a checksum mismatch, or a failed smoke-test leaves the old binary in place (it fails closed).

`mode = "off"` is the right choice for air-gapped or centrally-managed deployments — it produces zero spawns and zero reads beyond the config.

Source builds (`version = "dev"`) short-circuit the whole feature: no check, no segment, no subcommand action beyond a hint to run `go install …@latest`. The carve-out mirrors the release-notes feature and keeps tests/goldens inert.

---

## Troubleshooting

**Status line not appearing**

- Run `claude-statusline install` again — it reports "Already installed" or exactly what's wrong
- Check that the tool is actually piping JSON (test with `debug`)
- Claude Code: run `claude --debug` to log exit code and stderr from statusline invocations
- Ensure workspace trust is accepted (statusline requires the same trust as hooks)

**Segments are hidden unexpectedly**

- Check `debug` output to see if the fields are present in the payload
- Remember: zero values hide `cost`, `duration`, `lines-changed`, `tokens`, etc.
- `rate_limits` only appears for Claude Pro/Max after the first API call
- Burn rates, projections, and trends need ~5 minutes of session history
- `agent-name` only appears when running with `--agent`; `vim-mode` only with vim mode on

**Colors not showing / look wrong**

- `NO_COLOR=1` or `TERM=dumb` disables colors intentionally
- Claude Code may strip `COLORTERM` from the statusline environment; force themes with `color_depth = "truecolor"` in config.toml
- 256/16-color terminals get quantized theme colors — that's the fallback working as intended
- The TUI preview approximates colors; press `v` in `claude-statusline configure` to render against your real terminal
- Want the pre-1.0 colors? That's the default theme, `classic` (alias: `original`) — active whenever no `theme` is set

**Config seems ignored**

- The config moved to `~/.config/claude-statusline/config.toml` in 1.0 (your old `config.json` was migrated automatically and kept as `config.json.bak`)
- Mixing binary versions? A pre-1.0 binary reads `config.json`, 1.0+ reads `config.toml` — running the 1.0 binary once migrates the JSON away, and an older still-installed binary (e.g. Homebrew) falls back to defaults. Copy `config.json.bak` back to `config.json` to keep the old binary working until you upgrade; the 1.0 binary ignores it once `config.toml` exists
- Run `claude-statusline debug < payload.json` to see config warnings (unknown keys, bad values)

**Context percentage looks wrong**

- `used_percentage` is calculated from input tokens only (not output tokens)
- It may differ slightly from `/context` output due to timing of calculation

## License

MIT

## AI use

This project was developed primarily in the [Warp.dev](https://www.warp.dev/) terminal with [Moonshot AI's Kimi Code](https://www.moonshot.ai/) and [Zhipu AI's GLM 5.2](https://www.z.ai/), with [Claude Code](https://claude.ai/code) for code review. The 1.0.0 overhaul was built with [Claude Code](https://claude.ai/code).
