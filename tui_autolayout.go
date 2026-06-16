package main

// ─── Auto-Layout Overlay (priority + budget solver) ──────────────────
//
// A design-time overlay: the user ranks segments by priority (left list) and
// sets a budget (max width / max lines / density, right list); the solver packs
// them onto physical lines, demoting and dropping low-priority segments to fit,
// with a live preview rendered through the REAL builder. On apply it emits a
// concrete config (Segments/Lines/Reflow/Style) and persists the priorities +
// budget as [auto_layout] metadata so the ranking can be re-edited later.
//
// All tview/tcell usage stays confined to tui*.go per the codebase convention.

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// autoLayoutModel owns the overlay's widgets and editable state. It is created
// once in runConfigure and seeded each time the overlay opens.
type autoLayoutModel struct {
	app   *tview.Application
	pages *tview.Pages

	priorityList *tview.List
	budgetList   *tview.List
	preview      *tview.TextView
	results      *tview.TextView
	flex         *tview.Flex

	prios   []string         // current priority ranking (highest first)
	budget  autoLayoutBudget // current budget knobs
	mi      packMeasureInput // deterministic measurement inputs (preview data)
	palette palette          // for the live (colored) preview

	// commit is invoked on apply with the solved concrete layout. It mutates
	// the real cfg through the existing model, stores metadata, marks dirty,
	// and returns focus to the home screen.
	commit func(res packResult, prios []string, budget autoLayoutBudget)
	// back returns to the home screen without applying.
	back func()
}

// budgetRow is one editable budget knob.
type budgetRow struct {
	name string
	get  func(b autoLayoutBudget) string
	// dec/inc mutate the budget by one step; clamping lives in the accessors.
	dec func(b *autoLayoutBudget)
	inc func(b *autoLayoutBudget)
}

