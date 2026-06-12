# Spec: Async plugins (stale-while-revalidate)

Status: implemented
Target: claude-statusline (this repo), Go, `package main`

## Problem

Plugins (`[[plugins]]` in config.toml) are external commands exec'd **synchronously**
during every render (`runPluginRaw` in `plugins.go`), with a default 200ms timeout.
Any plugin slower than that either blocks the statusline or gets killed and shows
nothing. Commands like `kubectl`, `gh api`, or a weather fetch can never work.

## Solution overview

Add an opt-in `async = true` mode per plugin. Async plugins never block a render:

1. The render reads the plugin's **last cached output** from a cache file and
   displays it immediately (segment hides if no cache yet — consistent with the
   "segments auto-hide when data is missing" convention).
2. If the cache is missing or older than `refresh_ms`, the render spawns a
   **detached background refresher** and does not wait for it. The refresher
   runs the plugin command (with its own timeout), writes stdout to a temp
   file, and atomically renames it over the cache file. The *next* render picks
   up the fresh value.

The binary is short-lived (Claude Code execs it per update), so the refresher
must survive the parent exiting: it is implemented as a **hidden subcommand of
this same binary** (`claude-statusline plugin-refresh`), spawned detached.

Synchronous plugins (no `async` key, or `async = false`) keep **byte-identical
current behavior**. The bare no-args render path's behavior for existing
configs must not change in any way.

## Config schema

Extend `pluginDef` in `config.go`:

```go
type pluginDef struct {
	ID        string        `json:"id" toml:"id,omitempty"`
	Command   string        `json:"command" toml:"command"`
	Line      int           `json:"line" toml:"line,omitempty"`
	Desc      string        `json:"desc" toml:"desc,omitempty"`
	TimeoutMS int           `json:"timeout_ms" toml:"timeout_ms,omitempty"`
	Async     bool          `json:"async" toml:"async,omitempty"`       // NEW
	RefreshMS int           `json:"refresh_ms" toml:"refresh_ms,omitempty"` // NEW
	Fields    []pluginField `json:"fields" toml:"fields,omitempty"`
}
```

Semantics:

- `async` (bool, default false): opt into stale-while-revalidate.
- `refresh_ms` (int): how stale the cache may get before a background refresh
  is triggered. Only meaningful when `async = true`. Default **5000**.
  `validateConfig` (config.go) clamps values below **500** up to 500 and emits
  a warning (same pattern as other normalizations there); values when
  `async = false` are ignored silently.
- `timeout_ms` keeps its current meaning for sync plugins (default 200).
  For async plugins it bounds the **background** run instead: default **10000**,
  clamp to max **60000** with a warning. (A slow async plugin can afford a
  generous timeout because nothing is waiting on it.)

Example config (add to `config.toml.example` as a commented block, and to the
README plugin section):

```toml
# Async plugin: never blocks the render; shows the last cached value and
# refreshes it in the background at most every refresh_ms.
# [[plugins]]
# id = "k8s-context"
# command = "~/.config/claude-statusline/plugins/k8s.sh"
# async = true
# refresh_ms = 10000   # consider cache stale after 10s
# timeout_ms = 8000    # kill the background run after 8s
```

## Cache layout

New directory: sibling of the existing sessions state dir.

- `$XDG_STATE_HOME/claude-statusline/plugins/` (fallback
  `~/.local/state/claude-statusline/plugins/`). Add a `pluginCacheDir()`
  helper next to `stateDir()` in `state.go` (or in `plugins.go`), factoring
  the shared base-dir logic so the two stay consistent.
- Cache file per plugin: `<key>.out` where `key` is the first 16 hex chars of
  `sha256(def.Command)`. Keying by command (not session/dir) means one cache
  is shared across sessions; that is intentional and acceptable — async
  plugins are for environment-level data. Document this in the README.
- Lock/in-progress marker: `<key>.lock` in the same dir.
- Temp file for atomic write: `<key>.out.tmp` (write, then `os.Rename`).

Cache file content is the plugin's **raw trimmed stdout** (same string
`runPluginRaw` returns today). Multi-field plugins parse it with the existing
`parseKeyValueOutput` — no format change.

## Render-path behavior (plugins.go)

Replace the body of the segment `render` closures' call into a dispatcher:

```go
// runPluginRaw becomes the dispatcher:
func runPluginRaw(def pluginDef, p payload) string {
	if def.Async {
		return readAsyncPlugin(def, p)
	}
	return runPluginSync(def, p) // current runPluginRaw body, renamed
}
```

`readAsyncPlugin(def, p)`:

1. Compute cache path. `os.ReadFile` it. Missing file → cached value is `""`.
2. Decide whether to trigger a refresh via a pure helper (unit-testable):

   ```go
   // needsRefresh reports whether the cache (mtime, possibly zero when the
   // file is missing) is stale relative to refresh_ms at time now.
   func needsRefresh(mtime time.Time, now time.Time, refresh time.Duration) bool
   ```

   Missing file (zero mtime) → true. `now.Sub(mtime) >= refresh` → true.
