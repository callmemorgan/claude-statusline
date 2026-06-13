# Spec: Release notes (subcommand + post-upgrade takeover)

Status: shipped in v1.1.0 — historical record; code and CLAUDE.md are truth
Target: claude-statusline (this repo), Go, `package main`

## Problem

Users update via Homebrew (or `go install`) and never learn what changed. There
is no changelog in the repo, no way to ask the binary "what's new", and no
moment where new features get surfaced. We want:

1. A `release-notes` subcommand (and `--release-notes` flag spelling) that
   prints the notes for the installed version, with a version selector for
   older releases.
2. A one-time **takeover**: for a short, configurable window (default **25
   seconds**) after the binary first renders under a new version, the
   statusline output is replaced by a release-notes announcement. After the
   window passes, the next refresh goes back to the normal statusline and the
   announcement never returns (until the next upgrade).

## Decisions already made (do not re-litigate)

- Notes are **embedded at build time** (`go:embed` of `CHANGELOG.md`). No
  network, ever, on any path.
- The takeover **replaces the entire output**, and renders the **same number
  of lines** as the user's normal statusline would have occupied, so the
  terminal UI doesn't jump.
- The window is **time-based** from the first render under the new version
  (default 25s), then it disappears at the next refresh.
- Takeover fires **only on upgrades** — a first-ever install records the
  current version silently and shows nothing.
- `release-notes` prints the **current version by default**, takes an optional
  version argument, and `--all` prints the whole changelog.
- Settings live in a top-level **`[release_notes]`** table in config.toml (not
  a segment, no TUI work required).
- The takeover banner must mention how to see more and that it's
  configurable, e.g. `claude-statusline release-notes · [release_notes] in
  config.toml`.

## CHANGELOG.md (new file, repo root)

Create `CHANGELOG.md`. Format is strict so the parser stays trivial:

```markdown
# Changelog

## v1.1.0 — 2026-06-XX
- `release-notes` subcommand: print notes for the current or any past version
- Post-upgrade announcement: the statusline shows what's new for 25s after an
  update (configurable via `[release_notes]`)

## v1.0.2 — 2026-06-05
- fix(install): honor CLAUDE_CONFIG_DIR when resolving settings.json
```

Rules:

- Version sections start with `## v<MAJOR.MINOR.REVISION> — <YYYY-MM-DD>`
  (em dash). Bullets are plain `- ` lines. Nothing else is significant.
- Newest first.
- Seed the file with **all existing tags** (`git tag` shows v0.1.0 → v1.0.2);
  pull bullet content from the GitHub release notes (`gh release view vX.Y.Z`)
  or, where empty, summarize the commits between tags (`git log vA..vB
  --oneline`). Keep 2–5 bullets per version, user-facing wording (features and
  fixes, not refactors).
- Add the in-progress section for the version this feature ships in at the
  top (see Versioning in CLAUDE.md: this is a feature → REVISION or MINOR
  bump; use **v1.1.0** since this is a user-visible milestone).

## New file: `releasenotes.go`

All logic lives here (plus a small hook in `cmd.go` and `render.go`-adjacent
code in `runRender`). Use the standard section-divider comment style.

### Embedding and parsing

```go
//go:embed CHANGELOG.md
var changelogRaw string

// releaseNote is one version's section of CHANGELOG.md.
type releaseNote struct {
	Version string // "1.0.2" (no leading v)
	Date    string // "2026-06-05", may be empty
	Bullets []string
}

// parseChangelog splits the embedded changelog into sections, newest first.
func parseChangelog(raw string) []releaseNote
```

Parser: scan lines; `## ` starts a section — first whitespace-delimited token
after `## ` (trimmed of `v`) is the version, the part after the em dash (if
any) is the date; `- ` lines inside a section are bullets (trim the marker).
Ignore everything else. Malformed input never panics — worst case returns an
empty/partial slice.

### The `release-notes` subcommand

Add a `case "release-notes":` to the dispatch in `cmd.go`, calling
`runReleaseNotes(os.Args[2:])`. The existing `strings.TrimLeft(os.Args[1],
"-")` means `--release-notes` works with zero extra code — same as the other
legacy flag spellings.

`runReleaseNotes(args []string)` behavior:

