package main

// ─── Wizard ───────────────────────────────────────────────────────────
//
// A zero-dependency ANSI raw-mode onboarding wizard. It guides first-time
// users through theme, color depth, segment selection, and optional Claude
// Code installation, then writes config.toml and shells out to the install
// subcommand when requested.
//
// The implementation uses only the standard library, existing project
// packages, and golang.org/x/term (already a project dependency for install).

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

const (
	esc         = "\x1b"
	csi         = esc + "["
	clearScreen = csi + "2J"
	cursorHome  = csi + "H"
	hideCursor  = csi + "?25l"
	showCursor  = csi + "?25h"
)

// wizardColors holds the small set of SGR escapes used by the wizard. All
// fields are empty when colors are disabled so output stays plain text.
type wizardColors struct {
	Title    string
	Prompt   string
	Selected string
	Cursor   string
	Dim      string
	Reset    string
	Warn     string
	OK       string
}

func newWizardColors() wizardColors {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return wizardColors{}
	}
	return wizardColors{
		Title:    "\x1b[1;36m",
		Prompt:   "\x1b[1m",
		Selected: "\x1b[32m",
		Cursor:   "\x1b[34m",
		Dim:      "\x1b[90m",
		Reset:    "\x1b[0m",
		Warn:     "\x1b[33m",
		OK:       "\x1b[32m",
	}
}

// runWizard starts the interactive ANSI onboarding wizard. It supports
// --help and exits cleanly on cancel or completion.
func runWizard() {
	if hasHelpFlag() {
		fmt.Println("claude-statusline wizard")
		fmt.Println()
		fmt.Println("Interactive first-time setup wizard. Guides you through theme,")
		fmt.Println("color depth, segment selection, and optional Claude Code wiring.")
		fmt.Println()
		fmt.Println("Navigation: ↑/↓ move, Enter confirms, Space toggles checklists,")
		fmt.Println("q or Ctrl-C cancels at any step.")
		return
	}

	w := newWizard()
	if err := w.run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%s\n", err)
		os.Exit(1)
	}
}

