# Plan: Auto-update (background check + self-swap)

Spec: [`docs/specs/auto-update.md`](./auto-update.md) — decisions settled 2026-06-12.

This plan is a sequencing + mechanical-detail pass over the spec. Decisions
already locked in the spec are not re-litigated here; this document is about
*how* to land them, not *what* they are.

## Constraints to keep in mind throughout

- **Render path is sacred.** No network, no extra stdout/stderr, no blocking
  work before the print loop. The spawn happens after printing.
- **`version == "dev"` short-circuits the whole feature.** Same carve-out
  `release-notes` uses. This is what keeps goldens inert without `-update`.
- **Homebrew installs never have their binary swapped by us** — package-manager
  bookkeeping fights that. `auto` mode on brew runs `brew upgrade --cask` instead.
- **Auto means auto** — it crosses MAJOR versions, never downgrades.
- **No new dependencies.** `crypto/sha256`, `archive/tar`, `archive/zip`,
  `compress/gzip`, `net/http` are all stdlib.
- **Repo owner/name are compile-time constants** (`callmemorgan/claude-statusline`),
  not configurable.
- **Manual smoke uses an isolated copy of the binary** — never point the
  swap at the repo build you're editing. The spec's `Implementation order`
  step 7 is the canonical recipe.

## File layout

All new logic in **one** file: `update.go` + `update_test.go`. Touches to
existing files are minimal and listed below. This mirrors the plugin
system's pattern (logic in `plugins.go`, dispatch in `cmd.go`, trigger in
`runRender`).

| File | Change |
| --- | --- |
| `update.go` (new) | cache, install-kind detection, version compare, render trigger, worker, segment renderer, `update` subcommand, `update-check` subcommand, shared download/verify/extract/swap helpers |
| `update_test.go` (new) | all 11 tests from the spec |
| `config.go` | add `Update updateConfig \`toml:"update,omitempty"\`` field, `mergeWithDefaults` copy-through, `validateConfig` rules for `mode` / `check_hours` |
| `config_test.go` | one new test for the round-trip + validation rules (covered by spec test 10) |
| `cmd.go` | two dispatch cases: `update` and `update-check` (the latter hidden) |
| `runRender` (`cmd.go`) | one-line `maybeSpawnUpdateCheck(cfg.Update, start)` after the print loop, next to `st.Save()` |
| `segments.go` | one registry entry for `update`; one renderer; append to `defaultConfig()` |
| `plugins.go` | small generalization of `tryAcquireLock` so the update worker can share it (see "Lock generalization" below) |
| `install.go` | one-line mention in `verifyInstall` success output |
| `help.go` | Commands block: add `update` line; Configuration block: add `[update]` one-liner |
| `config.toml.example` | commented `[update]` block |
| `README.md` | command table row, segment table row, "Updates" section |
| `CLAUDE.md` + `AGENTS.md` | key-subsystems bullet for `update.go`; sacred-path carve-out note. **Keep the two files byte-identical** (the existing `AGENTS.md` mirror convention). |

## Lock generalization

`tryAcquireLock(lockPath, timeoutMS)` in `plugins.go` was generalized to
`tryAcquireLock(lockPath string, staleAfter time.Duration) bool`. The plugin
caller computes its own staleness budget (`timeout + 5s`, where `timeout`
defaults to 10s) and the update path passes `updateBrewTimeout + 2m`
(currently 7 minutes). This buffer is strictly larger than the 5-minute brew
upgrade timeout so a slow `brew upgrade` is never mistaken for a dead worker
and reaped while still running. The algorithm (O_CREATE|O_EXCL,
reap-if-older, one retry) is unchanged and is exercised by the existing
plugin tests and the new `update_test.go` cases.

The separate `acquireUpdateLock` helper was removed; the update path uses
the generalized primitive directly, matching the spec's "do not duplicate"
instruction.

## Pre-implementation fixes / prep

None. All the existing primitives the plan reuses are stable:

