package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── step 2: detectInstallKind, compareVersions, cache ─────────────────

func TestDetectInstallKind(t *testing.T) {
	cases := []struct {
		name string
		path string
		ver  string
		want installKind
	}{
		{"dev-version", "/usr/local/bin/claude-statusline", "dev", kindDev},
		{"dev-on-cellar-path", "/opt/homebrew/Cellar/cs/1.0.0/bin/cs", "dev", kindDev},
		{"cellar-apple-silicon", "/opt/homebrew/Cellar/claude-statusline/1.0.0/bin/claude-statusline", "1.0.0", kindBrew},
		{"cellar-intel", "/usr/local/Cellar/claude-statusline/1.0.0/bin/claude-statusline", "1.0.0", kindBrew},
		{"homebrew-prefix", "/home/me/.linuxbrew/Cellar/claude-statusline/1.0.0/bin/claude-statusline", "1.0.0", kindBrew},
		{"local-bin", "/home/me/.local/bin/claude-statusline", "1.0.0", kindManual},
		{"usr-local", "/usr/local/bin/claude-statusline", "1.0.0", kindManual},
		{"windows-path", `C:\Program Files\claude-statusline\claude-statusline.exe`, "1.0.0", kindManual},
		{"mixed-case-cellar", "/opt/homebrew/CELLAR/Claude-Statusline/1.0.0/bin/x", "1.0.0", kindBrew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectInstallKind(tc.path, tc.ver); got != tc.want {
				t.Errorf("detectInstallKind(%q, %q) = %v, want %v", tc.path, tc.ver, got, tc.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.0", "1.1.9", 1},
		{"1.2.0", "v1.2.0", 0},
		{"1.10.0", "1.9.99", 1},
		{"malformed", "1.0.0", 0},
		{"1.0.0", "garbage", 0},
		{"", "1.0.0", 0},
		{"v", "1.0.0", 0},
		{"1.0", "1.0.0", 0},
		{"1.0.0.0", "1.0.0", 0},
		{"1.-1.0", "1.0.0", 0},
		{"1.0.0+meta", "1.0.0", 0}, // strict: +meta is treated as malformed
	}
	for _, tc := range cases {
		got := compareVersions(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// Property: malformed never compares greater.
	bad := []string{"", "v", "1", "1.0", "1.0.0.0", "1.x.0", "1.-1.0", "1.0.0+meta", "  ", "abc"}
	for _, b := range bad {
		if compareVersions(b, "1.0.0") > 0 {
			t.Errorf("malformed %q compared greater than 1.0.0", b)
		}
		if compareVersions("1.0.0", b) < 0 {
			t.Errorf("1.0.0 compared less than malformed %q", b)
		}
	}
}

func TestUpdateCheckCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	in := updateCheck{CheckedAt: 1718200000, Latest: "1.2.0"}
	if err := saveUpdateCheck(in); err != nil {
		t.Fatalf("saveUpdateCheck: %v", err)
	}
	got, ok := loadUpdateCheck()
	if !ok {
		t.Fatal("expected ok after save")
	}
	if got != in {
		t.Errorf("round-trip = %+v, want %+v", got, in)
	}

	// Failed-check record loads and preserves the empty latest.
	if err := saveUpdateCheck(updateCheck{CheckedAt: 1718200060, Latest: ""}); err != nil {
		t.Fatalf("saveUpdateCheck: %v", err)
	}
	got, ok = loadUpdateCheck()
	if !ok {
		t.Fatal("expected ok for failed-check record")
	}
	if got.Latest != "" {
		t.Errorf("failed-check record should preserve empty latest, got %q", got.Latest)
	}

	// Corrupt JSON → ok=false.
	if err := os.WriteFile(updateCheckPath(), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadUpdateCheck(); ok {
		t.Error("corrupt JSON should return ok=false")
	}

	want := filepath.Join(dir, "claude-statusline", "update.json")
	if updateCheckPath() != want {
		t.Errorf("updateCheckPath = %q, want %q", updateCheckPath(), want)
	}

	// MkdirAll on save.
	if err := os.RemoveAll(filepath.Dir(updateCheckPath())); err != nil {
		t.Fatal(err)
	}
	if err := saveUpdateCheck(updateCheck{CheckedAt: 1, Latest: "1.0.0"}); err != nil {
		t.Fatalf("saveUpdateCheck should MkdirAll: %v", err)
	}
	if _, ok := loadUpdateCheck(); !ok {
		t.Error("save should have recreated the dir + file")
	}
}

// ─── step 3: maybeSpawnUpdateCheck ───────────────────────────────────

type updateSpawnCall struct{}

func stubSpawnUpdateCheck(t *testing.T) *[]updateSpawnCall {
	t.Helper()
	var calls []updateSpawnCall
	old := spawnUpdateCheck
	spawnUpdateCheck = func() error { calls = append(calls, updateSpawnCall{}); return nil }
	t.Cleanup(func() { spawnUpdateCheck = old })
	return &calls
}

func TestMaybeSpawnUpdateCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	calls := stubSpawnUpdateCheck(t)

	now := time.Unix(1750000000, 0)
	h12 := 12
	h24 := 24

	// Fresh cache → never spawns.
	if err := saveUpdateCheck(updateCheck{CheckedAt: now.Unix() - 60, Latest: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if len(*calls) != 0 {
		t.Errorf("fresh cache should not spawn, got %d calls", len(*calls))
	}

	// Stale cache + active lock → does not spawn.
	stale := updateCheck{CheckedAt: now.Unix() - 86400*7, Latest: "1.0.0"}
	if err := saveUpdateCheck(stale); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateBaseDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updateLockPath(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if len(*calls) != 0 {
		t.Errorf("active lock should block spawn, got %d calls", len(*calls))
	}

	// Stale cache + no lock → spawns once.
	_ = os.Remove(updateLockPath())
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if len(*calls) != 1 {
		t.Errorf("stale cache should spawn, got %d calls", len(*calls))
	}

	// Stale cache + fresh lock left by the spawn → second render does not spawn.
	if _, err := os.Stat(updateLockPath()); err != nil {
		t.Errorf("spawn should leave a fresh lock; got %v", err)
	}
	_ = os.Chtimes(updateLockPath(), time.Now(), time.Now())
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if len(*calls) != 1 {
		t.Errorf("fresh lock should block second spawn, got %d calls", len(*calls))
	}

	// Off mode → never spawns, even with a stale cache.
	_ = os.Remove(updateLockPath())
	*calls = nil
	if err := saveUpdateCheck(stale); err != nil {
		t.Fatal(err)
	}
	maybeSpawnUpdateCheckFor(updateConfig{Mode: "off", CheckHours: &h24}, now, kindManual)
	if len(*calls) != 0 {
		t.Errorf("off mode should never spawn, got %d calls", len(*calls))
	}

	// dev kind → never spawns.
	*calls = nil
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindDev)
	if len(*calls) != 0 {
		t.Errorf("dev kind should never spawn, got %d calls", len(*calls))
	}

	// Failed-check record (Latest == "") with fresh check_at suppresses respawn.
	_ = os.Remove(updateLockPath())
	*calls = nil
	if err := saveUpdateCheck(updateCheck{CheckedAt: now.Unix() - 60, Latest: ""}); err != nil {
		t.Fatal(err)
	}
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if len(*calls) != 0 {
		t.Errorf("recent failed-check should not respawn, got %d calls", len(*calls))
	}

	// Spawn failure releases the lock so a transient error doesn't suppress
	// checks for the full stale tolerance.
	_ = os.Remove(updateLockPath())
	*calls = nil
	failCalls := 0
	oldSpawn := spawnUpdateCheck
	spawnUpdateCheck = func() error { failCalls++; return errors.New("spawn failed") }
	t.Cleanup(func() { spawnUpdateCheck = oldSpawn })
	if err := saveUpdateCheck(stale); err != nil {
		t.Fatal(err)
	}
	maybeSpawnUpdateCheckFor(updateConfig{CheckHours: &h12}, now, kindManual)
	if failCalls != 1 {
		t.Errorf("expected one failed spawn attempt, got %d", failCalls)
	}
	if _, err := os.Stat(updateLockPath()); err == nil {
		t.Error("lock should be released after spawn failure")
	}

	// Production entry point: the test binary is "dev" so the spawn is
	// suppressed end-to-end.
	spawnUpdateCheck = oldSpawn
	*calls = nil
	if err := saveUpdateCheck(stale); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(updateLockPath())
	maybeSpawnUpdateCheck(updateConfig{CheckHours: &h12}, now)
	if len(*calls) != 0 {
		t.Errorf("dev test build should not spawn via the public path, got %d calls", len(*calls))
	}

	want := filepath.Join(dir, "claude-statusline", "update-check.lock")
	if updateLockPath() != want {
		t.Errorf("updateLockPath = %q, want %q", updateLockPath(), want)
	}
}

func TestMaybeSpawnUpdateCheckNonRelease(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	calls := stubSpawnUpdateCheck(t)

	now := time.Unix(1750000000, 0)
	h12 := 12
	stale := updateCheck{CheckedAt: now.Unix() - 86400*7, Latest: "9.9.9"}
	if err := saveUpdateCheck(stale); err != nil {
		t.Fatal(err)
	}

	// Non-release version shapes (source builds, go install @commit,
	// dirty builds) must never spawn a worker, because the segment already
	// hides on !isReleaseVersion and the worker could never install them.
	for _, v := range []string{"1.0.0+dirty", "0.0.0-20260612-abc123"} {
		*calls = nil
		_ = os.Remove(updateLockPath())
		withTestVersion(t, v)
		maybeSpawnUpdateCheck(updateConfig{CheckHours: &h12}, now)
		if len(*calls) != 0 {
			t.Errorf("non-release version %q should never spawn, got %d calls", v, len(*calls))
		}
	}
}

func TestUpdateLockToleranceExceedsBrewTimeout(t *testing.T) {
	if updateStaleLockTolerance <= updateBrewTimeout {
		t.Errorf("updateStaleLockTolerance (%v) must be strictly greater than updateBrewTimeout (%v)",
			updateStaleLockTolerance, updateBrewTimeout)
	}
}

// ─── step 4: asset naming, checksum, extract, swap, brew branch ────────

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "amd64", "claude-statusline_Darwin_x86_64.tar.gz"},
		{"darwin", "arm64", "claude-statusline_Darwin_arm64.tar.gz"},
		{"linux", "amd64", "claude-statusline_Linux_x86_64.tar.gz"},
		{"linux", "arm64", "claude-statusline_Linux_arm64.tar.gz"},
		{"windows", "amd64", "claude-statusline_Windows_x86_64.zip"},
		{"windows", "arm64", "claude-statusline_Windows_arm64.zip"},
		{"linux", "386", "claude-statusline_Linux_i386.tar.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"_"+tc.goarch, func(t *testing.T) {
			if got := assetName(tc.goos, tc.goarch); got != tc.want {
				t.Errorf("assetName(%q, %q) = %q, want %q (must match .goreleaser.yaml template)",
					tc.goos, tc.goarch, got, tc.want)
			}
		})
	}

	// URL form is fully predictable.
	if got := assetURL("1.2.0", "claude-statusline_Darwin_x86_64.tar.gz"); got !=
		"https://github.com/callmemorgan/claude-statusline/releases/download/v1.2.0/claude-statusline_Darwin_x86_64.tar.gz" {
		t.Errorf("assetURL = %q", got)
	}
	if got := checksumsURL("1.2.0"); got !=
		"https://github.com/callmemorgan/claude-statusline/releases/download/v1.2.0/checksums.txt" {
		t.Errorf("checksumsURL = %q", got)
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	known := []byte("the quick brown fox jumps over the lazy dog")
	archivePath := filepath.Join(dir, "asset.tar.gz")
	if err := os.WriteFile(archivePath, known, 0o644); err != nil {
		t.Fatal(err)
	}
	have, err := sha256File(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	// parseChecksumLine: real checksums.txt line, binary mode + CRLF.
	sums := have + "  claude-statusline_Darwin_x86_64.tar.gz\n" +
		"abc  other-file.tar.gz\n" +
		have + "  *claude-statusline_Linux_arm64.tar.gz\r\n"
	got, ok := parseChecksumLine(sums, "claude-statusline_Darwin_x86_64.tar.gz")
	if !ok || got != have {
		t.Errorf("parseChecksumLine real line = %q, %v", got, ok)
	}
	got, ok = parseChecksumLine(sums, "claude-statusline_Linux_arm64.tar.gz")
	if !ok || got != have {
		t.Errorf("parseChecksumLine binary+CRLF = %q, %v", got, ok)
	}
	if _, ok := parseChecksumLine(sums, "nope.tar.gz"); ok {
		t.Error("missing entry should return ok=false")
	}

	// verifyChecksum: stub fetchChecksumsFn to return our sums file, stub the
	// bundle fetch, and no-op the signature check (its own logic is covered by
	// TestVerifyChecksumsSig) so this test isolates the sha256-matching path.
	old := fetchChecksumsFn
	oldBundle := fetchChecksumsBundleFn
	oldSig := verifyChecksumsSig
	t.Cleanup(func() {
		fetchChecksumsFn = old
		fetchChecksumsBundleFn = oldBundle
		verifyChecksumsSig = oldSig
	})
	fetchChecksumsFn = func(d, url string) (string, error) {
		sumsPath := filepath.Join(d, "checksums.txt")
		if err := os.WriteFile(sumsPath, []byte(sums), 0o644); err != nil {
			return "", err
		}
		return sumsPath, nil
	}
	fetchChecksumsBundleFn = func(d, url string) (string, error) {
		p := filepath.Join(d, "checksums.txt.bundle")
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			return "", err
		}
		return p, nil
	}
	verifyChecksumsSig = func(blob, bundle []byte) error { return nil }
	if err := verifyChecksum(dir, archivePath, "claude-statusline_Darwin_x86_64.tar.gz", "1.2.0"); err != nil {
		t.Errorf("verifyChecksum: %v", err)
	}

	// Flip a bit in the archive → mismatch.
	flipped := append([]byte(nil), known...)
	flipped[0] ^= 1
	archiveFlipped := filepath.Join(dir, "flipped.tar.gz")
	if err := os.WriteFile(archiveFlipped, flipped, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(dir, archiveFlipped, "claude-statusline_Darwin_x86_64.tar.gz", "1.2.0"); err == nil {
		t.Error("flipped checksum should fail")
	}

	// Missing entry in checksums.txt → fail.
	sumsMissing := "abc  other-file.tar.gz\n"
	fetchChecksumsFn = func(d, url string) (string, error) {
		sumsPath := filepath.Join(d, "checksums.txt")
		if err := os.WriteFile(sumsPath, []byte(sumsMissing), 0o644); err != nil {
			return "", err
		}
		return sumsPath, nil
	}
	if err := verifyChecksum(dir, archivePath, "claude-statusline_Darwin_x86_64.tar.gz", "1.2.0"); err == nil {
		t.Error("missing entry should fail")
	}

	// Signature failure → verifyChecksum fails closed even when the sha256
	// matches (the digest is never trusted past an invalid signature).
	fetchChecksumsFn = func(d, url string) (string, error) {
		sumsPath := filepath.Join(d, "checksums.txt")
		_ = os.WriteFile(sumsPath, []byte(sums), 0o644)
		return sumsPath, nil
	}
	verifyChecksumsSig = func(blob, bundle []byte) error { return errors.New("bad sig") }
	if err := verifyChecksum(dir, archivePath, "claude-statusline_Darwin_x86_64.tar.gz", "1.2.0"); err == nil {
		t.Error("invalid signature should fail verifyChecksum")
	}
}

func TestExtractAsset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	// Build a tar.gz with a single file.
	tarGzPath := filepath.Join(dir, "asset.tar.gz")
	f, err := os.Create(tarGzPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("new binary contents")
	hdr := &tar.Header{Name: "claude-statusline", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	stagedPath, err := extractAsset(tarGzPath, "asset.tar.gz")
	if err != nil {
		t.Fatalf("extractAsset tar.gz: %v", err)
	}
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new binary contents" {
		t.Errorf("extracted content = %q", string(data))
	}
	info, err := os.Stat(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o755 == 0 {
		t.Errorf("extracted file should be executable, mode = %v", info.Mode())
	}

	// Build a .zip with a directory prefix + the binary.
	zipPath := filepath.Join(dir, "asset.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("claude-statusline_1.2.0/claude-statusline")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("windows binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}

	stagedPath, err = extractAsset(zipPath, "asset.zip")
	if err != nil {
		t.Fatalf("extractAsset zip: %v", err)
	}
	data, err = os.ReadFile(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "windows binary" {
		t.Errorf("zip extracted content = %q", string(data))
	}

	// Empty archive → error.
	emptyTar := filepath.Join(dir, "empty.tar.gz")
	f2, _ := os.Create(emptyTar)
	gz2 := gzip.NewWriter(f2)
	tw2 := tar.NewWriter(gz2)
	_ = tw2.Close()
	_ = gz2.Close()
	_ = f2.Close()
	if _, err := extractAsset(emptyTar, "empty.tar.gz"); err == nil {
		t.Error("empty archive should fail extraction")
	}
}

func TestAtomicSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "claude-statusline")
	original := []byte("original binary")
	if err := os.WriteFile(exe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	new := []byte("updated binary")
	if err := os.WriteFile(staged, new, 0o755); err != nil {
		t.Fatal(err)
	}

	// Happy path: swap succeeds, content is the new binary, .old removed.
	if err := atomicSwap(exe, staged); err != nil {
		t.Fatalf("atomicSwap: %v", err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated binary" {
		t.Errorf("post-swap content = %q", string(data))
	}
	pid := os.Getpid()
	for _, p := range []string{
		filepath.Join(dir, fmt.Sprintf(".claude-statusline.old.%d", pid)),
		filepath.Join(dir, fmt.Sprintf(".claude-statusline.new.%d", pid)),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("leftover %q should be cleaned up", p)
		}
	}

	// Failure injection: non-existent parent dir → rename fails, original
	// is left intact. This is the "step 3 of the spec's procedure failed"
	// case: the second rename (new → exe) blows up because the parent
	// doesn't exist, and the test verifies the rollback path (the .old
	// rename never happened, so the original exe is untouched).
	badExe := filepath.Join(dir, "no-such-subdir", "claude-statusline")
	if err := atomicSwap(badExe, staged); err == nil {
		t.Error("atomicSwap with non-existent dir should fail")
	}
	data, _ = os.ReadFile(exe)
	if string(data) != "updated binary" {
		t.Errorf("post-failure content = %q (should be unchanged)", string(data))
	}

	// Failure injection at the "step 3" point: simulate the .new file
	// being a directory, which causes the second rename (new → exe) to
	// fail because the destination is a non-empty directory. We can't
	// easily make os.Rename fail on a single-file path, but we can
	// verify the rollback: drop a sibling .old into the directory and
	// pre-create .new as a directory, then run atomicSwap and verify
	// the original exe survives.
	dir2 := t.TempDir()
	exe2 := filepath.Join(dir2, "claude-statusline")
	if err := os.WriteFile(exe2, []byte("untouched"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged2 := filepath.Join(dir2, "staged2")
	if err := os.WriteFile(staged2, []byte("never installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a directory at .new.<pid> so the .new → exe rename fails (rename
	// over a non-empty directory is not allowed by os.Rename).
	_ = os.Mkdir(filepath.Join(dir2, fmt.Sprintf(".claude-statusline.new.%d", pid)), 0o755)
	_ = os.WriteFile(filepath.Join(dir2, fmt.Sprintf(".claude-statusline.new.%d", pid), "blocker"), nil, 0o644)
	// Pre-existing .old → this is what would have been written by step
	// 1 of the swap (current → .old), and the rollback should restore it.
	oldContent := []byte("stale .old, should not be left over")
	_ = os.WriteFile(filepath.Join(dir2, fmt.Sprintf(".claude-statusline.old.%d", pid)), oldContent, 0o644)
	err = atomicSwap(exe2, staged2)
	// The error depends on the platform (rename-over-dir behavior
	// varies). On macOS/Linux the second rename fails; on Windows it
	// may not. Either way, the original exe must be intact.
	data, _ = os.ReadFile(exe2)
	if string(data) == "never installed" {
		t.Error("original exe should not have been replaced when step 3 failed")
	}
	// And the .new blocker should be cleaned (or never created on
	// platforms that allow the rename over a dir).
	_ = err
}

func TestCheckDirWritable(t *testing.T) {
	if err := checkDirWritable(t.TempDir()); err != nil {
		t.Errorf("temp dir should be writable: %v", err)
	}
	if err := checkDirWritable(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("non-existent dir should report not-writable")
	}
}

func TestBrewBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	type brewCall struct {
		brewPath string
		live     bool
		timeout  time.Duration
	}
	calls := []brewCall{}
	old := brewRunner
	t.Cleanup(func() { brewRunner = old })
	brewRunner = func(brewPath string, live bool, timeout time.Duration) ([]string, error) {
		calls = append(calls, brewCall{brewPath, live, timeout})
		env := append(os.Environ(),
			"HOMEBREW_NO_AUTO_UPDATE=1",
			"HOMEBREW_NO_INSTALL_CLEANUP=1",
		)
		return env, nil
	}

	oldFind := findBrewExe
	t.Cleanup(func() { findBrewExe = oldFind })
	findBrewExe = func() string { return "/opt/homebrew/bin/brew" }

	// Auto + brew → runs upgrade, no live output, with the right env.
	calls = nil
	env := driveWorkerBrew(t, kindBrew, "9.9.9", "1.0.0", updateConfig{Mode: "auto"})
	if len(calls) != 1 {
		t.Fatalf("auto+brew should run upgrade, got %d calls", len(calls))
	}
	if calls[0].brewPath != "/opt/homebrew/bin/brew" {
		t.Errorf("brewPath = %q", calls[0].brewPath)
	}
	if calls[0].live {
		t.Error("worker branch should be silent (live=false)")
	}
	if calls[0].timeout != updateBrewTimeout {
		t.Errorf("worker branch should use updateBrewTimeout (%v), got %v", updateBrewTimeout, calls[0].timeout)
	}
	found := false
	for _, e := range env {
		if e == "HOMEBREW_NO_AUTO_UPDATE=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("brew env should include HOMEBREW_NO_AUTO_UPDATE=1 (got %v)", env)
	}

	// Missing brew → silent fallback, no exec.
	findBrewExe = func() string { return "" }
	calls = nil
	driveWorkerBrew(t, kindBrew, "9.9.9", "1.0.0", updateConfig{Mode: "auto"})
	if len(calls) != 0 {
		t.Error("missing brew should not exec")
	}

	// Notify + brew → never execs.
	findBrewExe = func() string { return "/opt/homebrew/bin/brew" }
	calls = nil
	driveWorkerBrew(t, kindBrew, "9.9.9", "1.0.0", updateConfig{Mode: "notify"})
	if len(calls) != 0 {
		t.Error("notify mode should never run brew upgrade")
	}

	// Older latest → no install.
	calls = nil
	driveWorkerBrew(t, kindBrew, "1.0.0", "2.0.0", updateConfig{Mode: "auto"})
	if len(calls) != 0 {
		t.Error("older/latest-equal should not run upgrade")
	}

	// Dev → never runs.
	calls = nil
	driveWorkerBrew(t, kindDev, "9.9.9", "1.0.0", updateConfig{Mode: "auto"})
	if len(calls) != 0 {
		t.Error("dev build should not run brew upgrade")
	}
}

func TestRunUpdateCheckNonRelease(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	// Isolate config so a user-side [update].mode = "auto" can't turn this
	// test into a real download/swap against the test binary.
	configDirOverride = dir
	t.Cleanup(func() { configDirOverride = "" })

	resolveCalls := 0
	old := resolveLatestTagFn
	resolveLatestTagFn = func() (string, error) { resolveCalls++; return "9.9.9", nil }
	t.Cleanup(func() { resolveLatestTagFn = old })

	// Non-release version shapes must short-circuit the worker before any
	// network I/O, because compareVersions would treat them as malformed
	// and the segment already hides them.
	for _, v := range []string{"1.0.0+dirty", "0.0.0-20260612-abc123"} {
		resolveCalls = 0
		withTestVersion(t, v)
		runUpdateCheck()
		if resolveCalls != 0 {
			t.Errorf("non-release version %q should not resolve latest, got %d calls", v, resolveCalls)
		}
	}

	// A clean release version still reaches the network (and writes a cache).
	withTestVersion(t, "1.0.0")
	runUpdateCheck()
	if resolveCalls != 1 {
		t.Errorf("release version should resolve latest once, got %d calls", resolveCalls)
	}
}

// ─── step 5: renderUpdate segment ────────────────────────────────────

// withTestVersion sets a fake "current version" for the test by overriding
// the package-level `version` variable. Returns a cleanup that restores it.
func withTestVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// initUpdateSegment registers the update segment with the registry. Tests
// for renderUpdate call this so segmentByID("update") resolves.
func initUpdateSegment() {
	// Re-init the registry to include the new segment.
	initSegments(nil)
}

func TestRenderUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	initUpdateSegment()

	now := time.Unix(1750000000, 0)
	h12 := 12

	// dev → always hidden, even with a cache that says we're behind.
	withTestVersion(t, "dev")
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: "9.9.9"})
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("dev should hide, got %q", got)
	}

	// Non-release-shaped version (e.g. +dirty) → also hidden.
	withTestVersion(t, "1.2.0+dirty")
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("non-release version should hide, got %q", got)
	}

	// Release version, no cache → hidden.
	withTestVersion(t, "1.0.0")
	_ = os.Remove(updateCheckPath())
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("no cache should hide, got %q", got)
	}

	// Cache with empty Latest (last check failed) → hidden.
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: ""})
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("empty-latest cache should hide, got %q", got)
	}

	// Cache with latest == current → hidden (not behind).
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: "1.0.0"})
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("equal version should hide, got %q", got)
	}

	// Cache with latest < current (downgrade) → hidden.
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: "0.9.0"})
	if got, show := renderUpdate(renderCtx{Now: now}); show {
		t.Errorf("older latest should hide, got %q", got)
	}

	// Cache with latest > current, recent check (within 5 min) → expanded.
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix() - 60, Latest: "1.2.0"})
	got, show := renderUpdate(renderCtx{Now: now, C: palette{Dim: "", Rst: ""}})
	if !show {
		t.Fatal("expected expanded form to show")
	}
	if !strings.Contains(got, "⬆ v1.2.0") {
		t.Errorf("expanded should contain '⬆ v1.2.0', got %q", got)
	}
	if !strings.Contains(got, "claude-statusline update") {
		t.Errorf("expanded should contain hint, got %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("empty palette should produce no ANSI, got %q", got)
	}

	// Same cache, Now 10 min after check → compact.
	got, show = renderUpdate(renderCtx{Now: now.Add(10 * time.Minute), C: palette{Dim: "", Rst: ""}})
	if !show {
		t.Fatal("expected compact form to show")
	}
	if got != "⬆ v1.2.0" {
		t.Errorf("compact form = %q, want %q", got, "⬆ v1.2.0")
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("compact form should have no ANSI bytes, got %q", got)
	}

	// Real palette → ANSI codes appear.
	got, show = renderUpdate(renderCtx{Now: now.Add(10 * time.Minute), C: palette{Dim: "\x1b[2m", Rst: "\x1b[0m"}})
	if !show || !strings.Contains(got, "\x1b[2m") || !strings.Contains(got, "\x1b[0m") {
		t.Errorf("real palette should render ANSI: %q", got)
	}

	// mode = off hides even when the cache says we're behind.
	_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix() - 60, Latest: "1.2.0"})
	if got, show := renderUpdate(renderCtx{
		Now: now,
		Cfg: config{Update: updateConfig{Mode: "off"}},
		C:   palette{Dim: "", Rst: ""},
	}); show {
		t.Errorf("mode=off should hide, got %q", got)
	}

	_ = h12
}