3. If stale: attempt to acquire the lock — `os.OpenFile(lock, O_CREATE|O_EXCL, 0o644)`.
   - Acquired → spawn the detached refresher (below), close the fd. The
     **refresher** removes the lock when done; the render does not.
   - `EEXIST` → a refresh is already in flight; check the lock's mtime. If it
     is older than `timeout + 5s` (a crashed/killed refresher), remove it and
     try once more to acquire; otherwise skip spawning.
   - Any other error (e.g. cache dir missing) → `os.MkdirAll` the dir first;
     on persistent error, skip the refresh silently. **Never** let cache/lock
     errors affect render output or print anything.
4. Return the cached value (trimmed). Empty → segment hides via the existing
   `out != ""` check in `initSegments`.

The existing per-render `pluginCache` map continues to work unchanged for
multi-field async plugins (one file read + parse per command per render).

## Detached refresher

### Spawning (from the render path)

Spawn this same binary with a hidden subcommand, fully detached:

```go
exe, err := os.Executable()        // on error, skip the refresh silently
c := exec.Command(exe, "plugin-refresh")
c.Env = append(os.Environ(), refresher env vars...)
c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
applyDetachSysProcAttr(c)          // platform-specific, see below
err = c.Start()                    // no Wait; on error remove the lock
_ = c.Process.Release()
```

Configuration crosses the process boundary via env vars (keeps argv free of
user-controlled strings and reuses the existing env-passing idiom):

- `STATUSLINE_REFRESH_COMMAND` — `def.Command` (pre-`~` expansion; the
  refresher expands it the same way `runPluginSync` does).
- `STATUSLINE_REFRESH_TIMEOUT_MS` — resolved timeout (already clamped).
- `STATUSLINE_REFRESH_CACHE` — absolute cache file path.
- `STATUSLINE_REFRESH_LOCK` — absolute lock file path.
- Plus the full set of normal plugin env vars (`STATUSLINE_MODEL`,
  `STATUSLINE_DIR`, `STATUSLINE_BRANCH`, `STATUSLINE_SESSION`,
  `STATUSLINE_PRODUCT`, `STATUSLINE_COLUMNS`, `STATUSLINE_LINES`,
  `STATUSLINE_PAYLOAD`) computed exactly as `runPluginSync` does today —
  factor that env-building block into a shared `pluginEnv(def, p)` helper so
  sync and async stay identical.

`applyDetachSysProcAttr` needs build-tagged files:

- `detach_unix.go` (`//go:build !windows`): `c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}`
- `detach_windows.go` (`//go:build windows`): `CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | windows DETACHED_PROCESS (0x00000008)`

### The `plugin-refresh` subcommand (cmd.go + plugins.go)

Add a case to the dispatch in `cmd.go`. It is **hidden**: not listed in
`help.go`, not in the README command table. It must never be reachable from
the bare no-args path.

Behavior (`runPluginRefresh()`):

1. Read the four `STATUSLINE_REFRESH_*` env vars; if command or cache path is
   empty, exit 1.
2. `defer os.Remove(lockPath)` — the lock must be released on every exit path.
3. Run the plugin command with `exec.CommandContext` and the timeout, same
   `~`-expansion and `.Output()` handling as `runPluginSync`. The plugin sees
   the inherited `STATUSLINE_*` env automatically.
4. On success (exit 0): write trimmed stdout to `<cache>.tmp` (0o644), then
   `os.Rename` over the cache path. Empty stdout is a valid result — write
   the empty file (the segment hides).
5. On failure (non-zero exit, timeout, exec error):
   - If a cache file already exists: `os.Chtimes(cache, now, now)` to bump its
     mtime — **stale data beats blank**, and bumping mtime prevents a retry
     storm (next attempt waits another `refresh_ms`).
   - If no cache exists: write an empty cache file (so the miss is also
     rate-limited by `refresh_ms`).
6. Opportunistic hygiene: delete `*.out` files in the plugins cache dir whose
   mtime is older than **7 days** (covers plugins removed from config). Same
   spirit as `pruneStateDir`.
7. Exit 0. The refresher never prints to stdout/stderr.

## What must NOT change

- The bare render path for configs without async plugins: goldens must pass
  untouched, and `classic` theme output stays byte-identical.
- Sync plugin behavior, including the 200ms default timeout.
- `parseKeyValueOutput`, the multi-field registration in `initSegments`, and
  the `STATUSLINE_*` env contract (the refactor into `pluginEnv` must be
  behavior-preserving).
- The TUI: no new UI is required. Async plugins in the TUI preview render via
  the same code path (cached value, possibly triggering a background refresh)
  — that is acceptable and needs no special casing.

## Tests