- `applyDetachSysProcAttr` — already exists, per-platform.
- `stateBaseDir()` — `state.go`, the sibling-dir-of-`sessions` pattern.
- `writeFileAtomic` — `config.go`, tmp + rename in same dir.
- `versionString()` — `version.go`, returns "dev" for source builds.
- `isReleaseVersion` — `releasenotes.go`, the clean MAJOR.MINOR.REVISION
  regex. **Reused as the single gate** for the spawn, worker, and segment
  renderer: any version that is not a clean `MAJOR.MINOR.REVISION` is treated
  as a source build and produces no network I/O.
- `pluginCacheDir` / `last-version.json` paths — siblings in
  `stateBaseDir()`. New `update.json` joins them.
- `spawnRefresher` package-var pattern — copied for the test stub.

## Implementation steps (in order)

### Step 1 — Config table

**New code:**

- `updateConfig` struct (mode + check_hours pointer), `mode()` and
  `checkEvery()` accessors (nil → 24h, "" → "notify").
- `mergeWithDefaults` copies `loaded.Update` across.
- `validateConfig` rules:
  - `mode` not in {notify, auto, off} → warn, reset to "".
  - `check_hours` outside 1–168 → warn, reset to nil.
  - Warn messages match the spec's wording exactly:
    - `"%q is not notify/auto/off (using notify)"`
    - `"%d out of range 1-168 (using 24)"`

**Tests:** spec test 10 — round-trip, merge preserves, validation warns +
normalizes, defaults (`mode()=="notify"`, `checkEvery()==24h`).

**Acceptance bar:** `go test ./config_test.go ./...` green; the existing
config tests still pass (we're adding a field, not changing semantics for
existing fields).

### Step 2 — Detection + version compare + cache

**New code in `update.go`:**

- `installKind` enum + `detectInstallKind(exePath, version string) installKind`.
  Pure on its inputs (path + version string). `version == "dev"` → `kindDev`;
  path containing `/Cellar/`, `/Caskroom/`, or `/homebrew/` (case-insensitive on darwin's
  HFS+ defaults but I'll do `strings.Contains` to keep it simple and let
  the test assert on the exact strings) → `kindBrew`; otherwise `kindManual`.
- `compareVersions(a, b string) int` — leading `v` tolerated, malformed
  inputs return 0.
- `updateCheck` struct, `loadUpdateCheck()`, `saveUpdateCheck()` using
  `writeFileAtomic` + `MkdirAll` on the state dir.
- `updateCheckPath()` returning `stateBaseDir() + "/update.json"`.

**Tests:** spec tests 1, 2, 3 — table-driven for kinds, versions, cache.

**Why no `os.Executable` mock yet:** the spec is explicit that
`detectInstallKind` takes a resolved path as a parameter so the table test
needs no real symlinks. The call site in `runRender` resolves via
`os.Executable()` + `filepath.EvalSymlinks` and passes the result in. The
segment renderer can call `detectInstallKind("", currentVersion)` to get
`kindDev` for the `version == "dev"` short-circuit without ever touching
the filesystem.

### Step 3 — Lock rename + render-path trigger

**`plugins.go` change:** rename `tryAcquireLock`'s `timeoutMS int` parameter
to `staleAfter time.Duration`. Update `trySpawnRefresher`'s one call site
to pass `time.Duration(def.TimeoutMS) * time.Millisecond`. No semantic
change.

**`update.go` additions:**

- `spawnUpdateCheck` package var (default: real impl). Mirrors
  `spawnRefresher` for test stubbing.
- `maybeSpawnUpdateCheck(cfg updateConfig, now time.Time)`. Reads the
  cache, returns early unless `mode() != "off"`, the current version is a
  clean release (`isReleaseVersion(current)`), and the kind is not dev.
  Acquires `update-check.lock` (shared helper), spawns the detached worker,
  releases the lock on failure to spawn.

**`cmd.go` change:** add `maybeSpawnUpdateCheck(cfg.Update, start)` after
the print loop, next to `_ = st.Save()`. One line.