func hasHelpFlag() bool {
	for _, a := range os.Args[2:] {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

func cleanupTerminal() {
	// Best-effort cleanup if we exit in raw mode.
	fmt.Print(showCursor)
}

// wizard holds the wizard state and I/O primitives.
type wizard struct {
	cfg     config.Config
	install bool

	step       int
	themeIdx   int
	depthIdx   int
	segCursor  int
	segChecked map[string]bool
	yesNoIdx   int // 0 = yes, 1 = no

	in     *bufio.Reader
	out    io.Writer
	colors wizardColors

	// lineMode is true when stdin is not a terminal; input is read a line at a
	// time and mapped to the same actions as the raw-mode key parser.
	lineMode bool
}

func newWizard() *wizard {
	cfg := config.LoadConfig()

	themeIDs := palette.ThemeIDs()
	themeIdx := indexOf(themeIDs, firstNonEmpty(cfg.Theme, "classic"))
	if themeIdx < 0 {
		themeIdx = 0
	}

	depths := []string{"auto", "truecolor", "256", "16", "none"}
	depthIdx := indexOf(depths, firstNonEmpty(cfg.ColorDepth, "auto"))
	if depthIdx < 0 {
		depthIdx = 0
	}

	segChecked := make(map[string]bool)
	for _, id := range config.DefaultConfig().Segments {
		segChecked[id] = true
	}
	// If the user already has a segment list, honour it instead.
	if len(cfg.Segments) > 0 {
		for id := range segChecked {
			segChecked[id] = false
		}
		for _, id := range cfg.Segments {
			segChecked[id] = true
		}
	}

	return &wizard{
		cfg:        cfg,
		themeIdx:   themeIdx,
		depthIdx:   depthIdx,
		segChecked: segChecked,
		in:         bufio.NewReader(os.Stdin),
		out:        os.Stdout,
		colors:     newWizardColors(),
		lineMode:   !term.IsTerminal(int(os.Stdin.Fd())),
	}
}

func (w *wizard) run() error {
	if !w.lineMode {
		old, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("cannot set raw mode: %w", err)
		}
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	defer cleanupTerminal()

	fmt.Fprint(w.out, hideCursor+clearScreen+cursorHome)

	for w.step >= 0 && w.step <= 5 {
		switch w.step {
		case 0:
			if err := w.showWelcome(); err != nil {
				return err
			}
		case 1:
			if err := w.showTheme(); err != nil {
				return err
			}
		case 2:
			if err := w.showColorDepth(); err != nil {
				return err
			}
		case 3:
			if err := w.showSegments(); err != nil {
				return err
			}
		case 4:
			if err := w.showInstall(); err != nil {
				return err
			}
		case 5:
			return w.showSummary()
		}
	}
	return nil
}

// ─── Rendering primitives ─────────────────────────────────────────────

func (w *wizard) clearAndHome() {
	fmt.Fprint(w.out, clearScreen+cursorHome)
}

func (w *wizard) header(title string) {
	c := w.colors
	fmt.Fprintf(w.out, "%s%s%s\n\n", c.Title, title, c.Reset)
}

func (w *wizard) footer() {
	c := w.colors
	if w.lineMode {
		fmt.Fprintf(w.out, "\n%s[Enter] confirm  [q] quit%s\n", c.Dim, c.Reset)
		return
	}
	fmt.Fprintf(w.out, "\n%s↑/↓ move  Enter confirm  Space toggle  q quit%s\n", c.Dim, c.Reset)
}

func (w *wizard) println(format string, args ...any) {
	fmt.Fprintf(w.out, format+"\n", args...)
}

// ─── Welcome ──────────────────────────────────────────────────────────

func (w *wizard) showWelcome() error {
	w.clearAndHome()
	w.header("Welcome to claude-statusline")
	w.println("This wizard will set up your statusline in a few steps:")
	w.println("")
	w.println("  1. Pick a theme")
	w.println("  2. Pick a color depth")
	w.println("  3. Choose which segments to show")
	w.println("  4. Decide whether to wire into Claude Code")
	w.println("")
	w.println("You can cancel at any time with q or Ctrl-C.")
	w.footer()

	for {
		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "enter", " ":
			w.step = 1
			return nil
		case "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

// ─── Theme ────────────────────────────────────────────────────────────

func (w *wizard) showTheme() error {
	themes := palette.ThemeIDs()
	for {
		w.clearAndHome()
		w.header("Choose a theme")
		for i, id := range themes {
			w.drawListItem(i == w.themeIdx, id, paletteThemeDesc(id))
		}
		w.footer()

		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "up":
			if w.themeIdx > 0 {
				w.themeIdx--
			}
		case "down":
			if w.themeIdx < len(themes)-1 {
				w.themeIdx++
			}
		case "enter":
			w.cfg.Theme = themes[w.themeIdx]
			w.step = 2
			return nil
		case "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

// ─── Color depth ──────────────────────────────────────────────────────

func (w *wizard) showColorDepth() error {
	depths := []string{"auto", "truecolor", "256", "16", "none"}
	for {
		w.clearAndHome()
		w.header("Choose color depth")
		for i, d := range depths {
			label := d
			if d == "auto" {
				label = "auto (detect terminal)"
			}
			w.drawListItem(i == w.depthIdx, label, "")
		}
		w.footer()

		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "up":
			if w.depthIdx > 0 {
				w.depthIdx--
			}
		case "down":
			if w.depthIdx < len(depths)-1 {
				w.depthIdx++
			}
		case "enter":
			w.cfg.ColorDepth = depths[w.depthIdx]
			w.step = 3
			return nil
		case "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

// ─── Segments ─────────────────────────────────────────────────────────

func (w *wizard) showSegments() error {
	all := segments.All()
	if len(all) == 0 {
		segments.Init()
		all = segments.All()
	}
	ids := make([]string, 0, len(all))
	for _, s := range all {
		ids = append(ids, s.ID)
	}

	for {
		w.clearAndHome()
		w.header("Choose segments")
		for i, id := range ids {
			checked := w.segChecked[id]
			w.drawCheckItem(i == w.segCursor, checked, id, segmentDesc(id, all))
		}
		w.footer()

		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "up":
			if w.segCursor > 0 {
				w.segCursor--
			}
		case "down":
			if w.segCursor < len(ids)-1 {
				w.segCursor++
			}
		case " ":
			id := ids[w.segCursor]
			w.segChecked[id] = !w.segChecked[id]
		case "enter":
			selected := make([]string, 0, len(ids))
			for _, id := range ids {
				if w.segChecked[id] {
					selected = append(selected, id)
				}
			}
			w.cfg.Segments = selected
			w.step = 4
			return nil
		case "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

// ─── Install into Claude Code ─────────────────────────────────────────

func (w *wizard) showInstall() error {
	for {
		w.clearAndHome()
		w.header("Install into Claude Code?")
		w.println("Wire this binary into ~/.claude/settings.json so Claude Code")
		w.println("uses it as the statusline command.\n")

		w.drawYesNo(w.yesNoIdx == 0, w.yesNoIdx == 1)
		w.footer()

		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "left", "y":
			w.yesNoIdx = 0
		case "right", "n":
			w.yesNoIdx = 1
		case "enter":
			w.install = w.yesNoIdx == 0
			w.step = 5
			return nil
		case "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

// ─── Summary ──────────────────────────────────────────────────────────

func (w *wizard) showSummary() error {
	for {
		w.clearAndHome()
		w.header("Summary")

		c := w.colors
		fmt.Fprintf(w.out, "  %sTheme:%s       %s\n", c.Prompt, c.Reset, w.cfg.Theme)
		fmt.Fprintf(w.out, "  %sColor depth:%s %s\n", c.Prompt, c.Reset, w.cfg.ColorDepth)
		fmt.Fprintf(w.out, "  %sSegments:%s    %d selected\n", c.Prompt, c.Reset, len(w.cfg.Segments))
		fmt.Fprintf(w.out, "  %sInstall:%s     %s\n", c.Prompt, c.Reset, yesNo(w.install))
		w.println("")

		if w.install {
			w.println("Config will be saved and Claude Code will be wired.")
		} else {
			w.println("Config will be saved. Run 'claude-statusline install' later.")
		}
		w.println("")
		fmt.Fprintf(w.out, "%s[y/Enter] save  [n/q] cancel%s\n", c.Dim, c.Reset)

		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "y", "enter":
			return w.save()
		case "n", "q", "ctrl-c":
			return w.cancel("Wizard cancelled.")
		}
	}
}

func (w *wizard) save() error {
	if err := config.SaveConfig(w.cfg); err != nil {
		return fmt.Errorf("could not save config: %w", err)
	}
	w.clearAndHome()
	fmt.Fprintf(w.out, "%s✓ Configuration saved to %s%s\n", w.colors.OK, config.ConfigPath(), w.colors.Reset)

	if w.install {
		w.println("")
		fmt.Fprintf(w.out, "%sInstalling into Claude Code…%s\n", w.colors.Dim, w.colors.Reset)
		cmd := exec.Command(os.Args[0], "install", "--target", "claude", "--yes")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// The install subcommand already prints diagnostics; just report the
			// non-zero exit without adding noise.
			return fmt.Errorf("install step failed")
		}
	}

	return nil
}

func (w *wizard) cancel(msg string) error {
	w.step = -1
	w.clearAndHome()
	fmt.Fprintf(w.out, "%s%s%s\n", w.colors.Warn, msg, w.colors.Reset)
	return nil
}

// ─── Input handling ───────────────────────────────────────────────────

// readKey reads a single logical key from stdin. In raw mode it parses ANSI
// escape sequences; in line mode it consumes one line and returns a mapped
// action. Returned values: up, down, left, right, enter, space, y, n, q,
// ctrl-c, or an empty string for unrecognized input.
func (w *wizard) readKey() (string, error) {
	if w.lineMode {
		return w.readLineKey()
	}

	b, err := w.in.ReadByte()
	if err != nil {
		return "", err
	}

	// Ctrl-C or ESC in raw mode cancels.
	if b == 3 {
		return "ctrl-c", nil
	}
	if b == 27 {
		// Parse CSI sequences.
		nxt, err := w.in.ReadByte()
		if err != nil {
			return "q", nil // lone ESC treated as quit
		}
		if nxt != '[' {
			return "", nil
		}
		final, err := w.in.ReadByte()
		if err != nil {
			return "", nil
		}
		switch final {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		case 'C':
			return "right", nil
		case 'D':
			return "left", nil
		}
		return "", nil
	}

	switch b {
	case '\r', '\n':
		return "enter", nil
	case ' ':
		return "space", nil
	case 'y', 'Y':
		return "y", nil
	case 'n', 'N':
		return "n", nil
	case 'q', 'Q':
		return "q", nil
	}
	return "", nil
}

func (w *wizard) readLineKey() (string, error) {
	line, err := w.in.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "enter", nil
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return "y", nil
	case "n", "no":
		return "n", nil
	case "q", "quit", "cancel":
		return "q", nil
	}
	// For piped newline sequences the first blank line confirms the default;
	// any other text in line mode is treated as confirm to keep the wizard
	// drivable with simple input.
	return "enter", nil
}

// ─── Drawing helpers ──────────────────────────────────────────────────

func (w *wizard) drawListItem(selected bool, label, desc string) {
	c := w.colors
	cursor := "  "
	if selected {
		cursor = c.Cursor + "> " + c.Reset
	}
	name := label
	if selected {
		name = c.Selected + label + c.Reset
	}
	if desc != "" {
		fmt.Fprintf(w.out, "%s%s %s (%s)%s\n", cursor, name, c.Dim, desc, c.Reset)
		return
	}
	fmt.Fprintf(w.out, "%s%s\n", cursor, name)
}

func (w *wizard) drawCheckItem(selected, checked bool, label, desc string) {
	c := w.colors
	cursor := "  "
	if selected {
		cursor = c.Cursor + "> " + c.Reset
	}
	box := "[ ]"
	if checked {
		box = c.Selected + "[x]" + c.Reset
	}
	if desc != "" {
		fmt.Fprintf(w.out, "%s%s %s — %s%s%s\n", cursor, box, label, c.Dim, desc, c.Reset)
		return
	}
	fmt.Fprintf(w.out, "%s%s %s\n", cursor, box, label)
}

func (w *wizard) drawYesNo(yesSelected, noSelected bool) {
	c := w.colors
	yes := "[ ] Yes"
	no := "[ ] No"
	if yesSelected {
		yes = c.Cursor + "> [x] Yes" + c.Reset
	}
	if noSelected {
		no = c.Cursor + "> [x] No" + c.Reset
	}
	fmt.Fprintf(w.out, "  %s    %s\n", yes, no)
}

// ─── Small helpers ────────────────────────────────────────────────────

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func paletteThemeDesc(id string) string {
	t := palette.ThemeByID(id)
	return t.Desc
}

func segmentDesc(id string, all []segments.Info) string {
	for _, s := range all {
		if s.ID == id {
			return s.Desc
		}
	}
	return ""
}
