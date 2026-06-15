package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const porcelainSample = `# branch.oid 4b5d763
# branch.head main
# branch.upstream origin/main
# branch.ab +1 -2
1 .M N... 100644 100644 100644 abc def main.go
`

func TestParsePorcelainV2(t *testing.T) {
	info := parsePorcelainV2(porcelainSample)
	if info.Branch != "main" || !info.Dirty || info.Ahead != 1 || info.Behind != 2 {
		t.Errorf("parsed %+v", info)
	}
	clean := parsePorcelainV2("# branch.oid x\n# branch.head dev\n")
	if clean.Dirty || clean.Branch != "dev" || clean.Ahead != 0 {
		t.Errorf("clean parse %+v", clean)
	}
}

// stubGit replaces the git runner for a test.
func stubGit(t *testing.T, fn func(root string, timeout time.Duration) (string, error)) *int {
	t.Helper()
	calls := 0
	orig := runGitStatusCmd
	runGitStatusCmd = func(root string, timeout time.Duration) (string, error) {
		calls++
		return fn(root, timeout)
	}
	t.Cleanup(func() { runGitStatusCmd = orig })
	return &calls
}

func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGitStatusCacheTTL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fakeRepo(t)
	now := time.Unix(1750000000, 0)
	calls := stubGit(t, func(string, time.Duration) (string, error) { return porcelainSample, nil })

	info, ok := gitStatusFor(root, 10*time.Second, time.Second, now)
	if !ok || !info.Dirty || info.Ahead != 1 {
		t.Fatalf("first call: %+v ok=%v", info, ok)
	}
	// Within TTL: served from cache, no exec.
	if _, ok := gitStatusFor(root, 10*time.Second, time.Second, now.Add(5*time.Second)); !ok {
		t.Fatal("cached read failed")
	}
	if *calls != 1 {
		t.Errorf("expected 1 git exec, got %d", *calls)
	}
	// Past TTL: refreshed.
	if _, ok := gitStatusFor(root, 10*time.Second, time.Second, now.Add(30*time.Second)); !ok {
		t.Fatal("refresh failed")
	}
	if *calls != 2 {
		t.Errorf("expected 2 git execs, got %d", *calls)
	}
}

func TestGitStatusStaleFallbackOnError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fakeRepo(t)
	now := time.Unix(1750000000, 0)
	fail := false
	stubGit(t, func(string, time.Duration) (string, error) {
		if fail {
			return "", errors.New("timeout")
		}
		return porcelainSample, nil
	})

	if _, ok := gitStatusFor(root, 10*time.Second, time.Second, now); !ok {
		t.Fatal("prime failed")
	}
	fail = true
	// Past TTL but git fails → stale cached value still returned.
	info, ok := gitStatusFor(root, 10*time.Second, time.Second, now.Add(time.Minute))
	if !ok || !info.Dirty {
		t.Errorf("stale fallback missing: %+v ok=%v", info, ok)
	}

	// No cache at all + git failure → not ok.
	other := fakeRepo(t)
	if _, ok := gitStatusFor(other, 10*time.Second, time.Second, now); ok {
		t.Error("expected failure with no cache and failing git")
	}
}

func TestGitStatusNonRepo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := stubGit(t, func(string, time.Duration) (string, error) { return "", nil })
	if _, ok := gitStatusFor(t.TempDir(), time.Second, time.Second, time.Now()); ok {
		t.Error("non-repo dir should not report status")
	}
	if *calls != 0 {
		t.Error("git must not run outside a repo")
	}
}

// A worktree display name must append to the rich-status decorations, not
// replace them (regression: the worktree suffix used to rebuild the display
// from the bare branch, silently dropping the dirty marker and ahead/behind).
func TestGitBranchKeepsRichStatusWithWorktree(t *testing.T) {
	gitStatusPreview = &gitStatusInfo{Dirty: true, Ahead: 1, Behind: 2}
	t.Cleanup(func() { gitStatusPreview = nil })
	initSegments(nil)

	var p payload
	p.Workspace.CurrentDir = "/nonexistent/proj"
	p.Worktree = worktree{Name: "my-project", Branch: "feature/x"}
	cfg := config{Settings: map[string]map[string]any{"git-branch": {"git_status": true}}}
	seg, ok := segmentByID("git-branch")
	if !ok {
		t.Fatal("no git-branch segment")
	}
	out, show := seg.render(renderCtx{P: p, S: settingsFor(cfg, seg), Now: time.Now()})
	if !show {
		t.Fatal("git-branch hidden")
	}
	for _, want := range []string{"feature/x*", "↑1", "↓2", "(my-project)"} {
		if !strings.Contains(out, want) {
			t.Errorf("git-branch = %q, want substring %q", out, want)
		}
	}
}

