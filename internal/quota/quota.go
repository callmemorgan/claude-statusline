// Package quota implements the OAuth quota shim: a detached worker fetches
// Claude's OAuth usage endpoint (the same one `claude /usage` reads) and
// caches the model-class weekly windows; the render path injects them into
// RateLimits.ModelScoped — a field the statusline wire never populates —
// so the rate-limit-fable/-sonnet/-opus segments render even though Claude
// Code does not send these windows. If Claude Code ever ships them in the
// payload, add wire parsing back in internal/payload against the real field
// names and let it take precedence here. Only percentages and reset times
// are ever written to disk — never the OAuth token.
package quota

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/sys"
)

// ─── Cache ───────────────────────────────────────────────────────────

// usageURL is undocumented but stable in practice; the worker fails soft
// (empty cache, segments hide) if it ever changes shape or disappears.
const usageURL = "https://api.anthropic.com/api/oauth/usage"

const fetchTimeout = 10 * time.Second

// staleLockTolerance must exceed fetchTimeout so a slow fetch is not
// mistaken for a dead worker.
const staleLockTolerance = 30 * time.Second

// cacheEntry is one model-class weekly window, in the same wire shape as
// payload.ModelScopedLimit (resets_at as unix seconds).
type cacheEntry struct {
	DisplayName    string   `json:"display_name"`
	UsedPercentage *float64 `json:"used_percentage,omitempty"`
	ResetsAt       *int64   `json:"resets_at,omitempty"`
}

type cacheFile struct {
	FetchedAt   int64        `json:"fetched_at"`
	ModelScoped []cacheEntry `json:"model_scoped"`
}

// cachePath and lockPath are keyed by the resolved Claude Code profile so
// concurrent sessions on different profiles never clobber each other's cache
// or serialize behind each other's lock. The default profile keeps the legacy
// unsuffixed filenames for backward compatibility; a scoped profile appends
// the same 8-hex sha256 suffix Claude Code uses for its keychain service.
func cachePath(configDir string) string {
	return filepath.Join(state.StateBaseDir(), "quota-shim"+profileSuffix(configDir)+".json")
}

func lockPath(configDir string) string {
	return filepath.Join(state.StateBaseDir(), "quota-shim"+profileSuffix(configDir)+".lock")
}

// ─── Render-Path Injection ───────────────────────────────────────────

// spawnRefresh starts the detached quota-refresh worker. Tests stub this
// package variable to avoid real process spawning.
var spawnRefresh = spawnRefreshReal

func spawnRefreshReal() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(exe, "quota-refresh")
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	sys.ApplyDetachSysProcAttr(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// MaybeInject fills p.RateLimits.ModelScoped from the shim cache when the
// shim is enabled and the payload carries no model-class windows of its own,
// and spawns a background refresh when the cache is stale. It never blocks:
// one os.ReadFile when enabled, nothing when disabled.
func MaybeInject(p *payload.Payload, cfg config.QuotaShimConfig, now time.Time) {
	if !cfg.Enabled {
		return
	}
	// Resolving the profile here is env read + in-process sha256 only —
	// never a subprocess — so the render path stays cheap.
	configDir := resolveClaudeConfigDir(cfg)
	entries, mtime := loadCache(configDir)
	if needsRefresh(mtime, now, cfg.RefreshEvery()) {
		trySpawnRefresh(configDir)
	}
	if len(entries) == 0 || len(p.RateLimits.ModelScoped) > 0 {
		return
	}
	scoped := make([]payload.ModelScopedLimit, 0, len(entries))
	for _, e := range entries {
		scoped = append(scoped, payload.ModelScopedLimit{
			DisplayName:    e.DisplayName,
			UsedPercentage: e.UsedPercentage,
			ResetsAt:       e.ResetsAt,
		})
	}
	p.RateLimits.ModelScoped = scoped
}

func loadCache(configDir string) ([]cacheEntry, time.Time) {
	path := cachePath(configDir)
	var mtime time.Time
	if info, err := os.Stat(path); err == nil {
		mtime = info.ModTime()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, mtime
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, mtime
	}
	return f.ModelScoped, mtime
}

func needsRefresh(mtime time.Time, now time.Time, refresh time.Duration) bool {
	if mtime.IsZero() {
		return true
	}
	return now.Sub(mtime) >= refresh
}

func trySpawnRefresh(configDir string) {
	if err := os.MkdirAll(state.StateBaseDir(), 0o755); err != nil {
		return
	}
	if sys.TryAcquireLock(lockPath(configDir), staleLockTolerance) {
		if err := spawnRefresh(); err != nil {
			_ = os.Remove(lockPath(configDir))
		}
	}
}

// ─── Refresh Worker ──────────────────────────────────────────────────

// RunRefresh executes the hidden "quota-refresh" subcommand: fetch the usage
// endpoint, rewrite the cache atomically, release the lock. Fetch failures
// touch the cache mtime instead so the render path backs off rather than
// respawning every turn. The worker is a separate process, so it re-resolves
// the profile from config + the CLAUDE_CONFIG_DIR env it inherited from the
// spawning session — the same inputs MaybeInject resolved from — landing on
// the same cache/lock key without any positional hand-off.
func RunRefresh() error {
	full, _ := config.LoadConfigWarn()
	configDir := resolveClaudeConfigDir(full.QuotaShim)
	defer func() { _ = os.Remove(lockPath(configDir)) }()
	if err := os.MkdirAll(state.StateBaseDir(), 0o755); err != nil {
		return err
	}

	entries, err := fetchUsage(configDir)
	path := cachePath(configDir)
	now := time.Now()
	if err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			_ = os.Chtimes(path, now, now)
		} else {
			_ = os.WriteFile(path, nil, 0o600)
		}
		return nil
	}

	data, marshalErr := json.Marshal(cacheFile{FetchedAt: now.Unix(), ModelScoped: entries})
	if marshalErr != nil {
		return marshalErr
	}
	tmp := path + ".tmp"
	if writeErr := os.WriteFile(tmp, data, 0o600); writeErr == nil {
		_ = os.Rename(tmp, path)
	}
	_ = os.Remove(tmp)
	return nil
}

