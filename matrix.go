package main

import (
	"fmt"
	"os"
	"strings"
)

// ─── Scenario Matrix Subcommand ──────────────────────────────────────
//
// `claude-statusline matrix` renders the user's current config across the
// curated scenario set and prints each pane as labelled, plain text. It is a
// non-interactive companion to the TUI matrix overlay (key `m`), useful for
// piping into a file or a CI snapshot. Like every other subcommand it never
// touches the bare render path.
//
// Flags:
//   --plain   force color-free output (the runtime palette already honors
//             NO_COLOR / TERM=dumb; this flag overrides the environment)
//   --reflow MODE   override every scenario's reflow with MODE (off/cascade/group)

func runMatrix(args []string) {
	plain := false
	reflowOverride := ""
	for i := 0; i < len(args); i++ {
		a := strings.TrimLeft(args[i], "-")
		switch a {
		case "plain":
			plain = true
		case "reflow":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "matrix: --reflow requires a value (off|cascade|group)")
				os.Exit(2)
			}
			reflowOverride = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "matrix: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	if reflowOverride != "" {
		switch reflowOverride {
		case "off", "cascade", "group":
			// ok
		default:
			fmt.Fprintf(os.Stderr, "matrix: invalid --reflow %q (want off|cascade|group)\n", reflowOverride)
			os.Exit(2)
		}
	}

	cfg, _ := loadConfigWarn()
	initSegments(cfg.Plugins)

	now := scenarioNow()
	c := currentPalette(cfg)
	if plain {
		c = palette{}
	}

	scs := curatedScenarios(now)
	fmt.Printf("Scenario matrix — %d panes, config from %s\n", len(scs), configPath())
	fmt.Println("Each pane renders your config through the real builder at its own width.")
	fmt.Println()

	for _, sc := range scs {
		if reflowOverride != "" {
			sc.Reflow = reflowOverride
		}
		lines := renderScenario(sc, cfg, c, now)

		// scenarioFits measures visible width (ANSI stripped), so the colored
		// render is measured exactly as a plain one would be — no second build.
		fit := "fits"
		if !scenarioFits(lines, sc.Width) {
			fit = "OVERFLOWS (terminal would soft-wrap)"
		}
		fmt.Printf("── %s ──\n", sc.Name)
		fmt.Printf("   %s\n", sc.Note)
		fmt.Printf("   width %d · reflow %s · %d line(s) · %s\n",
			sc.Width, scenarioReflowLabel(sc, cfg), len(lines), fit)
		fmt.Println()
		fmt.Println(joinScenarioLines(lines))
		fmt.Println()
	}
}
