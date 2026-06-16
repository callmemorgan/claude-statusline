package main

// ─── TUI Session-Replay Scrubber ─────────────────────────────────────
//
// The scrubber page replays a recorded session's evolving payload+state so
// the user watches the *real* statusline animate while tuning. ←/→ steps the
// timeline; the live preview is produced by the same buildStatusline the
// render path uses, on the (payload, state, clock) tuple reconstructed for the
// scrubbed instant (replay.go). Config edits (toggle a segment, move it to a
// line) re-render the current frame live and round-trip through the same cfg
// model and saveConfig the rest of the TUI uses — tview/tcell stay confined
// to the tui*.go files.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

// scrubberState carries the mutable bits the scrubber page needs across
// rebuilds: the loaded sessions, the chosen session, the timeline index, and
// the per-session reset anchors.
type scrubberState struct {
	sessions []replaySession
	sel      int // index into sessions
	frame    int // timeline index within the selected session's samples
	anchors  resetAnchors
}

func (s *scrubberState) session() replaySession {
	if s.sel < 0 || s.sel >= len(s.sessions) {
		return replaySession{}
	}
	return s.sessions[s.sel]
}

func (s *scrubberState) selectSession(i int) {
	if i < 0 || i >= len(s.sessions) {
		return
	}
	s.sel = i
	s.frame = len(s.sessions[i].Samples) - 1 // start at the latest frame
	if s.frame < 0 {
		s.frame = 0
	}
	s.anchors = anchorsFor(s.sessions[i].Samples)
}

// scrubberView bundles the page primitive and the seed/refresh hooks the home
// screen wires up.
type scrubberView struct {
	page    tview.Primitive
	seed    func()      // (re)load sessions and reset to the latest frame
	refresh func()       // re-render the current frame (call after a cfg edit)
	list    *tview.List // the segment toggle list (focus target)
	state   *scrubberState
}