func (m *autoLayoutModel) budgetRows() []budgetRow {
	return []budgetRow{
		{
			name: "max width (cols)",
			get:  func(b autoLayoutBudget) string { return fmt.Sprintf("%d", b.width()) },
			dec:  func(b *autoLayoutBudget) { b.Width = clampInt(b.width()-5, 20, 300) },
			inc:  func(b *autoLayoutBudget) { b.Width = clampInt(b.width()+5, 20, 300) },
		},
		{
			name: "max lines",
			get:  func(b autoLayoutBudget) string { return fmt.Sprintf("%d", b.maxLines()) },
			dec:  func(b *autoLayoutBudget) { b.MaxLines = clampInt(b.maxLines()-1, 1, 9) },
			inc:  func(b *autoLayoutBudget) { b.MaxLines = clampInt(b.maxLines()+1, 1, 9) },
		},
		{
			name: "density",
			get:  func(b autoLayoutBudget) string { return b.density() },
			dec:  func(b *autoLayoutBudget) { b.Density = cycleDensity(b.density(), -1) },
			inc:  func(b *autoLayoutBudget) { b.Density = cycleDensity(b.density(), +1) },
		},
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func cycleDensity(cur string, dir int) string {
	idx := 0
	for i, d := range densityOrder {
		if d == cur {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(densityOrder)) % len(densityOrder)
	return densityOrder[idx]
}

// newAutoLayoutModel builds the overlay's widgets. The page is registered by the
// caller (runConfigure) via m.flex.
func newAutoLayoutModel(app *tview.Application, pages *tview.Pages) *autoLayoutModel {
	m := &autoLayoutModel{app: app, pages: pages}

	m.priorityList = tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	m.priorityList.SetBorder(true).SetTitle(" Priority (high → low) ")

	m.budgetList = tview.NewList().
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkSlateGrey).
		ShowSecondaryText(false)
	m.budgetList.SetBorder(true).SetTitle(" Budget ")

	m.results = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	m.results.SetBorder(true).SetTitle(" Solver ")

	m.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	m.preview.SetBorder(true).SetTitle(" Packed preview ")

	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true).
		SetText("[yellow::b]  Auto-layout — rank by priority, set a budget, the solver packs[-::-]")

	help := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetWrap(true).
		SetWordWrap(true).
		SetText(footerText("autolayout"))

	// Left column: priority list over the budget+solver panes.
	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(m.priorityList, 0, 2, true).
		AddItem(m.budgetList, 5, 0, false).
		AddItem(m.results, 4, 0, false)

	top := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(left, 0, 1, true).
		AddItem(m.preview, 0, 1, false)

	m.flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(title, 1, 0, false).
		AddItem(top, 0, 1, true).
		AddItem(help, 1, 0, false)

	return m
}

// seed initializes the overlay from the current config and measurement data,
// then renders. Focus lands on the priority list.
func (m *autoLayoutModel) seed(cfg config, mi packMeasureInput, pal palette) {
	m.mi = mi
	m.palette = pal
	// Re-use persisted priorities/budget if present, else derive from the
	// current layout so the ranking starts from what the user already has.
	if len(cfg.AutoLayout.Priorities) > 0 {
		m.prios = dedupePriorities(cfg.AutoLayout.Priorities)
	} else {
		m.prios = prioritiesFromConfig(cfg)
	}
	m.budget = cfg.AutoLayout.Budget
	m.refresh()
	m.app.SetFocus(m.priorityList)
}

// dedupePriorities drops duplicates and any ids no longer registered, then
// appends any registered-but-missing segment so the ranking is always complete.
func dedupePriorities(prios []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range prios {
		if seen[id] {
			continue
		}
		if _, ok := segmentByID(id); !ok {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, s := range registeredSegments {
		if !seen[s.id] {
			out = append(out, s.id)
		}
	}
	return out
}

// solve runs the packer against the current priorities + budget.
func (m *autoLayoutModel) solve() packResult {
	return packLayout(config{}, m.prios, m.budget, m.mi)
}

// refresh re-renders all four panes from the current state.
func (m *autoLayoutModel) refresh() {
	res := m.solve()

	// Which segments land where, so the priority list can annotate them.
	placedLine := map[string]int{}
	for li, segs := range res.Lines {
		for _, id := range segs {
			placedLine[id] = li + 1
		}
	}
	dropped := map[string]bool{}
	for _, id := range res.Dropped {
		dropped[id] = true
	}

	pIdx := m.priorityList.GetCurrentItem()
	m.priorityList.Clear()
	for rank, id := range m.prios {
		tag := "[gray]—[-]"
		if l, ok := placedLine[id]; ok {
			tag = fmt.Sprintf("[green]L%d[-]", l)
		} else if dropped[id] {
			tag = "[red]drop[-]"
		}
		m.priorityList.AddItem(fmt.Sprintf("%2d. %-18s %s", rank+1, id, tag), "", 0, nil)
	}
	if pIdx >= 0 && pIdx < len(m.prios) {
		m.priorityList.SetCurrentItem(pIdx)
	}

	bIdx := m.budgetList.GetCurrentItem()
	m.budgetList.Clear()
	for _, row := range m.budgetRows() {
		m.budgetList.AddItem(fmt.Sprintf("%-18s %s", row.name+":", row.get(m.budget)), "", 0, nil)
	}
	rows := m.budgetRows()
	if bIdx >= 0 && bIdx < len(rows) {
		m.budgetList.SetCurrentItem(bIdx)
	}

	placed := 0
	for _, l := range res.Lines {
		placed += len(l)
	}
	m.results.SetText(fmt.Sprintf("[white]%d placed[-] · [red]%d dropped[-] · %d/%d lines",
		placed, len(res.Dropped), len(res.Lines), m.budget.maxLines()))

	// Live preview through the REAL builder, with the solved concrete config.
	cfg := config{}
	applyPackResult(&cfg, res, m.budget.density())
	lines := buildStatusline(buildInput{
		P:     m.mi.P,
		C:     m.palette,
		Cfg:   cfg,
		State: m.mi.State,
		Width: m.budget.width(),
		Now:   m.mi.Now,
	})
	for i, l := range lines {
		pad := m.budget.width() - visibleWidth(l)
		if pad < 0 {
			pad = 0
		}
		lines[i] = l + strings.Repeat(" ", pad) + "\x1b[90m│\x1b[0m"
	}
	text := strings.Join(lines, "\n")
	if strings.TrimSpace(stripANSI(text)) == "" {
		m.preview.SetText("(no segments fit the budget)")
	} else {
		m.preview.SetText(ansiToTview(text))
	}
	m.preview.SetTitle(fmt.Sprintf(" Packed preview (%d cols) ", m.budget.width()))
}

// moveSelected moves the selected priority entry up (dir=-1) or down (dir=+1).
func (m *autoLayoutModel) moveSelected(dir int) {
	i := m.priorityList.GetCurrentItem()
	j := i + dir
	if i < 0 || i >= len(m.prios) || j < 0 || j >= len(m.prios) {
		return
	}
	m.prios[i], m.prios[j] = m.prios[j], m.prios[i]
	m.refresh()
	m.priorityList.SetCurrentItem(j)
}

// adjustBudget applies a step to the selected budget knob.
func (m *autoLayoutModel) adjustBudget(dir int) {
	rows := m.budgetRows()
	i := m.budgetList.GetCurrentItem()
	if i < 0 || i >= len(rows) {
		return
	}
	if dir < 0 {
		rows[i].dec(&m.budget)
	} else {
		rows[i].inc(&m.budget)
	}
	m.refresh()
}

// handleKey is the overlay's input handler, wired from runConfigure's global
// SetInputCapture when the front page is "autolayout". Returns nil to consume.
func (m *autoLayoutModel) handleKey(event *tcell.EventKey) *tcell.EventKey {
	focusBudget := m.app.GetFocus() == m.budgetList
	switch event.Key() {
	case tcell.KeyEscape:
		m.back()
		return nil
	case tcell.KeyTab:
		if focusBudget {
			m.app.SetFocus(m.priorityList)
		} else {
			m.app.SetFocus(m.budgetList)
		}
		return nil
	case tcell.KeyEnter:
		m.commit(m.solve(), append([]string(nil), m.prios...), m.budget)
		return nil
	case tcell.KeyLeft:
		m.adjustBudget(-1)
		return nil
	case tcell.KeyRight:
		m.adjustBudget(+1)
		return nil
	case tcell.KeyUp, tcell.KeyDown:
		dir := -1
		if event.Key() == tcell.KeyDown {
			dir = 1
		}
		if focusBudget {
			return event // normal list navigation
		}
		if event.Modifiers()&tcell.ModShift != 0 {
			m.moveSelected(dir) // reorder the priority entry
			return nil
		}
		return event // normal navigation
	case tcell.KeyRune:
		switch event.Rune() {
		case 'q', 'Q':
			m.back()
			return nil
		case 'a', 'A':
			m.commit(m.solve(), append([]string(nil), m.prios...), m.budget)
			return nil
		case 'k':
			if !focusBudget {
				m.moveSelected(-1)
				return nil
			}
		case 'j':
			if !focusBudget {
				m.moveSelected(+1)
				return nil
			}
		case 'b', 'B':
			m.app.SetFocus(m.budgetList)
			return nil
		}
	}
	return event
}

// autoLayoutMeasureInput builds the deterministic measurement input the overlay
// renders against: the synthetic sample payload + preview history, clocked at
// the call time (so countdowns animate consistently with the main preview).
func autoLayoutMeasureInput(pvState *sessionState) packMeasureInput {
	return packMeasureInput{P: samplePayload(), State: pvState, Now: time.Now()}
}
