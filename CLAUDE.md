# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and test

```bash
go build -o claude-statusline .
go test ./...                      # full suite (golden, migration, state, install splicer…)
go test -run Golden -update .      # regenerate golden files after intentional render changes
go test -run TestSessionState .    # single test

# Smoke test
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline

# Schema/config debugging
./claude-statusline debug < testdata/payloads/agy-full.json

# Interactive config TUI (the one thing tests don't cover — verify manually)
./claude-statusline configure
```

Golden tests render `testdata/payloads/* × configs` with an empty palette (color-free) and a fixed clock (`testNow`); fixtures use `resets_at` values relative to that clock so countdowns are deterministic. TESTING.md keeps copy-pasteable payloads for manual verification, mainly of the TUI.

**Careful when smoke-testing locally:** running `./claude-statusline` with no `config.toml` but an existing `config.json` migrates the real config (renames it to `.bak`). Use an isolated home: `HOME=/tmp/fake-home ./claude-statusline`.

## Architecture

One Go module, `package main`, split by concern. The binary's subcommands (`cmd.go` dispatch): bare stdin→stdout rendering (how Claude Code invokes it — must never change behavior), `install`/`uninstall`, `configure` (tview TUI), `version`, `debug`, `help`.

### Data flow

```
stdin JSON → readInput() → parsePayload() ┐
config.toml → loadConfigWarn() ───────────┼→ buildStatusline(buildInput) → stdout
state file  → loadState()/Record() ───────┘            └→ st.Save() after printing
```

`buildStatusline` (render.go) iterates `cfg.Segments`, builds a `renderCtx` per segment (payload, override-applied palette, resolved settings, optional state, injected clock), groups results by line (1–9), then reflows (`cascade` spills across line boundaries; `group` wraps each logical line independently).

### Key subsystems and their files

