package main

// ─── Wizard ───────────────────────────────────────────────────────────
//
// Interactive onboarding wizard for first-time users. Built with
// charmbracelet/huh, it walks through theme, color depth, segments, and
// Claude Code installation, then writes config.toml and optionally runs
// the install subcommand.

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/segments"
)

// runWizard launches the interactive onboarding wizard. It guides the user
// through theme, color depth, segment selection, and Claude Code wiring,
// then persists the config and optionally installs into Claude Code.
func runWizard() {
	fs := flag.NewFlagSet("wizard", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: claude-statusline wizard")
		fmt.Fprintln(os.Stderr, "\nInteractive setup wizard for claude-statusline.")
	}
	_ = fs.Parse(os.Args[2:])
	if len(fs.Args()) > 0 {
		fs.Usage()
		os.Exit(2)
	}

	maybeDisableWizardColors()

	// Load existing config for defaults; fall back to built-in defaults.
	cfg := config.LoadConfig()
	themeDefault := coalesce(cfg.Theme, "classic")
	depthDefault := coalesce(cfg.ColorDepth, "auto")

	segments.Init()

	var (
		theme         = themeDefault
		depth         = depthDefault
		selected      []string
		install       bool
		confirmedSave bool
	)

	// Start with the default segment list selected.
	selected = append([]string(nil), config.DefaultConfig().Segments...)

	themeOptions := make([]huh.Option[string], 0, len(palette.ThemeIDs()))
	for _, id := range palette.ThemeIDs() {
		t := palette.ThemeByID(id)
		label := id
		if t.Desc != "" {
			label = fmt.Sprintf("%s — %s", id, t.Desc)
		}
		themeOptions = append(themeOptions, huh.NewOption(label, id))
	}

	depthOptions := []huh.Option[string]{
		huh.NewOption("auto — detect from terminal", "auto"),
		huh.NewOption("truecolor — 24-bit RGB", "truecolor"),
		huh.NewOption("256 — xterm-256 colors", "256"),
		huh.NewOption("16 — standard ANSI colors", "16"),
		huh.NewOption("none — no colors", "none"),
	}

	segmentOptions := make([]huh.Option[string], 0, len(segments.All()))
	for _, s := range segments.All() {
		segmentOptions = append(segmentOptions, huh.NewOption(s.ID, s.ID))
	}

	// Step 1–5: collect choices.
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Welcome to claude-statusline").
				Description("This wizard will set up your statusline.\n\n"+
					"You'll pick a color theme, color depth, which segments to show, "+
					"and whether to wire the binary into Claude Code's settings.json."),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Theme").
				Description("Choose a color theme for the statusline.").
				Options(themeOptions...).
				Value(&theme),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Color depth").
				Description("How many colors your terminal should use.").
				Options(depthOptions...).
				Value(&depth),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Segments").
				Description("Select the segments to display, in order.").
				Options(segmentOptions...).
				Value(&selected),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install into Claude Code?").
				Description("Wire this binary into ~/.claude/settings.json so Claude Code uses it as its statusline.").
				Affirmative("Yes").
				Negative("No").
				Value(&install),
		),
	)

	if err := form.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Wizard cancelled — no changes made.")
		os.Exit(0)
	}

	// Step 6: summarize and confirm save.
	summary := buildWizardSummary(theme, depth, selected, install)
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Summary").
				Description(summary),
			huh.NewConfirm().
				Title("Save these settings?").
				Affirmative("Save").
				Negative("Cancel").
				Value(&confirmedSave),
		),
	)

	if err := confirmForm.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Wizard cancelled — no changes made.")
		os.Exit(0)
	}

	if !confirmedSave {
		fmt.Println("Settings discarded — no changes made.")
		os.Exit(0)
	}

	cfg.Theme = theme
	cfg.ColorDepth = depth
	cfg.Segments = selected

	if err := config.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Failed to save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Config saved to %s\n", config.ConfigPath())

	if install {
		fmt.Println()
		cmd := exec.Command(os.Args[0], "install", "--target", "claude", "--yes")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Install failed: %v\n", err)
			os.Exit(1)
		}
	}
}

// buildWizardSummary formats the chosen settings for the final review screen.
func buildWizardSummary(theme, depth string, segments []string, install bool) string {
	installStr := "No"
	if install {
		installStr = "Yes"
	}
	var b strings.Builder
	b.WriteString("Review your choices before saving:\n\n")
	fmt.Fprintf(&b, "  Theme:        %s\n", theme)
	fmt.Fprintf(&b, "  Color depth:  %s\n", depth)
	fmt.Fprintf(&b, "  Segments:     %s\n", strings.Join(segments, ", "))
	fmt.Fprintf(&b, "  Install:      %s\n", installStr)
	return b.String()
}

// maybeDisableWizardColors forces lipgloss/huh into no-color mode when the
// environment explicitly opts out, matching the renderer's NO_COLOR/TERM=dumb
// behavior.
func maybeDisableWizardColors() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// coalesce returns the first non-empty string argument.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