// TestGitStatusRealRepo is an integration test against the actual git binary.
func TestGitStatusRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-m", "init")

	now := time.Now()
	info, ok := gitStatusFor(root, time.Second, 5*time.Second, now)
	if !ok || info.Dirty || info.Branch != "main" {
		t.Errorf("clean repo: %+v ok=%v", info, ok)
	}

	// Modify a tracked file → dirty on the next refresh.
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok = gitStatusFor(root, time.Second, 5*time.Second, now.Add(2*time.Second))
	if !ok || !info.Dirty {
		t.Errorf("dirty repo: %+v ok=%v", info, ok)
	}
}

// ─── git-stash ────────────────────────────────────────────────────────

func stubGitStash(t *testing.T, fn func(root string, timeout time.Duration) (int, error)) *int {
	t.Helper()
	calls := 0
	orig := runGitStashCmd
	runGitStashCmd = func(root string, timeout time.Duration) (int, error) {
		calls++
		return fn(root, timeout)
	}
	t.Cleanup(func() { runGitStashCmd = orig })
	return &calls
}

func TestGitStashCacheTTL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fakeRepo(t)
	now := time.Unix(1750000000, 0)
	calls := stubGitStash(t, func(string, time.Duration) (int, error) { return 3, nil })

	if n, ok := gitStashFor(root, 10*time.Second, time.Second, now); !ok || n != 3 {
		t.Fatalf("first call: n=%d ok=%v", n, ok)
	}
	// Within TTL: served from cache, no exec.
	if n, ok := gitStashFor(root, 10*time.Second, time.Second, now.Add(5*time.Second)); !ok || n != 3 {
		t.Fatalf("cached read: n=%d ok=%v", n, ok)
	}
	if *calls != 1 {
		t.Errorf("expected 1 git exec, got %d", *calls)
	}
	// Past TTL: refreshed.
	if _, ok := gitStashFor(root, 10*time.Second, time.Second, now.Add(30*time.Second)); !ok {
		t.Fatal("refresh failed")
	}
	if *calls != 2 {
		t.Errorf("expected 2 git execs, got %d", *calls)
	}
}

func TestGitStashNoRefIsZero(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fakeRepo(t)
	now := time.Unix(1750000000, 0)
	// git rev-list refs/stash exits non-zero when no stash ref exists.
	calls := stubGitStash(t, func(string, time.Duration) (int, error) {
		return 0, errors.New("fatal: ambiguous argument 'refs/stash'")
	})

	n, ok := gitStashFor(root, 10*time.Second, time.Second, now)
	if !ok || n != 0 {
		t.Errorf("no stash ref should be zero: n=%d ok=%v", n, ok)
	}
	// Result is cached, so a repo that never stashed doesn't re-exec every render.
	if _, _ = gitStashFor(root, 10*time.Second, time.Second, now.Add(time.Second)); *calls != 1 {
		t.Errorf("zero result should be cached, got %d execs", *calls)
	}
}

func TestGitStashStaleFallbackOnError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fakeRepo(t)
	now := time.Unix(1750000000, 0)
	fail := false
	stubGitStash(t, func(string, time.Duration) (int, error) {
		if fail {
			return 0, errors.New("timeout")
		}
		return 5, nil
	})

	if n, ok := gitStashFor(root, 10*time.Second, time.Second, now); !ok || n != 5 {
		t.Fatalf("prime: n=%d ok=%v", n, ok)
	}
	fail = true
	// Past TTL but git fails → stale cached count returned, not zero.
	if n, ok := gitStashFor(root, 10*time.Second, time.Second, now.Add(time.Minute)); !ok || n != 5 {
		t.Errorf("stale fallback missing: n=%d ok=%v", n, ok)
	}
}

func TestGitStashNonRepo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := stubGitStash(t, func(string, time.Duration) (int, error) { return 0, nil })
	if _, ok := gitStashFor(t.TempDir(), time.Second, time.Second, time.Now()); ok {
		t.Error("non-repo dir should not report stash count")
	}
	if *calls != 0 {
		t.Error("git must not run outside a repo")
	}
}

func TestRenderGitStash(t *testing.T) {
	initSegments(nil)
	seg, ok := segmentByID("git-stash")
	if !ok {
		t.Fatal("no git-stash segment")
	}
	render := func() (string, bool) {
		return seg.render(renderCtx{
			P:   payload{Workspace: workspace{CurrentDir: "/whatever"}},
			S:   settingsFor(config{}, seg),
			C:   palette{Git: "", Rst: ""},
			Now: time.Unix(1750000000, 0),
		})
	}

	n := 3
	gitStashPreview = &n
	t.Cleanup(func() { gitStashPreview = nil })
	out, show := render()
	if !show || !strings.Contains(out, "⚑3") {
		t.Errorf("count 3 should show ⚑3, got %q show=%v", out, show)
	}

	n = 0
	if out, show := render(); show {
		t.Errorf("zero stashes should hide, got %q", out)
	}
}
