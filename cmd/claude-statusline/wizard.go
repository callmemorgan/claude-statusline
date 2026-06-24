package main

// ─── Wizard ───────────────────────────────────────────────────────────
//
// An interactive, keyboard-driven onboarding wizard built on
// charmbracelet/bubbletea. It guides first-time users through theme, color
// depth, segment selection, and optional Claude Code wiring, then writes
// config.toml and runs `install --target claude --yes` when requested.

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// runWizard is the entry point for the `wizard` subcommand.
func runWizard() {
	fs := flag.NewFlagSet("wizard", flag.ExitOnError)
	help := fs.Bool("help", false, "show help")
	_ = fs.Parse(os.Args[2:])

	if *help {
		fmt.Println(`claude-statusline wizard

An interactive onboarding wizard for claude-statusline.

Walks through theme, color depth, segment selection, and optional wiring
into Claude Code, then saves ~/.config/claude-statusline/config.toml.

Press q or Esc at any step to cancel without saving.`)
		return
	}

	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	m, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wizard error: %v\n", err)
		os.Exit(1)
	}

	wm, ok := m.(model)
	if !ok {
		return
	}
	if wm.quitting || wm.canceled {
		fmt.Fprintln(os.Stderr, "Wizard canceled — no changes were saved.")
		os.Exit(0)
	}
}

// wizard steps.
const (
	stepWelcome = iota
	stepTheme
	stepDepth
	stepSegments
	stepInstall
	stepSummary
	stepDone
)

// model is the bubbletea model for the wizard.
type model struct {
	width  int
	height int

	step int

	// Theme selection.
	themes   []palette.Theme
	themeIdx int

	// Color-depth selection.
	depths   []string
	depthIdx int

	// Segment multi-select.
	segs        []segments.Info
	segSelected map[string]bool
	segCursor   int

	// Install decision.
	install       bool
	installCursor int

	// Final state.
	quitting      bool
	canceled      bool
	done          bool
	errMsg        string
	installOutput string
}

// initialModel loads the current config (if any) and prepares defaults.
func initialModel() model {
	cfg := config.LoadConfig()

	// Initialize the segment registry and any configured plugin segments so the
	// multi-select list is complete.
	initSegments(cfg.Plugins)

	// Theme.
	themes := palette.BuiltinThemes
	theme := cfg.Theme
	if theme == "" {
		theme = "classic"
	}
	if theme == "original" {
		theme = "classic"
	}
	themeIdx := 0
	for i, t := range themes {
		if t.ID == theme {
			themeIdx = i
			break
		}
	}

	// Color depth.
	depths := []string{"auto", "truecolor", "256", "16", "none"}
	depth := strings.ToLower(cfg.ColorDepth)
	if depth == "" || depth == "24bit" {
		depth = "auto"
	}
	depthIdx := 0
	for i, d := range depths {
		if d == depth {
			depthIdx = i
			break
		}
	}

	// Segments.
	segs := segments.All()
	segSelected := make(map[string]bool, len(segs))
	for _, id := range cfg.Segments {
		segSelected[id] = true
	}

	return model{
		step:          stepWelcome,
		themes:        themes,
		themeIdx:      themeIdx,
		depths:        depths,
		depthIdx:      depthIdx,
		segs:          segs,
		segSelected:   segSelected,
		segCursor:     0,
		install:       true,
		installCursor: 0,
	}
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			if m.step == stepDone {
				m.quitting = true
				return m, tea.Quit
			}
			m.canceled = true
			return m, tea.Quit
		}

		switch m.step {
		case stepWelcome:
			return m.handleWelcome(msg)
		case stepTheme:
			return m.handleTheme(msg)
		case stepDepth:
			return m.handleDepth(msg)
		case stepSegments:
			return m.handleSegments(msg)
		case stepInstall:
			return m.handleInstall(msg)
		case stepSummary:
			return m.handleSummary(msg)
		case stepDone:
			m.quitting = true
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m model) handleWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		m.step = stepTheme
	}
	return m, nil
}

