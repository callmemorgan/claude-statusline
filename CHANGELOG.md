# Changelog

## v1.1.2 — 2026-06-14
- Auto-update now cryptographically verifies releases: `checksums.txt` is signed with a key-based cosign bundle and verified in-process against an embedded public key before any binary is installed. Verification is pure stdlib — no `cosign` needed at runtime — and fails closed on a missing or invalid signature.
- Hardened the self-swap pipeline: per-run staging directories and per-PID swap filenames so a foreground `claude-statusline update` and the background worker can never corrupt each other's swap; the foreground `update` now serializes through the same lock.
- Download client pins redirects to HTTPS on `github.com`/`*.githubusercontent.com`; archive extraction is bounded against decompression bombs; the staged binary keeps a `.exe` suffix on Windows.
- Hardened checksum parsing (anchored on the hex digest) and install-kind detection (path-component match, so `~/homebrew-fan/` is no longer misread as a Homebrew install).
- The update-available segment no longer pins its verbose hint on a future cache timestamp (clock-skew guard).

## v1.1.1 — 2026-06-13
- Background update checks: `notify` (default) shows an `⬆ vX.Y.Z` segment when a newer release exists; `auto` downloads, verifies, and atomically swaps the binary for manual installs; `off` disables all network activity.
- New `claude-statusline update` subcommand for explicit foreground updates, plus `update --check` to report without installing.
- Homebrew installs are upgraded via `brew upgrade claude-statusline` instead of self-swap.
- Render path does no network I/O: one cache read per render, with a detached worker spawned after printing at most once per `check_hours` interval.

## v1.1.0 — 2026-06-12
- `release-notes` subcommand: print notes for the current or any past version (`vX.Y.Z`, `--all`)
- Post-upgrade announcement: the statusline shows what's new for 25s after an update, then returns to normal (configurable via `[release_notes]` in config.toml; `announce = false` or `duration_seconds = 0` disables)

## v1.0.2 — 2026-06-11
- fix(install): honor `CLAUDE_CONFIG_DIR` when resolving settings.json
- docs: async plugin docs and current-pr example
- feat: async plugins with stale-while-revalidate caching
- docs: real screenshots for the GitHub page

## v1.0.1 — 2026-06-10
- docs: catch docs up to the post-sweep TUI features
- feat(tui): terminal view (`v`), wrapping footer, and the `original` theme alias for `classic`
- fix(tui): every option previews — synthetic state, demo mode, and git fix

## v1.0.0 — 2026-06-10
- feat: purpose-built help — in-TUI overlay and rewritten CLI help
- feat(tui): theme and preset pickers with live preview, and the `preset` config key
- feat(tui): color picker — swatch grid with theme roles, ANSI, hex, and recents
- feat(tui): full SGR parser for preview colors — truecolor and 256-color
- feat(tui): `/` filter for the segment list
- feat(tui): width-aware live preview with a `w` override
- feat(tui): plumbing overhaul — dirty tracking, status strip, and confirmations
- feat: install/uninstall subcommands for one-command onboarding
- feat: burn-rate projections, cost-per-hour, and context trend
- feat: opt-in rich git status — dirty marker and ahead/behind counts
- feat: `output-style`, `added-dirs`, and `email` segments
- feat: seven new bar iconsets with fractional-fill rendering
- feat: configurable separators and line padding (`[style]` table)
- feat: theme system with truecolor, 256-color, and 16-color rendering
- feat: per-session state store for burn-rate and projection features
- feat: TOML config with validation and automatic JSON migration
- feat: schema-driven per-segment settings replace ad-hoc feature maps
- feat: version subcommand with GoReleaser ldflags injection
- feat: golden-file test suite locking current renderer behavior
- feat: split main.go into per-concern files with subcommand dispatch
- docs: add release process, AGENTS.md sync rule, and doc-update step to CLAUDE.md

## v0.3.0 — 2026-06-01
- feat: warn on a Homebrew install if other `claude-statusline` installs are detected

## v0.2.1 — 2026-05-30
- feat: add `group` reflow mode to preserve line boundaries on wrap

## v0.2.0 — 2026-05-29
- Revert right-justify flyout arrow changes (deadlock regression)
- feat(tui): step field on numeric flyout features
- fix(config): don't persist irrelevant fields in per-segment settings
- feat(tui): include `bright-*` variants in color cycle
- fix(renderer): `ok`/`warn`/`crit` `default` colors use the natural color
- feat: mouse support and confirm modal in the configure TUI
- ui: checkmark indicator, flyout arrow, and `f`→`o` keybind rename
- flyout: per-segment settings for progress bars

## v0.1.2 — 2026-05-29
- Add a 5-column safety margin to the reflow budget
- Add missing ANSI reset to the timing suffix; merge adjacent map loops in `buildStatusline`

## v0.1.1 — 2026-05-29
- Clarify the `lines-changed` description (all lines ± by the agent in the session)
- Allow `<esc>` to quit the configure TUI
- Insert blank lines before reflowed original-config lines; fix missing ANSI reset in `renderAPIEfficiency`

## v0.1.0 — 2026-05-28
- Initial release
- chore: add MIT license
- docs: install instructions with cosign verification
