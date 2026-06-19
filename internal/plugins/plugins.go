package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/segments"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/sys"
)

// ─── Plugin System ───────────────────────────────────────────────────

// pluginCache holds per-command parsed output within a single render turn so
// multi-field plugins run their command only once per turn.
var pluginCache map[string]map[string]string

// ClearCache resets the per-render plugin cache.
func ClearCache() {
	pluginCache = map[string]map[string]string{}
}

// Load registers segments for the configured plugins.
func Load(plugins []config.PluginDef) {
	for _, p := range plugins {
		def := p
		if len(def.Fields) > 0 {
			// Multi-field plugin: register one segment per field.
			for _, f := range def.Fields {
				field := f
				line := field.Line
				if line < 1 {
					line = 1
				}
				desc := field.Desc
				if desc == "" {
					desc = field.ID
				}
				segments.Register(segments.Info{
					ID:           field.ID,
					Line:         line,
					Desc:         desc + " [plugin]",
					PrimaryColor: "Dim",
					Render: func(ctx segments.RenderCtx) (string, bool) {
						out := RunPluginField(def, ctx.P, field.ID)
						return out, out != ""
					},
				})
			}
		} else {
			// Single-field plugin: whole stdout is the segment value.
			line := def.Line
			if line < 1 {
				line = 1
			}
			desc := def.Desc
			if desc == "" {
				desc = def.ID
			}
			segments.Register(segments.Info{
				ID:           def.ID,
				Line:         line,
				Desc:         desc + " [plugin]",
				PrimaryColor: "Dim",
				Render: func(ctx segments.RenderCtx) (string, bool) {
					out := RunPluginRaw(def, ctx.P)
					return out, out != ""
				},
			})
		}
	}
}

// RunPluginField runs a multi-field plugin (cached per command) and returns
// the value for the requested field ID.
func RunPluginField(def config.PluginDef, p payload.Payload, fieldID string) string {
	if pluginCache == nil {
		pluginCache = map[string]map[string]string{}
	}
	if _, ok := pluginCache[def.Command]; !ok {
		raw := RunPluginRaw(def, p)
		pluginCache[def.Command] = parseKeyValueOutput(raw)
	}
	return pluginCache[def.Command][fieldID]
}

// parseKeyValueOutput parses "key:value" lines from plugin stdout.
func parseKeyValueOutput(raw string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if key != "" {
				result[key] = val
			}
		}
	}
	return result
}

// RunPluginRaw dispatches to the sync or async plugin implementation.
func RunPluginRaw(def config.PluginDef, p payload.Payload) string {
	if def.Async {
		return readAsyncPlugin(def, p)
	}
	return runPluginSync(def, p)
}

// pluginEnv returns the STATUSLINE_* environment variables passed to every
// plugin. The caller prepends os.Environ().
func pluginEnv(def config.PluginDef, p payload.Payload) []string {
	session := p.SessionName
	if session == "" {
		session = p.ConversationID
	}
	env := []string{
		"STATUSLINE_MODEL=" + p.Model.DisplayName,
		"STATUSLINE_DIR=" + p.Workspace.CurrentDir,
		"STATUSLINE_BRANCH=" + p.Worktree.Branch,
		"STATUSLINE_SESSION=" + session,
		"STATUSLINE_PRODUCT=" + p.Product,
		"STATUSLINE_COLUMNS=" + strconv.Itoa(p.TerminalWidth),
		"STATUSLINE_LINES=" + os.Getenv("LINES"),
	}
	if raw, err := json.Marshal(p); err == nil {
		env = append(env, "STATUSLINE_PAYLOAD="+string(raw))
	}
	return env
}

// runPluginSync executes the plugin command and returns the full trimmed stdout.
func runPluginSync(def config.PluginDef, p payload.Payload) string {
	timeout := time.Duration(def.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := expandPluginCommand(def.Command)
	c := exec.CommandContext(ctx, cmd)
	c.Env = append(os.Environ(), pluginEnv(def, p)...)

	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func expandPluginCommand(command string) string {
	home, _ := os.UserHomeDir()
	return strings.Replace(command, "~", home, 1)
}

// ─── Async Plugin Cache ──────────────────────────────────────────────

// spawnRefresher starts a detached background refresh for an async plugin.
// Tests stub this package variable to avoid real process spawning.
var spawnRefresher = spawnRefresherReal

func spawnRefresherReal(def config.PluginDef, p payload.Payload, cachePath, lockPath string) {
	exe, err := os.Executable()
	if err != nil {
		_ = os.Remove(lockPath)
		return
	}
	c := exec.Command(exe, "plugin-refresh")
	c.Env = append(os.Environ(),
		"STATUSLINE_REFRESH_COMMAND="+def.Command,
		"STATUSLINE_REFRESH_TIMEOUT_MS="+strconv.Itoa(def.TimeoutMS),
		"STATUSLINE_REFRESH_CACHE="+cachePath,
		"STATUSLINE_REFRESH_LOCK="+lockPath,
	)
	c.Env = append(c.Env, pluginEnv(def, p)...)
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	sys.ApplyDetachSysProcAttr(c)
	if err := c.Start(); err != nil {
		_ = os.Remove(lockPath)
		return
	}
	_ = c.Process.Release()
}

// pluginCacheKey returns the first 16 hex characters of sha256(command).
func pluginCacheKey(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])[:16]
}