- No args → print the section matching the installed version
  (`versionString()`). If the installed version has no section (e.g. `dev`
  source builds, or a stale changelog), print the **newest** section with a
  one-line header noting the mismatch:
  `no notes for v<dev>; showing latest (v1.0.2):`.
- One arg `v1.0.1` / `1.0.1` → that version's section; unknown version →
  print `no release notes for "v1.0.1" (known: v1.1.0, v1.0.2, …)` to stderr,
  exit 1.
- `--all` or `all` → every section, newest first, blank line between.

Output format (per section), colored via the palette (so `NO_COLOR` /
`TERM=dumb` degrade to plain text automatically — get colors with
`currentPalette(loadConfig())` like other paths do):

```
claude-statusline v1.0.2 — 2026-06-05
  • fix(install): honor CLAUDE_CONFIG_DIR when resolving settings.json
```

Version line bold/primary-role, bullets plain. Never use hardcoded ANSI
codes; go through the palette per the colors convention.

### Config: `[release_notes]` table

Extend `config` in `config.go` (after `State`, before `Plugins` to keep
tables grouped):

```go
ReleaseNotes releaseNotesConfig `toml:"release_notes,omitempty"`
```

```go
// releaseNotesConfig is the [release_notes] table in config.toml.
type releaseNotesConfig struct {
	Announce        *bool `toml:"announce,omitempty"`         // default true
	DurationSeconds *int  `toml:"duration_seconds,omitempty"` // default 25, 0 disables
}

func (r releaseNotesConfig) announce() bool      // nil or true → true
func (r releaseNotesConfig) duration() time.Duration // nil → 25s; 0 → 0 (off)
```

`validateConfig` additions (same warn-and-normalize pattern as the rest of
that function): `duration_seconds` outside **0–600** → warning
`"%d out of range 0-600 (using 25)"`, reset the pointer to nil.

