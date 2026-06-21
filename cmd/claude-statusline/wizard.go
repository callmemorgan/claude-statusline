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
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

const (
	esc            = "\x1b"
	csi            = esc + "["
	clearScreen    = csi + "2J"
	cursorHome     = csi + "H"
	hideCursor     = csi + "?25l"
	showCursor     = csi + "?25h"
	enterAltScreen = csi + "?1049h"
	exitAltScreen  = csi + "?1049l"
)

// colorDepths is the ordered list of color-depth choices the wizard offers.
// The selected index is resolved against this slice in newWizard and the same
// slice is rendered in showColorDepth, so the two never drift.
var colorDepths = []string{"auto", "truecolor", "256", "16", "none"}

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

// restore leaves the alternate screen, shows the cursor, and returns the
// terminal to cooked mode so any final output (the saved-config confirmation,
// the install subprocess) renders on the user's primary screen with normal
// newline handling instead of being discarded when the alt screen exits. It
// is idempotent and a no-op when raw mode was never entered (line mode).
func (w *wizard) restore() {
	if w.restored {
		return
	}
	w.restored = true
	if !w.raw {
		return
	}
	fmt.Fprint(w.out, exitAltScreen+showCursor)
	term.Restore(int(os.Stdin.Fd()), w.oldState)
	// Back in cooked mode the terminal translates "\n" itself, so drop the
	// CRLF-translating wrapper and write straight to stdout.
	w.out = os.Stdout
}

// crlfWriter wraps an io.Writer and converts lone LF bytes into CRLF. This
// keeps raw-mode terminal output left-aligned because the terminal driver is
// no longer translating newlines for us.
type crlfWriter struct {
	w io.Writer
}

func (c *crlfWriter) Write(p []byte) (n int, err error) {
	var buf []byte
	lastCR := false
	for _, b := range p {
		if b == '\n' && !lastCR {
			buf = append(buf, '\r', '\n')
		} else {
			buf = append(buf, b)
		}
		lastCR = b == '\r'
	}
	_, err = c.w.Write(buf)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// detectTerminalWidth returns a sensible terminal width for wrapping wizard
// output. It prefers the $COLUMNS environment variable, falls back to the
// kernel winsize, and clamps the result to a safe range. Embedded terminals
// (e.g. Claude Code's panel) sometimes report 0 or an absurd value, so the
// clamping prevents the ugly soft-wrapping seen when we trust a bogus width.
func detectTerminalWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 20 {
			return clampWidth(n)
		}
	}
	if n, _, err := term.GetSize(int(os.Stdin.Fd())); err == nil && n >= 20 {
		return clampWidth(n)
	}
	return 80
}

func clampWidth(n int) int {
	if n < 40 {
		return 40
	}
	if n > 200 {
		return 200
	}
	return n
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

	// raw, oldState, and restored track the raw-mode/alt-screen terminal
	// lifecycle so final output can be flushed to the primary screen.
	raw      bool
	oldState *term.State
	restored bool

	// lineMode is true when stdin is not a terminal; input is read a line at a
	// time and mapped to the same actions as the raw-mode key parser.
	lineMode bool

	// width is the terminal width used for wrapping output. It defaults to 80
	// when the terminal size cannot be detected.
	width int
}

func newWizard() *wizard {
	cfg := config.LoadConfig()

	themeIDs := palette.ThemeIDs()
	themeIdx := indexOf(themeIDs, firstNonEmpty(cfg.Theme, "classic"))
	if themeIdx < 0 {
		themeIdx = 0
	}

	depthIdx := indexOf(colorDepths, firstNonEmpty(cfg.ColorDepth, "auto"))
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
		width:      detectTerminalWidth(),
	}
}

func (w *wizard) run() error {
	if !w.lineMode {
		old, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("cannot set raw mode: %w", err)
		}
		w.raw = true
		w.oldState = old
		// Raw mode does not translate LF to CRLF, so plain "\n" leaves the
		// cursor hanging at the previous column and produces staggered lines
		// in terminals like Claude Code's. Wrap stdout so every "\n" becomes
		// "\r\n" while ANSI sequences pass through untouched.
		w.out = &crlfWriter{w.out}
		fmt.Fprint(w.out, enterAltScreen+hideCursor+clearScreen+cursorHome)
	} else {
		fmt.Fprint(w.out, clearScreen+cursorHome)
	}
	defer w.restore()

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

// printWrapped prints text wrapped to the wizard's terminal width.
func (w *wizard) printWrapped(text string) {
	for _, line := range wrapText(text, w.width) {
		w.println(line)
	}
}

// visibleWidth returns the number of visible runes in s, skipping ANSI CSI
// SGR escape sequences so wrapping calculations don't over-count.
func visibleWidth(s string) int {
	w := 0
	inEsc := false
	for i := 0; i < len(s); {
		b := s[i]
		if inEsc {
			if b == 'm' {
				inEsc = false
			}
			i++
			continue
		}
		if b == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			inEsc = true
			i += 2
			continue
		}
		// Count runes instead of bytes so multi-byte characters are one column.
		_, size := utf8.DecodeRuneInString(s[i:])
		w++
		i += size
	}
	return w
}

// wrapText wraps text into lines no longer than width visible columns. It
// breaks at spaces when possible and preserves existing newlines.
func wrapText(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if visibleWidth(line) <= width {
			lines = append(lines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			candidate := current + " " + word
			if visibleWidth(candidate) <= width {
				current = candidate
				continue
			}
			lines = append(lines, current)
			current = word
		}
		lines = append(lines, current)
	}
	return lines
}

// wrapTextIndent wraps text with a hanging indent: the first line starts at
// column 0, subsequent lines are indented by indent spaces.
func wrapTextIndent(text string, width, indent int) []string {
	inner := width - indent
	if inner < 1 {
		inner = 1
	}
	wrapped := wrapText(text, inner)
	if len(wrapped) == 0 {
		return wrapped
	}
	pad := strings.Repeat(" ", indent)
	for i := 1; i < len(wrapped); i++ {
		wrapped[i] = pad + wrapped[i]
	}
	return wrapped
}

// ─── Welcome ──────────────────────────────────────────────────────────

func (w *wizard) showWelcome() error {
	w.clearAndHome()
	w.header("Welcome to claude-statusline")
	w.printWrapped("This wizard will set up your statusline in a few steps:")
	w.println("")
	items := []string{
		"1. Pick a theme",
		"2. Pick a color depth",
		"3. Choose which segments to show",
		"4. Decide whether to wire into Claude Code",
	}
	for _, item := range items {
		for _, line := range wrapTextIndent(item, w.width-2, 4) {
			w.println("  " + line)
		}
	}
	w.println("")
	w.printWrapped("You can cancel at any time with q or Ctrl-C.")
	w.footer()

	for {
		k, err := w.readKey()
		if err != nil {
			return err
		}
		switch k {
		case "enter", "space":
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
	depths := colorDepths
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
		case "space":
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
		w.printWrapped("Wire this binary into ~/.claude/settings.json so Claude Code uses it as the statusline command.")
		w.println("")

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
			w.printWrapped("Config will be saved and Claude Code will be wired.")
		} else {
			w.printWrapped("Config will be saved. Run 'claude-statusline install' later.")
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
	// Leave the alt screen and raw mode first so the confirmation and the
	// install subprocess output land on the primary screen and are not erased
	// when the wizard exits.
	w.restore()
	if err := config.SaveConfig(w.cfg); err != nil {
		return fmt.Errorf("could not save config: %w", err)
	}
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
	w.restore()
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
