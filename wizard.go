package main

// ─── First-run Wizard: config assembly ───────────────────────────────
//
// The guided wizard collects a handful of high-level choices and assembles a
// full config from them. Everything in this file is pure (no tview): the TUI
// in tui_wizard.go only collects choices and then calls assembleWizardConfig,
// which round-trips through the same config model and save path as the
// list-based editor. The category → config logic lives here so it can be unit
// tested without a terminal.

// wizardCategory groups related segments under a single human-facing label so a
// newcomer toggles "Git" rather than five individual segment IDs. Segments
// auto-hide when their data is absent, so a generous category is safe to enable.
type wizardCategory struct {
	ID   string
	Name string
	Desc string
	// Segments lists the segment IDs this category contributes, in render
	// order. Only IDs that exist in the registry are emitted (so plugin-only
	// builds and future registry edits stay safe).
	Segments []string
	// DefaultOn marks the categories a newcomer gets when they accept the
	// opinionated defaults without touching anything.
	DefaultOn bool
}

// wizardCategories is the curated grouping presented in step 1. The order here
// is the order categories appear in the picker AND the order their segments are
// laid out, so the assembled config reads left-to-right top-to-bottom sensibly.
// IDs are validated against the registry at assembly time, so a typo here can
// never produce an invalid config — it just drops the unknown ID.
func wizardCategories() []wizardCategory {
	return []wizardCategory{
		{
			ID:        "project",
			Name:      "Project & directory",
			Desc:      "Where you are: current/project directory, session name, extra /add-dir roots",
			Segments:  []string{"session-name", "directory", "added-dirs"},
			DefaultOn: true,
		},
		{
			ID:        "git",
			Name:      "Git",
			Desc:      "Branch (with optional dirty/ahead-behind), stash count, lines changed",
			Segments:  []string{"git-branch", "git-stash", "lines-changed"},
			DefaultOn: true,
		},
		{
			ID:        "model",
			Name:      "Model & version",
			Desc:      "Model name and effort, output style, Claude Code version",
			Segments:  []string{"model", "output-style", "version"},
			DefaultOn: true,
		},
		{
			ID:        "cost",
			Name:      "Cost & tokens",
			Desc:      "Session spend, burn rate ($/h), token counts, cache hit %",
			Segments:  []string{"cost", "cost-rate", "tokens", "cache-percent"},
			DefaultOn: true,
		},
		{
			ID:        "context",
			Name:      "Context & limits",
			Desc:      "Context-window bar with time-to-compact, 5h and 7d quota bars",
			Segments:  []string{"context-window", "rate-limit-5h", "rate-limit-7d"},
			DefaultOn: true,
		},
		{
			ID:        "time",
			Name:      "Time",
			Desc:      "Elapsed session duration",
			Segments:  []string{"duration"},
			DefaultOn: false,
		},
		{
			ID:        "editor",
			Name:      "Editor & agents",
			Desc:      "Vim mode, agent name/state, sandbox status, plan tier",
			Segments:  []string{"vim-mode", "agent-name", "agent-state", "sandbox", "plan-tier"},
			DefaultOn: false,
		},
	}
}

// wizardDensity controls how many physical lines the assembled layout uses and
// how segments distribute across them.
type wizardDensity int

const (
	// densityCompact packs everything onto a single line — the terminal soft
	// wraps if it overflows. Quietest footprint.
	densityCompact wizardDensity = iota
	// densityBalanced is the opinionated default: a status/identity line, a
	// model/cost line, and a bars line, matching the natural segment lines.
	densityBalanced
	// densitySpacious spreads clusters across more lines for a roomy dashboard.
	densitySpacious
)

type wizardDensityInfo struct {
	Density wizardDensity
	Name    string
	Desc    string
	// Lines is how many physical lines this density targets (for the label).
	Lines int
}