`mergeWithDefaults` must copy `loaded.ReleaseNotes` across (one line — easy
to forget; there's a test for it below).

### Version-seen state

New file `$XDG_STATE_HOME/claude-statusline/last-version.json` (sibling of
`sessions/` and `plugins/` — use `stateBaseDir()` from state.go):

```json
{"version": "1.0.2", "first_seen": 1718200000}
```

- `version`: last version this machine rendered with.
- `first_seen`: the takeover window anchor — unix seconds of the render that
  **opened a window** (an upgrade). 0 means no window: fresh installs and
  versions recorded while announcements were disabled must use 0, otherwise
  the same-version window row below would flash the banner on the renders
  *after* the recording one.

Helpers in `releasenotes.go`:

```go
type versionSeen struct {
	Version   string `json:"version"`
	FirstSeen int64  `json:"first_seen"`
}

func loadVersionSeen() (versionSeen, bool) // ok=false on missing/corrupt
func saveVersionSeen(v versionSeen) error  // writeFileAtomic; caller decides
```

Corrupt or unreadable file behaves like a missing file (treat as fresh
install → record silently, never announce). Writes use `writeFileAtomic`
(config.go) — concurrent sessions racing on this file is benign (last writer
wins; both saw the same decision).

### Takeover decision (pure, unit-testable)

```go
// announceDecision: should this render be replaced by the announcement?
//   prev, prevOK — loaded versionSeen state
//   current      — versionString() version ("dev" for source builds)
//   now          — render clock
// Returns show (replace output this render) and next (state to persist,
// zero-value = nothing to write).
func announceDecision(prev versionSeen, prevOK bool, current string,
	cfg releaseNotesConfig, now time.Time) (show bool, next versionSeen)
```

Truth table:

| condition | show | persist |
|---|---|---|
| `current == "dev"` or `current == ""` | false | nothing (never touch state from dev builds) |
| `cfg.announce() == false` or `cfg.duration() == 0` | false | `{current, now}` if version changed/missing (keep state current so re-enabling later doesn't fire for an old upgrade) |
| `!prevOK` (fresh install) | false | `{current, 0}` (no window — `{current, now}` would announce on the next render) |
| `prev.Version != current` (upgrade — or downgrade, treated the same) | **true** | `{current, now}` |
| same version, `now < first_seen + duration` | **true** | nothing |
| same version, window passed | false | nothing |

### Render hook (`runRender` in cmd.go)

After `buildStatusline` returns `lines`, before printing:

```go
lines = maybeReleaseTakeover(cfg.ReleaseNotes, lines, colors, terminalWidth(p), start)
```

`maybeReleaseTakeover`:

1. `loadVersionSeen()` → `announceDecision(...)`. If `next` is non-zero,
   `saveVersionSeen(next)` (do this even when show=false — fresh-install
   recording). If the save **fails**, force `show = false`: showing a
   takeover we can't record as shown would replay it on every render until
   the state dir becomes writable. If `show` is false, return `lines`
   unchanged.
2. Otherwise build the announcement at exactly `max(len(lines), 1)` lines via
   `announceLines(note, n, width, colors)` and return it.

Crucial: when the decision machinery is **not** triggered (same version,
window long passed — the 99.99% case), the only added cost on the sacred
render path is one small `os.ReadFile`. No subprocesses, no network, no
stdout/stderr side effects, ever. Read/write errors are silently swallowed.

The elapsed-ms suffix that `runRender` appends to `lines[0]` stays untouched
— the takeover lines flow through the existing print loop unmodified.

### Announcement layout (`announceLines`)

Pure function: `announceLines(note releaseNote, n int, width int, colors palette) []string`.

Content, in priority order (then pad with `""` / drop from the end to hit
exactly `n` lines):

1. Line 1 (always): `✨ claude-statusline updated to v<X.Y.Z>` — if n == 1,
   instead compress everything to one line:
   `✨ claude-statusline v<X.Y.Z> — <first bullet, truncated> · claude-statusline release-notes`
2. Lines 2..n-1: changelog bullets, ` • <bullet>`, as many as fit.
3. Last line (when n ≥ 2): the hint —
   `↳ claude-statusline release-notes · configure: [release_notes] in config.toml`

Every line is ANSI-aware-truncated to `width` (reuse the existing
width/truncate helpers in render.go that reflow uses — do not write a new
ANSI-stripper). Colors via palette roles (e.g. primary for the version line,
dim for the hint); zero-value palette must yield clean plain text. Match the
leading-padding behavior of normal lines (`[style] padding`, default 1
space) so the takeover doesn't shift horizontally.

If the current version has no changelog section, fall back to the newest
section's bullets but keep the real version in line 1; if the changelog is
empty entirely, render line 1 + hint only.

## What must NOT change

- Goldens and `classic` byte-identity. Test/golden renders run with
  `version == "dev"`, which short-circuits the whole feature — verify no
  golden churn without `-update`.
- The bare render path for the post-window steady state: output identical to
  today (the ms-timing suffix, separators, reflow — all untouched).
- `debug`, `configure`, `install` behavior. The TUI needs **no** changes
  (no flyout, no preview of the takeover). Out of scope.
- Never print hints/warnings to stdout or stderr from the render path —
  including when `last-version.json` is unwritable.

## Tests

Match existing style: table tests, fixed clocks (`testNow` pattern), temp
dirs via `t.TempDir()` + `XDG_STATE_HOME`. New file `releasenotes_test.go`.

1. **`parseChangelog`** — table: well-formed multi-section file; missing
   date; stray prose between sections; empty input; bullets with `- ` inside
   text. Assert version/date/bullets.
2. **`announceDecision`** — table covering every row of the truth table
   above, including: dev build never shows and never persists; fresh install
   persists but doesn't show; upgrade shows and persists `{current, now}`;
   within-window same-version shows without persisting; expired window
   neither shows nor persists; `announce = false` and `duration_seconds = 0`
   suppress but still persist on version change.
3. **`announceLines`** — n=1 compressed form; n=3 (header/bullet/hint);
   n=8 with 2 bullets (padding to exact count); long bullet truncated at
   width; empty palette output contains no escape bytes (`\x1b`).
4. **Round-trip** — `loadVersionSeen`/`saveVersionSeen` with
   `XDG_STATE_HOME=t.TempDir()`: save→load equality; corrupt JSON → ok=false;
   missing dir → save creates it (MkdirAll like `state.Save`).
5. **Config** — `[release_notes]` TOML round-trips through
   `marshalConfigTOML`/decode; `mergeWithDefaults` preserves it;
   `duration_seconds = 9999` warns and resets; defaults: `announce()` true,
   `duration()` 25s.
6. **`runReleaseNotes` selection logic** — factor printing so the
   section-selection (current/arg/all/unknown) is testable without capturing
   stdout, or capture stdout per the existing test helpers if any.
7. **Goldens** — `go test ./...` green with zero golden changes.

## Documentation updates (required, same PR)

1. `help.go` — add `release-notes` to the Commands block:
   `release-notes  Show what changed in this version (also: vX.Y.Z, --all).`
   and add `[release_notes]` one-liner to the Configuration block:
   `announce (default true), duration_seconds (default 25, 0 = off)`.
2. `README.md` — command table row for `release-notes`; a short
   "What's new announcement" subsection: what the takeover is, the 25s
   default, how to disable (`announce = false` or `duration_seconds = 0`).
3. `config.toml.example` — commented block:

   ```toml
   # Post-upgrade announcement: after updating, the statusline shows the
   # release notes for a short window, then returns to normal.
   # [release_notes]
   # announce = true
   # duration_seconds = 25   # 0 disables the takeover entirely
   ```

4. `CLAUDE.md` — Releases section: add "update `CHANGELOG.md` (new `## vX.Y.Z`
   section at the top) before tagging — the release-notes feature embeds it
   at build time". Key-subsystems list: one bullet for
   `releasenotes.go`. **Copy CLAUDE.md over AGENTS.md after editing.**

## Implementation order

1. `CHANGELOG.md` seeded from tags/releases (+ the new version's section).
2. `releasenotes.go`: embed + `parseChangelog` (+ test 1).
3. Config table + validation + merge (+ test 5).
4. `versionSeen` load/save (+ test 4) and `announceDecision` (+ test 2).
5. `announceLines` (+ test 3).
6. Wire-up: `runReleaseNotes` + cmd.go dispatch case (+ test 6);
   `maybeReleaseTakeover` call in `runRender`.
7. Docs (help.go, README, config.toml.example, CLAUDE.md → AGENTS.md copy).
8. `go test ./...` + manual smoke (isolated env — never touch the real
   config/state):

   ```bash
   go build -o claude-statusline .
   mkdir -p /tmp/fake-home
   alias sl='HOME=/tmp/fake-home XDG_STATE_HOME=/tmp/fake-home/state XDG_CONFIG_HOME=/tmp/fake-home/config ./claude-statusline'
   # dev build: takeover must never fire
   echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | sl
   # simulate a release build + upgrade:
   go build -ldflags "-X main.version=1.0.9" -o claude-statusline . && echo '…payload…' | sl   # records silently (fresh install)
   go build -ldflags "-X main.version=1.1.0" -o claude-statusline . && echo '…payload…' | sl   # takeover appears
   sleep 26 && echo '…payload…' | sl                                                           # normal statusline again
   sl release-notes && sl release-notes v1.0.2 && sl release-notes --all
   ```

## Acceptance criteria

- [ ] `go test ./...` passes; zero golden changes.
- [ ] `claude-statusline release-notes` prints the installed version's notes;
      `release-notes v1.0.2` and `release-notes --all` work; unknown version
      exits 1 with the known-versions hint on stderr.
- [ ] `--release-notes` flag spelling works (free via existing dispatch).
- [ ] First-ever run on a machine records the version and shows **no**
      takeover.
- [ ] After a version change, renders within 25s (default) show the
      announcement at exactly the line count the normal statusline would
      have used; the next render after the window is the normal statusline.
- [ ] `announce = false` or `duration_seconds = 0` fully disables the
      takeover (and version state still advances so re-enabling doesn't
      fire for an old upgrade).
- [ ] `version == "dev"` builds never announce and never write
      `last-version.json`.
- [ ] `NO_COLOR=1` / `TERM=dumb` produce escape-free announcement and
      subcommand output.
- [ ] An unwritable state dir degrades silently — normal statusline output
      (the takeover is suppressed, not replayed), nothing on stderr.
- [ ] README, help.go, config.toml.example, CLAUDE.md + AGENTS.md updated
      (last two identical).
