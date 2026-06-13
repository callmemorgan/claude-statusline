package main

// ─── Update checking + self-swap ──────────────────────────────────────

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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
	"strconv"
	"strings"
	"time"
)

// installKind classifies the running binary so the worker can choose between
// self-swap and `brew upgrade`. kindDev is the dev/source-build short-circuit
// — it disables the whole feature and matches the release-notes carve-out so
// tests and goldens stay inert.
type installKind int

const (
	kindUnknown installKind = iota
	kindDev
	kindBrew
	kindManual
)

// updateRepoOwner / updateRepoName are compile-time constants; a
// configurable update URL would be a foot-gun.
const (
	updateRepoOwner = "callmemorgan"
	updateRepoName  = "claude-statusline"
)

// updateCheck is the on-disk cache. Stale-or-missing is the only signal the
// render path needs; the worker writes {now, ""} on network failure so a dead
// network doesn't respawn the worker on every render.
type updateCheck struct {
	CheckedAt int64  `json:"checked_at"`
	Latest    string `json:"latest"` // no leading v; "" = last check failed
}

// updateCheckPath is a sibling of last-version.json inside the state dir.
func updateCheckPath() string {
	return filepath.Join(stateBaseDir(), "update.json")
}

func loadUpdateCheck() (updateCheck, bool) {
	data, err := os.ReadFile(updateCheckPath())
	if err != nil {
		return updateCheck{}, false
	}
	var c updateCheck
	if err := json.Unmarshal(data, &c); err != nil {
		return updateCheck{}, false
	}
	return c, true
}

func saveUpdateCheck(c updateCheck) error {
	if err := os.MkdirAll(stateBaseDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return writeFileAtomic(updateCheckPath(), data)
}

// detectInstallKind classifies the running binary. Pure on its inputs
// (exePath is the resolved path; "" + version "dev" → kindDev without any
// filesystem call) so the table test needs no real symlinks. The caller
// resolves the path via os.Executable() + filepath.EvalSymlinks — brew's
// bin symlink points into the Cellar, which is the reliable signal.
func detectInstallKind(exePath, version string) installKind {
	if version == "dev" {
		return kindDev
	}
	low := strings.ToLower(exePath)
	if strings.Contains(low, "/cellar/") || strings.Contains(low, "/homebrew/") {
		return kindBrew
	}
	return kindManual
}

// currentExePath resolves the running binary's path, following symlinks when
// possible. On a symlink-resolution error it falls back to the unresolved
// os.Executable() path rather than discarding it: filepath.EvalSymlinks
// returns "" on error, and "" would misclassify a Homebrew install (whose bin
// symlink path still contains "/homebrew/") as a manual install — sending the
// auto worker down the self-swap path instead of `brew upgrade`.
func currentExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// compareVersions returns -1/0/+1 for MAJOR.MINOR.REVISION strings (leading
// "v" tolerated). Malformed input compares as equal-to-everything (0) so
// garbage from the network can never trigger a swap.
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return [3]int{}, false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		// strconv.ParseUint rejects empty strings, signs, and non-digits, and
		// — unlike a hand-rolled n=n*10+digit loop — overflow. So an oversized
		// tag from the network parses as malformed (compareVersions → 0) rather
		// than silently wrapping to a value that could look "newer".
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = int(n)
	}
	return out, true
}

// ─── Update check (render path + worker) ──────────────────────────────

// expandedWindow is the daily disclosure window after a check: the segment
// renders the full hint, then compacts. Five minutes, locked.
const expandedWindow = 5 * time.Minute

// spawnUpdateCheck is the detached worker entrypoint. Tests stub it via
// package var, mirroring spawnRefresher in plugins.go.
var spawnUpdateCheck = spawnUpdateCheckReal

// spawnUpdateCheckReal is the production spawn: detached exec.Command, no
// env plumbing (the worker re-reads config itself). Returns an error so the
// caller can release the lock if the worker never started.
func spawnUpdateCheckReal() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(exe, "update-check")
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	applyDetachSysProcAttr(c)
	if err := c.Start(); err != nil {
		return err
	}
	_ = c.Process.Release()
	return nil
}