**Tests:** spec test 4 — stub `spawnUpdateCheck`, assert:
- off-mode → never spawns
- fresh cache → never spawns
- stale cache → spawns once
- second stale render with active lock → does not spawn again (lock blocks)
- dev / non-release version (`+dirty`, Go pseudo-version) → never spawns

**Acceptance bar:** the bare render path still does *exactly one*
`os.ReadFile` of `update.json` on the happy path. Worst case (stale,
no lock) is one extra `os.OpenFile(O_CREATE|O_EXCL)` + one `exec.Command`.
The two existing plugin tests in `plugins_test.go` still pass (they
exercise `tryAcquireLock` end-to-end).

### Step 4 — Worker + download/verify/extract/swap

**`update.go` additions:** this is the largest step.

- `updateCheckExeArgs()` returning the `[]string{"update-check"}` slice —
  used by both the spawn and tests.
- `runUpdateCheck()` — the worker entrypoint. Same shape as
  `runPluginRefresh()`: hidden dispatch case, no user-visible output.
  - Returns immediately if `!isReleaseVersion(current)` (covers dev, dirty,
    and Go pseudo-versions) so non-release builds never hit the network.
  - Resolves the latest tag via HTTP `GET /releases/latest` with
    `CheckRedirect: http.ErrUseLastResponse`, 10s timeout, explicit
    `User-Agent: claude-statusline/<version>`. Parses `…/releases/tag/vX.Y.Z`
    from the 302 `Location` header.
  - Writes the cache (`{now, latest}` or `{now, ""}` on failure). Even a
    network failure writes the cache so a dead network doesn't respawn
    every render.
  - In notify mode, exits here.
  - In auto mode:
    - `kindBrew` → `runBrewUpgrade(brewPath)`. Locate `brew` via
      `exec.LookPath` with fallbacks `/opt/homebrew/bin/brew` and
      `/usr/local/bin/brew`. Env: `HOMEBREW_NO_AUTO_UPDATE=1`,
      `HOMEBREW_NO_INSTALL_CLEANUP=1`. 5-minute own-context timeout.
      Discarded stdout/stderr. Silent on failure. Missing brew → fall
      back to notify-only silently.
    - `kindManual` + newer + exe dir writable → `downloadAndSwap(latest,
      current)`. This is the shared function the `update` subcommand
      reuses.
- `runBrewUpgrade(brewPath string) error` — exec helper, used by both the
  worker (silent) and the `update` subcommand (live output).
- `downloadAndSwap(latest, current string) error` — the shared function.
  Returns nil on success, error on failure (worker discards, subcommand
  surfaces).
  - Resolves asset name via `assetName(goos, goarch)`. Cross-checks
    `runtime.GOOS`/`runtime.GOARCH` against the running binary so a
    swapped binary on a different machine is impossible.
  - Downloads to `stateBaseDir()/staging/claude-statusline.<ext>` with a
    64 MiB hard cap (read body into a counting reader; abort on overflow).
  - Downloads `checksums.txt` separately, parses the line for the asset,
    verifies the archive bytes' sha256. Mismatch → delete staging, abort.
  - Extracts the inner `claude-statusline` binary (`archive/tar`+`gzip`
    for `.tar.gz`, `archive/zip` for `.zip`), `chmod 0755`.
  - Smoke-tests: runs the staged binary with `version` (2s timeout),
    requires stdout to contain `latest` (the version we just downloaded).
  - **Atomic swap**, all renames in the exe's directory:
    1. copy staged → `<exeDir>/.claude-statusline.new`
    2. rename current exe → `<exeDir>/.claude-statusline.old`
    3. rename `.new` → exe path
    4. on (3) failure, rename `.old` back (rollback)
    5. remove `.old` (on Windows this fails while the old process lives;
       ignore the error and rely on step-0 cleanup at the start of the
       next worker run)
  - **Pre-clean** at the start: remove any `.old`/`.new` leftovers from
    previous (interrupted) runs.
  - Cleans the staging dir at the end.