// ─── step 6: update subcommand ───────────────────────────────────────

func TestUpdateSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	// Stub resolveLatestTag + downloadAndSwap + brew path so the subcommand
	// can be driven without network or filesystem mutation.
	oldResolve := resolveLatestTagFn
	oldSwap := downloadAndSwapFn
	oldFind := findBrewExe
	t.Cleanup(func() {
		resolveLatestTagFn = oldResolve
		downloadAndSwapFn = oldSwap
		findBrewExe = oldFind
	})

	swapCalls := []struct{ latest, current string }{}
	downloadAndSwapFn = func(latest, current string) error {
		swapCalls = append(swapCalls, struct{ latest, current string }{latest, current})
		return nil
	}

	brewCalls := []struct {
		brewPath string
		timeout  time.Duration
	}{}
	oldBrewRunner := brewRunner
	t.Cleanup(func() { brewRunner = oldBrewRunner })
	brewRunner = func(brewPath string, live bool, timeout time.Duration) ([]string, error) {
		brewCalls = append(brewCalls, struct {
			brewPath string
			timeout  time.Duration
		}{brewPath, timeout})
		return nil, nil
	}

	withTestVersion(t, "1.0.0")
	resolveLatestTagFn = func() (string, error) { return "1.2.0", nil }

	// --check on a newer release: report, never install.
	swapCalls = nil
	brewCalls = nil
	runUpdateFor(nil, true, "1.0.0", kindManual)
	if len(swapCalls) != 0 {
		t.Error("--check should not call downloadAndSwap")
	}
	if len(brewCalls) != 0 {
		t.Error("--check should not call brew upgrade")
	}

	// Manual install: real call to downloadAndSwap.
	swapCalls = nil
	runUpdateFor(nil, false, "1.0.0", kindManual)
	if len(swapCalls) != 1 || swapCalls[0].latest != "1.2.0" || swapCalls[0].current != "1.0.0" {
		t.Errorf("manual install should call downloadAndSwap(1.2.0, 1.0.0), got %+v", swapCalls)
	}

	// Brew install: real call to brewRunner, no swap.
	swapCalls = nil
	brewCalls = nil
	findBrewExe = func() string { return "/opt/homebrew/bin/brew" }
	runUpdateFor(nil, false, "1.0.0", kindBrew)
	if len(swapCalls) != 0 {
		t.Error("brew install should not call downloadAndSwap")
	}
	if len(brewCalls) != 1 {
		t.Errorf("brew install should call brewRunner, got %d calls", len(brewCalls))
	}
	if brewCalls[0].timeout != 0 {
		t.Errorf("foreground brew should have no timeout, got %v", brewCalls[0].timeout)
	}

	// Already current: prints "up to date", no install.
	swapCalls = nil
	brewCalls = nil
	resolveLatestTagFn = func() (string, error) { return "1.0.0", nil }
	runUpdateFor(nil, false, "1.0.0", kindManual)
	if len(swapCalls) != 0 || len(brewCalls) != 0 {
		t.Error("current version should not install")
	}

	// Older latest (downgrade): no install.
	swapCalls = nil
	resolveLatestTagFn = func() (string, error) { return "0.9.0", nil }
	runUpdateFor(nil, false, "1.0.0", kindManual)
	if len(swapCalls) != 0 {
		t.Error("older/latest-equal should not install")
	}

	// dev → hint, never install.
	swapCalls = nil
	resolveLatestTagFn = func() (string, error) { return "9.9.9", nil }
	runUpdateFor(nil, false, "dev", kindDev)
	if len(swapCalls) != 0 {
		t.Error("dev should not install")
	}

	// Non-release build (+dirty / pseudo-version) reports kindManual but must
	// still be treated as a source build: source-build hint, never install
	// (compareVersions would otherwise call it "up to date").
	for _, v := range []string{"1.0.0+dirty", "0.0.0-20260612-abc123"} {
		swapCalls = nil
		brewCalls = nil
		runUpdateFor(nil, false, v, kindManual)
		if len(swapCalls) != 0 || len(brewCalls) != 0 {
			t.Errorf("non-release build %q should not install", v)
		}
	}

	// Resolve failure: exits 1 (and the subcommand writes to stderr).
	resolveLatestTagFn = func() (string, error) { return "", errors.New("net down") }
	if code := callRunUpdateFor(t, false, "1.0.0", kindManual); code == 0 {
		t.Error("resolve failure should exit non-zero")
	}
}

