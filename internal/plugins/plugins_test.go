package plugins

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// writeScript drops an executable shell script into a temp dir.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginSingleField(t *testing.T) {
	def := config.PluginDef{ID: "hello", Command: writeScript(t, `echo "hello world"`), TimeoutMS: 5000}
	if got := RunPluginRaw(def, payload.Payload{}); got != "hello world" {
		t.Errorf("RunPluginRaw = %q, want %q", got, "hello world")
	}
}

func TestPluginMultiField(t *testing.T) {
	ClearCache()
	def := config.PluginDef{
		Command:   writeScript(t, "echo cpu:42%\necho mem: 73%"),
		Fields:    []config.PluginField{{ID: "cpu"}, {ID: "mem"}},
		TimeoutMS: 5000,
	}
	if got := RunPluginField(def, payload.Payload{}, "cpu"); got != "42%" {
		t.Errorf("cpu = %q, want %q", got, "42%")
	}
	if got := RunPluginField(def, payload.Payload{}, "mem"); got != "73%" {
		t.Errorf("mem = %q, want %q", got, "73%")
	}
}

func TestLoadMarksSegmentsAsPlugins(t *testing.T) {
	segments.Init()
	script := writeScript(t, `echo hello`)
	Load([]config.PluginDef{{ID: "hello", Command: script}})

	seg, ok := segments.ByID("hello")
	if !ok {
		t.Fatal("expected plugin segment to be registered")
	}
	if !seg.Plugin {
		t.Errorf("plugin segment Plugin = %v, want true", seg.Plugin)
	}
	if !strings.HasSuffix(seg.Desc, " [plugin]") {
		t.Errorf("plugin segment Desc = %q, want [plugin] suffix", seg.Desc)
	}

	builtIn, ok := segments.ByID("model")
	if !ok {
		t.Fatal("expected built-in segment")
	}
	if builtIn.Plugin {
		t.Errorf("built-in segment Plugin = %v, want false", builtIn.Plugin)
	}
}

func TestPluginPreviewSingleField(t *testing.T) {
	segments.Init()
	script := writeScript(t, `echo "real output"`)
	Load([]config.PluginDef{{ID: "demo", Command: script, Preview: "preview output"}})

	seg, ok := segments.ByID("demo")
	if !ok {
		t.Fatal("expected plugin segment to be registered")
	}
	if seg.Preview != "preview output" {
		t.Errorf("Preview = %q, want %q", seg.Preview, "preview output")
	}

	out, show := seg.Render(segments.RenderCtx{P: payload.Payload{}, Preview: false})
	if !show || out != "real output" {
		t.Errorf("normal render = %q, show=%v, want %q shown", out, show, "real output")
	}

	out, show = seg.Render(segments.RenderCtx{P: payload.Payload{}, Preview: true})
	if !show || out != "preview output" {
		t.Errorf("preview render = %q, show=%v, want %q shown", out, show, "preview output")
	}
}

func TestPluginPreviewMultiField(t *testing.T) {
	segments.Init()
	script := writeScript(t, "echo cpu:real-cpu\necho mem:real-mem")
	Load([]config.PluginDef{{
		Command: script,
		Fields: []config.PluginField{
			{ID: "cpu", Preview: "preview-cpu"},
			{ID: "mem", Preview: "preview-mem"},
		},
	}})

	cpu, ok := segments.ByID("cpu")
	if !ok {
		t.Fatal("expected cpu segment")
	}
	out, show := cpu.Render(segments.RenderCtx{P: payload.Payload{}, Preview: true})
	if !show || out != "preview-cpu" {
		t.Errorf("cpu preview = %q, show=%v, want %q", out, show, "preview-cpu")
	}

	mem, ok := segments.ByID("mem")
	if !ok {
		t.Fatal("expected mem segment")
	}
	out, show = mem.Render(segments.RenderCtx{P: payload.Payload{}, Preview: true})
	if !show || out != "preview-mem" {
		t.Errorf("mem preview = %q, show=%v, want %q", out, show, "preview-mem")
	}

	// Non-preview mode still runs the real command.
	out, show = cpu.Render(segments.RenderCtx{P: payload.Payload{}, Preview: false})
	if !show || out != "real-cpu" {
		t.Errorf("cpu normal = %q, show=%v, want %q", out, show, "real-cpu")
	}
}

func TestPluginEmptyPreviewFallsBackToRealCommand(t *testing.T) {
	segments.Init()
	script := writeScript(t, `echo "real output"`)
	Load([]config.PluginDef{{ID: "demo", Command: script, Preview: ""}})

	seg, ok := segments.ByID("demo")
	if !ok {
		t.Fatal("expected plugin segment")
	}
	out, show := seg.Render(segments.RenderCtx{P: payload.Payload{}, Preview: true})
	if !show || out != "real output" {
		t.Errorf("empty preview should fall back to real command, got %q show=%v", out, show)
	}
}

