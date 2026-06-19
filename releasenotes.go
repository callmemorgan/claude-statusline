package main

// ─── Release Notes ───────────────────────────────────────────────────
//
// `claude-statusline release-notes` prints notes for the current or any past
// version, sourced from the embedded CHANGELOG.md (no network). After an
// upgrade, the render path briefly replaces the normal statusline output
// with a short announcement of what's new; both flows share the same
// embedded data and pure formatter.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/sys"
	"github.com/callmemorgan/claude-statusline/internal/version"
)

//go:embed CHANGELOG.md
var changelogRaw string

// releaseBullet is one user-facing changelog bullet with an optional
// importance weight. Higher values are more important; ordinary bullets live
// in the 0–5 range and pinned/critical bullets can use much larger values
// (e.g. 99999) to force top placement.
type releaseBullet struct {
	Text       string
	Importance int32
}

// releaseNote is one version's section of CHANGELOG.md.
type releaseNote struct {
	Version string          // "1.0.2" (no leading v)
	Date    string          // "2026-06-05", may be empty
	Bullets []releaseBullet // user-facing bullets, sorted by importance
}

// reBulletImportance matches a leading importance marker like "[5]" or
// "[99999]". The number is parsed as an unsigned 32-bit integer.
var reBulletImportance = regexp.MustCompile(`^\[(\d+)\]\s*(.*)$`)

// parseBullet extracts an optional `[N]` importance marker from the start of a
// changelog bullet text. It returns the bullet and true if the input is
// non-empty. Missing or invalid markers default to importance 0.
func parseBullet(raw string) (releaseBullet, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return releaseBullet{}, false
	}
	if m := reBulletImportance.FindStringSubmatch(text); m != nil {
		if v, err := strconv.ParseUint(m[1], 10, 32); err == nil {
			return releaseBullet{Text: m[2], Importance: int32(v)}, true
		}
	}
	return releaseBullet{Text: text, Importance: 0}, true
}

// sortBulletsByImportance sorts bullets by descending importance, keeping the
// original order for equal values.
func sortBulletsByImportance(b []releaseBullet) {
	slices.SortStableFunc(b, func(a, b releaseBullet) int {
		if a.Importance > b.Importance {
			return -1
		}
		if a.Importance < b.Importance {
			return 1
		}
		return 0
	})
}

// parseChangelog splits the embedded changelog into sections, newest first.
// The format is intentionally strict: `## vX.Y.Z — YYYY-MM-DD` headers and
// `- ` bullets, nothing else is significant. Bullets may carry a leading
// `[N]` importance marker. Malformed input never panics.
func parseChangelog(raw string) []releaseNote {
	var out []releaseNote
	var cur *releaseNote
	for line := range strings.SplitSeq(raw, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmed, "## ") {
			if cur != nil {
				sortBulletsByImportance(cur.Bullets)
				out = append(out, *cur)
			}
			header := strings.TrimPrefix(trimmed, "## ")
			header = strings.TrimLeft(header, " \t")
			// header is "vX.Y.Z — YYYY-MM-DD" or "vX.Y.Z" or "vX.Y.Z — "
			ver := header
			date := ""
			if v, d, found := strings.Cut(header, "—"); found {
				ver = strings.TrimSpace(v)
				date = strings.TrimSpace(d)
			}
			ver = strings.TrimPrefix(ver, "v")
			cur = &releaseNote{Version: ver, Date: date}
			continue
		}
		if cur == nil {
			continue
		}
		if bulletText, ok := strings.CutPrefix(trimmed, "- "); ok {
			if bullet, ok := parseBullet(bulletText); ok {
				cur.Bullets = append(cur.Bullets, bullet)
			}
		}
	}
	if cur != nil {
		sortBulletsByImportance(cur.Bullets)
		out = append(out, *cur)
	}
	return out
}

// versionSeen is the on-disk record of "what version did this machine last
// render under". FirstSeen anchors the announcement window and is set only
// when a window opens (an upgrade); 0 means no window — fresh installs and
// versions recorded while announcements were disabled never announce.
type versionSeen struct {
	Version   string `json:"version"`
	FirstSeen int64  `json:"first_seen"`
}

// versionSeenPath is sibling of the sessions/ and plugins/ state directories.
func versionSeenPath() string {
	return filepath.Join(state.StateBaseDir(), "last-version.json")
}

func loadVersionSeen() (versionSeen, bool) {
	data, err := os.ReadFile(versionSeenPath())
	if err != nil {
		return versionSeen{}, false
	}
	var v versionSeen
	if err := json.Unmarshal(data, &v); err != nil {
		return versionSeen{}, false
	}
	return v, true
}