- **Segments** (`segments.go`) — `segmentInfo` registry in `allSegmentInfos()`; renderers are `func(ctx renderCtx) (string, bool)`; return `("", false)` to hide. Segments auto-hide when source data is missing/zero — never add tool-type checks.
- **Settings schema** (`schema.go`) — each segment declares `[]settingSpec` (kind bool/int/enum/color, default, bounds). `settingsFor` resolves+validates, `pruneSettings` strips defaults before saving, and the TUI flyout renders rows straight from the schema. There is no parallel feature map: adding a spec to a segment is the whole job.
- **Config** (`config.go`, `migrate.go`) — TOML at `~/.config/claude-statusline/config.toml` via go-toml/v2; legacy `config.json` migrates automatically (kept as `.bak`, TOML always wins). `validateConfig` normalizes bad values with warnings (shown in `debug`/TUI; stderr only with `STATUSLINE_VERBOSE=1`). Nil-vs-empty `segments` semantics: absent = defaults + plugin auto-append; `[]` = hide all.
- **Themes** (`themes.go`, `depth.go`, `colors.go`) — themes map 15 semantic roles to `themeColor{Hex, Ansi16}` and resolve into the `palette` struct renderers consume; depth (truecolor/256/16/none) detected from env or forced by `color_depth`. The palette carries its theme+depth so `resolveColorSpec` (hex / 256 index / role / legacy name) works wherever a palette flows. **`classic` must stay byte-identical to pre-1.0 output** (`"original"` is an accepted alias for it) — locked by tests.
- **Session state** (`state.go`) — per-session sample history under `$XDG_STATE_HOME/claude-statusline/sessions/`, keyed by `session_id`; powers `cost-rate`, rate-limit projections, and the context trend via the `series` API (Rate/Delta/Span/ProjectWhen/ProjectAt). Segments opt in with `needsState`. Trend features require ≥5min of history (projections: ≥window/4) and hide on flat/falling slopes.
- **Plugins** (`plugins.go`) — executable commands from `[[plugins]]`; single-field (whole stdout) or multi-field (`key:value` lines, one exec per turn). Context via `STATUSLINE_*` env vars. Async plugins read a cache under `$XDG_STATE_HOME/claude-statusline/plugins/` and refresh via a detached hidden `plugin-refresh` subcommand.
- **Release notes** (`releasenotes.go`) — embedded `CHANGELOG.md` (`go:embed`), `release-notes` subcommand, and the post-upgrade render-path takeover (`maybeReleaseTakeover` in `runRender`). Window-anchor state at `$XDG_STATE_HOME/claude-statusline/last-version.json`. Settings in the `[release_notes]` config table.
- **Auto-update** (`update.go`) — background check for new releases, `update` segment, `update` subcommand, and detached `update-check` worker. Default mode is `notify` (segment only); `auto` cross-compiles to `brew upgrade` for Homebrew installs or atomic self-swap (`download → sha256-verify → extract → smoke-test → rename`) for manual installs. Cache at `$XDG_STATE_HOME/claude-statusline/update.json`. The render-path trigger is `maybeSpawnUpdateCheck` (one `os.ReadFile`, one detached spawn at most per `check_hours`). `!isReleaseVersion(current)` short-circuits the whole feature (dev, dirty, Go pseudo-versions), mirroring the release-notes carve-out so tests/goldens stay inert.
- **Install** (`install.go`) — settings.json wiring via parse-gated byte splicing (never reformats the user's file; unparseable JSON aborts with a manual snippet); always verifies by piping a sample payload through the configured command.
- **TUI** (`tui.go`, `flyout.go`, `tui_pickers.go`, `tui_colorpicker.go`, `tui_text.go`, `tui_help.go`, `keymap.go`) — single segment-list home screen with floating picker overlays (`tview.Pages`); all selection goes through the `visible` slice + `selectedSegment()`; every mutation goes through `mutate()` (dirty tracking); footer and help generate from the `keymap` table (footers word-wrap via `footerRows`). tview/tcell/term stay confined to these files.
- **TUI preview data** — the preview must demonstrate every feature, so it runs on synthetic inputs: `samplePayload()` (carries all payload fields), `previewState()` (an hour of rising session history → cost-rate/projections/trends render), and `gitStatusPreview` (fakes rich git status inside the TUI only — must stay nil on the render path). Demo mode (`d`) sweeps the whole payload via `demoPreviewPayload`; `v` suspends the TUI and renders real escapes to the terminal. Locked by `tui_preview_test.go`.

## Key conventions

- **The bare no-args render path is sacred** — Claude Code invokes the bare binary; subcommands must never change its behavior, and it must never print hints to stdout/stderr.
- **Versioning**: MAJOR.MINOR.REVISION — not strict SemVer. Bump REVISION for bugfixes and features; MINOR for larger milestones.
- **Colors**: always respect `NO_COLOR` and `TERM=dumb` (empty palette). Use `palette` fields or `resolveColorSpec` — never hardcode ANSI codes in renderers. Settings-driven colors must also pass through `resolveColor`, which returns "" when colors are off.
- **Section dividers** use the pattern: `// ─── Section Name ───────────────────────────────────────────────────────────`
- **`AGENTS.md` is an identical copy of this file.** When editing `CLAUDE.md`, copy it over `AGENTS.md` so they stay in sync.

## Releases

Releases are cut by pushing a `vX.Y.Z` git tag — `.github/workflows/release.yml` runs GoReleaser (`.goreleaser.yaml`) to build darwin/linux/windows binaries, inject the version via ldflags (`-X main.version=…`), and sign with cosign. The `version` *segment* displays the calling tool's version from the payload; `claude-statusline version` shows this binary's.

Before tagging, **update `CHANGELOG.md` (new `## vX.Y.Z` section at the top)** — the release-notes feature embeds it at build time, so anything you forget won't be reachable from `claude-statusline release-notes`. Keep the existing section format (`## vX.Y.Z — YYYY-MM-DD` header, `- ` bullets, newest first).

## Adding a new built-in segment

1. Write a `renderXxx(ctx renderCtx) (string, bool)` function in `segments.go`.
2. Add an entry to `allSegmentInfos()`: id, natural line (1–9), description, primary color role, optional `settings: []settingSpec` (gives it a flyout automatically), optional `needsState`.
3. Add the segment ID to `defaultConfig()` if it should be on by default (fine when it self-hides without data).
4. Update the segment table in `README.md` and the lists in `help.go`; extend `config.toml.example` if the config schema changed.
5. Add a fixture/assertion: extend a `testdata/payloads/*.json` fixture (regenerate goldens with `-update`) or add a direct renderer test.

## Homebrew vs local binary

`/opt/homebrew/bin/claude-statusline` is the Homebrew install. `./claude-statusline` in the repo root is the local build. When testing changes, build locally and use `./claude-statusline` directly — and remember the config-migration caution above.