- `assetName(goos, goarch string) string` — pure function, exact mirror of
  the GoReleaser template. **Locked by spec test 5.**

**`cmd.go` change:** add `update-check` case (hidden — not in `help.go`).
Plus the `update` case in the visible dispatch list (see step 6).

**Tests:** spec tests 5, 6, 7, 9.
- Test 5: asset name table against the GoReleaser template. A rename in
  `.goreleaser.yaml` must fail this test.
- Test 6: real sha256 of known bytes against a synthetic `checksums.txt`
  line; flipped bit → fail.
- Test 7: `t.TempDir()` "exe" + staged replacement → swap succeeds,
  content is the new binary, `.old` removed. Failure injection at step 3
  → rollback leaves the original intact and `.new`/`.old` cleaned.
- Test 9: stub the brew exec (package var, same pattern as
  `spawnRefresher`). Auto+brew runs upgrade with the right env vars;
  missing brew → silent fallback. Notify+brew never execs.

**Why no HTTP mock (per spec):** the latest-tag resolution is one HTTPS
request, 10s timeout, no body parsing. Mocking it would be coverage
theater. The manual smoke (step 7) is the only honest way to verify
network behavior. The rest of the worker is exercised end-to-end via the
swap test (test 7).

### Step 5 — The `update` segment

**`segments.go` additions:**

- `renderUpdate(ctx renderCtx) (string, bool)`. Hide unless:
  - `ctx.S` is irrelevant (no settings on this segment).
  - The binary is not `kindDev` — call `detectInstallKind("", ctx.Version)`,
    which returns `kindDev` for `version == "dev"`. (Plus the
    `isReleaseVersion(ctx.Version)` belt-and-braces check; non-release
    shapes like `+dirty` short-circuit the segment too.)
  - The cache loads successfully.
  - `compareVersions(cache.Latest, ctx.Version) > 0` — newer only.
- Two forms:
  - **Expanded** while `ctx.Now.Unix() - cache.CheckedAt < expandedWindow`
    (const 5 min, declared as a named constant in `update.go`).
  - **Compact** otherwise.
- Colors through `ctx.C.<role>` — picks the same role family the
  `version` segment uses (Dim). Hint portion uses `ctx.C.Dim`. No
  hardcoded ANSI.
- `allSegmentInfos()` entry: id `update`, line 1, primaryColor `"Dim"`,
  no settings, no `needsState`. Description: "Update available notice".
- `defaultConfig()` appends `"update"` to the segments list. (Self-hides
  without data, so default-on is safe.)

**Tests:** spec test 8 — table-driven hides on 6 conditions (no cache,
equal version, older latest, mode=off, dev, non-release `+dirty` /
Go pseudo-version), expanded within 5 min, compact after, no `\x1b`
bytes when palette is empty.

**Goldens check:** because the test fixtures render under `version == "dev"`,
the segment must hide in every golden. Run `go test ./...` after this step
and verify zero golden churn without `-update`. If anything changes,
something is wrong (the segment was wired wrong, or the goldens are
non-representative).

### Step 6 — `update` subcommand

**`update.go` additions:**

- `runUpdate(args []string)`. The spec is explicit: this ignores `mode`
  (explicit intent) but **not** the safety rails (kind, major, checksum,
  smoke-test).
- `kindDev` → print "source build — update with `go install …@latest`",
  exit 0.
- `kindBrew` → run `brew upgrade --cask claude-statusline` in the foreground
  (same env as the worker, but with live stdout/stderr). Missing brew →
  print the manual command, exit 1.
- Already current → "claude-statusline vX.Y.Z is up to date", exit 0.
- Newer exists → print what it found, call `downloadAndSwap(latest,
  current)` (the shared function from step 4), print
  "updated vA → vB — run `claude-statusline release-notes` to see what
  changed".
- `--check` (and `--update` via the existing `TrimLeft("-")` dispatch) →
  resolve + report only, never install. Exit 0 either way.
- Any failure → message on stderr, exit 1.