func saveVersionSeen(v versionSeen) error {
	dir := filepath.Dir(versionSeenPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return sys.WriteFileAtomic(versionSeenPath(), data)
}

// reReleaseVersion matches a clean release-shaped version: MAJOR.MINOR.REVISION
// with no suffix. This rejects Go pseudo-versions ("0.1.0-0.20260612-abc"),
// "+dirty" / "+unknown" dev markers, and any other non-release build.
var reReleaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// isReleaseVersion reports whether v is a clean MAJOR.MINOR.REVISION — the
// only form the takeover feature should fire on. Anything else (dev, dirty
// pseudo-versions, go-install @commit) is treated as a source build.
func isReleaseVersion(v string) bool {
	return reReleaseVersion.MatchString(v)
}

// announceDecision decides whether this render should be replaced by the
// release-notes announcement. Returns show (replace output) and next (state
// to persist; a zero versionSeen means "don't write").
func announceDecision(prev versionSeen, prevOK bool, current string,
	cfg config.ReleaseNotesConfig, now time.Time) (show bool, next versionSeen) {
	// Source builds / non-release versions (dev, "+dirty", Go pseudo-versions
	// from `go install @commit`) never trigger and never write.
	// isReleaseVersion is the single gate; anything not matching
	// MAJOR.MINOR.REVISION is treated as a dev build.
	if !isReleaseVersion(current) {
		return false, versionSeen{}
	}
	// Disabled: still advance the version field (so re-enabling later
	// doesn't think the binary is "new"), but preserve the old FirstSeen so
	// the window check on re-enable will already be expired for an upgrade
	// that happened during the disabled period.
	if !cfg.AnnounceOrDefault() || cfg.Duration() == 0 {
		if !prevOK || prev.Version != current {
			return false, versionSeen{Version: current, FirstSeen: prev.FirstSeen}
		}
		return false, versionSeen{}
	}
	// Fresh install: record silently with no window anchor, show nothing —
	// a real FirstSeen here would put the *next* renders of this same
	// version inside the window below and flash the banner on a first-ever
	// install.
	if !prevOK {
		return false, versionSeen{Version: current, FirstSeen: 0}
	}
	// Upgrade (or downgrade): fire and reset the window anchor.
	if prev.Version != current {
		return true, versionSeen{Version: current, FirstSeen: now.Unix()}
	}
	// Same version, still inside the window: keep showing, don't re-persist.
	if now.Unix()-prev.FirstSeen < int64(cfg.Duration()/time.Second) {
		return true, versionSeen{}
	}
	// Same version, window expired.
	return false, versionSeen{}
}

// findNote returns the section matching v (no leading v), or false.
func findNote(notes []releaseNote, v string) (releaseNote, bool) {
	for _, n := range notes {
		if n.Version == v {
			return n, true
		}
	}
	return releaseNote{}, false
}

// releaseNotesBetween collects the most important bullets from all versions
// strictly after `from` up to and including `to`, sorted by descending
// importance. If `from` or `to` are malformed, or `to` is not newer than
// `from`, it falls back to `to`'s own bullets. The result is capped at
// `limit` (≤ 0 means unlimited).
func releaseNotesBetween(notes []releaseNote, from, to string, limit int) []releaseBullet {
	target, ok := findNote(notes, to)
	if !ok {
		return nil
	}
	// Malformed versions or non-upgrades fall back to the target version only.
	if compareVersions(to, from) <= 0 {
		return target.Bullets
	}
	var collected []releaseBullet
	for _, n := range notes {
		if compareVersions(n.Version, from) > 0 && compareVersions(n.Version, to) <= 0 {
			collected = append(collected, n.Bullets...)
		}
	}
	sortBulletsByImportance(collected)
	if limit > 0 && len(collected) > limit {
		collected = collected[:limit]
	}
	return collected
}

// runReleaseNotes implements the `release-notes` subcommand.
func runReleaseNotes(args []string) {
	notes := parseChangelog(changelogRaw)
	current, _, _ := version.VersionString()

	cfg, _ := config.LoadConfigWarn()
	colors := palette.CurrentPalette(cfg.Theme, cfg.ColorDepth, cfg.ThemeColors)

	mode, target, fallback, missing := selectReleaseNote(notes, current, args)
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s\n", missing[0])
		if mode == "" {
			os.Exit(1)
		}
		if fallback != nil {
			fmt.Fprintln(os.Stderr)
		}
	}
	switch mode {
	case "all":
		for i, n := range notes {
			if i > 0 {
				fmt.Println()
			}
			printReleaseNote(n, colors)
		}
	case "one":
		if fallback != nil {
			printReleaseNote(*fallback, colors)
		} else {
			printReleaseNote(target, colors)
		}
	case "range":
		printReleaseNote(target, colors)
	}
}