// pluginCachePaths returns the cache, lock, and temp file paths for a command.
func pluginCachePaths(command string) (cache, lock, tmp string) {
	base := filepath.Join(state.PluginCacheDir(), pluginCacheKey(command))
	return base + ".out", base + ".lock", base + ".out.tmp"
}

// needsRefresh reports whether the cache (mtime, possibly zero when the file is
// missing) is stale relative to refresh_ms at time now.
func needsRefresh(mtime time.Time, now time.Time, refresh time.Duration) bool {
	if mtime.IsZero() {
		return true
	}
	return now.Sub(mtime) >= refresh
}

// readAsyncPlugin returns the cached plugin output, triggering a background
// refresh when the cache is stale or missing.
func readAsyncPlugin(def config.PluginDef, p payload.Payload) string {
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	data, _ := os.ReadFile(cachePath)
	cached := strings.TrimSpace(string(data))

	var mtime time.Time
	if info, err := os.Stat(cachePath); err == nil {
		mtime = info.ModTime()
	}

	refresh := time.Duration(def.RefreshMS) * time.Millisecond
	if refresh <= 0 {
		refresh = 5000 * time.Millisecond
	}
	if needsRefresh(mtime, time.Now(), refresh) {
		trySpawnRefresher(def, p, cachePath, lockPath)
	}

	return cached
}

// trySpawnRefresher acquires the plugin lock and, if successful, starts a
// background refresh. It silently ignores cache/lock errors so the render is
// never delayed.
func trySpawnRefresher(def config.PluginDef, p payload.Payload, cachePath, lockPath string) {
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return
	}
	timeout := time.Duration(def.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10000 * time.Millisecond
	}
	if sys.TryAcquireLock(lockPath, timeout+5*time.Second) {
		spawnRefresher(def, p, cachePath, lockPath)
	}
}

// ─── Plugin Refresh Subcommand ───────────────────────────────────────

// RunPluginRefresh executes the hidden "plugin-refresh" subcommand. It reads
// configuration from STATUSLINE_REFRESH_* env vars, runs the plugin command,
// atomically updates the cache, and releases the lock. It returns an error
// only when invoked without the required env vars; plugin execution failures
// are handled internally and do not produce an error.
func RunPluginRefresh() error {
	command := os.Getenv("STATUSLINE_REFRESH_COMMAND")
	timeoutMS, _ := strconv.Atoi(os.Getenv("STATUSLINE_REFRESH_TIMEOUT_MS"))
	cachePath := os.Getenv("STATUSLINE_REFRESH_CACHE")
	lockPath := os.Getenv("STATUSLINE_REFRESH_LOCK")
	if command == "" || cachePath == "" {
		return errors.New("missing STATUSLINE_REFRESH_COMMAND or STATUSLINE_REFRESH_CACHE")
	}
	defer func() { _ = os.Remove(lockPath) }()

	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := expandPluginCommand(command)
	c := exec.CommandContext(ctx, cmd)
	// Inherit the normal STATUSLINE_* env, but strip the refresher's internal
	// control variables so plugins do not depend on them.
	c.Env = filterRefresherEnv(os.Environ())
	out, err := c.Output()

	now := time.Now()
	if err == nil {
		trimmed := strings.TrimSpace(string(out))
		// pluginCachePaths returns tmp as cachePath + ".tmp".
		tmpPath := cachePath + ".tmp"
		writeErr := os.WriteFile(tmpPath, []byte(trimmed), 0o644)
		if writeErr == nil {
			_ = os.Rename(tmpPath, cachePath)
		}
		_ = os.Remove(tmpPath)
	} else {
		if _, statErr := os.Stat(cachePath); statErr == nil {
			_ = os.Chtimes(cachePath, now, now)
		} else {
			_ = os.WriteFile(cachePath, nil, 0o644)
		}
	}

	prunePluginCacheDir(filepath.Dir(cachePath))
	return nil
}

// filterRefresherEnv removes STATUSLINE_REFRESH_* variables from the env slice.
func filterRefresherEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "STATUSLINE_REFRESH_") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// prunePluginCacheDir removes cache, lock, and temp files older than 7 days,
// covering plugins that have been removed from config.
func prunePluginCacheDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".out") && !strings.HasSuffix(name, ".lock") && !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