// maybeSpawnUpdateCheck reads the cache and, if it's stale, spawns a
// detached `update-check` worker. Returns immediately under all conditions:
// no network I/O, no blocking, no stdout/stderr output. Called from runRender
// after the print loop, next to st.Save().
func maybeSpawnUpdateCheck(cfg updateConfig, now time.Time) {
	if cfg.mode() == "off" {
		return
	}
	current, _, _ := versionString()
	if !isReleaseVersion(current) {
		return
	}
	exe := currentExePath()
	maybeSpawnUpdateCheckFor(cfg, now, detectInstallKind(exe, current))
}

// maybeSpawnUpdateCheckFor is the kind-aware helper; tests drive it directly
// with a known kind to exercise each branch. Production callers go through
// maybeSpawnUpdateCheck.
func maybeSpawnUpdateCheckFor(cfg updateConfig, now time.Time, kind installKind) {
	if cfg.mode() == "off" {
		return
	}
	if kind == kindDev {
		return
	}
	if cache, ok := loadUpdateCheck(); ok {
		// A future CheckedAt (clock skew, restored backup, hand-edited cache)
		// makes elapsed negative; without the >= 0 guard that reads as
		// "always fresh" and wedges the check forever. Treat it as stale so
		// the next render spawns a worker and overwrites it with a sane time.
		if elapsed := now.Unix() - cache.CheckedAt; elapsed >= 0 && elapsed < int64(cfg.checkEvery().Seconds()) {
			return
		}
	}
	if tryAcquireLock(updateLockPath(), updateStaleLockTolerance) {
		if err := spawnUpdateCheck(); err != nil {
			_ = os.Remove(updateLockPath())
		}
	}
}

// updateLockPath is the lock file the render path and the worker share to
// serialize concurrent checks. Lock-acquire-and-spawn matches the plugin
// pattern in plugins.go.
func updateLockPath() string {
	return filepath.Join(stateBaseDir(), "update-check.lock")
}

// updateBrewTimeout is the worker's per-upgrade budget for `brew upgrade`.
// The foreground `claude-statusline update` subcommand uses no timeout.
const updateBrewTimeout = 5 * time.Minute

// updateStaleLockTolerance is how long an orphaned lock is considered alive.
// It must be strictly larger than updateBrewTimeout so a slow brew upgrade
// isn't mistaken for a dead worker and reaped while still running.
const updateStaleLockTolerance = updateBrewTimeout + 2*time.Minute

// runUpdate is the foreground, explicit subcommand. It ignores [update].mode
// (explicit intent) but not the safety rails (kind, version compare, checksum,
// smoke-test). Behavior:
//   kindDev        → hint to go install; exit 0.
//   kindBrew       → brew upgrade (live output); missing brew → exit 1.
//   newer exists   → download + swap, share the worker's pipeline.
//   already current → "up to date"; exit 0.
//   --check        → resolve + report; never install.
func runUpdate(args []string) {
	checkOnly := false
	for _, a := range args {
		if a == "--check" {
			checkOnly = true
		}
	}

	current, _, _ := versionString()
	exe := currentExePath()
	kind := detectInstallKind(exe, current)
	runUpdateFor(args, checkOnly, current, kind)
}

