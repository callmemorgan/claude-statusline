package main

import "testing"

func TestOriginalThemeAliasConfig(t *testing.T) {
	cfg := config{Theme: "original"}
	warns := validateConfig(&cfg)
	for _, w := range warns {
		if w.Path == "theme" {
			t.Errorf("theme=original warned: %s", w.Msg)
		}
	}
	if cfg.Theme != "original" {
		t.Errorf("validateConfig cleared theme=original to %q", cfg.Theme)
	}
}

func TestValidateConfigThemeKeys(t *testing.T) {
	cfg := config{Theme: "vaporwave", ColorDepth: "8bit", ThemeColors: map[string]string{"git": "notacolor", "ghost": "#fff"}}
	warns := validateConfig(&cfg)
	if cfg.Theme != "" || cfg.ColorDepth != "" {
		t.Errorf("bad theme/depth should reset: %+v", cfg)
	}
	if len(cfg.ThemeColors) != 0 {
		t.Errorf("bad theme_colors entries should drop: %v", cfg.ThemeColors)
	}
	if len(warns) < 4 {
		t.Errorf("expected 4+ warnings, got %v", warns)
	}
}