func (m model) handleTheme(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.themeIdx > 0 {
			m.themeIdx--
		}
	case "down", "j":
		if m.themeIdx < len(m.themes)-1 {
			m.themeIdx++
		}
	case "enter":
		m.step = stepDepth
	}
	return m, nil
}

func (m model) handleDepth(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.depthIdx > 0 {
			m.depthIdx--
		}
	case "down", "j":
		if m.depthIdx < len(m.depths)-1 {
			m.depthIdx++
		}
	case "enter":
		m.step = stepSegments
	}
	return m, nil
}

func (m model) handleSegments(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.segCursor > 0 {
			m.segCursor--
		}
	case "down", "j":
		if m.segCursor < len(m.segs)-1 {
			m.segCursor++
		}
	case " ":
		id := m.segs[m.segCursor].ID
		m.segSelected[id] = !m.segSelected[id]
	case "enter":
		m.step = stepInstall
	}
	return m, nil
}

func (m model) handleInstall(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "down", "k", "j", "left", "right", "h", "l":
		m.installCursor = 1 - m.installCursor
		m.install = m.installCursor == 0
	case "enter":
		m.step = stepSummary
	}
	return m, nil
}

func (m model) handleSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.saveAndFinish()
	}
	return m, nil
}

// saveAndFinish persists the config, optionally installs into Claude Code, and
// moves to the done step.
func (m model) saveAndFinish() (tea.Model, tea.Cmd) {
	cfg := config.LoadConfig()
	cfg.Theme = m.selectedTheme()
	if cfg.Theme == "classic" {
		cfg.Theme = "" // keep the file tidy; classic is the default
	}

	depth := m.depths[m.depthIdx]
	if depth == "auto" {
		cfg.ColorDepth = ""
	} else {
		cfg.ColorDepth = depth
	}

	selected := make([]string, 0, len(m.segs))
	for _, s := range m.segs {
		if m.segSelected[s.ID] {
			selected = append(selected, s.ID)
		}
	}
	cfg.Segments = selected

	if err := config.SaveConfig(cfg); err != nil {
		m.errMsg = fmt.Sprintf("Could not save config: %v", err)
		m.step = stepDone
		return m, nil
	}

	if m.install {
		cmd := exec.Command(os.Args[0], "install", "--target", "claude", "--yes")
		out, err := cmd.CombinedOutput()
		m.installOutput = string(out)
		if err != nil {
			if m.installOutput != "" {
				m.installOutput += "\n"
			}
			m.installOutput += fmt.Sprintf("Install command failed: %v", err)
		}
	}

	m.step = stepDone
	m.done = true
	return m, nil
}

func (m model) selectedTheme() string {
	return m.themes[m.themeIdx].ID
}

// View implements tea.Model.
func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	switch m.step {
	case stepWelcome:
		b.WriteString(m.viewWelcome())
	case stepTheme:
		b.WriteString(m.viewTheme())
	case stepDepth:
		b.WriteString(m.viewDepth())
	case stepSegments:
		b.WriteString(m.viewSegments())
	case stepInstall:
		b.WriteString(m.viewInstall())
	case stepSummary:
		b.WriteString(m.viewSummary())
	case stepDone:
		b.WriteString(m.viewDone())
	}

	b.WriteString("\n\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m model) header() string {
	return titleStyle.Render("claude-statusline setup wizard")
}

func (m model) footer() string {
	if m.step == stepDone {
		return dimStyle.Render("Press any key to exit")
	}
	return dimStyle.Render("↑/↓ navigate · Space toggle · Enter confirm · q/Esc cancel")
}

func (m model) viewWelcome() string {
	var b strings.Builder
	b.WriteString("Welcome! This wizard will get you set up in a few steps.\n\n")
	b.WriteString("You can pick a theme, color depth, which segments to show, and whether\n")
	b.WriteString("to wire this binary into Claude Code automatically.\n\n")
	b.WriteString(selectedStyle.Render("Press Enter to start"))
	return b.String()
}