**`cmd.go` change:** add `update` case in the visible dispatch list (just
above the `default:`).

**Tests:** spec test 9 covers the auto/brew interaction. The subcommand's
own behavior is table-driven in a separate test that stubs
`downloadAndSwap` (a new package var, same pattern) to assert exit codes
and stdout for: dev, brew, current, newer, --check. The "newer" path
relies on the shared function already being tested in step 4.

**Docs:** `help.go` Commands block — add `update  Check for a new release
and install it (--check: report only).` Configuration block — add
`[update] mode: notify/auto/off, check_hours`. `update-check` stays
unlisted.

**`install.go` change:** append one line to the verified-render output in
`verifyInstall`: `update checks: notify (configure via [update] in
config.toml)`. Lives right after "Customize anytime: claude-statusline
configure". No interaction with the verify step itself.

**`config.toml.example` change:** append the commented `[update]` block
from the spec.

### Step 7 — Tests + manual smoke

**`go test ./...` — must be green, zero golden changes.** If a golden
needs updating, something is wrong with the `dev` short-circuit (most
likely: the segment isn't hiding, or the renderer is now touching the
cache when it shouldn't).

**Manual smoke (run by user, not by this plan):** the spec's recipe in
`Implementation order` step 7 is the canonical procedure. The
`/tmp/upd-test/bin/claude-statusline` carve-out is the one thing this
plan can't enforce — but the test suite + the swap test in test 7
together cover the "swaps the right file" invariant end-to-end with a
fake exe.

**`README.md` change:**

- Command table row: `update` — Check for a new release and install it.
- Segment table row: `update` — `⬆ vX.Y.Z` when behind, hides when
  current.
- "Updates" section: notify default, the daily expanded disclosure
  (5 minutes after each check), `auto` opt-in (self-swap for manual
  installs, `brew upgrade` for brew installs), the
  no-network-on-render guarantee, `mode = "off"` for air-gapped or
  managed deployments.

**`CLAUDE.md` + `AGENTS.md` change:**

- Key-subsystems bullet for `update.go` mirroring the
  `releasenotes.go` / `plugins.go` entries.
- Note the sacred-path carve-out: post-print spawn, single cache read.
- **Copy CLAUDE.md over AGENTS.md so they stay byte-identical.**

## What the test plan covers vs the spec

| Spec test | Plan step | New / existing code |
| --- | --- | --- |
| 1. `detectInstallKind` | step 2 | new |
| 2. `compareVersions` | step 2 | new |
| 3. Cache round-trip | step 2 | new |
| 4. `maybeSpawnUpdateCheck` | step 3 | new + `tryAcquireLock` rename |
| 5. Asset naming | step 4 | new |
| 6. Checksum verify | step 4 | new |
| 7. Swap (incl. rollback) | step 4 | new |
| 8. `renderUpdate` | step 5 | new |
| 9. Brew branch | step 4 + 6 | new + subcommand path |
| 10. Config | step 1 | new in `config_test.go` |
| 11. Goldens | every step (verify) | existing |

The `go test ./...` bar from test 11 is checked *continuously* (after
each step), not just at the end. A surprise re-render shows up in the
right step instead of "everything broke at the end."

## Risks I'm flagging up front

1. **`detectInstallKind` path matching.** `/Cellar/`, `/Caskroom/`, and `/homebrew/`
   cover macOS Homebrew on both Apple Silicon (`/opt/homebrew/`) and
   Intel (`/usr/local/`). Linuxbrew uses `~/.linuxbrew/Cellar/...`,
   which contains `/Cellar/` so the substring check catches it. Cask
   installs resolve into `/opt/homebrew/Caskroom/...`, so `/Caskroom/`
   catches those. I'm not going to be more clever than that — the spec's
   call site is `filepath.EvalSymlinks` on the running binary, and the
   brew bin symlink always points into a Cellar or Caskroom.
2. **Windows .zip extraction.** `archive/zip` is in stdlib, but the
   inner entry name might not be exactly `claude-statusline` — GoReleaser
   can prefix with a directory. Test 7 will exercise this; if extraction
   fails, the test points directly at the bug.
3. **Network on the render path.** Strictly forbidden. The render trigger
   does a single `os.ReadFile`. The worker does the network. The lock is
   the only way the render path coordinates with the worker, and the
   lock acquisition is `O_CREATE|O_EXCL` — one syscall on the happy path.
4. **Stage dir on a different filesystem than the exe.** The spec calls
   this out: same-directory renames are atomic, but the staged binary
   may live on a different filesystem (e.g. `~/.local/state/...` is on
   the data volume, the exe is in `/usr/local/bin` on a read-only root).
   Step (e)1 uses **copy** (not rename) into the exe's directory
   specifically because of this. The renames after that are all
   same-directory and atomic.
5. **Test 7's failure-injection is load-bearing.** The spec says "failure
   injection at step 3 → rollback leaves the original intact." I need to
   make sure the test actually exercises step 3 of the spec's procedure
   (the rename of `.new` → exe), not some earlier failure. The
   injection point: chmod 0000 the parent dir so the rename fails, then
   verify the original is intact and `.new`/`.old` are cleaned by the
   next-run pre-clean.

## Acceptance criteria — direct mapping

Every box in the spec's "Acceptance criteria" maps to a specific check:

- [x] `go test ./...` green, zero golden changes — last line of step 7.
- [x] No network I/O on render path — `runRender` change is one line
  that calls `maybeSpawnUpdateCheck`; the function does one
  `os.ReadFile` + at most one `O_CREATE|O_EXCL` + at most one
  `exec.Command` spawn. Code review of step 3 + step 5.
- [x] `mode = "off"` produces zero spawns and zero reads beyond the
  config — `maybeSpawnUpdateCheck` returns immediately on `mode() ==
  "off"`. Test 4 covers this.
- [x] Notify segment appears when behind (expanded 5 min after check,
  compact otherwise), disappears when current, never on
  dev/current/downgrade. Test 8.
- [x] Auto on manual: replaced within one check interval; next render
  announces via release-notes takeover; checksum/smoke failure leaves
  the old binary. Tests 6 + 7 + manual smoke.
- [x] Auto on brew: never touches the binary directly; runs
  `brew upgrade --cask claude-statusline` with `HOMEBREW_NO_AUTO_UPDATE=1`;
  silent when brew absent. Test 9.
- [x] `install` output mentions the default. One-line `install.go`
  change in step 6.
- [x] `update` / `--check` behave per the table; honest exit codes.
  Subcommand test in step 6.
- [x] Concurrent workers never corrupt the exe (lock + rename rollback;
  `.old`/`.new` cleanup). Tests 4 + 7.
- [x] README, help.go, install output, config.toml.example, CLAUDE.md +
  AGENTS.md. All in step 6 (and AGENTS.md sync).

## Open questions for the user

None. The spec is decisive on every point I'd otherwise ask about. The
only place the spec is loose is "primary color role (pick the same role
family the `version` segment uses)" — the `version` segment uses `Dim`,
so I'll use `Dim`. If you want a different choice, say so before step 5.

If you want to adjust anything in this plan, the highest-leverage points
are:

- **Step 1's "mode not in {notify, auto, off} → warn and reset to ''"**
  — I picked the lenient "reset and continue" pattern matching the
  existing `reflow` / `theme` validations. Strict mode (refuse to
  start) would be unusual for a renderer.
- **Step 4's "Asset name lock against GoReleaser template"** — the test
  asserts the *exact* filename for each (goos, goarch) pair. If a future
  GoReleaser template change ships, this test will fail loudly. That's
  the point, but worth flagging.
- **Step 5's segment line (line 1)** — putting the `update` segment on
  line 1 makes it the first thing the user sees. Line 3 (with the other
  "ambient" indicators like context window) would be quieter. The spec
  says "line 1" — I'm following it, but it does compete visually with
  `vim-mode` and `session-name` which sit there by default.