Match existing test style (table tests, `testNow`-style fixed clocks; no
sleeping where avoidable).

1. **`needsRefresh`** — table test: missing file (zero mtime), fresh, exactly
   at boundary, stale.
2. **Cache read path** — `TestAsyncPluginReadsCache`: point
   `XDG_STATE_HOME` at `t.TempDir()`, pre-write a cache file, build a
   `pluginDef{Async: true}`, assert `runPluginRaw` returns the cached content
   without executing the command (use a command path that would fail loudly,
   e.g. `/nonexistent`), and that a lock file appears when the cache is stale.
3. **Stampede** — with an existing fresh lock file, assert no second
   refresher spawn is attempted (assert the lock fd acquisition fails and the
   function still returns the cached value); with a lock older than
   timeout+5s, assert it is replaced.
4. **Refresher round-trip** — `TestPluginRefreshSubcommand`: set the
   `STATUSLINE_REFRESH_*` env to a trivial shell script (created in
   `t.TempDir()`, `chmod 0o755`) that echoes a known string; call
   `runPluginRefresh()` directly (not via exec); assert the cache file
   contains the trimmed output, the lock file is gone, and `.tmp` is gone.
5. **Refresher failure semantics** — script exits 1: pre-existing cache keeps
   its content but its mtime is bumped; with no pre-existing cache, an empty
   cache file is created.
6. **Config validation** — `refresh_ms = 100` clamps to 500 with a warning;
   async `timeout_ms = 0` resolves to 10000; `timeout_ms = 120000` clamps to
   60000.
7. **Goldens** — run `go test ./...`; no golden changes expected. Do not pass
   `-update`.

Note for tests on the detached-spawn step: do not test actual `os.Executable`
re-exec in unit tests; factor the spawn into a `var spawnRefresher = func(...)`
package variable and stub it where needed.

## Documentation updates (required, same PR)

1. `README.md` — extend the plugin section: an "Async plugins" subsection
   with the example above, the staleness/one-cycle-behind tradeoff, the
   cache-shared-across-sessions note, and the new `async`/`refresh_ms` rows in
   the plugin option table.
2. `config.toml.example` — the commented async example block shown above.
3. `CLAUDE.md` — in the Plugins bullet under "Key subsystems", append one
   sentence: async plugins read a cache under
   `$XDG_STATE_HOME/claude-statusline/plugins/` and refresh via a detached
   hidden `plugin-refresh` subcommand. **Copy CLAUDE.md over AGENTS.md after
   editing** (they must stay identical).
4. Do NOT add `plugin-refresh` to `help.go` or the README command list.

## Implementation order

1. `config.go`: `pluginDef` fields + `validateConfig` clamps (+ test 6).
2. `plugins.go`: factor `pluginEnv`, rename current `runPluginRaw` →
   `runPluginSync`, add dispatcher (goldens still green here).
3. Cache dir helper + `needsRefresh` (+ test 1).
4. `readAsyncPlugin` with lock protocol + stubbed `spawnRefresher`
   (+ tests 2, 3).
5. `runPluginRefresh` + `cmd.go` dispatch case (+ tests 4, 5).
6. Detach: `spawnRefresher` real implementation + `detach_unix.go` /
   `detach_windows.go`. Verify `GOOS=windows go build ./...` compiles.
7. Docs (README, config.toml.example, CLAUDE.md → AGENTS.md copy).
8. Full `go test ./...` + manual smoke test:

   ```bash
   go build -o claude-statusline .
   # isolated env so the real config is never touched/migrated:
   mkdir -p /tmp/fake-home
   HOME=/tmp/fake-home XDG_STATE_HOME=/tmp/fake-home/state \
     XDG_CONFIG_HOME=/tmp/fake-home/config ./claude-statusline debug < testdata/payloads/agy-full.json
   # then add an async [[plugins]] entry pointing at a script that sleeps 2s
   # and echoes text; render twice: first render shows nothing, a render
   # after ~3s shows the text.
   ```

## Acceptance criteria

- [ ] `go test ./...` passes; no golden file changes.
- [ ] A sync-only config renders byte-identically to before the change.
- [ ] An async plugin whose command sleeps 2s never delays a render; its
      value appears on the first render after the background run completes.
- [ ] Two back-to-back renders while a refresh is in flight spawn exactly one
      refresher (lock honored).
- [ ] Killing a refresher mid-run leaves a lock that is reaped (and refresh
      retried) once it is older than timeout+5s.
- [ ] A failing async command keeps showing the previous value and retries no
      more often than `refresh_ms`.
- [ ] `NO_COLOR`, `TERM=dumb`, and the bare-path "never print hints" rule are
      unaffected (the refresher writes nothing to stdout/stderr).
- [ ] `GOOS=windows go build ./...` succeeds.
- [ ] README, config.toml.example, CLAUDE.md and AGENTS.md updated (last two
      identical).