// runUpdateFor is the testable inner path: takes the kind as a parameter
// so tests can drive each branch without the real os.Executable() coming
// back as a Go test binary. Production callers go through runUpdate.
func runUpdateFor(args []string, checkOnly bool, current string, kind installKind) {
	// kindDev only catches version == "dev"; a +dirty or Go pseudo-version
	// build reports kindManual but is still a source build. Gate on the same
	// isReleaseVersion check the worker and segment use, so `update` prints the
	// source-build hint instead of "up to date" (compareVersions treats the
	// non-release current version as malformed and would otherwise report 0).
	if kind == kindDev || !isReleaseVersion(current) {
		fmt.Println("claude-statusline is a source build; update with `go install github.com/" + updateRepoOwner + "/" + updateRepoName + "@latest`.")
		return
	}

	latest, err := resolveLatestTagFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude-statusline update: could not resolve latest release: %v\n", err)
		osExit(1)
		return
	}
	_ = saveUpdateCheck(updateCheck{CheckedAt: time.Now().Unix(), Latest: latest})

	cmp := compareVersions(latest, current)
	if cmp <= 0 {
		fmt.Printf("claude-statusline v%s is up to date.\n", current)
		return
	}
	fmt.Printf("Latest release: v%s (current v%s)\n", latest, current)

	if checkOnly {
		return
	}

	switch kind {
	case kindBrew:
		brewPath := findBrewExe()
		if brewPath == "" {
			fmt.Fprintln(os.Stderr, "claude-statusline update: brew not found; please run `brew upgrade claude-statusline` manually.")
			osExit(1)
			return
		}
		// The subcommand path uses the same runner the worker uses (with
		// live output) so tests can stub one seam for both call sites.
		_, err := brewRunner(brewPath, true, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "claude-statusline update: brew upgrade failed: %v\n", err)
			osExit(1)
			return
		}
	case kindManual:
		if err := downloadAndSwapFn(latest, current); err != nil {
			fmt.Fprintf(os.Stderr, "claude-statusline update: %v\n", err)
			osExit(1)
			return
		}
		fmt.Printf("Updated v%s → v%s. Run `claude-statusline release-notes` to see what changed.\n", current, latest)
	}
}

// ─── Worker: tag resolution, brew upgrade, self-swap ──────────────────

// updateMaxDownloadBytes caps any single download at 64 MiB. We never
// trust Content-Length alone; the read loops count bytes and abort on
// overflow. The cap is generous (a real release archive is ~7 MiB) and
// guards against a malicious or corrupted mirror.
const updateMaxDownloadBytes = 64 * 1024 * 1024

// userAgent is sent on every update-related request. The version is the
// running binary's, which makes the request self-identifying in GH logs
// (and lets us correlate errors when users paste the headers).
func updateUserAgent() string {
	v, _, _ := versionString()
	if v == "dev" {
		return "claude-statusline/dev"
	}
	return "claude-statusline/" + v
}