// buildScrubberPage constructs the replay scrubber page. It shares the live
// cfg through accessor/mutator closures so edits flow through the same dirty
// tracking and saveConfig the main screen uses. The returned refresh hook lets
// the home screen re-render the frame after any external cfg change.
func buildScrubberPage(
	cfgRef *config,
	mutate func(func()),
	flash func(color, msg string),
) *scrubberView {
	st := &scrubberState{}

	// The replayed frame's rendered statusline.
	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	previewBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(preview, 0, 1, false)
	previewBox.SetBorder(true).SetTitle(" Replay preview ")

	// Timeline strip: index, elapsed offset, and a textual slider.
	timeline := tview.NewTextView().SetDynamicColors(true)
	timelineBox := tview.NewFlex().AddItem(timeline, 0, 1, false)
	timelineBox.SetBorder(true).SetTitle(" Timeline (←/→ step · ⇧←/→ jump · ,/. session) ")

	// Sampled values at the current frame, so the user can see what's driving
	// the trend/projection segments.
	readout := tview.NewTextView().SetDynamicColors(true)
	readout.SetBorder(true).SetTitle(" Frame ")

	// A compact segment toggle list so editing re-renders the frame live.
	list := tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		SetSelectedTextColor(tcell.ColorWhite).
		ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Segments (space toggle · 1-9 line) ")

	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("scrubber"))

	// refreshList repaints the segment toggle list from cfg.
	refreshList := func() {
		idx := list.GetCurrentItem()
		list.Clear()
		for _, s := range registeredSegments {
			mark := "  "
			if segmentEnabled(*cfgRef, s.id) {
				mark = "✓ "
			}
			line := effectiveLine(s.id, *cfgRef)
			lineStr := ""
			if s2, ok := segmentByID(s.id); ok && line != s2.line {
				lineStr = fmt.Sprintf(" [L%d]", line)
			}
			list.AddItem(mark+s.id+lineStr, "", 0, nil)
		}
		if idx >= 0 && idx < len(registeredSegments) {
			list.SetCurrentItem(idx)
		}
	}

	// renderFrame reconstructs and renders the current timeline frame through
	// the real builder, then repaints the timeline + readout.
	renderFrame := func() {
		sess := st.session()
		if len(sess.Samples) == 0 {
			preview.SetText("(no samples)")
			timeline.SetText("")
			readout.SetText("")
			return
		}
		base := samplePayload()
		_, _, panelW, _ := preview.GetInnerRect()
		width := panelW
		if width <= 0 {
			width = 80
		}
		p, state, now := reconstructAt(sess.Samples, st.frame, base, st.anchors)
		lines := buildStatusline(buildInput{
			P: p, C: currentPalette(*cfgRef), Cfg: *cfgRef, State: state, Width: width, Now: now,
		})
		for i, l := range lines {
			lines[i] = strings.TrimLeft(l, " ")
		}
		text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
		if strings.TrimSpace(text) == "" {
			text = "(statusline hidden — no segments enabled)"
		} else {
			text = ansiToTview(text)
		}
		preview.SetText(text)

		// Timeline slider.
		n := len(sess.Samples)
		off := time.Duration(sess.Samples[st.frame].T-sess.Samples[0].T) * time.Second
		const ticks = 40
		pos := 0
		if n > 1 {
			pos = st.frame * (ticks - 1) / (n - 1)
		}
		var bar strings.Builder
		for i := 0; i < ticks; i++ {
			if i == pos {
				bar.WriteString("[yellow]●[-]")
			} else {
				bar.WriteString("[gray]─[-]")
			}
		}
		timeline.SetText(fmt.Sprintf("frame [::b]%d[-::-]/%d   +%s\n%s",
			st.frame+1, n, replayDuration(off), bar.String()))

		// Readout of the sampled drivers.
		cur := sess.Samples[st.frame]
		rl := func(p *float64) string {
			if p == nil {
				return "—"
			}
			return fmt.Sprintf("%.0f%%", *p)
		}
		readout.SetText(fmt.Sprintf(
			"[::b]%s[-::-]  (%s)\ncost $%.2f · ctx %.0f%% · in %d · out %d\nrl5h %s · rl7d %s",
			sess.Label, now.Format("15:04:05"),
			cur.Cost, cur.CtxPct, cur.InTok, cur.OutTok, rl(cur.RL5h), rl(cur.RL7d)))
	}

	seed := func() {
		st.sessions = listReplaySessions(time.Now())
		st.selectSession(0)
		refreshList()
		renderFrame()
	}

	// step moves the timeline; jump moves by ~10%.
	step := func(delta int) {
		sess := st.session()
		n := len(sess.Samples)
		if n == 0 {
			return
		}
		st.frame += delta
		if st.frame < 0 {
			st.frame = 0
		}
		if st.frame >= n {
			st.frame = n - 1
		}
		renderFrame()
	}

	cycleSession := func(delta int) {
		if len(st.sessions) <= 1 {
			return
		}
		next := (st.sel + delta + len(st.sessions)) % len(st.sessions)
		st.selectSession(next)
		renderFrame()
		flash("cyan", "session: "+st.session().ID)
	}

	// toggleSelected / moveSelected funnel through the shared mutate so dirty
	// tracking and the same cfg model apply; both re-render the live frame.
	toggleSelected := func() {
		idx := list.GetCurrentItem()
		if idx < 0 || idx >= len(registeredSegments) {
			return
		}
		id := registeredSegments[idx].id
		mutate(func() { toggleSegmentIn(cfgRef, id) })
		refreshList()
		renderFrame()
	}

	moveSelected := func(n int) {
		idx := list.GetCurrentItem()
		if idx < 0 || idx >= len(registeredSegments) {
			return
		}
		seg := registeredSegments[idx]
		mutate(func() { assignSegmentLine(cfgRef, seg.id, seg.line, n) })
		refreshList()
		renderFrame()
	}

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyLeft:
			if event.Modifiers()&tcell.ModShift != 0 {
				step(-jumpStep(st))
			} else {
				step(-1)
			}
			return nil
		case tcell.KeyRight:
			if event.Modifiers()&tcell.ModShift != 0 {
				step(jumpStep(st))
			} else {
				step(1)
			}
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case ' ':
				toggleSelected()
				return nil
			case ',':
				cycleSession(-1)
				return nil
			case '.':
				cycleSession(1)
				return nil
			default:
				if r := event.Rune(); r >= '1' && r <= '9' {
					moveSelected(int(r - '0'))
					return nil
				}
			}
		}
		return event
	})

	// Layout: segment list on the left, replay panels stacked on the right.
	rightCol := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(previewBox, 0, 1, false).
		AddItem(timelineBox, 4, 0, false).
		AddItem(readout, 5, 0, false)

	body := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(list, 0, 1, true).
		AddItem(rightCol, 0, 2, false)

	page := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, true).
		AddItem(help, 1, 0, false)

	return &scrubberView{
		page:    page,
		seed:    seed,
		refresh: renderFrame,
		list:    list,
		state:   st,
	}
}