func (m model) viewTheme() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("1. Theme") + "\n\n")
	for i, t := range m.themes {
		marker := "○"
		style := itemStyle
		if i == m.themeIdx {
			marker = "●"
			style = selectedStyle
		}
		line := fmt.Sprintf("%s %s — %s", marker, t.ID, t.Desc)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m model) viewDepth() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("2. Color depth") + "\n\n")
	for i, d := range m.depths {
		marker := "○"
		style := itemStyle
		if i == m.depthIdx {
			marker = "●"
			style = selectedStyle
		}
		label := d
		if label == "auto" {
			label = "auto (detect from terminal)"
		}
		b.WriteString(style.Render(fmt.Sprintf("%s %s", marker, label)) + "\n")
	}
	return b.String()
}

func (m model) viewSegments() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("3. Segments") + "\n\n")
	b.WriteString("Choose the segments you want in your statusline.\n\n")

	for i, s := range m.segs {
		checked := "[ ]"
		style := itemStyle
		if m.segSelected[s.ID] {
			checked = "[x]"
		}
		if i == m.segCursor {
			style = selectedStyle
			checked = ">" + checked[1:]
		}
		line := fmt.Sprintf("%s %s — %s", checked, s.ID, s.Desc)
		b.WriteString(style.Render(line) + "\n")
	}

	count := 0
	for _, sel := range m.segSelected {
		if sel {
			count++
		}
	}
	b.WriteString(fmt.Sprintf("\n%s selected", dimStyle.Render(fmt.Sprintf("%d", count))))
	return b.String()
}

func (m model) viewInstall() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("4. Install into Claude Code") + "\n\n")
	b.WriteString("Wire this binary into ~/.claude/settings.json so Claude Code uses it?\n\n")

	choices := []struct {
		yes  bool
		text string
	}{
		{true, "Yes — add to Claude Code settings"},
		{false, "No — keep the binary standalone"},
	}
	for i, c := range choices {
		marker := "○"
		style := itemStyle
		if m.installCursor == i {
			marker = "●"
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s %s", marker, c.text)) + "\n")
	}
	return b.String()
}

func (m model) viewSummary() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("5. Summary") + "\n\n")

	theme := m.selectedTheme()
	if theme == "" {
		theme = "classic"
	}
	b.WriteString(fmt.Sprintf("Theme:       %s\n", highlightStyle.Render(theme)))

	depth := m.depths[m.depthIdx]
	b.WriteString(fmt.Sprintf("Color depth: %s\n", highlightStyle.Render(depth)))

	selected := make([]string, 0, len(m.segs))
	for _, s := range m.segs {
		if m.segSelected[s.ID] {
			selected = append(selected, s.ID)
		}
	}
	b.WriteString(fmt.Sprintf("Segments:    %d selected\n", len(selected)))
	if len(selected) > 0 {
		b.WriteString(dimStyle.Render("  " + strings.Join(selected, ", ")) + "\n")
	}

	installLabel := "No"
	if m.install {
		installLabel = "Yes"
	}
	b.WriteString(fmt.Sprintf("Install:     %s\n", highlightStyle.Render(installLabel)))

	b.WriteString("\n" + selectedStyle.Render("Press Enter to save"))
	return b.String()
}

func (m model) viewDone() string {
	var b strings.Builder
	if m.errMsg != "" {
		b.WriteString(errorStyle.Render(m.errMsg) + "\n")
		return b.String()
	}

	b.WriteString(successStyle.Render("✓ Configuration saved") + "\n")
	b.WriteString(dimStyle.Render(config.ConfigPath()) + "\n")

	if m.install {
		b.WriteString("\n" + sectionStyle.Render("Claude Code install output") + "\n")
		if m.installOutput == "" {
			b.WriteString(dimStyle.Render("(no output)"))
		} else {
			b.WriteString(m.installOutput)
		}
	}
	return b.String()
}

// Styles.
var (
	titleStyle = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("#cba6f7"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#89b4fa"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a6e3a1"))
	itemStyle = lipgloss.NewStyle()
	highlightStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f9e2af"))
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a6e3a1"))
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f38ba8"))
)
