package payload

import (
	"encoding/json"
	"testing"
	"time"
)

func TestModelClassWindowsPreferDirectFields(t *testing.T) {
	pctF, pctS, pctO := 67.0, 22.0, 8.0
	scoped := 99.0
	r := RateLimits{
		SevenDayOverageIncluded: LimitWindow{UsedPercentage: &pctF},
		SevenDaySonnet:          LimitWindow{UsedPercentage: &pctS},
		SevenDayOpus:            LimitWindow{UsedPercentage: &pctO},
		ModelScoped: []ModelScopedLimit{
			{DisplayName: "Fable", UsedPercentage: &scoped},
			{DisplayName: "Sonnet", UsedPercentage: &scoped},
			{DisplayName: "Opus", UsedPercentage: &scoped},
		},
	}
	if got := *r.Fable().UsedPercentage; got != 67 {
		t.Errorf("Fable() = %v, want 67 (direct field)", got)
	}
	if got := *r.Sonnet().UsedPercentage; got != 22 {
		t.Errorf("Sonnet() = %v, want 22", got)
	}
	if got := *r.Opus().UsedPercentage; got != 8 {
		t.Errorf("Opus() = %v, want 8", got)
	}
}

func TestModelClassWindowsFallBackToModelScoped(t *testing.T) {
	util := 0.42
	resetISO := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"model_scoped": [
			{"display_name": "Fable", "utilization": 0.42, "resets_at": "2026-07-10T12:00:00Z"},
			{"display_name": "Claude Sonnet", "used_percentage": 33, "resets_at": 1752148800}
		]
	}`)
	var r RateLimits
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	f := r.Fable()
	if f.UsedPercentage == nil || *f.UsedPercentage != util*100 {
		t.Errorf("Fable from utilization = %v, want 42", f.UsedPercentage)
	}
	if f.ResetsAt == nil || *f.ResetsAt != resetISO.Unix() {
		t.Errorf("Fable resets_at = %v, want %d", f.ResetsAt, resetISO.Unix())
	}
	s := r.Sonnet()
	if s.UsedPercentage == nil || *s.UsedPercentage != 33 {
		t.Errorf("Sonnet from used_percentage = %v, want 33", s.UsedPercentage)
	}
	if s.ResetsAt == nil || *s.ResetsAt != 1752148800 {
		t.Errorf("Sonnet resets_at = %v, want 1752148800", s.ResetsAt)
	}
	if r.Opus().UsedPercentage != nil {
		t.Errorf("Opus should be empty without data, got %v", r.Opus())
	}
}

func TestParsePayloadModelClassFields(t *testing.T) {
	raw := []byte(`{
		"model": {"display_name": "Claude Fable 5"},
		"workspace": {"current_dir": "~"},
		"rate_limits": {
			"five_hour": {"used_percentage": 10, "resets_at": 1},
			"seven_day_overage_included": {"used_percentage": 55, "resets_at": 2},
			"seven_day_sonnet": {"used_percentage": 12, "resets_at": 3}
		}
	}`)
	p := ParsePayload(raw)
	if p.RateLimits.Fable().UsedPercentage == nil || *p.RateLimits.Fable().UsedPercentage != 55 {
		t.Errorf("Fable = %v", p.RateLimits.Fable())
	}
	if p.RateLimits.Sonnet().UsedPercentage == nil || *p.RateLimits.Sonnet().UsedPercentage != 12 {
		t.Errorf("Sonnet = %v", p.RateLimits.Sonnet())
	}
	if p.RateLimits.Opus().UsedPercentage != nil {
		t.Errorf("Opus should be absent, got %v", p.RateLimits.Opus())
	}
}
