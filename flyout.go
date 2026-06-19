package main

import (
	"time"

	"github.com/rivo/tview"

	"github.com/callmemorgan/claude-statusline/internal/payload"
)

// ─── Flyout Helpers ──────────────────────────────────────────────────
//
// The flyout panel is fully schema-driven: it renders whatever settingSpec
// list the selected segment declares (segmentInfo.settings). Only the two
// ephemeral actions — stress_test and sync_to_all — have bespoke handling.

// segmentSpecs returns the settings schema for a segment ID, or nil when the
// segment has no configurable settings (no flyout).
func segmentSpecs(segID string) []settingSpec {
	if s, ok := segmentByID(segID); ok {
		return s.settings
	}
	return nil
}

// progressBarSegmentIDs returns the segments that share bar settings via
// "Sync to all bars": any registered segment whose schema contains a
// bar_width setting. Adding a new bar segment automatically joins the group.
func progressBarSegmentIDs() []string {
	var ids []string
	for _, s := range registeredSegments {
		for _, sp := range s.settings {
			if sp.Key == "bar_width" {
				ids = append(ids, s.id)
				break
			}
		}
	}
	return ids
}

func ptrBool(v bool) *bool          { return &v }
func ptrFloat64(v float64) *float64 { return &v }

// stressTestActive tracks which flyout segments have stress-test preview
// enabled. Session-only by design: it is never persisted to the config, so
// reopening the TUI always starts with the animation off.
var stressTestActive = map[string]bool{}
var stressTestTimers = map[string]*time.Timer{}

func scheduleStressTick(app *tview.Application, segID string, updateFn func()) {
	stressTestTimers[segID] = time.AfterFunc(50*time.Millisecond, func() {
		app.QueueUpdateDraw(func() {
			if stressTestActive[segID] {
				updateFn()
				scheduleStressTick(app, segID, updateFn)
			}
		})
	})
}

func stopStressTest(segID string) {
	delete(stressTestActive, segID)
	if t, ok := stressTestTimers[segID]; ok {
		t.Stop()
		delete(stressTestTimers, segID)
	}
}

// flyoutValueStr renders the current value of a flyout row.
func flyoutValueStr(segID string, sp settingSpec, cfg config) string {
	switch sp.Key {
	case "stress_test":
		if stressTestActive[segID] {
			return "on"
		}
		return "off"
	case "sync_to_all":
		return ""
	}
	seg, ok := segmentByID(segID)
	if !ok {
		return ""
	}
	return settingsFor(cfg, seg).ValueString(sp)
}

func cycleOption(options []string, current string, delta int) string {
	idx := 0
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(options)) % len(options)
	return options[idx]
}

// applyFlyoutChange mutates one setting by kind: bools toggle, enums cycle by
// delta, ints step by delta (clamped to the spec bounds). The pruned result is
// written back to cfg.Settings. Ephemeral specs never touch the config.
func applyFlyoutChange(segID string, sp settingSpec, cfg *config, delta int) {
	if sp.Key == "stress_test" {
		stressTestActive[segID] = !stressTestActive[segID]
		return
	}
	seg, ok := segmentByID(segID)
	if !ok {
		return
	}
	s := settingsFor(*cfg, seg)
	switch sp.Kind {
	case kindBool:
		s[sp.Key] = !s.Bool(sp.Key)
	case kindEnum, kindColor:
		s[sp.Key] = cycleOption(sp.Options, s.Str(sp.Key), delta)
	case kindInt:
		v := s.Int(sp.Key) + delta
		if v < sp.Min {
			v = sp.Min
		}
		if v > sp.Max {
			v = sp.Max
		}
		s[sp.Key] = v
	}
	setSegmentSettings(cfg, segID, pruneSettings(seg, s))
}

// setFlyoutValue writes one setting directly (used by the color picker).
func setFlyoutValue(segID string, sp settingSpec, cfg *config, value string) {
	seg, ok := segmentByID(segID)
	if !ok {
		return
	}
	s := settingsFor(*cfg, seg)
	s[sp.Key] = sp.coerce(value)
	setSegmentSettings(cfg, segID, pruneSettings(seg, s))
}

// syncSettingsToAllBars copies the source segment's settings to every other
// bar segment, pruned against each target's own schema (keys a target doesn't
// declare are dropped).
func syncSettingsToAllBars(cfg *config, sourceID string) {
	source, ok := segmentByID(sourceID)
	if !ok {
		return
	}
	s := settingsFor(*cfg, source)
	for _, target := range progressBarSegmentIDs() {
		if target == sourceID {
			continue
		}
		tseg, ok := segmentByID(target)
		if !ok {
			continue
		}
		setSegmentSettings(cfg, target, pruneSettings(tseg, s))
	}
}

// flyoutPreviewPayload returns a payload modified for the flyout preview.
// If stress test is active, it overrides the percentage fields so the preview
// animates through all threshold states; rate-limit resets wind down with the
// bar so the countdown animates too.
func flyoutPreviewPayload(segID string, base payload.Payload) payload.Payload {
	if !stressTestActive[segID] {
		return base
	}
	p := base
	pct := int((time.Now().UnixMilli() % 2000) * 100 / 2000)
	resetIn := func(windowSecs int64) *int64 {
		v := time.Now().Unix() + windowSecs*int64(100-pct)/100
		return &v
	}
	switch segID {
	case "context-window":
		p.Exceeds200K = ptrBool(pct > 80)
		p.ContextWindow.UsedPercentage = ptrFloat64(float64(pct))
	case "rate-limit-5h":
		p.RateLimits.FiveHour.UsedPercentage = ptrFloat64(float64(pct))
		p.RateLimits.FiveHour.ResetsAt = resetIn(5 * 3600)
	case "rate-limit-7d":
		p.RateLimits.SevenDay.UsedPercentage = ptrFloat64(float64(pct))
		p.RateLimits.SevenDay.ResetsAt = resetIn(7 * 24 * 3600)
	}
	return p
}

// demoPreviewPayload is the whole-statusline counterpart of the per-segment
// stress test, driving the TUI's demo mode (d): every percentage sweeps 0→100
// together, countdowns wind down with the bars, and cost and lines-changed
// grow, so threshold colors and width changes are visible across the line.
func demoPreviewPayload(base payload.Payload, now time.Time) payload.Payload {
	p := base
	pct := int((now.UnixMilli() % 5000) * 100 / 5000)
	resetIn := func(windowSecs int64) *int64 {
		v := now.Unix() + windowSecs*int64(100-pct)/100
		return &v
	}
	p.Exceeds200K = ptrBool(pct > 80)
	p.ContextWindow.UsedPercentage = ptrFloat64(float64(pct))
	p.RateLimits.FiveHour.UsedPercentage = ptrFloat64(float64(pct))
	p.RateLimits.FiveHour.ResetsAt = resetIn(5 * 3600)
	p.RateLimits.SevenDay.UsedPercentage = ptrFloat64(float64(pct))
	p.RateLimits.SevenDay.ResetsAt = resetIn(7 * 24 * 3600)
	p.Cost.TotalCostUSD = 2.5 * float64(pct) / 100
	p.Cost.TotalLinesAdded = int64(3 * pct)
	p.Cost.TotalLinesRemoved = int64(pct)
	return p
}