func fetchUsage(configDir string) ([]cacheEntry, error) {
	token, err := loadToken(configDir)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("usage endpoint returned " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseUsage(body)
}

// ─── Endpoint Parsing ────────────────────────────────────────────────

// oauthWindow is a named weekly window in the usage response. utilization is
// on the 0–100 percent scale (verified against the /usage display).
type oauthWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

// oauthLimit is one entry of the response's limits[] array; weekly_scoped
// entries carry the per-model windows (Fable's included weekly quota).
type oauthLimit struct {
	Kind     string          `json:"kind"`
	Percent  *float64        `json:"percent"`
	ResetsAt json.RawMessage `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type oauthUsage struct {
	SevenDaySonnet *oauthWindow `json:"seven_day_sonnet"`
	SevenDayOpus   *oauthWindow `json:"seven_day_opus"`
	Limits         []oauthLimit `json:"limits"`
}

// parseUsage extracts the model-class weekly windows: every named
// weekly_scoped limit, plus the top-level seven_day_sonnet/seven_day_opus
// windows when no scoped entry already covers that model class.
func parseUsage(data []byte) ([]cacheEntry, error) {
	var u oauthUsage
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	var entries []cacheEntry
	for _, l := range u.Limits {
		if l.Kind != "weekly_scoped" || l.Percent == nil {
			continue
		}
		if l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			continue
		}
		pct := *l.Percent
		entries = append(entries, cacheEntry{
			DisplayName:    l.Scope.Model.DisplayName,
			UsedPercentage: &pct,
			ResetsAt:       payload.ParseResetsAt(l.ResetsAt),
		})
	}
	covered := func(needle string) bool {
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.DisplayName), needle) {
				return true
			}
		}
		return false
	}
	addWindow := func(w *oauthWindow, name string) {
		if w == nil || w.Utilization == nil || covered(strings.ToLower(name)) {
			return
		}
		pct := *w.Utilization
		entries = append(entries, cacheEntry{
			DisplayName:    name,
			UsedPercentage: &pct,
			ResetsAt:       payload.ParseResetsAt(w.ResetsAt),
		})
	}
	addWindow(u.SevenDaySonnet, "Sonnet")
	addWindow(u.SevenDayOpus, "Opus")
	return entries, nil
}

// ─── Status Subcommand ───────────────────────────────────────────────

// RunStatus implements `claude-statusline quota`: a foreground fetch that
// prints the model-class weekly windows plus shim cache state, so users can
// verify the shim end-to-end before enabling it.
func RunStatus(w io.Writer) error {
	full, _ := config.LoadConfigWarn()
	cfg := full.QuotaShim
	if cfg.Enabled {
		fmt.Fprintf(w, "quota shim: enabled (refresh every %s)\n", cfg.RefreshEvery())
	} else {
		fmt.Fprintln(w, "quota shim: disabled — enable with [quota_shim] enabled = true in config.toml")
	}

	configDir := resolveClaudeConfigDir(cfg)
	profile := configDir
	if profile == "" {
		profile = "default (~/.claude)"
	}
	fmt.Fprintf(w, "profile: %s (keychain service %q)\n", profile, keychainServiceName(configDir))

	if entries, mtime := loadCache(configDir); !mtime.IsZero() {
		fmt.Fprintf(w, "cache: %s (updated %s ago, %d window(s))\n",
			cachePath(configDir), time.Since(mtime).Round(time.Second), len(entries))
	} else {
		fmt.Fprintf(w, "cache: %s (absent)\n", cachePath(configDir))
	}

	entries, err := fetchUsage(configDir)
	if err != nil {
		return fmt.Errorf("live fetch failed: %w", err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "live: endpoint reachable, but no model-class weekly windows on this account")
		return nil
	}
	for _, e := range entries {
		line := "live: " + e.DisplayName
		if e.UsedPercentage != nil {
			line += fmt.Sprintf(" %.0f%% used", *e.UsedPercentage)
		}
		if e.ResetsAt != nil {
			line += " (resets " + time.Unix(*e.ResetsAt, 0).Local().Format("2006-01-02 15:04") + ")"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// ─── Profile Resolution ──────────────────────────────────────────────

// resolveClaudeConfigDir picks the Claude Code profile the shim should read:
// the explicit [quota_shim].claude_config_dir config value (expanded for a
// leading "~/"), else the CLAUDE_CONFIG_DIR the process inherited, else ""
// (the default ~/.claude profile).
func resolveClaudeConfigDir(cfg config.QuotaShimConfig) string {
	if cfg.ClaudeConfigDir != "" {
		return expandHome(cfg.ClaudeConfigDir)
	}
	return os.Getenv("CLAUDE_CONFIG_DIR")
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// profileSuffix is the per-profile key shared by the keychain service name
// and the shim's cache/lock filenames: empty for the default profile, else
// "-" + the first 8 hex chars of sha256(configDir) — the exact literal
// config dir string Claude Code itself hashes, no trailing-slash or other
// normalization.
func profileSuffix(configDir string) string {
	if configDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(configDir))
	return "-" + hex.EncodeToString(sum[:])[:8]
}

// keychainServiceName mirrors Claude Code's own keychain namespacing: the
// default profile uses "Claude Code-credentials" unsuffixed, while any
// CLAUDE_CONFIG_DIR-scoped profile gets the sha256[:8] suffix.
func keychainServiceName(configDir string) string {
	return "Claude Code-credentials" + profileSuffix(configDir)
}

// ─── Token Discovery ─────────────────────────────────────────────────

// loadToken finds the Claude Code OAuth access token for the given profile
// (configDir; "" means the default profile) without ever persisting it:
// $CLAUDE_CODE_OAUTH_TOKEN, then the macOS keychain (profile-scoped service),
// then the credentials file Claude Code writes on Linux/Windows.
func loadToken(configDir string) (string, error) {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return t, nil
	}
	if runtime.GOOS == "darwin" {
		if t, err := keychainToken(configDir); err == nil && t != "" {
			return t, nil
		}
	}
	return credentialsFileToken(configDir)
}

func keychainToken(configDir string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainServiceName(configDir), "-w").Output()
	if err != nil {
		return "", err
	}
	return accessTokenFromCredentials(out)
}

func credentialsFileToken(configDir string) (string, error) {
	var candidates []string
	if configDir != "" {
		candidates = append(candidates, filepath.Join(configDir, ".credentials.json"))
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, ".credentials.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude", ".credentials.json"))
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			if t, err := accessTokenFromCredentials(data); err == nil && t != "" {
				return t, nil
			}
		}
	}
	return "", errors.New("no Claude Code OAuth token found (keychain or .credentials.json)")
}

func accessTokenFromCredentials(data []byte) (string, error) {
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", err
	}
	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("credentials JSON has no claudeAiOauth.accessToken")
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}
