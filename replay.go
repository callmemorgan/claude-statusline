package main

// ─── Session Replay / Scrubber ───────────────────────────────────────
//
// The replay scrubber reconstructs a recorded session's evolving payload and
// state at each sampled instant, so the configurator can animate the *real*
// statusline across a session's history. This is the only honest way to
// evaluate the trend/projection/rate segments (cost-rate, context trend,
// rate-limit projections): those features are pure functions of the recorded
// samples plus a `now`, so truncating the sample history to a timeline index
// and rendering with the corresponding clock reproduces exactly what the
// statusline showed at that moment.
//
// The state file records only the seven sample fields (see state.go). Fields
// the renderer needs but that aren't sampled — model name, directory, branch,
// the rate-limit *reset instants* — are layered onto a synthetic base payload
// so payload-driven segments still render. Reconstruction never invents the
// numbers a replay segment depends on; those come straight from the sample.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// replaySession is one recorded (or synthesized) session offered to the
// scrubber: a display label, the session id, and its full sample history in
// wall-clock order.
type replaySession struct {
	ID      string
	Label   string // human label for the picker (id + age + sample count)
	Samples []sample
	mod     time.Time // file mtime, used for ordering and the age label
	synth   bool      // true for the synthesized fallback session
}

// listReplaySessions enumerates recorded session files under stateDir(),
// newest first by mtime. It reads each file directly (bypassing loadState's
// retention prune) so the scrubber can replay the whole recorded history.
// When no real session is found it returns a single synthesized rising
// session so the mode is always demonstrable.
func listReplaySessions(now time.Time) []replaySession {
	dir := stateDir()
	entries, err := os.ReadDir(dir)
	var out []replaySession
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			st, rerr := readSessionFile(path)
			if rerr != nil || len(st.Samples) == 0 {
				continue
			}
			info, ierr := e.Info()
			mod := now
			if ierr == nil {
				mod = info.ModTime()
			}
			out = append(out, replaySession{
				ID:      st.SessionID,
				Label:   replayLabel(st.SessionID, len(st.Samples), st.Samples, now),
				Samples: st.Samples,
				mod:     mod,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })

	if len(out) == 0 {
		out = append(out, syntheticReplaySession(now))
	}
	return out
}

// readSessionFile reads one session file without applying the retention
// window — the scrubber wants every recorded sample, not just the
// trailing 48h. Returns a non-nil state with the parsed samples.
func readSessionFile(path string) (*sessionState, error) {
	st := &sessionState{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	return st, nil
}

// replayLabel builds the picker label, e.g. "abc12345  12 samples · 58m".
func replayLabel(id string, n int, samples []sample, now time.Time) string {
	short := id
	if len(short) > 12 {
		short = short[:12]
	}
	span := ""
	if len(samples) >= 2 {
		d := time.Duration(samples[len(samples)-1].T-samples[0].T) * time.Second
		span = " · " + replayDuration(d)
	}
	noun := "samples"
	if n == 1 {
		noun = "sample"
	}
	return short + "  " + strconv.Itoa(n) + " " + noun + span
}

// replayDuration renders a coarse human duration for labels: 45s, 12m, 3h, 2d.
func replayDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d >= time.Minute:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
}

// syntheticReplaySession mirrors previewState's rising hour-long session so
// the scrubber is always demonstrable, even on a machine that has never run
// the statusline. The samples are identical to previewState(now) so the
// synthetic replay exercises the same documented rates ($0.42/h, etc.).
func syntheticReplaySession(now time.Time) replaySession {
	st := previewState(now)
	return replaySession{
		ID:      "synthetic",
		Label:   "synthetic  " + strconv.Itoa(len(st.Samples)) + " samples · 1h (no recorded sessions)",
		Samples: st.Samples,
		synth:   true,
	}
}

// reconstructAt rebuilds the (payload, state, clock) tuple for one timeline
// index of a recorded session. It is the pure heart of the scrubber:
//
//   - state := the samples truncated to [0, idx] (a fresh *sessionState), so
//     every series-derived feature (cost-rate, projections, context trend)
//     sees exactly the history that existed at that instant.
//   - now := the sampled wall-clock time of that index, so trend windows and
//     projection spans line up with the truncated history.
//   - payload := the synthetic base with the sampled numeric fields layered
//     on (cost, context %, in/out tokens, both rate-limit %s). Pointer fields
//     are set only when the sample carried them, so an absent rate-limit stays
//     absent (the segment auto-hides) rather than reading as 0%.
//
// The rate-limit *reset instants* are not recorded; they are phased to the
// session's own timeline — the first window boundary is firstT + windowSecs,
// and resetInstant() rolls that forward by whole windows so it always lands
// strictly after the scrubbed frame's `now`. That mirrors a real rate-limit
// window: the countdown winds down within a window and rolls over at the
// boundary, never reading "now" forever once a long session passes the first
// reset. resetAnchors carries the per-session first-boundary anchors.
type resetAnchors struct {
	fiveHour int64
	sevenDay int64
}

const (
	fiveHourSecs = 5 * 3600
	sevenDaySecs = 7 * 24 * 3600
)

// anchorsFor computes the first reset boundary for a session's samples,
// phased to the first sample so the countdown is stable across scrub frames.
func anchorsFor(samples []sample) resetAnchors {
	if len(samples) == 0 {
		return resetAnchors{}
	}
	first := samples[0].T
	return resetAnchors{
		fiveHour: first + fiveHourSecs,
		sevenDay: first + sevenDaySecs,
	}
}