func TestAsyncPluginPreviewShortCircuits(t *testing.T) {
	segments.Init()
	calls := stubSpawnRefresher(t)
	Load([]config.PluginDef{{
		ID:        "async-demo",
		Command:   "/nonexistent",
		Async:     true,
		RefreshMS: 5000,
		TimeoutMS: 500,
		Preview:   "async-preview",
	}})

	seg, ok := segments.ByID("async-demo")
	if !ok {
		t.Fatal("expected plugin segment")
	}
	out, show := seg.Render(segments.RenderCtx{P: payload.Payload{}, Preview: true})
	if !show || out != "async-preview" {
		t.Errorf("preview = %q, show=%v, want %q shown", out, show, "async-preview")
	}
	if len(*calls) != 0 {
		t.Errorf("preview should not spawn background refresher, got %d calls", len(*calls))
	}
}

func TestPluginTimeout(t *testing.T) {
	def := config.PluginDef{ID: "slow", Command: writeScript(t, "sleep 5; echo done"), TimeoutMS: 50}
	if got := RunPluginRaw(def, payload.Payload{}); got != "" {
		t.Errorf("timed-out plugin should return empty, got %q", got)
	}
}

func TestPluginNonZeroExit(t *testing.T) {
	def := config.PluginDef{ID: "fail", Command: writeScript(t, "echo oops; exit 3"), TimeoutMS: 5000}
	if got := RunPluginRaw(def, payload.Payload{}); got != "" {
		t.Errorf("failing plugin should return empty, got %q", got)
	}
}

func TestPluginMissingExecutable(t *testing.T) {
	def := config.PluginDef{ID: "ghost", Command: "/nonexistent/plugin.sh"}
	if got := RunPluginRaw(def, payload.Payload{}); got != "" {
		t.Errorf("missing plugin should return empty, got %q", got)
	}
}

func TestParseKeyValueOutput(t *testing.T) {
	out := parseKeyValueOutput("cpu:42\n  mem : 73 \nnocolon\n:novalue\nurl:http://x:1\n")
	if out["cpu"] != "42" {
		t.Errorf("cpu = %q", out["cpu"])
	}
	if out["mem"] != "73" {
		t.Errorf("whitespace not trimmed: %q", out["mem"])
	}
	if _, ok := out["nocolon"]; ok {
		t.Error("line without colon should be skipped")
	}
	if _, ok := out[""]; ok {
		t.Error("empty key should be skipped")
	}
	if out["url"] != "http://x:1" {
		t.Errorf("only first colon splits: %q", out["url"])
	}
}

// ─── Async plugin tests ──────────────────────────────────────────────

type spawnCall struct {
	def       config.PluginDef
	cachePath string
	lockPath  string
}

// stubSpawnRefresher replaces spawnRefresher with a recorder for the duration
// of the test, avoiding real process execs.
func stubSpawnRefresher(t *testing.T) *[]spawnCall {
	t.Helper()
	var calls []spawnCall
	old := spawnRefresher
	spawnRefresher = func(def config.PluginDef, p payload.Payload, cachePath, lockPath string) {
		calls = append(calls, spawnCall{def: def, cachePath: cachePath, lockPath: lockPath})
	}
	t.Cleanup(func() { spawnRefresher = old })
	return &calls
}

func setRefreshEnv(t *testing.T, def config.PluginDef, cachePath, lockPath string) {
	t.Helper()
	t.Setenv("STATUSLINE_REFRESH_COMMAND", def.Command)
	t.Setenv("STATUSLINE_REFRESH_TIMEOUT_MS", strconv.Itoa(def.TimeoutMS))
	t.Setenv("STATUSLINE_REFRESH_CACHE", cachePath)
	t.Setenv("STATUSLINE_REFRESH_LOCK", lockPath)
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Unix(1000000, 0)
	refresh := 5 * time.Second
	cases := []struct {
		name  string
		mtime time.Time
		want  bool
	}{
		{"missing", time.Time{}, true},
		{"fresh", now.Add(-4 * time.Second), false},
		{"boundary", now.Add(-5 * time.Second), true},
		{"stale", now.Add(-6 * time.Second), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsRefresh(tc.mtime, now, refresh); got != tc.want {
				t.Errorf("needsRefresh(%v, %v, %v) = %v, want %v", tc.mtime, now, refresh, got, tc.want)
			}
		})
	}
}

func TestAsyncPluginReadsCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	ClearCache()
	calls := stubSpawnRefresher(t)

	def := config.PluginDef{Async: true, Command: "/nonexistent", RefreshMS: 5000, TimeoutMS: 500}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)

	// Missing cache: empty result, refresh triggered.
	if got := RunPluginRaw(def, payload.Payload{}); got != "" {
		t.Errorf("missing cache = %q, want empty", got)
	}
	if len(*calls) != 1 {
		t.Errorf("spawn calls = %d, want 1", len(*calls))
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("expected lock file for missing cache")
	}

	// Fresh cache: cached value, no new refresh.
	if err := os.WriteFile(cachePath, []byte("cached-value"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RunPluginRaw(def, payload.Payload{}); got != "cached-value" {
		t.Errorf("fresh cache = %q, want %q", got, "cached-value")
	}
	if len(*calls) != 1 {
		t.Errorf("fresh cache should not spawn, got %d calls", len(*calls))
	}

	// Stale cache: cached value, refresh triggered again.
	_ = os.Remove(lockPath)
	_ = os.Chtimes(cachePath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if got := RunPluginRaw(def, payload.Payload{}); got != "cached-value" {
		t.Errorf("stale cache = %q, want %q", got, "cached-value")
	}
	if len(*calls) != 2 {
		t.Errorf("stale cache should spawn again, got %d calls", len(*calls))
	}
}

func TestAsyncPluginStampedeLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	ClearCache()
	calls := stubSpawnRefresher(t)

	def := config.PluginDef{Async: true, Command: "/nonexistent", RefreshMS: 5000, TimeoutMS: 500}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Stale cache with a fresh lock in place: no second spawn.
	if err := os.WriteFile(cachePath, []byte("value"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(cachePath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RunPluginRaw(def, payload.Payload{}); got != "value" {
		t.Errorf("locked cache = %q, want %q", got, "value")
	}
	if len(*calls) != 0 {
		t.Errorf("fresh lock should block spawn, got %d calls", len(*calls))
	}

	// Stale cache with an old lock: lock is reaped and refresh is spawned.
	_ = os.Chtimes(lockPath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if got := RunPluginRaw(def, payload.Payload{}); got != "value" {
		t.Errorf("reaped lock cache = %q, want %q", got, "value")
	}
	if len(*calls) != 1 {
		t.Errorf("stale lock should be replaced, got %d calls", len(*calls))
	}
}

func TestPluginRefreshSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	script := writeScript(t, `echo "refreshed value"`)
	def := config.PluginDef{Command: script, TimeoutMS: 1000}
	cachePath, lockPath, tmpPath := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setRefreshEnv(t, def, cachePath, lockPath)

	if err := RunPluginRefresh(); err != nil {
		t.Fatalf("RunPluginRefresh: %v", err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "refreshed value" {
		t.Errorf("cache = %q, want %q", string(data), "refreshed value")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmp file should be removed")
	}
}

func TestPluginRefreshFailureKeepsCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	script := writeScript(t, `echo preserved; exit 1`)
	def := config.PluginDef{Command: script, TimeoutMS: 1000}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(cachePath, []byte("old value"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldMtime := time.Now().Add(-time.Hour)
	_ = os.Chtimes(cachePath, oldMtime, oldMtime)
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setRefreshEnv(t, def, cachePath, lockPath)

	if err := RunPluginRefresh(); err != nil {
		t.Fatalf("RunPluginRefresh: %v", err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil || string(data) != "old value" {
		t.Errorf("cache content should be preserved, got %q", string(data))
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(oldMtime) {
		t.Errorf("cache mtime should be bumped on failure")
	}
}

func TestPluginRefreshFailureCreatesEmptyCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	script := writeScript(t, `exit 1`)
	def := config.PluginDef{Command: script, TimeoutMS: 1000}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setRefreshEnv(t, def, cachePath, lockPath)

	if err := RunPluginRefresh(); err != nil {
		t.Fatalf("RunPluginRefresh: %v", err)
	}

	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("empty cache file should be created on failure")
	}
	data, _ := os.ReadFile(cachePath)
	if len(data) != 0 {
		t.Errorf("empty cache should be empty, got %q", string(data))
	}
}

func TestPluginRefreshMissingEnv(t *testing.T) {
	t.Setenv("STATUSLINE_REFRESH_COMMAND", "")
	t.Setenv("STATUSLINE_REFRESH_CACHE", "")
	if err := RunPluginRefresh(); err == nil {
		t.Error("expected error when refresh env vars are missing")
	}
}

func TestPluginRefreshZeroTimeoutFloor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	script := writeScript(t, "sleep 0.05; echo slow")
	def := config.PluginDef{Command: script, TimeoutMS: 0}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setRefreshEnv(t, def, cachePath, lockPath)

	if err := RunPluginRefresh(); err != nil {
		t.Fatalf("RunPluginRefresh: %v", err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil || string(data) != "slow" {
		t.Errorf("zero timeout should floor to 10s, got cache %q", string(data))
	}
}

func TestPluginRefreshFiltersInternalEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	script := writeScript(t, `echo "refresh_cmd=${STATUSLINE_REFRESH_COMMAND:-}"`)
	def := config.PluginDef{Command: script, TimeoutMS: 1000}
	cachePath, lockPath, _ := pluginCachePaths(def.Command)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setRefreshEnv(t, def, cachePath, lockPath)

	if err := RunPluginRefresh(); err != nil {
		t.Fatalf("RunPluginRefresh: %v", err)
	}
	data, _ := os.ReadFile(cachePath)
	if string(data) != "refresh_cmd=" {
		t.Errorf("plugin should not see STATUSLINE_REFRESH_COMMAND, got %q", string(data))
	}
}