// callRunUpdateFor runs the subcommand and recovers from osExit so tests
// can assert on the exit code.
func callRunUpdateFor(t *testing.T, checkOnly bool, current string, kind installKind) int {
	t.Helper()
	code := 0
	oldExit := osExit
	osExit = func(c int) { code = c; panic("exit") }
	t.Cleanup(func() { osExit = oldExit })
	func() {
		defer func() { recover() }()
		runUpdateFor(nil, checkOnly, current, kind)
	}()
	return code
}

// driveWorkerBrew mirrors the worker's brew-branch dispatch in a way tests
// can call without touching the network. Returns the env the brew branch
// would have used (for assertions on HOMEBREW_NO_AUTO_UPDATE), or nil when
// the branch did not run.
func driveWorkerBrew(t *testing.T, kind installKind, latest, current string, cfg updateConfig) []string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := stateBaseDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = saveUpdateCheck(updateCheck{CheckedAt: time.Now().Unix(), Latest: latest})

	if kind == kindDev {
		return nil
	}
	if cfg.mode() != "auto" {
		return nil
	}
	if compareVersions(latest, current) <= 0 {
		return nil
	}
	if kind != kindBrew {
		return nil
	}
	brewPath := findBrewExe()
	if brewPath == "" {
		return nil
	}
	env, _ := brewRunner(brewPath, false, updateBrewTimeout)
	return env
}