// resolveLatestTag hits GitHub's /releases/latest with redirects disabled
// and parses the tag from the 302 Location header. Returns "" on any
// failure (network, non-302, unparseable tag).
func resolveLatestTag() (string, error) {
	url := "https://github.com/" + updateRepoOwner + "/" + updateRepoName + "/releases/latest"
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", updateUserAgent())
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, updateMaxDownloadBytes))
	switch resp.StatusCode {
	case http.StatusFound, http.StatusMovedPermanently,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		// expected redirect responses
	default:
		return "", fmt.Errorf("unexpected status %d from releases/latest", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	const marker = "/releases/tag/"
	idx := strings.Index(loc, marker)
	if idx < 0 {
		return "", fmt.Errorf("could not parse tag from Location: %q", loc)
	}
	tag := strings.TrimPrefix(loc[idx+len(marker):], "v")
	tag = strings.TrimRight(tag, "/")
	tag = strings.TrimSpace(tag)
	if _, ok := parseVersion(tag); !ok {
		return "", fmt.Errorf("invalid tag in Location: %q", loc)
	}
	return tag, nil
}

// assetName returns the GoReleaser asset filename for a given
// (goos, goarch) pair. Mirrors the template in .goreleaser.yaml exactly —
// a rename in that file must fail TestAssetName.
func assetName(goos, goarch string) string {
	osTitle := strings.ToUpper(goos[:1]) + strings.ToLower(goos[1:])
	arch := goarch
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "386":
		arch = "i386"
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return "claude-statusline_" + osTitle + "_" + arch + "." + ext
}

func assetURL(tag, name string) string {
	return "https://github.com/" + updateRepoOwner + "/" + updateRepoName + "/releases/download/v" + tag + "/" + name
}

func checksumsURL(tag string) string {
	return "https://github.com/" + updateRepoOwner + "/" + updateRepoName + "/releases/download/v" + tag + "/checksums.txt"
}

// fetchToTemp downloads url to a fresh file in stateBaseDir()/staging/,
// bounded by updateMaxDownloadBytes. Returns the path to the file. The
// caller is responsible for cleaning up the staging dir.
func fetchToTemp(url, name string) (string, error) {
	if err := os.MkdirAll(stagingDir(), 0o755); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", updateUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	limited := io.LimitReader(resp.Body, updateMaxDownloadBytes+1)
	out, err := os.CreateTemp(stagingDir(), name+".*")
	if err != nil {
		return "", err
	}
	written, err := io.Copy(out, limited)
	if err != nil {
		out.Close()
		os.Remove(out.Name())
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	if written > updateMaxDownloadBytes {
		os.Remove(out.Name())
		return "", fmt.Errorf("download exceeded %d bytes", updateMaxDownloadBytes)
	}
	return out.Name(), nil
}

func stagingDir() string {
	return filepath.Join(stateBaseDir(), "staging")
}

func cleanupStaging() {
	_ = os.RemoveAll(stagingDir())
}

// verifyChecksum fetches checksums.txt, finds the line for name, and
// confirms the file at path has the expected sha256.
func verifyChecksum(path, name, tag string) error {
	sumsPath, err := fetchChecksumsFn(checksumsURL(tag))
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer os.Remove(sumsPath)
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	want, ok := parseChecksumLine(string(data), name)
	if !ok {
		return fmt.Errorf("no checksum for %q in checksums.txt", name)
	}
	have, err := sha256File(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, have) {
		return fmt.Errorf("checksum mismatch: have %s, want %s", have, want)
	}
	return nil
}

// parseChecksumLine finds the line for name and returns the leading hex
// digest. GoReleaser writes "<sha256-hex>  claude-statusline_Darwin_x86_64.tar.gz".
// Tolerates binary-mode "*name" and CRLF.
func parseChecksumLine(sums, name string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		line = strings.TrimRight(line, "\r")
		idx := strings.LastIndex(line, "  ")
		if idx < 0 {
			continue
		}
		left, right := line[:idx], line[idx+2:]
		right = strings.TrimPrefix(right, "*")
		if right == name {
			return strings.TrimSpace(left), true
		}
	}
	return "", false
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksumsFn is the test seam for verifyChecksum. The real
// implementation downloads checksums.txt; tests stub it with a precomputed
// sums file. Mirrors the spawnRefresher pattern.
var fetchChecksumsFn = func(url string) (string, error) {
	return fetchToTemp(url, "checksums.txt")
}

// resolveLatestTagFn is the test seam for the worker and the `update`
// subcommand. The real implementation makes an HTTPS request to GitHub;
// tests stub it with a known tag.
var resolveLatestTagFn = resolveLatestTag

// downloadAndSwapFn is the test seam for the worker's manual-install
// branch and the `update` subcommand. The real implementation downloads
// + verifies + extracts + smoke-tests + swaps; tests stub it to assert
// on whether it was called (and with which args).
var downloadAndSwapFn = downloadAndSwap

// osExit is the seam for subcommand exit-code tests. The real os.Exit
// is replaced by a panic in tests so the goroutine can recover and the
// test can continue. runUpdateFor uses this so tests can assert on
// exit codes without terminating the test runner.
var osExit = os.Exit

// extractAsset pulls the inner claude-statusline binary out of a .tar.gz
// or .zip archive, returning the path to the extracted file (chmod 0755).
func extractAsset(archivePath, name string) (string, error) {
	if strings.HasSuffix(name, ".zip") {
		return extractZip(archivePath)
	}
	return extractTarGz(archivePath)
}

func extractTarGz(archivePath string) (string, error) {
	if err := os.MkdirAll(stagingDir(), 0o755); err != nil {
		return "", err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("claude-statusline binary not found in archive")
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != "claude-statusline" && base != "claude-statusline.exe" {
			continue
		}
		outPath := filepath.Join(stagingDir(), "claude-statusline.extracted")
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(outPath)
			return "", err
		}
		if err := out.Close(); err != nil {
			os.Remove(outPath)
			return "", err
		}
		return outPath, nil
	}
}

func extractZip(archivePath string) (string, error) {
	if err := os.MkdirAll(stagingDir(), 0o755); err != nil {
		return "", err
	}
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if base != "claude-statusline" && base != "claude-statusline.exe" {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return "", err
		}
		outPath := filepath.Join(stagingDir(), "claude-statusline.extracted")
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			src.Close()
			return "", err
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			src.Close()
			os.Remove(outPath)
			return "", err
		}
		src.Close()
		if err := out.Close(); err != nil {
			os.Remove(outPath)
			return "", err
		}
		return outPath, nil
	}
	return "", errors.New("claude-statusline binary not found in archive")
}

