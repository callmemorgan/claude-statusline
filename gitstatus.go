package main

// ─── Rich Git Status (opt-in) ────────────────────────────────────────
//
// The always-on git-branch segment reads .git files directly (no exec). The
// opt-in rich status (dirty marker, ahead/behind counts) needs `git status`,
// so results are cached per repo root with a short TTL: at most one git exec
// per TTL window, a hard timeout bounds pathological repos, and any error
// falls back to the last cached value (any age) or plain branch display.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type gitStatusInfo struct {
	TS     int64  `json:"ts"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
	Ahead  int    `json:"ahead,omitempty"`
	Behind int    `json:"behind,omitempty"`
}

func gitCacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "claude-statusline")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".cache", "claude-statusline")
}

func gitCachePath() string {
	return filepath.Join(gitCacheDir(), "git-status.json")
}

// repoRoot walks up from dir to the directory containing .git.
func repoRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// runGitStatusCmd executes git status in root; a var so tests can stub it.
var runGitStatusCmd = func(root string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "--branch", "-uno", "--no-renames")
	cmd.Dir = root
	out, err := cmd.Output()
	return string(out), err
}

// parsePorcelainV2 extracts branch, ahead/behind, and dirtiness. With -uno,
// untracked files don't count as dirty — only tracked modifications do.
func parsePorcelainV2(out string) gitStatusInfo {
	var info gitStatusInfo
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			info.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(f, "+"), "-")); err == nil {
					if strings.HasPrefix(f, "+") {
						info.Ahead = n
					} else {
						info.Behind = n
					}
				}
			}
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			info.Dirty = true
		}
	}
	return info
}

func loadGitCache() map[string]gitStatusInfo {
	data, err := os.ReadFile(gitCachePath())
	if err != nil {
		return map[string]gitStatusInfo{}
	}
	cache := map[string]gitStatusInfo{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string]gitStatusInfo{}
	}
	return cache
}

func saveGitCache(cache map[string]gitStatusInfo, now time.Time) {
	// Drop entries that haven't been refreshed in a day.
	cutoff := now.Add(-24 * time.Hour).Unix()
	for k, v := range cache {
		if v.TS < cutoff {
			delete(cache, k)
		}
	}
	if err := os.MkdirAll(gitCacheDir(), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = writeFileAtomic(gitCachePath(), data)
}

// gitStatusPreview, when non-nil, short-circuits gitStatusFor. The configure
// TUI sets it so the preview can demonstrate rich status (dirty marker,
// ahead/behind) without the sample payload pointing at a real repo. Never set
// on the render path.
var gitStatusPreview *gitStatusInfo

// gitStatusFor returns rich status for the repo containing dir. Fresh cache
// entries are returned without exec; stale ones trigger a bounded git run,
// falling back to the stale value on error or timeout.
func gitStatusFor(dir string, ttl, timeout time.Duration, now time.Time) (gitStatusInfo, bool) {
	if gitStatusPreview != nil {
		return *gitStatusPreview, true
	}
	root := repoRoot(dir)
	if root == "" {
		return gitStatusInfo{}, false
	}
	cache := loadGitCache()
	cached, hasCached := cache[root]
	if hasCached && now.Unix()-cached.TS < int64(ttl/time.Second) {
		return cached, true
	}

	out, err := runGitStatusCmd(root, timeout)
	if err != nil {
		return cached, hasCached // stale beats nothing
	}
	info := parsePorcelainV2(out)
	info.TS = now.Unix()
	cache[root] = info
	saveGitCache(cache, now)
	return info, true
}

// ─── Git Stash Count (opt-in) ────────────────────────────────────────
//
// A separate cached counter mirroring gitStatusFor: at most one bounded `git`
// exec per TTL window per repo root, with fallback to the last cached value on
// error. Kept in its own cache file so enabling both the git-stash and the rich
// git-branch segments never costs two uncached execs per render.

type gitStashInfo struct {
	TS    int64 `json:"ts"`
	Count int   `json:"count"`
}

func gitStashCachePath() string {
	return filepath.Join(gitCacheDir(), "git-stash.json")
}

// runGitStashCmd counts stash entries. `git rev-list --walk-reflogs --count
// refs/stash` prints a single integer; it exits non-zero when no stash ref
// exists, which gitStashFor treats as zero. A var so tests can stub it.
var runGitStashCmd = func(root string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-list", "--walk-reflogs", "--count", "refs/stash")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, err
	}
	return n, nil
}

func loadGitStashCache() map[string]gitStashInfo {
	data, err := os.ReadFile(gitStashCachePath())
	if err != nil {
		return map[string]gitStashInfo{}
	}
	cache := map[string]gitStashInfo{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string]gitStashInfo{}
	}
	return cache
}

func saveGitStashCache(cache map[string]gitStashInfo, now time.Time) {
	cutoff := now.Add(-24 * time.Hour).Unix()
	for k, v := range cache {
		if v.TS < cutoff {
			delete(cache, k)
		}
	}
	if err := os.MkdirAll(gitCacheDir(), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = writeFileAtomic(gitStashCachePath(), data)
}

// gitStashPreview, when non-nil, short-circuits gitStashFor so the configure
// TUI can demonstrate the stash count without the sample payload pointing at a
// real repo. Never set on the render path.
var gitStashPreview *int

// gitStashFor returns the stash count for the repo containing dir, cached like
// gitStatusFor. A missing stash ref (git exits non-zero) is treated as zero and
// cached, so a repo that has never stashed doesn't re-exec every render. A
// stale cached value beats nothing on a transient error.
func gitStashFor(dir string, ttl, timeout time.Duration, now time.Time) (int, bool) {
	if gitStashPreview != nil {
		return *gitStashPreview, true
	}
	root := repoRoot(dir)
	if root == "" {
		return 0, false
	}
	cache := loadGitStashCache()
	cached, hasCached := cache[root]
	if hasCached && now.Unix()-cached.TS < int64(ttl/time.Second) {
		return cached.Count, true
	}
	n, err := runGitStashCmd(root, timeout)
	if err != nil {
		if hasCached {
			return cached.Count, true // stale beats nothing
		}
		n = 0 // no stash ref (or git unavailable) → zero
	}
	cache[root] = gitStashInfo{TS: now.Unix(), Count: n}
	saveGitStashCache(cache, now)
	return n, true
}
