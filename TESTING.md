# Testing

Most behavior is covered by the automated suite — run it first:

```bash
go test ./...
```

| Area | Where |
|------|-------|
| Renderer output (payload × config) | `render_test.go` + `testdata/golden/` (regenerate: `go test -run Golden -update .`) |
| Reflow (cascade spill, group boundaries) | `reflow_test.go` |
| Config load/save, validation, presets | `config_test.go` |
| JSON→TOML migration | `migrate_test.go` |
| Session state, burn rates, projections | `state_test.go`, `state_segments_test.go` |
| Themes, depth detection, color specs | `colors_test.go` |
| Install/uninstall JSON splicing | `install_test.go` |
| Rich git status (incl. real-git integration) | `gitstatus_test.go` |
| Plugins (exec, timeout, multi-field) | `plugins_test.go` |
| Format helpers, iconsets, filter, ansiToTview, footer wrap | `helpers_test.go` |
| TUI preview data (synthetic state, sample payload, demo sweep) | `tui_preview_test.go` |

What follows is the **manual** checklist: smoke tests against the real binary and the interactive TUI, which the suite can't drive.

> **Isolation:** run manual tests with `HOME=/tmp/csl-test-home` (and optionally `XDG_STATE_HOME`/`XDG_CACHE_HOME` pointed at temp dirs) so you never migrate or overwrite your real config.

## Build

```bash
go build -o claude-statusline .
```

## Smoke tests

### Minimal payload

```bash
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline
```

Expect: directory + model + tokens + empty context bar; timing suffix on line 1.

### Full Claude Code payload

```bash
cat <<'JSON' | ./claude-statusline
{
  "session_id": "manual-test",
  "session_name": "my-project",
  "version": "1.5.0",
  "exceeds_200k_tokens": true,
  "model": {"display_name": "Claude Sonnet 4.6", "id": "claude-sonnet-4-6"},
  "output_style": {"name": "Explanatory"},
  "workspace": {"current_dir": "/Users/me/code/my-project", "project_dir": "/Users/me/code/my-project", "git_worktree": "my-project", "added_dirs": ["/tmp/lib-a"]},
  "cost": {"total_cost_usd": 1.23, "total_lines_added": 100, "total_lines_removed": 50, "total_duration_ms": 3661000, "total_api_duration_ms": 2400000},
  "context_window": {"total_input_tokens": 1234567, "total_output_tokens": 89012, "context_window_size": 200000, "used_percentage": 72.5, "current_usage": {"input_tokens": 1200000, "output_tokens": 89012, "cache_creation_input_tokens": 10000, "cache_read_input_tokens": 50000}},
  "rate_limits": {"five_hour": {"used_percentage": 45, "resets_at": 9999999999}, "seven_day": {"used_percentage": 12, "resets_at": 9999999999}},
  "agent": {"name": "ReviewBot"},
  "worktree": {"name": "my-project", "branch": "feature/test"},
  "vim": {"mode": "normal"},
  "effort": {"level": "high"}
}
JSON
```

Expect: all Claude Code segments — vim mode, session name, directory, `+1 dir`, branch, lines changed, cache %, cost, model with ⬆ badge, `✎ Explanatory`, version, duration, API efficiency, tokens, context bar with `>200k`, both rate-limit bars with countdowns. Run it a few times over five minutes (same `session_id`, rising `used_percentage`/`total_cost_usd`) and `$X/h` plus `→NN%` projections appear.

### Full Antigravity (agy) payload

```bash
cat <<'JSON' | ./claude-statusline
{
  "conversation_id": "fbce29fe-0688-4fba-8cc1-0b769834c6d7",
  "product": "antigravity",
  "version": "1.0.2",
  "model": {"display_name": "Gemini 3.5 Flash (High)"},
  "workspace": {"current_dir": "/Users/me/code/my-project", "project_dir": "file:///Users/me/code/my-project"},
  "context_window": {"total_input_tokens": 116778, "total_output_tokens": 35463, "context_window_size": 1048576, "used_percentage": 11.1},
  "agent_state": "tool_use",
  "sandbox": {"enabled": false},
  "artifact_count": 2,
  "plan_tier": "Google AI Pro"
}
JSON
```

Expect: UUID trimmed to `fbce29fe`, `file://` stripped, plan tier and agent state visible; no cost/duration/rate limits.

### Full pi payload

```bash
cat <<'JSON' | ./claude-statusline
{
  "cwd": "/Users/me/code/my-project",
  "session_id": "pi:manual-test",
  "model": {"id": "claude-sonnet-4", "display_name": "Claude Sonnet"},
  "workspace": {"current_dir": "/Users/me/code/my-project", "project_dir": "/Users/me/code/my-project"},
  "context_window": {"used_percentage": 42.5, "remaining_percentage": 57.5, "context_window_size": 200000, "current_usage": null},
  "cost": {"total_cost_usd": null},
  "version": null,
  "output_style": {"name": "default"}
}
JSON
```

