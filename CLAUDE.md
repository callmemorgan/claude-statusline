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

`buildStatusline` (render.go) iterates `cfg.Segments`, builds a `renderCtx` per segment (payload, override-applied palette, resolved settings, optional state, injected clock), groups results by line (1–9), then reflows. Wrapping is **opt-in**: the default (`off`/`""`, via `buildStatuslineNoWrap` = cascade with no column budget) emits each logical line as-is and lets the terminal soft-wrap; `cascade` spills segments across line boundaries; `group` wraps each logical line independently.

### Key subsystems and their files

- **Segments** (`segments.go`) — `segmentInfo` registry in `allSegmentInfos()`; renderers are `func(ctx renderCtx) (string, bool)`; return `("", false)` to hide. Segments auto-hide when source data is missing/zero — never add tool-type checks.
- **Settings schema** (`schema.go`) — each segment declares `[]settingSpec` (kind bool/int/enum/color, default, bounds). `settingsFor` resolves+validates, `pruneSettings` strips defaults before saving, and the TUI flyout renders rows straight from the schema. There is no parallel feature map: adding a spec to a segment is the whole job.
- **Config** (`config.go`, `migrate.go`) — TOML at `~/.config/claude-statusline/config.toml` via go-toml/v2; legacy `config.json` migrates automatically (kept as `.bak`, TOML always wins). `validateConfig` normalizes bad values with warnings (shown in `debug`/TUI; stderr only with `STATUSLINE_VERBOSE=1`). Nil-vs-empty `segments` semantics: absent = defaults + plugin auto-append; `[]` = hide all.
- **Themes** (`themes.go`, `depth.go`, `colors.go`) — themes map 15 semantic roles to `themeColor{Hex, Ansi16}` and resolve into the `palette` struct renderers consume; depth (truecolor/256/16/none) detected from env or forced by `color_depth`. The palette carries its theme+depth so `resolveColorSpec` (hex / 256 index / role / legacy name) works wherever a palette flows. **`classic` must stay byte-identical to pre-1.0 output** (`"original"` is an accepted alias for it) — locked by tests.
- **Session state** (`state.go`) — per-session sample history under `$XDG_STATE_HOME/claude-statusline/sessions/`, keyed by `session_id`; powers `cost-rate`, rate-limit projections, and the context trend via the `series` API (Rate/Delta/Span/ProjectWhen/ProjectAt). Segments opt in with `needsState`. Trend features require ≥5min of history (projections: ≥window/4) and hide on flat/falling slopes.
- **Plugins** (`plugins.go`) — executable commands from `[[plugins]]`; single-field (whole stdout) or multi-field (`key:value` lines, one exec per turn). Context via `STATUSLINE_*` env vars. Async plugins read a cache under `$XDG_STATE_HOME/claude-statusline/plugins/` and refresh via a detached hidden `plugin-refresh` subcommand.
- **Release notes** (`releasenotes.go`) — embedded `CHANGELOG.md` (`go:embed`) with optional per-bullet `[N]` importance markers, `release-notes` subcommand (including `vX.Y.Z..vA.B.C` cross-version summaries), and the post-upgrade render-path takeover (`maybeReleaseTakeover` in `runRender`). The takeover sorts bullets by importance, surfaces the highest-importance bullets across the whole upgrade span, and expands up to `[release_notes].max_lines` (default 10; `0` or `"status-line"` keeps the statusline's own height). Window-anchor state at `$XDG_STATE_HOME/claude-statusline/last-version.json`. Settings in the `[release_notes]` config table.
- **Auto-update** (`update.go`) — background check for new releases, `update` segment, `update`/`update verify` subcommands, and detached `update-check` worker. Default mode is `notify` (segment only); `auto` cross-compiles to `brew upgrade --cask` for Homebrew installs or atomic self-swap (`download → verify-sig → sha256-verify → extract → smoke-test → rename`) for manual installs. npm installs are detected (`kindNpm`) and excluded from `auto` self-swap so the binary never fights the package manager; update npm installs with `npm update -g @morgan.rebrand/claude-statusline`. pi installs use the same npm package and are covered by the same rule; update them with `pi update --extension npm:@morgan.rebrand/claude-statusline` or `pi update`. Cache at `$XDG_STATE_HOME/claude-statusline/update.json`. The render-path trigger is `maybeSpawnUpdateCheck` (one `os.ReadFile`, one detached spawn at most per `check_hours`). `!isReleaseVersion(current)` short-circuits the whole feature (dev, dirty, Go pseudo-versions), mirroring the release-notes carve-out so tests/goldens stay inert.
  - **Signature verification** (`verifyChecksumsSigReal`) authenticates `checksums.txt` against the embedded `cosign.pub` before trusting any digest (fail-closed, pure `crypto/ecdsa`, no runtime cosign). It reads the signature from **either** `messageSignature.signature` (newer sigstore bundle) **or** top-level `base64Signature` (legacy cosign bundle) — the bytes are identical; which field cosign emits depends on the resolved cosign version. To keep published bundles stable and readable by already-installed binaries, `scripts/sign-checksums.sh` (the GoReleaser `signs` cmd) signs then **normalizes** the bundle to the lean `{"messageSignature":{"signature":…}}` shape.
  - **Update-outcome confirmation**: the install path writes `update-result.json` (`from`/`to`/`method`/`verified`/`at`); `renderUpdate` shows `✓ updated to vX` for ~5 min when the running version matches `to` (checked before the mode==off guard, so a manual `update` still confirms). `update verify` runs the same signature check on demand and prints `cosignKeyFingerprint()`.
  - **Homebrew tap refresh**: `brew upgrade --cask` runs with `HOMEBREW_NO_AUTO_UPDATE=1`, so `refreshBrewTap` (seam `refreshBrewTapFn`) first `git pull`s our tap's checkout (`brew --repository callmemorgan/tap`) before both brew call sites — otherwise a stale local tap makes brew report "already installed" against an old cask. Best-effort: any failure falls through to the upgrade against whatever's cached.
- **Install** (`install.go`) — settings.json wiring via parse-gated byte splicing (never reformats the user's file; unparseable JSON aborts with a manual snippet); always verifies by piping a sample payload through the configured command.
- **TUI** (`tui.go`, `flyout.go`, `tui_pickers.go`, `tui_colorpicker.go`, `tui_text.go`, `tui_help.go`, `keymap.go`) — single segment-list home screen with floating picker overlays (`tview.Pages`); all selection goes through the `visible` slice + `selectedSegment()`; every mutation goes through `mutate()` (dirty tracking); footer and help generate from the `keymap` table (footers word-wrap via `footerRows`). tview/tcell/term stay confined to these files.
- **TUI preview data** — the preview must demonstrate every feature, so it runs on synthetic inputs: `samplePayload()` (carries all payload fields), `previewState()` (an hour of rising session history → cost-rate/projections/trends render), and `gitStatusPreview` (fakes rich git status inside the TUI only — must stay nil on the render path). Demo mode (`d`) sweeps the whole payload via `demoPreviewPayload`; `v` suspends the TUI and renders real escapes to the terminal. Locked by `tui_preview_test.go`.

## Key conventions

- **The bare no-args render path is sacred** — Claude Code invokes the bare binary; subcommands must never change its behavior, and it must never print hints to stdout/stderr.
- **Versioning**: MAJOR.MINOR.REVISION — not strict SemVer. Bump REVISION for bugfixes and features; MINOR for larger milestones.
- **Colors**: always respect `NO_COLOR` and `TERM=dumb` (empty palette). Use `palette` fields or `resolveColorSpec` — never hardcode ANSI codes in renderers. Settings-driven colors must also pass through `resolveColor`, which returns "" when colors are off.
- **Section dividers** use the pattern: `// ─── Section Name ───────────────────────────────────────────────────────────`
- **Commit messages** use [Conventional Commits](https://www.conventionalcommits.org/) with a scope when helpful:
  - `feat(segment): add git-stash segment`
  - `fix(update): refresh the Homebrew tap before brew upgrade`
  - `docs: changelog for v1.3.2`
  - `ci: resolve release-workflow warnings`
  - `refactor: tryAcquireLock takes a staleness duration`
  - `chore: ignore .worktrees/`
  
  Use lowercase after the colon, imperative mood, and keep the summary under 72 characters. History before this convention is frozen; do not rewrite it.
- **`AGENTS.md` is an identical copy of this file.** When editing `CLAUDE.md`, copy it over `AGENTS.md` so they stay in sync.

## Releases

Releases are cut by pushing a `vX.Y.Z` git tag — `.github/workflows/release.yml` runs GoReleaser (`.goreleaser.yaml`) to build darwin/linux/windows binaries, inject the version via ldflags (`-X main.version=…`), and sign with cosign. The `version` *segment* displays the calling tool's version from the payload; `claude-statusline version` shows this binary's.

Before tagging, **update `CHANGELOG.md` (new `## vX.Y.Z` section at the top)** — the release-notes feature embeds it at build time, so anything you forget won't be reachable from `claude-statusline release-notes`. Keep the existing section format (`## vX.Y.Z — YYYY-MM-DD` header, `- ` bullets, newest first). Bullets may carry a leading `[N]` importance marker: ordinary items use 0–5, critical/pinned items can use much larger values (e.g. 99999) to force top placement. Bullets without a marker default to importance 0.

Pushing a tag also triggers `.github/workflows/ci.yml`, which runs `go test ./...`, `golangci-lint`, and Node checks: it syntax-checks `npm/claude-statusline/bin/claude-statusline.js` and `scripts/build-npm.mjs`, builds the Go binary, exercises the npm shim with a real payload via `CLAUDE_STATUSLINE_BIN`, and smoke-tests the pi TypeScript extension via `scripts/test-pi-extension.mjs`. Push the branch and tag separately to avoid double-triggering the workflows.

### npm distribution

Every GitHub release is also published to npm as `@morgan.rebrand/claude-statusline` with per-platform optional dependencies (`@morgan.rebrand/claude-statusline-<os>-<cpu>`). The main package contains the small Node shim in `npm/claude-statusline/bin/claude-statusline.js` and the pi extension in `npm/claude-statusline/extensions/pi-statusline.ts`; `scripts/build-npm.mjs` repacks the GoReleaser archives under `dist/` into the platform packages and the main package. The `npm-publish` job in `.github/workflows/release.yml` runs it automatically using npm OIDC trusted publishing (no long-lived token).

To set it up:
1. Create an npm account and the `@morgan.rebrand` organization.
2. Bootstrap each package on npm once. npm (unlike PyPI) **requires a package to already exist before a trusted publisher can be configured for it**, so the first publish must be a manual, authenticated one — there is no "let the workflow create them" path. Log in (`npm login`), then `npm publish --access public` a placeholder `0.0.0` of the main package (its committed source under `npm/claude-statusline/` is publishable as-is) and of each `@morgan.rebrand/claude-statusline-<os>-<cpu>` package (a minimal `package.json` with `name`/`version: 0.0.0`/`os`/`cpu`/`repository` is enough). Publish the 7 platform packages first, the main package last. After the first tagged release, the `0.0.0` placeholders can be `npm unpublish`ed per-version (main first — it depends on the platform `0.0.0`s).

   Platform packages: `darwin-arm64`, `darwin-x64`, `linux-arm`, `linux-arm64`, `linux-x64`, `win32-arm64`, `win32-x64`.
3. In each package's **Trusted Publisher** settings on npmjs.com, add a **GitHub Actions** publisher (this is why step 2 is mandatory — the form only appears once the package exists):
   - Organization or user: `callmemorgan`
   - Repository: `claude-statusline`
   - Workflow filename: `release.yml` (bare filename, not a path)
   - Environment: leave blank — the `npm-publish` job does not declare a GitHub Environment
   - Allowed actions: check **Allow `npm publish`** (leave `npm stage publish` unchecked)
4. Confirm `.github/workflows/release.yml` grants `id-token: write` (the `npm-publish` job already does).
5. Push a `vX.Y.Z` tag. The `release` job builds binaries and uploads `dist/`; the `npm-publish` job downloads it, runs `scripts/build-npm.mjs`, and publishes all packages with `--provenance`.

The publishes are idempotent: `npm view` checks skip any package/version already on the registry, so re-running the job is safe. The main package's `optionalDependencies` are pinned to the exact same version, so installing `@morgan.rebrand/claude-statusline@X.Y.Z` always pulls platform binaries built from the same tag.

## Adding a new built-in segment

1. Write a `renderXxx(ctx renderCtx) (string, bool)` function in `segments.go`.
2. Add an entry to `allSegmentInfos()`: id, natural line (1–9), description, primary color role, optional `settings: []settingSpec` (gives it a flyout automatically), optional `needsState`.
3. Add the segment ID to `defaultConfig()` if it should be on by default (fine when it self-hides without data).
4. Update the segment table in `README.md` and the lists in `help.go`; extend `config.toml.example` if the config schema changed.
5. Add a fixture/assertion: extend a `testdata/payloads/*.json` fixture (regenerate goldens with `-update`) or add a direct renderer test.

## Adding a new built-in theme

1. Add a theme to `builtinThemes` in `themes.go` with a unique id, description, and a colour for each of the 15 semantic roles (`model`, `dir`, `git`, `changes`, `duration`, `cost`, `dim`, `ok`, `warn`, `crit`, `agent`, `vim`, `accent`, `session`, `sep`). Use `themeColor{Hex: "#rrggbb"}` for truecolour/256/16 fallback, or `ansiRole("\x1b[…m")` for an explicit 16-colour-only theme like `classic`.
2. If the theme should emit no colour at all (e.g. `monochrome`), set every role to `ansiRole("")`; `resolvePalette` treats an all-empty theme as disabled so no colour escapes are emitted.
3. Update the theme list in `help.go`, `config.toml.example`, and `README.md`.
4. Add the theme and a canonical background to `THEMES`/`BG` in `scripts/screenshots.py`, then regenerate screenshots with `python3 scripts/screenshots.py`.
5. Run `go test ./...` and smoke-test the theme with a sample payload.

## Homebrew vs local binary

`/opt/homebrew/bin/claude-statusline` is the Homebrew install. `./claude-statusline` in the repo root is the local build. When testing changes, build locally and use `./claude-statusline` directly — and remember the config-migration caution above.
