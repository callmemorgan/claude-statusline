package releasenotes

// ─── Release Notes Tests ──────────────────────────────────────────────

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/callmemorgan/claude-statusline/internal/config"

	"github.com/pelletier/go-toml/v2"

	"github.com/callmemorgan/claude-statusline/internal/ansi"
	"github.com/callmemorgan/claude-statusline/internal/palette"
	"github.com/callmemorgan/claude-statusline/internal/version"
)

// b makes a releaseBullet with default importance 0.
func b(text string) releaseBullet { return releaseBullet{Text: text} }

// ib makes a releaseBullet with the given importance.
func ib(text string, importance int32) releaseBullet {
	return releaseBullet{Text: text, Importance: importance}
}

// ─── parseChangelog ───────────────────────────────────────────────────

func TestParseChangelog(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []releaseNote
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name: "well-formed multi-section",
			input: `# Changelog

## v1.1.0 — 2026-06-12
- first
- second

## v1.0.0 — 2026-06-10
- only
`,
			want: []releaseNote{
				{Version: "1.1.0", Date: "2026-06-12", Bullets: []releaseBullet{b("first"), b("second")}},
				{Version: "1.0.0", Date: "2026-06-10", Bullets: []releaseBullet{b("only")}},
			},
		},
		{
			name: "missing date",
			input: `## v1.0.0
- x
`,
			want: []releaseNote{{Version: "1.0.0", Date: "", Bullets: []releaseBullet{b("x")}}},
		},
		{
			name: "stray prose between sections is ignored",
			input: `# header

intro paragraph
more prose

## v0.1.0 — 2025-01-01
- a
- b

in-between commentary

## v0.0.1
- c
`,
			want: []releaseNote{
				{Version: "0.1.0", Date: "2025-01-01", Bullets: []releaseBullet{b("a"), b("b")}},
				{Version: "0.0.1", Date: "", Bullets: []releaseBullet{b("c")}},
			},
		},
		{
			name: "bullets with - inside text",
			input: `## v1.0.0 — 2025-01-01
- feat: foo - bar
- plain bullet
`,
			want: []releaseNote{{
				Version: "1.0.0", Date: "2025-01-01",
				Bullets: []releaseBullet{b("feat: foo - bar"), b("plain bullet")},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseChangelog(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got %+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Version != tc.want[i].Version {
					t.Errorf("[%d] version = %q, want %q", i, got[i].Version, tc.want[i].Version)
				}
				if got[i].Date != tc.want[i].Date {
					t.Errorf("[%d] date = %q, want %q", i, got[i].Date, tc.want[i].Date)
				}
				if !slices.Equal(got[i].Bullets, tc.want[i].Bullets) {
					t.Errorf("[%d] bullets = %v, want %v", i, got[i].Bullets, tc.want[i].Bullets)
				}
			}
		})
	}
}

// ─── parseBullet ──────────────────────────────────────────────────────

