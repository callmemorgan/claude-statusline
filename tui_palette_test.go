package main

import "testing"

func TestIsPaletteOverlayPage(t *testing.T) {
	overlayPages := []string{
		"palette",
		"prompt",
		"help",
		"confirm",
		"palquit",
		"colorpicker",
		"themepicker",
		"presetpicker",
		"picker:foo",
	}
	for _, name := range overlayPages {
		if !isPaletteOverlayPage(name) {
			t.Errorf("expected %q to be treated as an overlay page", name)
		}
	}

	nonOverlayPages := []string{"home", "unknown", ""}
	for _, name := range nonOverlayPages {
		if isPaletteOverlayPage(name) {
			t.Errorf("expected %q NOT to be treated as an overlay page", name)
		}
	}
}