Expect: directory, model, and context bar; no cost, rate limits, or burn-rate projections (pi does not expose those fields).

### Themes at each depth

```bash
P='testdata/payloads/claude-full.json'
printf 'theme = "tokyo-night"\n' > /tmp/csl-test-home/.config/claude-statusline/config.toml
COLORTERM=truecolor          ./claude-statusline < $P   # 38;2;… escapes
COLORTERM= TERM=xterm-256color TERM_PROGRAM= ./claude-statusline < $P   # 38;5;… escapes
COLORTERM= TERM=xterm TERM_PROGRAM=          ./claude-statusline < $P   # basic 16
NO_COLOR=1                   ./claude-statusline < $P   # no escapes at all
```

### Migration

```bash
mkdir -p /tmp/csl-test-home/.config/claude-statusline
cp ~/.config/claude-statusline/config.json.bak /tmp/csl-test-home/.config/claude-statusline/config.json  # or any v0.3 config
HOME=/tmp/csl-test-home ./claude-statusline < testdata/payloads/claude-full.json
```

Expect: one stderr line about migration; `config.toml` created, `config.json.bak` kept; second run silent; rendering identical to v0.3.

### Install / uninstall

```bash
HOME=/tmp/csl-test-home ./claude-statusline install --dry-run
HOME=/tmp/csl-test-home ./claude-statusline install --yes      # backup + splice + verified sample render
HOME=/tmp/csl-test-home ./claude-statusline install --yes      # "Already installed"
HOME=/tmp/csl-test-home ./claude-statusline uninstall --yes
```

Expect: only the `statusLine` key changes — every other byte of settings.json is preserved. A JSONC settings file aborts untouched with a paste-able snippet.

### Errors and edge cases

```bash
echo 'not json' | ./claude-statusline          # falls back to minimal render
./claude-statusline bogus; echo $?             # unknown command → exit 2
./claude-statusline version                    # version, commit, date, go
echo '{}' | ./claude-statusline debug          # schema table + config warnings
```

## --configure TUI checklist

Run `HOME=/tmp/csl-test-home ./claude-statusline configure` in a real terminal:

- [ ] **Toggle / line / reorder**: `space`, `1-9`, `←/→`, `⇧↑/↓` behave; preview updates live; status strip shows yellow ● once dirty
- [ ] **Save & stay**: `s` flashes `✓ Saved to …config.toml` and stays; quit after save is instant
- [ ] **Dirty quit**: make a change, `q` → Save & quit / Discard / Cancel modal
- [ ] **Reset confirm**: `r` asks before resetting
- [ ] **Theme picker**: `t` — moving the highlight restyles the preview underneath; Esc restores; Enter applies and updates the strip
- [ ] **Preset picker**: `p` — hover previews the full layout; Esc restores the snapshot; Enter applies; manual edit flips strip to `(custom)`
- [ ] **Color picker**: `C` on a segment — swatch grid (theme/ANSI/recent), hover live-previews, `d` resets, Esc cancels; in a flyout, `enter` on `ok_color` opens the same picker
- [ ] **Filter**: `/` then type — list filters; Enter keeps it; Esc clears; actions work on filtered rows
- [ ] **Width preview**: `w` cycles auto/80/60/40 — ruler appears, reflow visible; resizing the terminal in auto mode re-wraps the preview
- [ ] **Demo mode**: `d` — the whole preview animates (bars sweep 0–100%, countdowns wind down, cost and lines-changed grow); `d` again stops and restores the static sample
- [ ] **Terminal view**: `v` — TUI hides, the statusline renders with real escapes against your terminal background, enter returns; colors match what Claude Code will show
- [ ] **Footer fits**: at narrow widths the key hints wrap onto extra rows instead of trailing off the right edge (max 3 rows)
- [ ] **Everything previews**: with defaults, the preview shows `✎ Explanatory`, `+1 dir`, `$0.42/h`, `↗ ~NNm` on the context bar, and a `→NN%` projection on the 5h bar; in the `git-branch` flyout, toggling `git_status` adds `* ↑1↓2`
- [ ] **Flyout**: `o` on `context-window` — value rows render from the schema; `⇧←/→` coarse-steps numbers; clamped values stay in range; `sync_to_all` modal names source and targets
- [ ] **Stress test**: in a rate-limit flyout, toggle `stress_test` — bar AND countdown animate together
- [ ] **Help**: `?` shows the keymap-generated overlay; `r` inside opens the README; Esc backs out level by level
- [ ] **Mouse**: click toggles a segment, double-click opens its flyout

## Plugins (manual)

Point a `[[plugins]]` entry at a script that prints `key:value` lines; verify the segments appear, a `sleep 5` script with `timeout_ms = 200` hides quietly, and `debug` mode still renders.

## Performance

```bash
time (for i in $(seq 100); do ./claude-statusline < testdata/payloads/claude-full.json > /dev/null; done)
```

Expect well under 1 second for 100 renders (state recording included).
