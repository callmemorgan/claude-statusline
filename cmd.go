package main

// ─── Command Dispatch ────────────────────────────────────────────────

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/payload"
	"github.com/callmemorgan/claude-statusline/internal/plugins"
	"github.com/callmemorgan/claude-statusline/internal/render"
	"github.com/callmemorgan/claude-statusline/internal/segments"
	"github.com/callmemorgan/claude-statusline/internal/state"
	"github.com/callmemorgan/claude-statusline/internal/version"
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
			version.RunVersion()
			return
		case "configure":
			runConfigure()
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
			if err := plugins.RunPluginRefresh(); err != nil {
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

	input := payload.ReadInput()
	p := payload.ParsePayload(input)

	if debug {
		printDebugSchema(input, p)
		cfg, warns := config.LoadConfigWarn()
		initSegments(cfg.Plugins)
		warns = append(warns, validateSegmentRefs(cfg)...)
		printConfigWarnings(warns)
		return
	}

	cfg, warns := config.LoadConfigWarn()
	colors := palette.CurrentPalette(cfg.Theme, cfg.ColorDepth, cfg.ThemeColors)
	if os.Getenv("STATUSLINE_VERBOSE") != "" {
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "claude-statusline: config: %s\n", w)
		}
	}
	initSegments(cfg.Plugins)

	st := state.LoadState(cfg.State, segments.FirstNonEmpty(p.SessionID, p.ConversationID), start)
	st.Record(p, start)

	width := payload.TerminalWidth(p)
	style := render.StyleFor(cfg, colors)
	lines := render.Statusline(render.Input{P: p, C: colors, Cfg: cfg, State: st, Width: width, Now: start})

	lines = maybeReleaseTakeover(cfg.ReleaseNotes, lines, colors, width, style.Padding, start)

	elapsedMS := float64(time.Since(start).Microseconds()) / 1000.0
	if len(lines) > 0 {
		fmt.Printf("%s%s%s%.1fms%s\n", lines[0], style.Sep, colors.Dim, elapsedMS, colors.Rst)
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

// initSegments initializes the segment registry and registers plugin segments.
// It also wires the update segment renderer, which lives in the root package to
// avoid an import cycle with the update-check machinery.
func initSegments(pluginDefs []config.PluginDef) {
	segments.Init()
	plugins.Load(pluginDefs)
	segments.UpdateRenderer = renderUpdate
}

// validateSegmentRefs reports config references to segments or setting keys
// that don't exist. Requires initSegments to have run (so plugin segments are
// registered). Read-only: unknown IDs are kept (the renderer skips them).
func validateSegmentRefs(cfg config.Config) []config.ConfigWarning {
	var warns []config.ConfigWarning
	known := func(id string) bool {
		_, ok := segments.ByID(id)
		return ok
	}
	for _, id := range cfg.Segments {
		if !known(id) {
			warns = append(warns, config.ConfigWarning{Path: "segments", Msg: fmt.Sprintf("unknown segment %q", id)})
		}
	}
	for id := range cfg.Lines {
		if !known(id) {
			warns = append(warns, config.ConfigWarning{Path: "lines." + id, Msg: "unknown segment"})
		}
	}
	for id := range cfg.Colors {
		if !known(id) {
			warns = append(warns, config.ConfigWarning{Path: "colors." + id, Msg: "unknown segment"})
		}
	}
	for id, vals := range cfg.Settings {
		seg, ok := segments.ByID(id)
		if !ok {
			warns = append(warns, config.ConfigWarning{Path: "settings." + id, Msg: "unknown segment"})
			continue
		}
		for key := range vals {
			found := false
			for _, sp := range seg.Settings {
				if sp.Key == key && !sp.Ephemeral {
					found = true
					break
				}
			}
			if !found {
				warns = append(warns, config.ConfigWarning{Path: "settings." + id + "." + key, Msg: "unknown setting key (ignored)"})
			}
		}
	}
	return warns
}