// parseVersionRange parses arguments like "v1.0.0..v1.5.0" or "1.0.0..1.5.0".
// It returns the two version strings with any leading "v" stripped.
func parseVersionRange(arg string) (from, to string, ok bool) {
	arg = strings.TrimPrefix(arg, "v")
	parts := strings.Split(arg, "..")
	if len(parts) != 2 {
		return "", "", false
	}
	from = strings.TrimPrefix(parts[0], "v")
	to = strings.TrimPrefix(parts[1], "v")
	if from == "" || to == "" {
		return "", "", false
	}
	return from, to, true
}

// selectReleaseNote parses the subcommand args and returns what to print.
// The returned mode is "one", "all", "range", or "" (unknown / error). For
// "one" and "range" the caller prints `target`; if `fallback` is non-nil the
// caller should print `fallback` instead and surface `missing` to the user
// first. For mode "" the caller exits 1 after printing `missing`. This
// function is pure with respect to stdout/stderr — it returns everything the
// caller needs to format and present, so the dispatch logic is testable
// without capture.
func selectReleaseNote(notes []releaseNote, current string, args []string) (mode string, target releaseNote, fallback *releaseNote, missing []string) {
	const emptyChangelog = "no release notes available (CHANGELOG.md is empty or malformed)"
	switch {
	case len(args) == 0:
		n, ok := findNote(notes, current)
		if ok {
			return "one", n, nil, nil
		}
		if len(notes) == 0 {
			return "", releaseNote{}, nil, []string{emptyChangelog}
		}
		fb := notes[0]
		return "one", releaseNote{}, &fb, []string{fmt.Sprintf("no notes for v%s; showing latest (v%s):", current, fb.Version)}
	case args[0] == "--all" || args[0] == "all":
		if len(notes) == 0 {
			return "", releaseNote{}, nil, []string{emptyChangelog}
		}
		return "all", releaseNote{}, nil, nil
	default:
		arg := strings.TrimPrefix(args[0], "v")
		// Try a version range first.
		if from, to, ok := parseVersionRange(arg); ok {
			if len(notes) == 0 {
				return "", releaseNote{}, nil, []string{emptyChangelog}
			}
			bullets := releaseNotesBetween(notes, from, to, 0)
			if bullets == nil {
				known := make([]string, len(notes))
				for i, x := range notes {
					known[i] = "v" + x.Version
				}
				return "", releaseNote{}, nil, []string{fmt.Sprintf("no release notes ending at \"v%s\" (known: %s)", to, strings.Join(known, ", "))}
			}
			return "range", releaseNote{Version: from + ".." + to, Bullets: bullets}, nil, nil
		}
		n, ok := findNote(notes, arg)
		if !ok {
			known := make([]string, len(notes))
			for i, x := range notes {
				known[i] = "v" + x.Version
			}
			return "", releaseNote{}, nil, []string{fmt.Sprintf("no release notes for \"v%s\" (known: %s)", arg, strings.Join(known, ", "))}
		}
		return "one", n, nil, nil
	}
}

// bulletDisplayText returns the text to show for a bullet. Bullets with an
// importance well above the ordinary 0–5 range are prefixed so users notice
// them even in a long list.
func bulletDisplayText(b releaseBullet) string {
	if b.Importance >= 100 {
		return "[PINNED] " + b.Text
	}
	return b.Text
}

func printReleaseNote(n releaseNote, c palette.Palette) {
	header := fmt.Sprintf("claude-statusline v%s", n.Version)
	if n.Date != "" {
		header += " — " + n.Date
	}
	if c.Purple != "" {
		fmt.Printf("%s%s%s\n", c.Purple, header, c.Rst)
	} else {
		fmt.Println(header)
	}
	for _, b := range n.Bullets {
		text := bulletDisplayText(b)
		if c.Dim != "" {
			fmt.Printf("%s  • %s%s\n", c.Dim, text, c.Rst)
		} else {
			fmt.Printf("  • %s\n", text)
		}
	}
}

// ─── Render-path takeover ────────────────────────────────────────────