// smokeTest runs the staged binary with "version" and requires the output
// to contain the expected version. A 2-second timeout guards against a
// binary that hangs.
func smokeTest(stagedPath, expectedVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, stagedPath, "version")
	out, err := c.Output()
	if err != nil {
		return fmt.Errorf("staged binary failed to run: %w", err)
	}
	if !strings.Contains(string(out), expectedVersion) {
		return fmt.Errorf("staged binary reported %q, expected to contain %q", strings.TrimSpace(string(out)), expectedVersion)
	}
	return nil
}

// resolveBrew looks up brew on PATH and falls back to the common install
// locations. Returns "" if no brew is found.
func resolveBrew() string {
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findBrewExe is the test seam for the brew branch: tests inject their own
// path via this package var. Mirrors spawnRefresher.
var findBrewExe = resolveBrew

// brewRunner runs `brew upgrade claude-statusline` with rails that keep
// the worker polite to the user's system. live=true streams output to the
// caller's terminal; live=false discards it. timeout=0 means no timeout
// (used by the foreground subcommand); otherwise a context with that timeout
// wraps the exec. Tests can replace it via the package var to assert on the
// brew invocation without running it.
//
// Returns the env it would have used (so tests can assert
// HOMEBREW_NO_AUTO_UPDATE) and the run error. The env capture is the
// shape most testable, since reading os.Environ() before the runner
// appends is a bug magnet.
var brewRunner = func(brewPath string, live bool, timeout time.Duration) ([]string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	c := exec.CommandContext(ctx, brewPath, "upgrade", "claude-statusline")
	c.Env = append(os.Environ(),
		"HOMEBREW_NO_AUTO_UPDATE=1",
		"HOMEBREW_NO_INSTALL_CLEANUP=1",
	)
	if live {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	} else {
		c.Stdout = io.Discard
		c.Stderr = io.Discard
	}
	return c.Env, c.Run()
}

// downloadAndSwap runs the full self-swap pipeline: download the asset,
// verify checksum, extract, smoke-test, then atomically swap. Used by both
// the worker (silent) and the foreground `update` subcommand (loud).
func downloadAndSwap(latest, current string) error {
	_ = current // unused; kept for signature parity with downloadAndSwapFn.
	want := assetName(runtime.GOOS, runtime.GOARCH)
	if !strings.HasSuffix(want, ".tar.gz") && !strings.HasSuffix(want, ".zip") {
		return fmt.Errorf("asset name %q has unknown format", want)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Fail fast if the exe's directory isn't writable (e.g. a root-owned
	// /usr/local/bin for a non-root user) so auto mode doesn't re-download and
	// re-verify the whole archive every interval only to fail at the final
	// in-place swap — and so the foreground `update` gives a clear reason.
	if err := checkDirWritable(filepath.Dir(exe)); err != nil {
		return fmt.Errorf("cannot update in place (%s): %w", filepath.Dir(exe), err)
	}
	defer cleanupStaging()
	preCleanExeStaging()

	archivePath, err := fetchToTemp(assetURL(latest, want), want)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	if err := verifyChecksum(archivePath, want, latest); err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	stagedPath, err := extractAsset(archivePath, want)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	if err := smokeTest(stagedPath, latest); err != nil {
		return fmt.Errorf("smoke test: %w", err)
	}
	if err := atomicSwap(exe, stagedPath); err != nil {
		return fmt.Errorf("swap: %w", err)
	}
	return nil
}

// checkDirWritable reports whether dir accepts new files, by creating and
// immediately removing a temp file. Only runs on the (rare) install path,
// never on the render path.
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// preCleanExeStaging removes any leftover .old / .new in the exe's
// directory from a previous interrupted run. Runs at the start of every
// swap so a crashed worker can't pile up.
func preCleanExeStaging() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	_ = os.Remove(filepath.Join(dir, ".claude-statusline.old"))
	_ = os.Remove(filepath.Join(dir, ".claude-statusline.new"))
}

// atomicSwap copies the staged binary into the exe's directory as
// .claude-statusline.new, then renames current→.old and .new→current in
// the same directory (atomic). On failure, .old is restored; .old is
// removed at the end (Windows can keep it alive while the old process is
// still running, so on Windows the cleanup is best-effort and the next-run
// preCleanExeStaging will retry).
func atomicSwap(exePath, stagedPath string) error {
	dir := filepath.Dir(exePath)
	newPath := filepath.Join(dir, ".claude-statusline.new")
	oldPath := filepath.Join(dir, ".claude-statusline.old")

	// Stage: copy (not rename) stagedPath into newPath, because the
	// staging dir may be on a different filesystem than the exe.
	if err := copyFile(stagedPath, newPath, 0o755); err != nil {
		return err
	}

	// Flip: current → .old, .new → current. Both happen in the same dir.
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	if err := os.Rename(newPath, exePath); err != nil {
		// Rollback: put .old back as the exe.
		if rbErr := os.Rename(oldPath, exePath); rbErr != nil {
			return fmt.Errorf("swap failed (%v) and rollback also failed (%v): binary may be at %s", err, rbErr, oldPath)
		}
		_ = os.Remove(newPath)
		return err
	}

	// Best-effort cleanup.
	_ = os.Remove(oldPath)
	return nil
}

// copyFile copies src to dst with the given mode, via a temp file in the
// same directory as dst (so the final rename is atomic).
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// runUpdateCheck is the worker's entrypoint. The render path never invokes
// this directly — it's the value of the `update-check` dispatch case. All
// worker errors are silent: this is detached, there's nowhere to print.
func runUpdateCheck() {
	defer func() { _ = os.Remove(updateLockPath()) }()

	current, _, _ := versionString()
	exe := currentExePath()
	kind := detectInstallKind(exe, current)
	if kind == kindDev || !isReleaseVersion(current) {
		return
	}
	cfg, _ := loadConfigWarn()
	// Honor mode=off in the worker itself, not just at the spawn gate: the
	// `update-check` subcommand is directly dispatchable and mode can flip
	// between spawn and exec, so "off = no network, ever" must hold here too.
	if cfg.Update.mode() == "off" {
		return
	}

	now := time.Now()
	latest, err := resolveLatestTagFn()
	if err != nil {
		// Write the failure record so the render path doesn't keep
		// respawning us on every render. Silent on write error.
		_ = saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: ""})
		return
	}
	if err := saveUpdateCheck(updateCheck{CheckedAt: now.Unix(), Latest: latest}); err != nil {
		return
	}
	if cfg.Update.mode() != "auto" {
		return
	}
	if compareVersions(latest, current) <= 0 {
		return
	}
	switch kind {
	case kindBrew:
		brewPath := findBrewExe()
		if brewPath == "" {
			return
		}
		// Brew upgrade is slow; the runner discards output. Failure is
		// silent — the next interval retries.
		_, _ = brewRunner(brewPath, false, updateBrewTimeout)
	case kindManual:
		_ = downloadAndSwapFn(latest, current)
	}
}
