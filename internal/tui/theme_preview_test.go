package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Moving the cursor on the settings face applies themes LIVE (previewSettings), so
// every way of closing the overlay owes the same cleanup: put the committed theme
// back. `esc` always did. `?` now closes from EITHER face, and closing straight
// out of settings is the newest path — the one with no cleanup at all before this,
// because ? used to bounce to the keys face instead of closing.
//
// The fixture pins serika_dark (theme index 1, NOT 0) on purpose: it is also the
// only thing that catches the settings cursor failing to be parked on the
// committed theme when tab flips into the face. With a default-theme fixture that
// assertion passes vacuously.
func TestTUI_ClosingHelpWithQuestionMarkDropsThemePreview(t *testing.T) {
	cfg, repos := twoRepos(t)
	cfg.Theme = "serika_dark"
	applyTheme(themeByName(cfg.Theme))

	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	committed := themeByName("serika_dark").Accent

	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	step := func(m Model, k string) Model { mm, _ := m.Update(rk(k)); return mm.(Model) }
	flip := func(m Model) Model { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab}); return mm.(Model) }

	m = step(m, "?") // overlay opens on the keys face
	if !m.showHelp || !m.showKeys {
		t.Fatal("? should open the overlay on the keys face")
	}

	m = flip(m) // -> settings face, cursor parked on the committed theme
	if m.showKeys {
		t.Fatal("tab should flip to the settings face")
	}
	if m.settingsCursor != themeIndex("serika_dark") {
		t.Fatalf("flipping into settings must park the cursor on the committed theme: "+
			"cursor=%d, want %d", m.settingsCursor, themeIndex("serika_dark"))
	}

	m = step(m, "j") // preview the next theme live — NOT committed
	if borderAccent == committed {
		t.Fatal("j should have previewed a different theme")
	}
	previewed := borderAccent

	m = flip(m) // -> keys face, preview still live
	if !m.showKeys {
		t.Fatal("tab should flip back to the keys face")
	}
	if borderAccent != previewed {
		t.Fatal("switching faces should not itself restore the theme")
	}

	m = flip(m) // back to settings, preview still live
	m = step(m, "?")
	if m.showHelp {
		t.Fatal("? on the settings face should close the overlay")
	}
	if m.cfg.Theme != "serika_dark" {
		t.Fatalf("the theme was never committed, cfg.Theme = %q", m.cfg.Theme)
	}
	if borderAccent != committed {
		t.Errorf("closing with ? from settings leaked the live preview: accent is %v, want the committed %v",
			borderAccent, committed)
	}
}
