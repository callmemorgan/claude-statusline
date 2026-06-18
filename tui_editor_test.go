package main

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestByteOffsetMultibyte(t *testing.T) {
	// Two lines: first line has a multibyte rune, second line is ASCII.
	// The byte offset of the end of the second line must account for the
	// extra byte in "é" so tview.TextArea.Replace lands in the right place.
	text := "café\ngit-bra"
	off := byteOffset(text, 1, 7) // end of "git-bra" (7 runes)
	want := len("café\n") + len("git-bra")
	if off != want {
		t.Errorf("byteOffset: got %d want %d", off, want)
	}

	// Cursor inside the multibyte line: column 3 runes ("caf") is 3 bytes;
	// column 4 runes ("café") is 5 bytes.
	if got := byteOffset(text, 0, 3); got != 3 {
		t.Errorf("byteOffset inside multibyte line: got %d want 3", got)
	}
	if got := byteOffset(text, 0, 4); got != 5 {
		t.Errorf("byteOffset after multibyte rune: got %d want 5", got)
	}
}

// TestEditorTabCompletionMultibyte checks that inserting a completion after
// non-ASCII text on a previous line replaces the correct byte range. Before
// the byte-offset fix, the offset was computed in runes, so the replacement
// would land in the middle of the multibyte rune or the wrong line.
func TestEditorTabCompletionMultibyte(t *testing.T) {
	initSegments(nil)
	editor := tview.NewTextArea().
		SetText("café\ngit-bra", true)

	insertCompletion(editor, "git-bra", "git-branch")

	got := editor.GetText()
	want := "café\ngit-branch"
	if got != want {
		t.Errorf("completion after multibyte text:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestEditorTabCompletionMultibytePrefix exercises a completion where the
// partial word itself contains a multibyte rune, ensuring the start offset is
// computed in bytes.
func TestEditorTabCompletionMultibytePrefix(t *testing.T) {
	initSegments(nil)
	editor := tview.NewTextArea().
		SetText("cost[colör", true)

	insertCompletion(editor, "cost[colör", "color")

	got := editor.GetText()
	want := "cost[color"
	if got != want {
		t.Errorf("completion with multibyte partial:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestCursorPrefixMultibyte(t *testing.T) {
	text := "café[di"
	// cursorPrefix is called with row/column from tview, where column is a
	// rune index. Here we just check the helper returns the correct prefix.
	prefix := cursorPrefixFromLine(text, 6) // after "café[d"
	if !strings.HasSuffix(prefix, "café[d") {
		t.Errorf("cursorPrefix: got %q want suffix %q", prefix, "café[d")
	}
}

// cursorPrefixFromLine is a test-only helper that mirrors cursorPrefix's
// rune-based slicing without needing a tview.TextArea.
func cursorPrefixFromLine(line string, col int) string {
	r := []rune(line)
	if col > len(r) {
		col = len(r)
	}
	return string(r[:col])
}