// resetInstant rolls a first-boundary anchor forward by whole windows until it
// is strictly after now, so the reconstructed reset is always a future instant
// (the countdown winds down and the projection has a valid target) even when a
// session spans more than one window.
func resetInstant(anchor, windowSecs, now int64) int64 {
	if now < anchor || windowSecs <= 0 {
		return anchor
	}
	// Number of whole windows to skip so the boundary lands after now.
	skip := (now-anchor)/windowSecs + 1
	return anchor + skip*windowSecs
}

func reconstructAt(samples []sample, idx int, base payload, anchors resetAnchors) (payload, *sessionState, time.Time) {
	if len(samples) == 0 {
		return base, &sessionState{SessionID: "replay"}, time.Now()
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(samples) {
		idx = len(samples) - 1
	}

	// Truncate the history to everything up to and including this index. Copy
	// the slice so the caller's backing array is never aliased or mutated.
	hist := make([]sample, idx+1)
	copy(hist, samples[:idx+1])
	st := &sessionState{SessionID: "replay", Samples: hist, retention: 48 * time.Hour}

	cur := samples[idx]
	now := time.Unix(cur.T, 0)

	p := base
	// Layer the sampled numeric fields over the base. These are the fields the
	// state file actually records (state.go), so the reconstruction is exact.
	p.Cost.TotalCostUSD = cur.Cost
	p.ContextWindow.TotalInputTokens = cur.InTok
	p.ContextWindow.TotalOutputTokens = cur.OutTok
	p.ContextWindow.UsedPercentage = ptrFloat64(cur.CtxPct)
	p.Exceeds200K = ptrBool(cur.CtxPct > 80)

	// Rate-limit percentages are pointers in the sample: present only when the
	// recorded payload carried them. Preserve that — an absent quota stays
	// absent so the segment auto-hides, distinguishing "unknown" from "0%".
	if cur.RL5h != nil {
		p.RateLimits.FiveHour.UsedPercentage = ptrFloat64(*cur.RL5h)
		anchor := resetInstant(anchors.fiveHour, fiveHourSecs, cur.T)
		p.RateLimits.FiveHour.ResetsAt = &anchor
	} else {
		p.RateLimits.FiveHour.UsedPercentage = nil
		p.RateLimits.FiveHour.ResetsAt = nil
	}
	if cur.RL7d != nil {
		p.RateLimits.SevenDay.UsedPercentage = ptrFloat64(*cur.RL7d)
		anchor := resetInstant(anchors.sevenDay, sevenDaySecs, cur.T)
		p.RateLimits.SevenDay.ResetsAt = &anchor
	} else {
		p.RateLimits.SevenDay.UsedPercentage = nil
		p.RateLimits.SevenDay.ResetsAt = nil
	}

	return p, st, now
}

// ─── replay subcommand ───────────────────────────────────────────────

// runReplay is the `replay` subcommand. With an interactive terminal and no
// dump flag it launches the in-TUI scrubber; otherwise (or with --list /
// --frames) it prints recorded sessions or a non-interactive frame dump, so
// the reconstruction is observable without a tty. It never touches the bare
// render path and never writes config.
func runReplay(args []string) {
	var (
		listOnly bool
		dump     bool
		sessID   string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--list" || a == "-l":
			listOnly = true
		case a == "--frames" || a == "--dump":
			dump = true
		case a == "--session" || a == "-s":
			if i+1 < len(args) {
				i++
				sessID = args[i]
			}
		case strings.HasPrefix(a, "--session="):
			sessID = strings.TrimPrefix(a, "--session=")
		default:
			fmt.Fprintf(os.Stderr, "replay: unknown argument %q\n", a)
			os.Exit(2)
		}
	}

	now := time.Now()
	sessions := listReplaySessions(now)

	if listOnly {
		fmt.Printf("recorded sessions under %s:\n", stateDir())
		for _, s := range sessions {
			fmt.Printf("  %s\n", s.Label)
		}
		return
	}

	cfg, _ := loadConfigWarn()
	initSegments(cfg.Plugins)

	// Pick the session: by id if requested, else most recent.
	sel := sessions[0]
	if sessID != "" {
		for _, s := range sessions {
			if s.ID == sessID {
				sel = s
				break
			}
		}
	}

	if dump || !term.IsTerminal(int(os.Stdin.Fd())) {
		dumpReplayFrames(sel, cfg)
		return
	}

	runReplayTUI(sel.ID)
}

// dumpReplayFrames renders every timeline frame of a session to stdout with a
// color palette, each prefixed by its index and elapsed offset. This is the
// non-interactive view of exactly what the scrubber animates, and what the
// reconstruction unit tests assert against.
func dumpReplayFrames(sel replaySession, cfg config) {
	colors := currentPalette(cfg)
	style := styleFor(cfg, colors)
	width := 80
	if w := terminalWidth(payload{}); w > 0 {
		width = w
	}
	anchors := anchorsFor(sel.Samples)
	base := samplePayload()

	fmt.Printf("session %q — %d frames\n", sel.ID, len(sel.Samples))
	for i := range sel.Samples {
		p, st, now := reconstructAt(sel.Samples, i, base, anchors)
		lines := buildStatusline(buildInput{P: p, C: colors, Cfg: cfg, State: st, Width: width, Now: now})
		off := time.Duration(sel.Samples[i].T-sel.Samples[0].T) * time.Second
		fmt.Printf("\n── frame %d/%d  +%s ──\n", i+1, len(sel.Samples), replayDuration(off))
		for li, l := range lines {
			if li == 0 {
				fmt.Printf("%s%s\n", l, style.sep)
				continue
			}
			fmt.Println(l)
		}
	}
}
