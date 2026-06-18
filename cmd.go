package main

// ─── Command Dispatch ────────────────────────────────────────────────

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// dispatch routes subcommands. The bare no-args invocation is the renderer —
// that is how Claude Code calls the binary, so it must stay untouched.
// Legacy --flag spellings are accepted as aliases for each subcommand.
func dispatch() {
	if len(os.Args) > 1 {
		switch strings.TrimLeft(os.Args[1], "-") {
		case "help", "h":
			printHelp()
			return
		case "version", "v", "V":
			runVersion()
			return
		case "configure":
			runConfigure(os.Args[2:])
			return
		case "install":
			runInstall(os.Args[2:])
			return
		case "uninstall":
			runUninstall(os.Args[2:])
			return
		case "debug":
			runRender(true)
			return
		case "plugin-refresh":
			if err := runPluginRefresh(); err != nil {
				os.Exit(1)
			}
			return
		case "release-notes":
			runReleaseNotes(os.Args[2:])
			return
		case "update":
			runUpdate(os.Args[2:])
			return
		case "update-check":
			runUpdateCheck()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (try: claude-statusline --help)\n", os.Args[1])
			os.Exit(2)
		}
	}
	runRender(false)
}

// runRender is the default mode: read the JSON payload from stdin and print
// the statusline. With debug=true it prints the schema-comparison table instead.
func runRender(debug bool) {
	start := time.Now()

	input := readInput()
	p := parsePayload(input)

	if debug {
		printDebugSchema(input, p)
		cfg, warns := loadConfigWarn()
		initSegments(cfg.Plugins)
		warns = append(warns, validateSegmentRefs(cfg)...)
		printConfigWarnings(warns)
		return
	}

	cfg, warns := loadConfigWarn()
	colors := currentPalette(cfg)
	if os.Getenv("STATUSLINE_VERBOSE") != "" {
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "claude-statusline: config: %s\n", w)
		}
	}
	initSegments(cfg.Plugins)

	st := loadState(cfg.State, firstNonEmpty(p.SessionID, p.ConversationID), start)
	st.Record(p, start)

	width := terminalWidth(p)
	style := styleFor(cfg, colors)
	lines := buildStatusline(buildInput{P: p, C: colors, Cfg: cfg, State: st, Width: width, Now: start})

	lines = maybeReleaseTakeover(cfg.ReleaseNotes, lines, colors, width, style.padding, start)

	elapsedMS := float64(time.Since(start).Microseconds()) / 1000.0
	if len(lines) > 0 {
		fmt.Printf("%s%s%s%.1fms%s\n", lines[0], style.sep, colors.Dim, elapsedMS, colors.Rst)
		for _, l := range lines[1:] {
			fmt.Println(l)
		}
	} else {
		fmt.Printf("%s%.1fms%s\n", colors.Dim, elapsedMS, colors.Rst)
	}

	// Persist state after printing so disk I/O never delays output.
	_ = st.Save()

	// Spawn the update-check worker after output. This is the only
	// post-render side effect, and it never blocks: the worker is
	// detached, returns immediately, and respects `mode = "off"`.
	maybeSpawnUpdateCheck(cfg.Update, start)
}

// configureDirect and configureList are function variables so tests can
// substitute stubs and verify dispatch without needing a terminal.
var (
	configureDirect = runConfigureDirect
	configureList   = runConfigureList
)

// runConfigure dispatches to the list-based TUI by default, or to the
// direct-manipulation (visual) TUI when --direct is passed. The list UI also
// offers a v key shortcut to switch into direct mode mid-session.
func runConfigure(args []string) {
	direct := false
	for _, a := range args {
		if a == "--direct" {
			direct = true
			break
		}
	}
	if direct {
		configureDirect()
		return
	}
	if configureList() {
		configureDirect()
	}
}