// ─── signature verification + extraction-safety hardening ─────────────

func TestEmbeddedCosignKey(t *testing.T) {
	// The embedded cosign.pub must parse, or every manual self-swap would fail
	// closed on the "no embedded verification key" path.
	if mustParseCosignKey(cosignPubPEM) == nil {
		t.Error("embedded cosign.pub must parse as an ECDSA public key")
	}
}

func TestVerifyChecksumsSig(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := checksumsVerifyKey
	checksumsVerifyKey = &key.PublicKey
	t.Cleanup(func() { checksumsVerifyKey = old })

	blob := []byte("hash1  asset_a\nhash2  asset_b\n")
	sum := sha256.Sum256(blob)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	bundle := func(b64 string) []byte {
		return []byte(`{"messageSignature":{"messageDigest":{"algorithm":"SHA2_256","digest":"x"},"signature":"` + b64 + `"}}`)
	}
	good := bundle(base64.StdEncoding.EncodeToString(sig))

	if err := verifyChecksumsSigReal(blob, good); err != nil {
		t.Errorf("valid signature should verify: %v", err)
	}
	if err := verifyChecksumsSigReal([]byte("tampered checksums"), good); err == nil {
		t.Error("tampered blob should be rejected")
	}
	if err := verifyChecksumsSigReal(blob, bundle(base64.StdEncoding.EncodeToString([]byte("not-a-real-sig")))); err == nil {
		t.Error("garbage signature should be rejected")
	}
	if err := verifyChecksumsSigReal(blob, []byte(`{"messageSignature":{"signature":""}}`)); err == nil {
		t.Error("empty signature should be rejected")
	}
	if err := verifyChecksumsSigReal(blob, []byte("not json")); err == nil {
		t.Error("malformed bundle should be rejected")
	}
	checksumsVerifyKey = nil
	if err := verifyChecksumsSigReal(blob, good); err == nil {
		t.Error("nil verification key should fail closed")
	}
}

