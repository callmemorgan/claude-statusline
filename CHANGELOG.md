# Changelog

<!--
Bullet importance markers: a leading `[N]` tells the release-notes renderer
how to order bullets. Ordinary items use 0–5; critical/pinned items can use
much larger values (e.g. 99999) to force top placement. Bullets without a
marker default to importance 0.
-->

## v1.8.4 — 2026-07-16
- [5] **Quota shim reads the right account on multi-profile machines.** Claude Code namespaces its macOS keychain item per `CLAUDE_CONFIG_DIR` (`Claude Code-credentials-<sha256[:8]>`; unsuffixed for the default `~/.claude` profile), but the shim always read the unsuffixed entry — so with two logins on one machine it reported the *default* profile's account (possibly a different account entirely), regardless of which profile was running Claude Code. The shim now resolves the profile (`[quota_shim].claude_config_dir` → `$CLAUDE_CONFIG_DIR` → default) and reads that profile's scoped keychain service and `.credentials.json`.
- [4] New `[quota_shim]` option `claude_config_dir`: point the shim at a specific Claude Code profile (same value you'd export as `CLAUDE_CONFIG_DIR`, `~/` expands); wins over the inherited env. Unset keeps today's behavior.
- [3] **Per-profile shim cache.** The quota cache and lock files are keyed by the resolved profile (default profile keeps the legacy `quota-shim.json`/`quota-shim.lock` names; scoped profiles append the same 8-hex suffix as the keychain service), so concurrent sessions on different profiles no longer clobber each other's windows.
- [1] `claude-statusline quota` now prints the resolved profile and keychain service name so you can confirm the shim is reading the right account.

## v1.8.3 — 2026-07-09
- [4] **Model-class windows are shim-only now.** Removed the speculative statusline-payload parsing of `seven_day_overage_included` / `seven_day_sonnet` / `seven_day_opus` / `model_scoped` — Claude Code does not send these fields to the statusline, so the binary no longer pretends to read them from the wire. The Fable/Sonnet/Opus bars are fed exclusively by the opt-in `[quota_shim]` OAuth bridge; if Claude Code ever ships the fields, wire parsing will return against the real schema.

## v1.8.2 — 2026-07-09
- [5] **OAuth quota shim.** New opt-in `[quota_shim]` config table lights up the `rate-limit-fable` / `rate-limit-sonnet` / `rate-limit-opus` bars today: a detached background worker fetches Claude's OAuth usage endpoint (the data behind `claude /usage`) and the render path fills `rate_limits.model_scoped` when the statusline payload lacks the model-class windows. Payload data always wins once Claude Code ships the fields. Only percentages and reset times are cached — never the OAuth token.
- [3] New `claude-statusline quota` subcommand: verifies the shim end-to-end (cache state + live fetch of the model-class weekly windows).
- [1] Rate-limit bars omit the reset countdown instead of rendering `(?)` when `resets_at` is absent (common for inactive shim windows).

## v1.8.1 — 2026-07-09
- [5] **Fable / Sonnet / Opus weekly quota bars.** New segments `rate-limit-fable`, `rate-limit-sonnet`, and `rate-limit-opus` mirror the existing 5h/7d bars (countdown, burn-rate projection, flyout stress-test). They parse `seven_day_overage_included` (Fable 5 included weekly), `seven_day_sonnet`, `seven_day_opus`, and optional `model_scoped[]` when Claude Code sends them — and self-hide until those fields appear on the statusline wire.
- [4] **Auto-enable Fable next to the 7d bar.** Schema v2 inserts `rate-limit-fable` after `rate-limit-7d` for existing configs (line + bar settings copied). The same upgrade runs for pre-1.0 `config.json` migrations before the TOML is written, so legacy users aren't left stamped at schema 2 without the Fable segment.
- [2] Defaults and quota-focused presets include the three model-class bars alongside `rate-limit-5h` / `rate-limit-7d`.

## v1.8.0 — 2026-07-04
- [5] **Claude Code statusline integration v2.** The payload now accepts Claude Code's newer statusline fields — `prompt_id`, `pr`, `repo`, `worktree`, `thinking`, and the `statusLine` / `subagentStatusLine` install options — and exposes them as new built-in segments.
- [4] **New built-in segments:** `prompt-id`, `pr`, `repo`, and `thinking` join the segment list, plus the `dir` segment now renders a `worktree` suffix when one is present in the payload.
- [3] **Install hook coverage.** `claude-statusline install` now writes both the existing `mcpStatusLine` hook and the newer `statusLine` / `subagentStatusLine` settings shapes used by current Claude Code versions.

## v1.7.2 — 2026-06-22
- [5] **Post-upgrade takeover now shows actual release notes.** The announcement no longer wastes lines on a configuration hint; every available line is used for the version header and CHANGELOG bullets.
- [4] **Plugin `preview` values are now prominent in the TUI assembler.** Custom plugin segments display their configured `preview` sample in the assembler, flyout, demo mode, and terminal view, so you can see exactly how a plugin will look before it runs a real command.

## v1.7.1 — 2026-06-22
- [4] **Plugins are now visible in the TUI assembler.** Plugin segments are marked with a 📌 indicator, and the footer shows a matching legend. Plugins can also declare a `preview` value that is rendered in the assembler, flyout preview, demo mode, and terminal view instead of running the real command against synthetic data.

## v1.7.0 — 2026-06-21
- [3] **Dedicated npm READMEs.** Every published npm package — the main `@morgan.rebrand/claude-statusline` package and all seven per-platform optional dependencies — now ships with its own README. The build script verifies each README exists before packing, so the matrix can't drift.

## v1.6.1 — 2026-06-18
- [5] **Revert Homebrew distribution to a Formula.** The v1.6.0 cask collided with the existing `claude-statusline` formula in the tap, which broke `brew upgrade` and auto-update for existing installs. We're going back to the `brews` formula in GoReleaser; install and upgrade with `brew install claude-statusline` and `brew upgrade claude-statusline` as before. A cask under a different token (`agents-statusline`) will be introduced separately later.
- [2] Fixed remaining Biome lint/format errors in the npm shim and `scripts/build-npm.mjs` so `make lint-js` is green.

## v1.6.0 — 2026-06-18
- [1] **Repo reorganization.** The source tree is now laid out as a conventional Go multi-package project: a thin `cmd/claude-statusline` entry point plus focused `internal/` packages (`config`, `segments`, `render`, `tui`, `update`, `plugins`, etc.). No behavior changes — the stdin→stdout render path, all tests, golden files, and the release pipeline remain intact.
- [0] **Homebrew distribution is now a Cask.** Migrated `.goreleaser.yaml` from the deprecated `brews` config to `homebrew_casks` for precompiled binaries, matching GoReleaser's current recommendation. macOS installs now strip the Gatekeeper quarantine bit in a post-install hook. Update with `brew upgrade --cask claude-statusline`.
- [0] Fixed remaining Biome lint/format errors in the npm shim and `scripts/build-npm.mjs` so `make lint-js` is green.

## v1.5.2 — 2026-06-18
- [5] **pi extension.** `pi install npm:@morgan.rebrand/claude-statusline` now wires the renderer into pi's footer as a first-class extension. The TypeScript extension refreshes on session/turn/model events, requires no separate `claude-statusline install` step inside pi, and resolves the Go binary from the same per-platform npm optional dependencies. Update it with `pi update --extension npm:@morgan.rebrand/claude-statusline` or alongside pi with `pi update`.
- [2] Added a CI smoke test for the pi TypeScript extension.

## v1.5.1 — 2026-06-18
- [4] **Configurable takeover height.** The post-upgrade announcement now expands up to `[release_notes].max_lines` (default 10), so typical minor-version updates fit without truncation. Set `max_lines = 0` or `max_lines = "status-line"` to keep the announcement at your statusline's normal height.

## v1.5.0 — 2026-06-18
- [5] **Importance-weighted release notes.** CHANGELOG.md bullets can now carry a leading `[N]` marker; the post-upgrade takeover and `release-notes` subcommand sort bullets by importance so the most impactful changes are seen first.
- [3] `release-notes vX.Y.Z..vA.B.C` prints a cross-version summary of the highest-importance bullets between two versions, sorted by priority.
- [2] The post-upgrade takeover now summarizes the most important changes across the whole upgrade span (e.g. v1.0.0 → v1.5.0) instead of only the latest version's bullets.

## v1.4.1 — 2026-06-18
- [4] **Fix npm publish.** Removed an accidental `pi` extension dependency from `scripts/build-npm.mjs` that caused the `v1.4.0` npm publish to fail because `npm/claude-statusline/extensions/pi-statusline.ts` did not exist.
- [1] Fixed a `gofmt` issue in `config_test.go` so the CI lint job passes.

## v1.4.0 — 2026-06-18
- [4] **Two new npm platform packages.** Added `windows/arm64` and `linux/arm` (armv7) binaries to the GoReleaser build matrix and npm optional-dependency set.
- [1] Added a CI lint/test workflow (triggered only on tag pushes) with a valid `golangci-lint` v2 config.

## v1.3.2 — 2026-06-18
- [4] **Three new themes.** `paper` and `solarized-light` are tuned for light terminal backgrounds with high-contrast, legible colours. `monochrome` is adaptive black-and-white: it emits no colour escapes at all and uses the terminal's configured foreground colour, so it works on both light and dark backgrounds.
- [1] Regenerated theme screenshots for the full palette gallery.
- [0] `.gitignore` local `node_modules/` used by dev tooling.

## v1.3.0 — 2026-06-15
- [5] **Install via npm.** `npm i -g @morgan.rebrand/claude-statusline` now works on macOS, Linux, and Windows. The main package is a tiny Node shim that execs the correct prebuilt binary from a per-platform `optionalDependencies` package; every GitHub release publishes them automatically with npm trusted publishing (OIDC, no token) and provenance. Homebrew and manual installs remain the lowest-latency options since the npm shim pays one Node spawn per render.
- [3] `auto` update mode now recognizes npm installs and leaves them alone — npm owns the binary, so self-swap would fight the package manager. The `update` segment and `claude-statusline update` print `npm update -g @morgan.rebrand/claude-statusline` for npm installs instead.

## v1.2.4 — 2026-06-14
- [4] **Line wrapping is now opt-in.** The default `reflow` is `"off"`: a line wider than the terminal is left as-is for the terminal to soft-wrap, instead of reflowing segments across physical lines. Set `reflow = "cascade"` (greedy spill) or `reflow = "group"` (each logical line wraps independently) to opt back in. Only affects narrow terminals; output that already fit is unchanged.

## v1.2.3 — 2026-06-14
- [4] **Fix: Homebrew updates now pick up new releases.** `brew upgrade` runs with `HOMEBREW_NO_AUTO_UPDATE=1` (to stay fast), which meant a stale local tap made it report "already installed" against an old formula — so `claude-statusline update` on a Homebrew install could never actually upgrade even when the segment showed a newer version available. The brew path now refreshes just our tap (`git pull` on its checkout) before upgrading, keeping it fast while letting brew see the published release. (Note: this self-heals from v1.2.3 onward — the first hop onto v1.2.3 still needs a one-time `brew update`/tap refresh.)

## v1.2.2 — 2026-06-14
- [3] New opt-in `git-stash` segment: shows the git stash count (`⚑N`) and hides when there are no stashes. Runs a cached, bounded `git rev-list` like the rich git-branch status (its own cache file, so enabling both segments never costs two execs). Off by default — add it in `configure`.

## v1.2.1 — 2026-06-14
- [4] **Fix: signed auto-update now verifies real releases.** The in-process verifier read only the newer sigstore bundle shape (`messageSignature.signature`), but a release's `checksums.txt.bundle` may instead carry the signature under the legacy `base64Signature` field depending on the cosign version CI resolves — so v1.2.0 could fail closed on its own release and never self-update (manual installs only; Homebrew was unaffected). The verifier now accepts either field, and the release pipeline normalizes the published bundle to a version-stable shape so already-installed binaries can always verify the next release.
- [2] The `update` segment briefly shows `✓ updated to vX` after a self-update lands (reads `update-result.json`, written by the worker/foreground install paths; self-hides once the short window passes or when the running version doesn't match).
- [2] New `claude-statusline update verify`: fetches the latest release's `checksums.txt` and signature and checks them against the embedded key on demand, printing the key fingerprint. Installs nothing; fails closed.

## v1.2.0 — 2026-06-14
- [5] Auto-update now cryptographically verifies releases: `checksums.txt` is signed with a key-based cosign bundle and verified in-process against an embedded public key before any binary is installed. Verification is pure stdlib — no `cosign` needed at runtime — and fails closed on a missing or invalid signature.
- [3] Hardened the self-swap pipeline: per-run staging directories and per-PID swap filenames so a foreground `claude-statusline update` and the background worker can never corrupt each other's swap; the foreground `update` now serializes through the same lock.
- [2] Download client pins redirects to HTTPS on `github.com`/`*.githubusercontent.com`; archive extraction is bounded against decompression bombs; the staged binary keeps a `.exe` suffix on Windows.
- [2] Hardened checksum parsing (anchored on the hex digest) and install-kind detection (path-component match, so `~/homebrew-fan/` is no longer misread as a Homebrew install).
- [1] The update-available segment no longer pins its verbose hint on a future cache timestamp (clock-skew guard).

## v1.1.1 — 2026-06-13
- [5] Background update checks: `notify` (default) shows an `⬆ vX.Y.Z` segment when a newer release exists; `auto` downloads, verifies, and atomically swaps the binary for manual installs; `off` disables all network activity.
- [3] New `claude-statusline update` subcommand for explicit foreground updates, plus `update --check` to report without installing.
- [2] Homebrew installs are upgraded via `brew upgrade claude-statusline` instead of self-swap.
- [2] Render path does no network I/O: one cache read per render, with a detached worker spawned after printing at most once per `check_hours` interval.

## v1.1.0 — 2026-06-12
- [4] `release-notes` subcommand: print notes for the current or any past version (`vX.Y.Z`, `--all`)
- [3] Post-upgrade announcement: the statusline shows what's new for 25s after an update, then returns to normal (configurable via `[release_notes]` in config.toml; `announce = false` or `duration_seconds = 0` disables)

## v1.0.2 — 2026-06-11
- [3] feat: async plugins with stale-while-revalidate caching
- [2] fix(install): honor `CLAUDE_CONFIG_DIR` when resolving settings.json
- [1] docs: async plugin docs and current-pr example
- [1] docs: real screenshots for the GitHub page

## v1.0.1 — 2026-06-10
- [3] feat(tui): terminal view (`v`), wrapping footer, and the `original` theme alias for `classic`
- [2] fix(tui): every option previews — synthetic state, demo mode, and git fix
- [1] docs: catch docs up to the post-sweep TUI features

## v1.0.0 — 2026-06-10
- [5] feat: TOML config with validation and automatic JSON migration
- [5] feat: schema-driven per-segment settings replace ad-hoc feature maps
- [4] feat: theme system with truecolor, 256-color, and 16-color rendering
- [4] feat: install/uninstall subcommands for one-command onboarding
- [4] feat: burn-rate projections, cost-per-hour, and context trend
- [4] feat: purpose-built help — in-TUI overlay and rewritten CLI help
- [4] feat(tui): theme and preset pickers with live preview, and the `preset` config key
- [3] feat(tui): color picker — swatch grid with theme roles, ANSI, hex, and recents
- [3] feat: opt-in rich git status — dirty marker and ahead/behind counts
- [3] feat: `output-style`, `added-dirs`, and `email` segments
- [3] feat: per-session state store for burn-rate and projection features
- [3] feat(tui): full SGR parser for preview colors — truecolor and 256-color
- [2] feat: seven new bar iconsets with fractional-fill rendering
- [2] feat(tui): `/` filter for the segment list
- [2] feat(tui): width-aware live preview with a `w` override
- [2] feat(tui): plumbing overhaul — dirty tracking, status strip, and confirmations
- [2] feat: version subcommand with GoReleaser ldflags injection
- [1] feat: golden-file test suite locking current renderer behavior
- [1] feat: split main.go into per-concern files with subcommand dispatch
- [1] docs: add release process, AGENTS.md sync rule, and doc-update step to CLAUDE.md

## v0.3.0 — 2026-06-01
- [2] feat: warn on a Homebrew install if other `claude-statusline` installs are detected

## v0.2.1 — 2026-05-30
- [3] feat: add `group` reflow mode to preserve line boundaries on wrap

## v0.2.0 — 2026-05-29
- [3] Revert right-justify flyout arrow changes (deadlock regression)
- [2] feat(tui): step field on numeric flyout features
- [2] fix(config): don't persist irrelevant fields in per-segment settings
- [2] fix(renderer): `ok`/`warn`/`crit` `default` colors use the natural color
- [2] feat: mouse support and confirm modal in the configure TUI
- [1] feat(tui): include `bright-*` variants in color cycle
- [1] ui: checkmark indicator, flyout arrow, and `f`→`o` keybind rename
- [1] flyout: per-segment settings for progress bars

## v0.1.2 — 2026-05-29
- [2] Add a 5-column safety margin to the reflow budget
- [1] Add missing ANSI reset to the timing suffix; merge adjacent map loops in `buildStatusline`

## v0.1.1 — 2026-05-29
- [1] Clarify the `lines-changed` description (all lines ± by the agent in the session)
- [1] Allow `<esc>` to quit the configure TUI
- [1] Insert blank lines before reflowed original-config lines; fix missing ANSI reset in `renderAPIEfficiency`

## v0.1.0 — 2026-05-28
- [5] Initial release
- [1] chore: add MIT license
- [1] docs: install instructions with cosign verification