func wizardDensities() []wizardDensityInfo {
	return []wizardDensityInfo{
		{Density: densityCompact, Name: "Compact", Desc: "One line — quiet; the terminal wraps if it overflows", Lines: 1},
		{Density: densityBalanced, Name: "Balanced", Desc: "Three lines — status, model/cost, and progress bars", Lines: 3},
		{Density: densitySpacious, Name: "Spacious", Desc: "Up to four lines — a roomy dashboard with bars spread out", Lines: 4},
	}
}

func densityInfo(d wizardDensity) wizardDensityInfo {
	for _, di := range wizardDensities() {
		if di.Density == d {
			return di
		}
	}
	return wizardDensities()[1]
}

// wizardChoices is the complete set of decisions the wizard collects. It is the
// sole input to assembleWizardConfig, which makes the assembly trivially
// testable.
type wizardChoices struct {
	// Categories is the set of enabled category IDs.
	Categories map[string]bool
	Density    wizardDensity
	// Theme is a theme ID ("" / "classic" = the default look).
	Theme string
	// GitStatus opts the git-branch segment into rich dirty/ahead-behind
	// status — the one high-value per-segment tweak worth surfacing in a
	// newcomer flow.
	GitStatus bool
}

// defaultWizardChoices is the opinionated starting point: the DefaultOn
// categories, balanced density, classic theme. A newcomer who presses through
// without changing anything lands here.
func defaultWizardChoices() wizardChoices {
	cats := map[string]bool{}
	for _, c := range wizardCategories() {
		if c.DefaultOn {
			cats[c.ID] = true
		}
	}
	return wizardChoices{
		Categories: cats,
		Density:    densityBalanced,
		Theme:      "",
		GitStatus:  true,
	}
}

// wizardLineFor maps a segment's natural line to the physical line for the
// chosen density. Compact collapses everything to line 1; balanced keeps the
// natural 1/2/3 clustering; spacious pushes the two rate-limit bars to their
// own line so the dashboard breathes.
func wizardLineFor(segID string, naturalLine int, d wizardDensity) int {
	switch d {
	case densityCompact:
		return 1
	case densitySpacious:
		switch segID {
		case "rate-limit-5h", "rate-limit-7d":
			return 4
		}
		return naturalLine
	default: // densityBalanced
		return naturalLine
	}
}

