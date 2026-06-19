package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/callmemorgan/claude-statusline/internal/config"
	"github.com/callmemorgan/claude-statusline/internal/payload"
)

// ─── Debug Schema ────────────────────────────────────────────────────

func printDebugSchema(raw []byte, p payload.Payload) {
	tool := "claude-code"
	if p.Product == "antigravity" {
		tool = "agy"
	}
	fmt.Printf("=== payload source: %s ===\n\n", tool)

	// Parse raw JSON into a map to check which keys are actually present.
	var raw2 map[string]json.RawMessage
	_ = json.Unmarshal(raw, &raw2)
	present := func(key string) string {
		if _, ok := raw2[key]; ok {
			return "✓"
		}
		return "✗"
	}

	type row struct{ field, claude, agy, got string }
	rows := []row{
		// Claude Code fields
		{"session_name", "✓", "✗", present("session_name")},
		{"cost", "✓", "✗", present("cost")},
		{"rate_limits", "✓", "✗", present("rate_limits")},
		{"agent.name", "✓", "✗", func() string {
			if _, ok := raw2["agent"]; ok {
				return "✓"
			}
			return "✗"
		}()},
		{"worktree", "✓", "✗", present("worktree")},
		{"vim", "✓", "✗", present("vim")},
		{"effort", "✓", "✗", present("effort")},
		// agy fields
		{"conversation_id", "✗", "✓", present("conversation_id")},
		{"product", "✗", "✓", present("product")},
		{"agent_state", "✗", "✓", present("agent_state")},
		{"sandbox.enabled", "✗", "✓", present("sandbox")},
		{"artifact_count", "✗", "✓", present("artifact_count")},
		{"plan_tier", "✗", "✓", present("plan_tier")},
		// shared
		{"model", "✓", "✓", present("model")},
		{"workspace", "✓", "✓", present("workspace")},
		{"context_window", "✓", "✓", present("context_window")},
		{"version", "✓", "✓", present("version")},
	}

	fmt.Printf("%-22s  %-8s  %-8s  %-8s\n", "field", "claude", "agy", "got")
	fmt.Println(strings.Repeat("-", 54))
	for _, r := range rows {
		mismatch := ""
		if tool == "claude-code" && r.claude == "✓" && r.got == "✗" {
			mismatch = "  ← MISSING"
		}
		if tool == "agy" && r.agy == "✓" && r.got == "✗" {
			mismatch = "  ← MISSING"
		}
		fmt.Printf("%-22s  %-8s  %-8s  %-8s%s\n", r.field, r.claude, r.agy, r.got, mismatch)
	}

	fmt.Println()
	fmt.Printf("parsed values:\n")
	fmt.Printf("  product        = %q\n", p.Product)
	fmt.Printf("  session_name   = %q\n", p.SessionName)
	fmt.Printf("  conversation_id= %q\n", p.ConversationID)
	fmt.Printf("  model          = %q\n", p.Model.DisplayName)
	fmt.Printf("  workspace.cur  = %q\n", p.Workspace.CurrentDir)
	fmt.Printf("  workspace.proj = %q\n", p.Workspace.ProjectDir)
	fmt.Printf("  agent_state    = %q\n", p.AgentState)
	fmt.Printf("  plan_tier      = %q\n", p.PlanTier)
	fmt.Printf("  sandbox        = %v\n", p.Sandbox.Enabled)
	fmt.Printf("  artifact_count = %d\n", p.ArtifactCount)
	fmt.Printf("  cost_usd       = %.4f\n", p.Cost.TotalCostUSD)
	fmt.Printf("  vim_mode       = %q\n", p.Vim.Mode)
	fmt.Printf("  effort         = %q\n", p.Effort.Level)
}

// printConfigWarnings lists config validation warnings in --debug output.
func printConfigWarnings(warns []config.ConfigWarning) {
	if len(warns) == 0 {
		return
	}
	fmt.Println()
	fmt.Printf("config warnings (%s):\n", config.ConfigPath())
	for _, w := range warns {
		fmt.Printf("  ! %s\n", w)
	}
}