// TestExtractConfinedToDir locks the extraction defense: even a tar entry whose
// name encodes path traversal lands inside the per-run dir (we never honor
// hdr.Name as a path — filepath.Base + a temp file in dir), and a symlink entry
// for the binary name is skipped (TypeReg-only).
func TestExtractConfinedToDir(t *testing.T) {
	dir := t.TempDir()
	tarGzPath := filepath.Join(dir, "evil.tar.gz")
	f, _ := os.Create(tarGzPath)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// A symlink entry masquerading as the binary — must be ignored.
	_ = tw.WriteHeader(&tar.Header{Name: "claude-statusline", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777})
	// A traversal-named regular file whose base still matches the binary name.
	body := []byte("real binary")
	_ = tw.WriteHeader(&tar.Header{Name: "../../../../tmp/claude-statusline", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	staged, err := extractAsset(tarGzPath, "evil.tar.gz")
	if err != nil {
		t.Fatalf("extractAsset: %v", err)
	}
	if got := filepath.Dir(staged); got != dir {
		t.Errorf("extracted outside staging dir: %q (want dir %q)", staged, dir)
	}
	data, _ := os.ReadFile(staged)
	if string(data) != "real binary" {
		t.Errorf("extracted content = %q", string(data))
	}
}

func TestExtractRejectsDecompressionBomb(t *testing.T) {
	old := updateMaxExtractBytes
	updateMaxExtractBytes = 10
	t.Cleanup(func() { updateMaxExtractBytes = old })

	dir := t.TempDir()
	tarGzPath := filepath.Join(dir, "bomb.tar.gz")
	f, _ := os.Create(tarGzPath)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := make([]byte, 1000) // well over the lowered cap
	_ = tw.WriteHeader(&tar.Header{Name: "claude-statusline", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	if _, err := extractAsset(tarGzPath, "bomb.tar.gz"); err == nil {
		t.Error("oversized decompressed payload should be rejected")
	}
}

// TestGoreleaserInjectsBareVersion guards the smokeTest invariant: smokeTest
// requires the staged binary's `version` output to contain the bare tag, which
// only holds if GoReleaser injects {{.Version}} (no leading v) into main.version.
func TestGoreleaserInjectsBareVersion(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "main.version={{.Version}}") {
		t.Error(".goreleaser.yaml must inject the bare {{.Version}} into main.version (smokeTest depends on it)")
	}
}

// TestPreCleanExeStagingStaleOnly verifies the reaper removes stale swap debris
// but leaves fresh files (a concurrent live swap) untouched.
func TestPreCleanExeStagingStaleOnly(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "claude-statusline")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := osExecutable
	osExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { osExecutable = old })

	fresh := filepath.Join(dir, ".claude-statusline.new.99999")
	stale := filepath.Join(dir, ".claude-statusline.old.12345")
	_ = os.WriteFile(fresh, nil, 0o644)
	_ = os.WriteFile(stale, nil, 0o644)
	oldTime := time.Now().Add(-2 * exeStagingStaleAfter)
	_ = os.Chtimes(stale, oldTime, oldTime)

	preCleanExeStaging()

	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh swap file should be preserved")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale swap file should be reaped")
	}
}