// jumpStep is ~10% of the session length, min 1, for ⇧←/→.
func jumpStep(st *scrubberState) int {
	n := len(st.session().Samples)
	j := n / 10
	if j < 1 {
		j = 1
	}
	return j
}

// runReplayTUI launches a standalone scrubber when `replay` is invoked
// directly (rather than reached from the configure home screen). It loads the
// real config, builds the scrubber page, and runs a minimal app with save +
// quit. Edits round-trip through saveConfig like the main configurator.
func runReplayTUI(preferSession string) {
	cfg, _ := loadConfigWarn()
	initSegments(cfg.Plugins)

	app := tview.NewApplication()
	pages := tview.NewPages()

	dirty := false
	var view *scrubberView

	flashView := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	flashGen := 0
	flash := func(color, msg string) {
		flashGen++
		gen := flashGen
		flashView.SetText(fmt.Sprintf("[%s]%s[-] ", color, msg))
		time.AfterFunc(2500*time.Millisecond, func() {
			app.QueueUpdateDraw(func() {
				if flashGen == gen {
					flashView.SetText("")
				}
			})
		})
	}

	mutate := func(fn func()) {
		fn()
		dirty = true
	}

	view = buildScrubberPage(&cfg, mutate, flash)

	// Seed, then honor a preferred session id if one was passed.
	view.seed()
	if preferSession != "" {
		for i, s := range view.state.sessions {
			if s.ID == preferSession {
				view.state.selectSession(i)
				view.refresh()
				break
			}
		}
	}

	// Help overlay — generated from the same keymap table as the rest of the
	// TUI, so the standalone scrubber advertises the bindings its footer lists.
	helpView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false).
		SetText(buildHelpText())
	helpView.SetBorder(true).SetTitle(" Help (q/Esc close) ")

	var quitModal *tview.Modal
	doSave := func() bool {
		if err := saveConfig(cfg); err != nil {
			flash("red", fmt.Sprintf("✗ save failed: %v", err))
			return false
		}
		dirty = false
		flash("green", "✓ Saved to "+configPath())
		return true
	}

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(view.page, 0, 1, true).
		AddItem(flashView, 1, 0, false)

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		name, _ := pages.GetFrontPage()
		if name == "quit" {
			return event
		}
		if name == "help" {
			switch event.Key() {
			case tcell.KeyEscape:
				pages.SwitchToPage("scrubber")
				app.SetFocus(view.list)
				return nil
			case tcell.KeyRune:
				if r := event.Rune(); r == 'q' || r == 'Q' {
					pages.SwitchToPage("scrubber")
					app.SetFocus(view.list)
					return nil
				}
			}
			return event
		}
		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case '?':
				pages.SwitchToPage("help")
				app.SetFocus(helpView)
				return nil
			case 's', 'S':
				doSave()
				return nil
			case 'q', 'Q':
				if dirty {
					pages.SwitchToPage("quit")
					app.SetFocus(quitModal)
				} else {
					app.Stop()
				}
				return nil
			}
		case tcell.KeyEscape:
			if dirty {
				pages.SwitchToPage("quit")
				app.SetFocus(quitModal)
			} else {
				app.Stop()
			}
			return nil
		}
		return event
	})

	quitModal = tview.NewModal().
		SetText("Unsaved changes.").
		AddButtons([]string{"Save & quit", "Discard", "Cancel"}).
		SetDoneFunc(func(_ int, label string) {
			switch label {
			case "Save & quit":
				if doSave() {
					app.Stop()
				}
			case "Discard":
				app.Stop()
			default:
				pages.SwitchToPage("scrubber")
				app.SetFocus(view.list)
			}
		})

	pages.AddPage("scrubber", root, true, true)
	pages.AddPage("help", helpView, true, false)
	pages.AddPage("quit", quitModal, true, false)

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Defensive: callers gate on this, but never start a TUI without a tty.
		return
	}

	if err := app.SetRoot(pages, true).EnableMouse(true).SetFocus(view.list).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running replay TUI: %v\n", err)
		os.Exit(1)
	}
}