// maybeReleaseTakeover runs the decision machinery and, if the current
// render should be replaced by the announcement, returns the takeover lines
// matching `lines` in count (minimum 1, so the announcement still shows
// when every segment hid). State-file errors degrade silently to the
// normal statusline: an unreadable file reads as a fresh install (no
// takeover), and a failed save suppresses the takeover rather than replay
// it on every render.
//
// The padding argument is the user's [style].padding (default 1) so the
// takeover lines indent identically to the renderer's lines. The per-line
// truncation budget mirrors the renderer's width reserves: line 0 reserves
// columns for the trailing " │ X.Xms" timing suffix, every line keeps the
// safety margin.
func maybeReleaseTakeover(cfg config.ReleaseNotesConfig, lines []string, c palette.Palette, width int, padding int, now time.Time) []string {
	prev, prevOK := loadVersionSeen()
	current, _, _ := version.VersionString()
	show, next := announceDecision(prev, prevOK, current, cfg, now)
	if next.Version != "" {
		if saveVersionSeen(next) != nil {
			// Couldn't record that we showed (or would show) the
			// announcement. Showing anyway would replay the takeover on
			// every render until the state dir becomes writable — degrade
			// to the normal statusline instead.
			show = false
		}
	}
	if !show {
		return lines
	}
	notes := parseChangelog(changelogRaw)
	statusLines := max(len(lines), 1)
	maxLines := cfg.ResolvedMaxLines()
	if maxLines == config.SameAsStatusLineSentinel || maxLines <= 0 {
		maxLines = statusLines
	}
	n := max(statusLines, maxLines)
	var target releaseNote
	if len(notes) == 0 {
		target = releaseNote{Version: current}
	} else {
		// If we know the previous version, surface the highest-importance
		// bullets across the whole upgrade span, not just the latest release.
		if prevOK && isReleaseVersion(prev.Version) {
			target = releaseNote{
				Version: current,
				Bullets: releaseNotesBetween(notes, prev.Version, current, n),
			}
		} else {
			var ok bool
			target, ok = findNote(notes, current)
			if !ok {
				// Fall back to the newest section's bullets but keep the real version.
				target = notes[0]
				target.Version = current
			}
		}
	}
	budgets := takeoverLineBudgets(width, n, padding)
	return announceLines(target, n, budgets, c, padding)
}

// takeoverLineBudgets returns the per-line visible-column budget for a
// takeover rendering, matching the renderer's own reserves (timing suffix
// on line 0, safety margin on every line). Returns nil if width is unknown
// (≤ 0), in which case announceLines skips truncation.
func takeoverLineBudgets(width int, n int, padding int) []int {
	if width <= 0 {
		return nil
	}
	out := make([]int, n)
	for i := range out {
		// The leading padding (style.padding, default 1) is added back by
		// announceLines, so reserve it here too. (The renderer instead counts
		// padding inside each measured line — same accounting, other side.)
		out[i] = max(lineBudget(width, i == 0)-padding, 10)
	}
	return out
}

// announceLines builds the announcement at exactly n lines, padded or
// truncated as needed. budgets[i] is the per-line visible-column cap (the
// caller computes it from the terminal width and renderer's reserves);
// passing nil disables truncation. padding is the leading indent (matches
// [style].padding so the takeover doesn't shift horizontally). Pure: no
// I/O, easy to unit-test.
func announceLines(note releaseNote, n int, budgets []int, c palette.Palette, padding int) []string {
	n = max(n, 1)
	// Present bullets in importance order regardless of how the caller built
	// the note. The sort is stable, so equal-importance bullets keep their
	// original order.
	sortBulletsByImportance(note.Bullets)
	accent := c.Purple
	dim := c.Dim
	rst := c.Rst

	hint := "↳ claude-statusline release-notes · configure: [release_notes] in config.toml"

	// Slot layout: line 0 = header, line n-1 = hint (when n>=2), the rest
	// are bullets (as many as fit), padded with empties to keep the hint
	// last. The n=1 form packs everything into a single header line.
	var out []string
	if n == 1 {
		hdr := "✨ claude-statusline v" + note.Version
		if len(note.Bullets) > 0 {
			hdr += " — " + bulletDisplayText(note.Bullets[0])
		}
		hdr += " · claude-statusline release-notes"
		out = []string{hdr}
	} else {
		hdr := "✨ claude-statusline updated to v" + note.Version
		mid := n - 2 // slots between header and hint
		bullets := min(mid, len(note.Bullets))
		out = make([]string, 0, n)
		out = append(out, hdr)
		for i := range bullets {
			out = append(out, " • "+bulletDisplayText(note.Bullets[i]))
		}
		for len(out) < n-1 {
			out = append(out, "")
		}
		out = append(out, hint)
	}

	pad := strings.Repeat(" ", padding)
	for i, l := range out {
		if i < len(budgets) && budgets[i] > 0 {
			l = truncateToWidth(l, budgets[i])
		}
		switch {
		case i == 0 && accent != "":
			l = pad + accent + l + rst
		case dim != "":
			l = pad + dim + l + rst
		default:
			l = pad + l
		}
		out[i] = l
	}
	return out
}

// truncateToWidth shortens s to at most `width` visible columns, appending
// an ellipsis when it has to cut. Plain-text contract: callers must apply
// colors AFTER truncation (announceLines does this). ANSI-aware
// truncation lives next to stripANSI/visibleWidth in render.go and isn't
// duplicated here.
func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	if ansi.VisibleWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	target := width - 1 // reserve a column for the ellipsis
	// Rune-by-rune copy; matches the visibleWidth semantics used everywhere
	// else in the codebase (utf8.RuneCountInString, one column per rune).
	count := 0
	for i := range s {
		if count >= target {
			return s[:i] + "…"
		}
		count++
	}
	return s
}
