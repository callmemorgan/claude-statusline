package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/payload"
)

func useTempStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return filepath.Join(dir, "claude-statusline", "sessions")
}

func costPayload(c float64) payload.Payload {
	var p payload.Payload
	p.Cost.TotalCostUSD = c
	return p
}

func TestSessionStateRecordSaveLoad(t *testing.T) {
	sessions := useTempStateDir(t)
	now := time.Unix(1750000000, 0)
	cfg := StateConfig{}

	st := LoadState(cfg, "abc-123", now)
	if st == nil {
		t.Fatal("state should load when enabled with a session id")
	}
	st.Record(costPayload(0.10), now)
	st.Record(costPayload(0.11), now.Add(2*time.Second)) // debounced
	st.Record(costPayload(0.20), now.Add(30*time.Second))
	if len(st.Samples) != 2 {
		t.Fatalf("debounce failed: %d samples", len(st.Samples))
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessions, "abc-123.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	st2 := LoadState(cfg, "abc-123", now.Add(time.Minute))
	if len(st2.Samples) != 2 {
		t.Errorf("round-trip lost samples: %d", len(st2.Samples))
	}
	if v, ok := st2.Series("cost").Last(); !ok || v != 0.20 {
		t.Errorf("cost series wrong: %v %v", v, ok)
	}
}

func TestSessionStateDisabledAndNil(t *testing.T) {
	useTempStateDir(t)
	off := false
	if st := LoadState(StateConfig{Enabled: &off}, "x", time.Now()); st != nil {
		t.Error("disabled state must return nil")
	}
	if st := LoadState(StateConfig{}, "", time.Now()); st != nil {
		t.Error("empty session id must return nil")
	}
	// nil receiver methods must be safe.
	var st *SessionState
	st.Record(payload.Payload{}, time.Now())
	if err := st.Save(); err != nil {
		t.Error("nil Save should be a no-op")
	}
	if s := st.Series("cost"); s != nil {
		t.Error("nil Series should be empty")
	}
}

func TestSessionStateCorruptFile(t *testing.T) {
	sessions := useTempStateDir(t)
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "bad.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := LoadState(StateConfig{}, "bad", time.Now())
	if st == nil || len(st.Samples) != 0 {
		t.Error("corrupt state should start fresh, not fail")
	}
}

func TestSessionStateLoadPrunesOldSamples(t *testing.T) {
	sessions := useTempStateDir(t)
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1750000000, 0)
	old := now.Add(-100 * time.Hour).Unix()
	recent := now.Add(-time.Hour).Unix()
	data, _ := json.Marshal(SessionState{SessionID: "s", Samples: []Sample{{T: old, Cost: 1}, {T: recent, Cost: 2}}})
	if err := os.WriteFile(filepath.Join(sessions, "s.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	st := LoadState(StateConfig{}, "s", now) // default 48h retention
	if len(st.Samples) != 1 || st.Samples[0].Cost != 2 {
		t.Errorf("expected only the recent sample, got %+v", st.Samples)
	}
}

func TestSessionStateSanitizesID(t *testing.T) {
	sessions := useTempStateDir(t)
	now := time.Unix(1750000000, 0)
	st := LoadState(StateConfig{}, "weird/../id with spaces", now)
	st.Record(costPayload(1), now)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sessions)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one state file, got %v err=%v", entries, err)
	}
	if name := entries[0].Name(); name != "weird-..-id-with-spaces.json" {
		t.Errorf("unexpected sanitized name %q", name)
	}
}

func TestSeriesRateDeltaProjection(t *testing.T) {
	now := time.Unix(1750000000, 0)
	st := &SessionState{}
	// Cost rises $0.50/h; rl5h rises 10%/h from 40%.
	for i := 0; i <= 6; i++ {
		ts := now.Add(time.Duration(i-6) * 10 * time.Minute)
		rl := 40.0 + float64(i-6+6)*10.0/6.0
		st.Samples = append(st.Samples, Sample{
			T:    ts.Unix(),
			Cost: 1.0 + 0.5*float64(i)/6.0,
			RL5h: &rl,
		})
	}

	rate, ok := st.Series("cost").Rate(time.Hour, now)
	if !ok || rate < 0.49 || rate > 0.51 {
		t.Errorf("cost rate = %v ok=%v, want ~0.5/h", rate, ok)
	}

	delta, ok := st.Series("rl5h").Delta(time.Hour, now)
	if !ok || delta < 9.9 || delta > 10.1 {
		t.Errorf("rl5h delta = %v ok=%v, want ~10", delta, ok)
	}

	// At 10%/h from 50%, hits 100% in ~5h.
	when, ok := st.Series("rl5h").ProjectWhen(100, time.Hour, now)
	if !ok {
		t.Fatal("projection should be available")
	}
	hours := when.Sub(now).Hours()
	if hours < 4.9 || hours > 5.1 {
		t.Errorf("ProjectWhen = %.2fh out, want ~5h", hours)
	}

	// Value projected 2h ahead: 50% + 2*10 = ~70%.
	at, ok := st.Series("rl5h").ProjectAt(now.Add(2*time.Hour), time.Hour, now)
	if !ok || at < 69 || at > 71 {
		t.Errorf("ProjectAt = %v ok=%v, want ~70", at, ok)
	}

	if span := st.Series("cost").Span(time.Hour, now); span != time.Hour {
		t.Errorf("Span = %v, want 1h", span)
	}

	// Too little data → no rate.
	short := &SessionState{Samples: []Sample{{T: now.Unix(), Cost: 1}}}
	if _, ok := short.Series("cost").Rate(time.Hour, now); ok {
		t.Error("rate with one point should not be ok")
	}

	// Falling series never projects a future crossing.
	falling := &SessionState{Samples: []Sample{
		{T: now.Add(-30 * time.Minute).Unix(), Cost: 2},
		{T: now.Unix(), Cost: 1},
	}}
	if _, ok := falling.Series("cost").ProjectWhen(3, time.Hour, now); ok {
		t.Error("negative slope must not project")
	}
}

func TestPruneStateDir(t *testing.T) {
	sessions := useTempStateDir(t)
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(sessions, "old.json")
	if err := os.WriteFile(oldFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(sessions, "fresh.json")
	if err := os.WriteFile(fresh, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	pruneStateDir(sessions, 48*time.Hour)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("stale session file should be deleted")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh session file should survive")
	}
}
