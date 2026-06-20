package main

// ─── Wizard ───────────────────────────────────────────────────────────
//
// A survey-driven onboarding flow for first-time users. It guides through
// theme, color depth, segment selection, and Claude Code wiring, then writes
// config.toml and (optionally) runs the install subcommand.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/AlecAivazis/survey.v1"
	"gopkg.in/AlecAivazis/survey.v1/core"
	"gopkg.in/AlecAivazis/survey.v1/terminal"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/plugins"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// runWizard starts the interactive onboarding wizard.
func runWizard() {
	if hasHelpFlag() {
		printWizardHelp()
		return
	}

	configureSurvey()

	// Load existing config so the wizard defaults to the user's current choices.
	cfg := config.LoadConfig()

	// Ensure the segment registry is populated (including any plugin segments
	// declared in the existing config).
	segments.Init()
	plugins.Load(cfg.Plugins)

	// Resolve defaults.
	theme := firstNonEmpty(cfg.Theme, "classic")
	colorDepth := firstNonEmpty(cfg.ColorDepth, "auto")
	selectedSegments := config.DefaultConfig().Segments
	if len(cfg.Segments) > 0 {
		selectedSegments = append([]string(nil), cfg.Segments...)
	}
	install := true

	printWelcome()

	// ─── Theme ──────────────────────────────────────────────────────────
	themeOpts := palette.ThemeIDs()
	themeIdx := indexOf(themeOpts, theme)
	if themeIdx < 0 {
		themeIdx = indexOf(themeOpts, "classic")
	}
	err := survey.AskOne(&survey.Select{
		Message: "Choose a theme:",
		Options: themeOpts,
		Default: themeOpts[themeIdx],
		Help:    "The theme drives the colors used by every segment.",
	}, &theme, nil)
	if handleInterrupt(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ theme prompt failed: %v\n", err)
		os.Exit(1)
	}

	// ─── Color Depth ────────────────────────────────────────────────────
	depthOpts := []string{"auto", "truecolor", "256", "16", "none"}
	depthIdx := indexOf(depthOpts, colorDepth)
	if depthIdx < 0 {
		depthIdx = 0
	}
	err = survey.AskOne(&survey.Select{
		Message: "Choose color depth:",
		Options: depthOpts,
		Default: depthOpts[depthIdx],
		Help:    "Auto detects your terminal; choose 'none' to disable colors.",
	}, &colorDepth, nil)
	if handleInterrupt(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ color depth prompt failed: %v\n", err)
		os.Exit(1)
	}

	// ─── Segments ───────────────────────────────────────────────────────
	allSegmentIDs := segmentIDs()
	err = survey.AskOne(&survey.MultiSelect{
		Message: "Choose which segments to show:",
		Options: allSegmentIDs,
		Default: selectedSegments,
		Help:    "Segments hide automatically when their data is missing.",
	}, &selectedSegments, nil)
	if handleInterrupt(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ segment prompt failed: %v\n", err)
		os.Exit(1)
	}

	// ─── Install into Claude Code ───────────────────────────────────────
	err = survey.AskOne(&survey.Confirm{
		Message: "Install the statusline into Claude Code?",
		Default: install,
		Help:    "Wires the binary into ~/.claude/settings.json.",
	}, &install, nil)
	if handleInterrupt(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ install prompt failed: %v\n", err)
		os.Exit(1)
	}

	// ─── Summary ────────────────────────────────────────────────────────
	printSummary(theme, colorDepth, selectedSegments, install)

	var save bool
	err = survey.AskOne(&survey.Confirm{
		Message: "Save these settings?",
		Default: true,
	}, &save, nil)
	if handleInterrupt(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ save prompt failed: %v\n", err)
		os.Exit(1)
	}
	if !save {
		fmt.Println("Wizard cancelled — no changes were saved.")
		return
	}

	// ─── Persist ────────────────────────────────────────────────────────
	cfg.Theme = theme
	cfg.ColorDepth = colorDepth
	cfg.Segments = selectedSegments
	if err := config.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "✗ cannot save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote config to %s\n", config.ConfigPath())

	if install {
		fmt.Println("Installing into Claude Code...")
		cmd := exec.Command(os.Args[0], "install", "--target", "claude", "--yes")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ install failed: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("✓ Wizard complete.")
}

// configureSurvey disables color/icons when the user has opted out of color.
func configureSurvey() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		core.DisableColor = true
		core.QuestionIcon = ""
		core.ErrorIcon = ""
		core.HelpIcon = ""
	}
}

// hasHelpFlag reports whether the user asked for help on the wizard subcommand.
func hasHelpFlag() bool {
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--help", "-h", "-help":
			return true
		}
	}
	return false
}

// printWizardHelp prints usage for the wizard subcommand.
func printWizardHelp() {
	fmt.Println(`claude-statusline wizard — interactive onboarding

Guides first-time users through theme, color depth, segment selection, and
Claude Code wiring, then writes ~/.config/claude-statusline/config.toml.

Flags:
  -h, --help   Show this message.

The wizard can be cancelled at any prompt with Ctrl-C; no files are written
until the final confirmation.`)
}

// printWelcome prints the introduction banner.
func printWelcome() {
	fmt.Println()
	fmt.Println("Welcome to claude-statusline!")
	fmt.Println()
	fmt.Println("This wizard will set up your statusline: pick a theme, color depth,")
	fmt.Println("which segments to show, and whether to wire it into Claude Code.")
	fmt.Println()
}

// printSummary renders the final review screen.
func printSummary(theme, colorDepth string, segs []string, install bool) {
	fmt.Println()
	fmt.Println("─── Summary ─────────────────────────────────────────────────────────")
	fmt.Printf("Theme:       %s\n", theme)
	fmt.Printf("Color depth: %s\n", colorDepth)
	fmt.Printf("Segments:    %d selected\n", len(segs))
	if len(segs) > 0 {
		preview := strings.Join(segs, ", ")
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		fmt.Printf("             %s\n", preview)
	}
	installText := "no"
	if install {
		installText = "yes"
	}
	fmt.Printf("Install:     %s\n", installText)
	fmt.Println()
}

// handleInterrupt detects Ctrl-C / interrupt and prints a friendly message.
// It returns true when the caller should abort the wizard.
func handleInterrupt(err error) bool {
	if err == terminal.InterruptErr {
		fmt.Println("\nWizard cancelled — no changes were saved.")
		return true
	}
	return false
}

// segmentIDs returns the IDs of all registered segments.
func segmentIDs() []string {
	all := segments.All()
	ids := make([]string, len(all))
	for i, s := range all {
		ids[i] = s.ID
	}
	return ids
}

// indexOf returns the index of v in s, or -1 if not present.
func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// firstNonEmpty returns the first non-empty string argument.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