// assembleWizardConfig turns high-level choices into a full config that
// round-trips through the normal save path. It starts from the config that was
// loaded when the wizard launched, then mutates only the fields the wizard
// controls: theme, segment list/density, and the git-status toggle. Existing
// plugins, custom colors, style, update/release_notes settings, color_depth,
// reflow, and unrelated per-segment settings are preserved.
//
// registry is normally registeredSegments; it is a parameter so tests can pass
// a deterministic set without depending on plugin registration.
func assembleWizardConfig(base config, choices wizardChoices, registry []segmentInfo) config {
	known := make(map[string]segmentInfo, len(registry))
	for _, s := range registry {
		known[s.id] = s
	}

	cfg := copyWizardConfigBase(base)
	cfg.Theme = normalizeWizardTheme(choices.Theme)

	// Classify which segments belong to wizard categories and which categories
	// are selected, so we can add/remove only the segments the wizard controls.
	selectedSeg := map[string]bool{}
	categorySeg := map[string]bool{}
	for _, cat := range wizardCategories() {
		for _, id := range cat.Segments {
			categorySeg[id] = true
			if choices.Categories[cat.ID] {
				selectedSeg[id] = true
			}
		}
	}

	var segs []string
	switch {
	case base.Segments == nil:
		// No explicit segment list: build the opinionated wizard layout from the
		// selected categories, then auto-append plugin segments exactly like
		// mergeWithDefaults does when segments is absent.
		seen := map[string]bool{}
		for _, cat := range wizardCategories() {
			if !choices.Categories[cat.ID] {
				continue
			}
			for _, id := range cat.Segments {
				if seen[id] {
					continue
				}
				if _, ok := known[id]; !ok {
					continue
				}
				seen[id] = true
				segs = append(segs, id)
			}
		}
		inSegs := map[string]bool{}
		for _, id := range segs {
			inSegs[id] = true
		}
		for _, p := range base.Plugins {
			if len(p.Fields) > 0 {
				for _, f := range p.Fields {
					if f.ID != "" && !inSegs[f.ID] {
						inSegs[f.ID] = true
						segs = append(segs, f.ID)
					}
				}
			} else if p.ID != "" && !inSegs[p.ID] {
				inSegs[p.ID] = true
				segs = append(segs, p.ID)
			}
		}
	case len(base.Segments) == 0:
		// Explicit "hide everything" survives untouched.
		segs = []string{}
	default:
		// Preserve plugin/custom segments that are not part of any category,
		// then append the selected category segments in category order. This
		// keeps plugin segment IDs and user-defined custom segments while
		// letting the wizard add/remove its built-in categories. The update
		// segment is always appended at the end below, so drop any existing
		// placement to keep the output stable.
		seen := map[string]bool{}
		for _, id := range base.Segments {
			if categorySeg[id] || id == "update" {
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			segs = append(segs, id)
		}
		for _, cat := range wizardCategories() {
			if !choices.Categories[cat.ID] {
				continue
			}
			for _, id := range cat.Segments {
				if seen[id] {
					continue
				}
				if _, ok := known[id]; !ok {
					continue
				}
				seen[id] = true
				segs = append(segs, id)
			}
		}
	}

	// Always offer the update notice when anything else is shown — it
	// self-hides when no update is pending, so it costs a newcomer nothing.
	if len(segs) > 0 {
		if _, ok := known["update"]; ok && !hasSeg(segs, "update") {
			segs = append(segs, "update")
		}
	}

	// An empty selection means "hide everything"; preserve that intent as a
	// non-nil empty slice so it round-trips (nil would mean "defaults").
	if segs == nil {
		segs = []string{}
	}
	cfg.Segments = segs

	// Apply density line mapping on top of any existing line overrides.
	lines := map[string]int{}
	if base.Lines != nil {
		for k, v := range base.Lines {
			lines[k] = v
		}
	}
	for _, id := range segs {
		info, ok := known[id]
		if !ok {
			continue
		}
		line := wizardLineFor(id, info.line, choices.Density)
		if line != info.line {
			lines[id] = line
		} else {
			delete(lines, id)
		}
	}
	if len(lines) == 0 {
		lines = nil
	}
	cfg.Lines = lines

	// The one surfaced per-segment tweak: rich git status. Apply it through
	// the same prune cycle the editor uses so only non-default keys persist,
	// while leaving unrelated per-segment settings untouched.
	if hasSeg(segs, "git-branch") {
		if seg, ok := known["git-branch"]; ok {
			s := settingsFor(cfg, seg)
			if choices.GitStatus {
				s["git_status"] = true
			} else {
				s["git_status"] = false
			}
			setSegmentSettings(&cfg, "git-branch", pruneSettings(seg, s))
		}
	}

	// Normalize exactly like a loaded config so the wizard output is
	// indistinguishable from a hand-edited file.
	validateConfig(&cfg)
	return cfg
}

// copyWizardConfigBase returns a shallow copy of base with a deep copy of
// Settings so the wizard can mutate per-segment overrides without changing the
// caller's config.
func copyWizardConfigBase(base config) config {
	cfg := base
	if base.Settings != nil {
		cfg.Settings = make(map[string]map[string]any, len(base.Settings))
		for segID, vals := range base.Settings {
			inner := make(map[string]any, len(vals))
			for k, v := range vals {
				inner[k] = v
			}
			cfg.Settings[segID] = inner
		}
	}
	return cfg
}

func hasSeg(segs []string, id string) bool {
	for _, s := range segs {
		if s == id {
			return true
		}
	}
	return false
}

// normalizeWizardTheme maps the picker's "classic" sentinel to the empty
// theme string the config uses for the default look, mirroring the theme
// picker's behavior.
func normalizeWizardTheme(id string) string {
	if id == "classic" || id == "original" {
		return ""
	}
	return id
}