func TestParseBullet(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want releaseBullet
		ok   bool
	}{
		{"plain", "plain bullet", b("plain bullet"), true},
		{"importance 5", "[5] important fix", ib("important fix", 5), true},
		{"pinned 99999", "[99999] app renamed", ib("app renamed", 99999), true},
		{"whitespace after marker", "[3]   spaced", ib("spaced", 3), true},
		{"missing marker", "ordinary", b("ordinary"), true},
		{"invalid marker", "[abc] not numeric", b("[abc] not numeric"), true},
		{"empty", "", releaseBullet{}, false},
		{"whitespace only", "   ", releaseBullet{}, false},
		{"zero marker", "[0] explicit zero", ib("explicit zero", 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseBullet(tc.raw)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ─── sortBulletsByImportance ──────────────────────────────────────────

func TestSortBulletsByImportance(t *testing.T) {
	in := []releaseBullet{
		b("low"),
		ib("high", 5),
		ib("medium", 3),
		ib("highest", 10),
		ib("also high", 5),
	}
	sortBulletsByImportance(in)
	want := []releaseBullet{
		ib("highest", 10),
		ib("high", 5),
		ib("also high", 5),
		ib("medium", 3),
		b("low"),
	}
	if !slices.Equal(in, want) {
		t.Errorf("got %v, want %v", in, want)
	}
}

// ─── releaseNotesBetween ──────────────────────────────────────────────

func TestReleaseNotesBetween(t *testing.T) {
	notes := []releaseNote{
		{Version: "1.2.0", Bullets: []releaseBullet{ib("1.2 pinned", 99999), b("1.2 ordinary")}},
		{Version: "1.1.0", Bullets: []releaseBullet{ib("1.1 high", 5), b("1.1 low")}},
		{Version: "1.0.0", Bullets: []releaseBullet{b("1.0 only")}},
	}
	t.Run("single version returns its bullets sorted", func(t *testing.T) {
		got := releaseNotesBetween(notes, "1.0.9", "1.1.0", 10)
		want := []releaseBullet{ib("1.1 high", 5), b("1.1 low")}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("multi-version span collects and sorts", func(t *testing.T) {
		got := releaseNotesBetween(notes, "1.0.0", "1.2.0", 10)
		want := []releaseBullet{
			ib("1.2 pinned", 99999),
			ib("1.1 high", 5),
			b("1.2 ordinary"),
			b("1.1 low"),
		}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("limit caps result", func(t *testing.T) {
		got := releaseNotesBetween(notes, "1.0.0", "1.2.0", 2)
		if len(got) != 2 || got[0].Importance != 99999 || got[1].Importance != 5 {
			t.Errorf("got %v, want top two", got)
		}
	})
	t.Run("non-upgrade falls back to target", func(t *testing.T) {
		got := releaseNotesBetween(notes, "1.2.0", "1.1.0", 10)
		want := notes[1].Bullets // target version's own bullets
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("unknown target returns nil", func(t *testing.T) {
		if got := releaseNotesBetween(notes, "1.0.0", "9.9.9", 10); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// ─── bulletDisplayText ────────────────────────────────────────────────

func TestBulletDisplayText(t *testing.T) {
	cases := []struct {
		name string
		b    releaseBullet
		want string
	}{
		{"ordinary", b("ordinary"), "ordinary"},
		{"importance 5", ib("five", 5), "five"},
		{"high threshold", ib("high", 99), "high"},
		{"pinned", ib("pinned", 100), "[PINNED] pinned"},
		{"pinned 99999", ib("renamed", 99999), "[PINNED] renamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bulletDisplayText(tc.b); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── announceLines ────────────────────────────────────────────────────

func TestAnnounceLines(t *testing.T) {
	emptyPalette := palette.Palette{}
	coloredPalette := palette.Palette{Purple: "\x1b[35m", Dim: "\x1b[2m", Rst: "\x1b[0m"}
	note := releaseNote{
		Version: "1.1.0",
		Date:    "2026-06-12",
		Bullets: []releaseBullet{b("first bullet"), b("second bullet"), b("third bullet")},
	}
	// noBudgets disables truncation; padding=1 matches the renderer's default.
	noBudgets := func(n int) []int { return nil }
	t.Run("n=1 compressed form", func(t *testing.T) {
		got := announceLines(note, 1, noBudgets(1), emptyPalette, 1)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if !strings.Contains(got[0], "v1.1.0") || !strings.Contains(got[0], "first bullet") {
			t.Errorf("missing version or first bullet: %q", got[0])
		}
		if strings.ContainsRune(got[0], 0x1b) {
			t.Errorf("empty palette should produce no escapes: %q", got[0])
		}
		if !strings.HasPrefix(got[0], " ") {
			t.Errorf("line should start with single-space pad, got %q", got[0])
		}
	})
	t.Run("n=3 header/bullet/bullet", func(t *testing.T) {
		got := announceLines(note, 3, noBudgets(3), emptyPalette, 1)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		if !strings.Contains(got[0], "v1.1.0") {
			t.Errorf("line 0 should mention version: %q", got[0])
		}
		if !strings.Contains(got[1], "first bullet") {
			t.Errorf("line 1 should be first bullet: %q", got[1])
		}
		if !strings.Contains(got[2], "second bullet") {
			t.Errorf("line 2 should be second bullet: %q", got[2])
		}
	})
	t.Run("n=8 with 3 bullets pads empties after them", func(t *testing.T) {
		got := announceLines(note, 8, noBudgets(8), emptyPalette, 1)
		if len(got) != 8 {
			t.Fatalf("len = %d, want 8", len(got))
		}
		// 1 header (0) + 3 bullets (1,2,3) + 4 empty pads (4,5,6,7).
		if !strings.Contains(got[1], "first bullet") {
			t.Errorf("line 1 should be first bullet, got %q", got[1])
		}
		// Pads should be empty visible content (just the pad space).
		for i := 4; i <= 7; i++ {
			if got[i] != " " {
				t.Errorf("line %d should be a single pad space, got %q", i, got[i])
			}
		}
	})
	t.Run("n=4 shows all bullets", func(t *testing.T) {
		got := announceLines(note, 4, noBudgets(4), emptyPalette, 1)
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4", len(got))
		}
		// header + 3 bullets
		if !strings.Contains(got[1], "first bullet") {
			t.Errorf("line 1 should be first bullet: %q", got[1])
		}
		if !strings.Contains(got[2], "second bullet") {
			t.Errorf("line 2 should be second bullet: %q", got[2])
		}
		if !strings.Contains(got[3], "third bullet") {
			t.Errorf("line 3 should be third bullet: %q", got[3])
		}
	})
	t.Run("long bullet truncated at budget", func(t *testing.T) {
		wide := releaseNote{
			Version: "1.1.0",
			Bullets: []releaseBullet{b(strings.Repeat("a", 200))},
		}
		// budgets[0] reserves timing suffix, budgets[1+] is safety-margin only.
		budgets := takeoverLineBudgets(40, 3, 1)
		got := announceLines(wide, 3, budgets, emptyPalette, 1)
		if ansi.VisibleWidth(got[1]) > 40 {
			t.Errorf("line 1 visible width = %d, want <= 40", ansi.VisibleWidth(got[1]))
		}
		if !strings.HasSuffix(got[1], "…") {
			t.Errorf("truncated line should end with ellipsis: %q", got[1])
		}
	})
	t.Run("empty palette output has no escapes", func(t *testing.T) {
		got := announceLines(note, 4, noBudgets(4), emptyPalette, 1)
		for i, l := range got {
			if strings.ContainsRune(l, 0x1b) {
				t.Errorf("line %d contains ANSI escape: %q", i, l)
			}
		}
	})
	t.Run("colored palette wraps header in accent", func(t *testing.T) {
		got := announceLines(note, 3, noBudgets(3), coloredPalette, 1)
		if !strings.Contains(got[0], "\x1b[35m") {
			t.Errorf("line 0 missing accent color: %q", got[0])
		}
	})
	t.Run("no bullets pads with empties", func(t *testing.T) {
		empty := releaseNote{Version: "1.1.0"}
		got := announceLines(empty, 2, noBudgets(2), emptyPalette, 1)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if !strings.Contains(got[0], "v1.1.0") {
			t.Errorf("line 0 should have version: %q", got[0])
		}
		if got[1] != " " {
			t.Errorf("line 1 should be a single pad space, got %q", got[1])
		}
	})
	t.Run("padding=3 indents all lines by 3", func(t *testing.T) {
		got := announceLines(note, 3, noBudgets(3), emptyPalette, 3)
		for i, l := range got {
			if !strings.HasPrefix(l, "   ") {
				t.Errorf("line %d should start with 3-space pad, got %q", i, l)
			}
		}
	})
	t.Run("padding=0 no leading indent", func(t *testing.T) {
		got := announceLines(note, 3, noBudgets(3), emptyPalette, 0)
		for i, l := range got {
			// Visible-width of the leading pad should be 0 — but the bullet
			// formatter adds its own internal space, so we just check no
			// extra indent was added by announceLines.
			if strings.HasPrefix(l, " ") {
				// Acceptable: bullet lines start with " •". Reject: a second
				// leading space.
				if strings.HasPrefix(l, "  ") {
					t.Errorf("line %d has double leading space, got %q", i, l)
				}
			}
		}
	})
	t.Run("pinned bullets sort to the front and show prefix", func(t *testing.T) {
		pinnedFirst := releaseNote{
			Version: "1.2.0",
			Bullets: []releaseBullet{
				b("ordinary"),
				ib("pinned", 100),
				ib("high", 5),
			},
		}
		got := announceLines(pinnedFirst, 3, noBudgets(3), emptyPalette, 1)
		if !strings.Contains(got[1], "[PINNED] pinned") {
			t.Errorf("line 1 should show pinned bullet first: %q", got[1])
		}
	})
}

// ─── takeoverLineBudgets ─────────────────────────────────────────────

func TestTakeoverLineBudgets(t *testing.T) {
	t.Run("no width returns nil", func(t *testing.T) {
		if got := takeoverLineBudgets(0, 3, 1); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
	t.Run("line 0 reserves timing suffix, others only safety margin + padding", func(t *testing.T) {
		budgets := takeoverLineBudgets(80, 3, 1)
		if len(budgets) != 3 {
			t.Fatalf("len = %d, want 3", len(budgets))
		}
		// line 0: 80 - 5 (safety) - 15 (timing) - 1 (pad) = 59
		// lines 1,2: 80 - 5 - 1 = 74
		if budgets[0] != 59 {
			t.Errorf("line 0 budget = %d, want 59", budgets[0])
		}
		if budgets[1] != 74 || budgets[2] != 74 {
			t.Errorf("lines 1,2 budgets = %d,%d, want 74,74", budgets[1], budgets[2])
		}
	})
	t.Run("narrow terminals get a floor of 10", func(t *testing.T) {
		budgets := takeoverLineBudgets(15, 2, 1)
		for i, b := range budgets {
			if b < 10 {
				t.Errorf("budget %d below floor at line %d", b, i)
			}
		}
	})
}

// ─── versionSeen round-trip ───────────────────────────────────────────

func TestVersionSeenRoundTrip(t *testing.T) {
	t.Run("save and load equality", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", dir)
		// versionSeenPath uses stateBaseDir() which joins XDG_STATE_HOME with
		// "claude-statusline". We need that to be created on save.
		v := versionSeen{Version: "1.1.0", FirstSeen: 1700000000}
		saveVersionSeen(v)
		got, ok := loadVersionSeen()
		if !ok {
			t.Fatal("loadVersionSeen returned ok=false after save")
		}
		if got != v {
			t.Errorf("got %+v, want %+v", got, v)
		}
	})
	t.Run("corrupt JSON returns ok=false", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", dir)
		if err := os.MkdirAll(filepath.Join(dir, "claude-statusline"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "claude-statusline", "last-version.json"), []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := loadVersionSeen(); ok {
			t.Error("ok=true on corrupt file")
		}
	})
	t.Run("missing file returns ok=false", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", dir)
		if _, ok := loadVersionSeen(); ok {
			t.Error("ok=true on missing file")
		}
	})
	t.Run("save creates directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "deep", "nested"))
		saveVersionSeen(versionSeen{Version: "1.1.0", FirstSeen: 1})
		if _, err := os.Stat(filepath.Join(dir, "deep", "nested", "claude-statusline", "last-version.json")); err != nil {
			t.Errorf("file not created: %v", err)
		}
	})
	t.Run("JSON shape matches spec", func(t *testing.T) {
		v := versionSeen{Version: "1.0.2", FirstSeen: 1718200000}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		if !strings.Contains(s, `"version":"1.0.2"`) {
			t.Errorf("missing version field: %s", s)
		}
		if !strings.Contains(s, `"first_seen":1718200000`) {
			t.Errorf("missing first_seen field: %s", s)
		}
	})
}

// ─── config: [release_notes] ──────────────────────────────────────────

func TestReleaseNotesConfigRoundTrip(t *testing.T) {
	t.Run("TOML round-trip", func(t *testing.T) {
		announce := false
		dur := 60
		loaded := config.Config{ReleaseNotes: config.ReleaseNotesConfig{Announce: &announce, DurationSeconds: &dur, MaxLines: int64(12)}}
		data, err := config.MarshalConfigTOML(loaded)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "[release_notes]") {
			t.Errorf("expected [release_notes] table in:\n%s", data)
		}
		var got config.Config
		if err := toml.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.ReleaseNotes.Announce == nil || *got.ReleaseNotes.Announce != false {
			t.Errorf("Announce not preserved: %+v", got.ReleaseNotes.Announce)
		}
		if got.ReleaseNotes.DurationSeconds == nil || *got.ReleaseNotes.DurationSeconds != 60 {
			t.Errorf("DurationSeconds not preserved: %+v", got.ReleaseNotes.DurationSeconds)
		}
		if got.ReleaseNotes.MaxLines == nil || got.ReleaseNotes.ResolvedMaxLines() != 12 {
			t.Errorf("MaxLines not preserved: %+v", got.ReleaseNotes.MaxLines)
		}
	})
	t.Run("config.MergeWithDefaults preserves", func(t *testing.T) {
		announce := false
		dur := 0
		loaded := config.Config{Segments: []string{}, ReleaseNotes: config.ReleaseNotesConfig{Announce: &announce, DurationSeconds: &dur, MaxLines: "status-line"}}
		got := config.MergeWithDefaults(loaded)
		if got.ReleaseNotes.Announce == nil || *got.ReleaseNotes.Announce != false {
			t.Errorf("Announce lost in merge: %+v", got.ReleaseNotes.Announce)
		}
		if got.ReleaseNotes.DurationSeconds == nil || *got.ReleaseNotes.DurationSeconds != 0 {
			t.Errorf("DurationSeconds lost in merge: %+v", got.ReleaseNotes.DurationSeconds)
		}
		if got.ReleaseNotes.MaxLines == nil || got.ReleaseNotes.ResolvedMaxLines() != config.SameAsStatusLineSentinel {
			t.Errorf("MaxLines lost in merge: %+v", got.ReleaseNotes.MaxLines)
		}
	})
	t.Run("out of range duration warns and resets", func(t *testing.T) {
		dur := 9999
		cfg := config.Config{ReleaseNotes: config.ReleaseNotesConfig{DurationSeconds: &dur}}
		warns := config.ValidateConfig(&cfg)
		found := false
		for _, w := range warns {
			if w.Path == "release_notes.duration_seconds" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected warning, got %+v", warns)
		}
		if cfg.ReleaseNotes.DurationSeconds != nil {
			t.Errorf("expected nil after reset, got %+v", *cfg.ReleaseNotes.DurationSeconds)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		var r config.ReleaseNotesConfig
		if !r.AnnounceOrDefault() {
			t.Error("AnnounceOrDefault() default should be true")
		}
		if r.Duration() != 25*time.Second {
			t.Errorf("duration() default = %v, want 25s", r.Duration())
		}
		if r.ResolvedMaxLines() != config.DefaultMaxLines {
			t.Errorf("resolvedMaxLines() default = %d, want %d", r.ResolvedMaxLines(), config.DefaultMaxLines)
		}
	})
}

// ─── resolvedMaxLines ─────────────────────────────────────────────────

func TestResolvedMaxLines(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"nil", nil, config.DefaultMaxLines},
		{"int64 10", int64(10), 10},
		{"int 0", 0, 0},
		{"status-line", "status-line", config.SameAsStatusLineSentinel},
		{"same-as-status-line", "same-as-status-line", config.SameAsStatusLineSentinel},
		{"statusline", "statusline", config.SameAsStatusLineSentinel},
		{"same-as-statusline", "same-as-statusline", config.SameAsStatusLineSentinel},
		{"uppercase", "STATUS-LINE", config.SameAsStatusLineSentinel},
		{"underscores", "same_as_status_line", config.SameAsStatusLineSentinel},
		{"spaces", "same as status line", config.SameAsStatusLineSentinel},
		{"invalid string", "nope", config.DefaultMaxLines},
		{"invalid type", 1.5, config.DefaultMaxLines},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := config.ReleaseNotesConfig{MaxLines: tc.val}
			if got := r.ResolvedMaxLines(); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// ─── config.ValidateConfig max_lines ─────────────────────────────────────────

func TestValidateMaxLines(t *testing.T) {
	t.Run("negative int warns and resets", func(t *testing.T) {
		cfg := config.Config{ReleaseNotes: config.ReleaseNotesConfig{MaxLines: int64(-5)}}
		warns := config.ValidateConfig(&cfg)
		found := false
		for _, w := range warns {
			if w.Path == "release_notes.max_lines" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected warning, got %+v", warns)
		}
		if cfg.ReleaseNotes.MaxLines != nil {
			t.Errorf("expected nil after reset, got %+v", cfg.ReleaseNotes.MaxLines)
		}
	})
	t.Run("invalid string warns and resets", func(t *testing.T) {
		cfg := config.Config{ReleaseNotes: config.ReleaseNotesConfig{MaxLines: "lots"}}
		warns := config.ValidateConfig(&cfg)
		found := false
		for _, w := range warns {
			if w.Path == "release_notes.max_lines" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected warning, got %+v", warns)
		}
		if cfg.ReleaseNotes.MaxLines != nil {
			t.Errorf("expected nil after reset, got %+v", cfg.ReleaseNotes.MaxLines)
		}
	})
	t.Run("valid symbolic accepted", func(t *testing.T) {
		cfg := config.Config{ReleaseNotes: config.ReleaseNotesConfig{MaxLines: "status-line"}}
		warns := config.ValidateConfig(&cfg)
		for _, w := range warns {
			if w.Path == "release_notes.max_lines" {
				t.Errorf("unexpected warning: %+v", w)
			}
		}
		if cfg.ReleaseNotes.MaxLines == nil {
			t.Error("expected MaxLines preserved")
		}
	})
}

// ─── findNote helper ──────────────────────────────────────────────────

func TestFindNote(t *testing.T) {
	notes := []releaseNote{
		{Version: "1.1.0", Bullets: []releaseBullet{b("a")}},
		{Version: "1.0.2", Bullets: []releaseBullet{b("b")}},
	}
	if n, ok := findNote(notes, "1.0.2"); !ok || n.Version != "1.0.2" {
		t.Errorf("expected to find 1.0.2, got %+v / %v", n, ok)
	}
	if _, ok := findNote(notes, "0.0.1"); ok {
		t.Error("found nonexistent version")
	}
}

// ─── Run selection ───────────────────────────────────────────────────

func TestSelectReleaseNote(t *testing.T) {
	notes := []releaseNote{
		{Version: "1.1.0", Bullets: []releaseBullet{b("a")}},
		{Version: "1.0.2", Bullets: []releaseBullet{b("b")}},
		{Version: "0.9.0", Bullets: []releaseBullet{b("c")}},
	}
	t.Run("no args picks current", func(t *testing.T) {
		mode, target, fallback, missing := selectReleaseNote(notes, "1.0.2", nil)
		if mode != "one" || target.Version != "1.0.2" || fallback != nil || len(missing) > 0 {
			t.Errorf("got mode=%q target=%+v fallback=%v missing=%v", mode, target, fallback, missing)
		}
	})
	t.Run("no args falls back to newest when current missing", func(t *testing.T) {
		mode, _, fallback, missing := selectReleaseNote(notes, "9.9.9", nil)
		if mode != "one" || fallback == nil || fallback.Version != "1.1.0" {
			t.Errorf("got mode=%q fallback=%+v", mode, fallback)
		}
		if len(missing) == 0 || !strings.Contains(missing[0], "no notes for v9.9.9") {
			t.Errorf("expected missing message, got %v", missing)
		}
	})
	t.Run("no args on empty notes signals error", func(t *testing.T) {
		mode, _, _, missing := selectReleaseNote(nil, "1.0.0", nil)
		if mode != "" {
			t.Errorf("expected empty mode, got %q", mode)
		}
		if len(missing) == 0 || !strings.Contains(missing[0], "empty or malformed") {
			t.Errorf("expected empty-notes message, got %v", missing)
		}
	})
	t.Run("arg picks matching version", func(t *testing.T) {
		mode, target, fallback, missing := selectReleaseNote(notes, "9.9.9", []string{"v0.9.0"})
		if mode != "one" || target.Version != "0.9.0" || fallback != nil || len(missing) > 0 {
			t.Errorf("got mode=%q target=%+v fallback=%v missing=%v", mode, target, fallback, missing)
		}
	})
	t.Run("arg without v prefix works", func(t *testing.T) {
		_, target, _, _ := selectReleaseNote(notes, "9.9.9", []string{"1.0.2"})
		if target.Version != "1.0.2" {
			t.Errorf("target=%+v, want 1.0.2", target)
		}
	})
	t.Run("arg unknown signals error with known-versions hint", func(t *testing.T) {
		mode, _, _, missing := selectReleaseNote(notes, "9.9.9", []string{"v9.9.9"})
		if mode != "" {
			t.Errorf("expected empty mode, got %q", mode)
		}
		if len(missing) == 0 {
			t.Fatal("expected missing message")
		}
		s := missing[0]
		if !strings.Contains(s, "v9.9.9") {
			t.Errorf("expected v9.9.9 in message: %q", s)
		}
		for _, v := range []string{"v1.1.0", "v1.0.2", "v0.9.0"} {
			if !strings.Contains(s, v) {
				t.Errorf("expected %q in known list: %q", v, s)
			}
		}
	})
	t.Run("--all returns all mode", func(t *testing.T) {
		mode, _, _, _ := selectReleaseNote(notes, "9.9.9", []string{"--all"})
		if mode != "all" {
			t.Errorf("mode=%q, want all", mode)
		}
	})
	t.Run("all (no dashes) also returns all mode", func(t *testing.T) {
		mode, _, _, _ := selectReleaseNote(notes, "9.9.9", []string{"all"})
		if mode != "all" {
			t.Errorf("mode=%q, want all", mode)
		}
	})
	t.Run("--all on empty notes signals error", func(t *testing.T) {
		mode, _, _, missing := selectReleaseNote(nil, "1.0.0", []string{"--all"})
		if mode != "" {
			t.Errorf("expected empty mode, got %q", mode)
		}
		if len(missing) == 0 {
			t.Error("expected missing message for --all on empty notes")
		}
	})
	t.Run("range arg returns cross-version summary", func(t *testing.T) {
		mode, target, fallback, missing := selectReleaseNote(notes, "9.9.9", []string{"v0.9.0..v1.1.0"})
		if mode != "range" || fallback != nil || len(missing) > 0 {
			t.Errorf("got mode=%q target=%+v fallback=%v missing=%v", mode, target, fallback, missing)
		}
		if target.Version != "0.9.0..1.1.0" {
			t.Errorf("target.Version = %q, want 0.9.0..1.1.0", target.Version)
		}
		if len(target.Bullets) == 0 {
			t.Error("expected bullets in range summary")
		}
	})
	t.Run("range arg without v prefix works", func(t *testing.T) {
		mode, target, _, _ := selectReleaseNote(notes, "9.9.9", []string{"1.0.2..1.1.0"})
		if mode != "range" || target.Version != "1.0.2..1.1.0" {
			t.Errorf("got mode=%q target=%+v", mode, target)
		}
	})
	t.Run("range arg with unknown end signals error", func(t *testing.T) {
		mode, _, _, missing := selectReleaseNote(notes, "9.9.9", []string{"v1.0.0..v9.9.9"})
		if mode != "" {
			t.Errorf("expected empty mode, got %q", mode)
		}
		if len(missing) == 0 || !strings.Contains(missing[0], "v9.9.9") {
			t.Errorf("expected unknown-end message, got %v", missing)
		}
	})
}

func TestAnnounceDecision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	announceFalse := false
	durZero := 0
	announceTrue := true
	durPtr := func(d int) *int { return &d }

	cases := []struct {
		name     string
		prev     versionSeen
		prevOK   bool
		current  string
		cfg      config.ReleaseNotesConfig
		now      time.Time
		wantShow bool
		wantNext versionSeen
	}{
		{
			name:     "dev build never shows and never persists",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "dev",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "empty current never shows",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "dirty dev build (v1.0.2+dirty) never shows",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "1.0.2+dirty",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "Go pseudo-version never shows (go install @commit)",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "0.1.0-0.20260612120000-abc123abc123",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "fresh install persists with no window anchor and does not show",
			prev:     versionSeen{},
			prevOK:   false,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{Version: "1.1.0", FirstSeen: 0},
		},
		{
			name:     "second render after fresh install does not announce",
			prev:     versionSeen{Version: "1.1.0", FirstSeen: 0},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "upgrade shows and persists new anchor",
			prev:     versionSeen{Version: "1.0.2", FirstSeen: now.Unix() - 1000},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: true,
			wantNext: versionSeen{Version: "1.1.0", FirstSeen: now.Unix()},
		},
		{
			name:     "within window same version shows without persisting",
			prev:     versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: true,
			wantNext: versionSeen{},
		},
		{
			name:     "expired window does not show or persist",
			prev:     versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 60},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "announce=false suppresses but still persists version on change",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceFalse, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 10},
		},
		{
			name:     "duration_seconds=0 suppresses but still persists version on change",
			prev:     versionSeen{Version: "1.0.0", FirstSeen: now.Unix() - 10},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: &durZero},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 10},
		},
		{
			name:     "announce=false same version noop",
			prev:     versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 5},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceFalse, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
		{
			name:     "re-enable after disabled upgrade does not fire (stale FirstSeen)",
			prev:     versionSeen{Version: "1.1.0", FirstSeen: now.Unix() - 1000},
			prevOK:   true,
			current:  "1.1.0",
			cfg:      config.ReleaseNotesConfig{Announce: &announceTrue, DurationSeconds: durPtr(25)},
			now:      now,
			wantShow: false,
			wantNext: versionSeen{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			show, next := announceDecision(tc.prev, tc.prevOK, tc.current, tc.cfg, tc.now)
			if show != tc.wantShow {
				t.Errorf("show = %v, want %v", show, tc.wantShow)
			}
			if next != tc.wantNext {
				t.Errorf("next = %+v, want %+v", next, tc.wantNext)
			}
		})
	}
}

// ─── MaybeTakeover ─────────────────────────────────────────────

// TestMaybeReleaseTakeoverSaveFailure locks the degrade path: when the new
// version state can't be persisted, the takeover is suppressed (otherwise an
// unwritable state dir would replay the announcement on every render).
func TestMaybeReleaseTakeoverSaveFailure(t *testing.T) {
	oldVersion := version.Version
	version.Version = "1.1.0"
	t.Cleanup(func() { version.Version = oldVersion })

	lines := []string{"normal statusline"}
	now := time.Unix(1_750_000_000, 0)

	setupPrev := func(t *testing.T) string {
		t.Helper()
		base := t.TempDir()
		t.Setenv("XDG_STATE_HOME", base)
		dir := filepath.Join(base, "claude-statusline")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(versionSeen{Version: "1.0.2", FirstSeen: 0})
		if err := os.WriteFile(filepath.Join(dir, "last-version.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("upgrade with writable state fires", func(t *testing.T) {
		setupPrev(t)
		// Pin to the statusline line count so the save-failure assertion stays
		// focused on whether the takeover fires at all.
		got := MaybeTakeover(config.ReleaseNotesConfig{MaxLines: "status-line"}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != len(lines) || got[0] == lines[0] {
			t.Fatalf("takeover did not fire: %q", got)
		}
		if !strings.Contains(got[0], "v1.1.0") {
			t.Errorf("takeover line missing version: %q", got[0])
		}
	})

	t.Run("upgrade with unwritable state dir is suppressed", func(t *testing.T) {
		dir := setupPrev(t)
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		got := MaybeTakeover(config.ReleaseNotesConfig{}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != 1 || got[0] != lines[0] {
			t.Fatalf("takeover not suppressed on save failure: %q", got)
		}
	})
}

// TestMaybeReleaseTakeoverMaxLines verifies that max_lines expands (or limits)
// the takeover line count relative to the statusline's own line count.
func TestMaybeReleaseTakeoverMaxLines(t *testing.T) {
	oldVersion := version.Version
	version.Version = "1.5.0"
	t.Cleanup(func() { version.Version = oldVersion })

	lines := []string{"line1", "line2"} // statusline uses 2 lines
	now := time.Unix(1_750_000_000, 0)

	setupPrev := func(t *testing.T) {
		t.Helper()
		base := t.TempDir()
		t.Setenv("XDG_STATE_HOME", base)
		dir := filepath.Join(base, "claude-statusline")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(versionSeen{Version: "1.0.0", FirstSeen: 0})
		if err := os.WriteFile(filepath.Join(dir, "last-version.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("default expands to 10 lines", func(t *testing.T) {
		setupPrev(t)
		got := MaybeTakeover(config.ReleaseNotesConfig{}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != 10 {
			t.Errorf("len = %d, want 10", len(got))
		}
	})
	t.Run("numeric max_lines honored", func(t *testing.T) {
		setupPrev(t)
		got := MaybeTakeover(config.ReleaseNotesConfig{MaxLines: int64(5)}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != 5 {
			t.Errorf("len = %d, want 5", len(got))
		}
	})
	t.Run("status-line keeps statusline line count", func(t *testing.T) {
		setupPrev(t)
		got := MaybeTakeover(config.ReleaseNotesConfig{MaxLines: "status-line"}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != len(lines) {
			t.Errorf("len = %d, want %d", len(got), len(lines))
		}
	})
	t.Run("max_lines 0 means same as statusline", func(t *testing.T) {
		setupPrev(t)
		got := MaybeTakeover(config.ReleaseNotesConfig{MaxLines: int64(0)}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != len(lines) {
			t.Errorf("len = %d, want %d", len(got), len(lines))
		}
	})
	t.Run("max_lines never shrinks below statusline", func(t *testing.T) {
		setupPrev(t)
		got := MaybeTakeover(config.ReleaseNotesConfig{MaxLines: int64(1)}, lines, palette.Palette{}, 80, 1, now)
		if len(got) != len(lines) {
			t.Errorf("len = %d, want %d (should not shrink)", len(got), len(lines))
		}
	})
}
