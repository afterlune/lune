package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var themeModes = []ThemeMode{ThemeModeAuto, ThemeModeDark, ThemeModeLight}

type ActiveThemePicker struct {
	Selected int
}

func (m *tuiModel) openThemePicker() {
	selected := 0
	for i, mode := range themeModes {
		if mode == CurrentThemeMode() {
			selected = i
			break
		}
	}
	m.modals.open()
	m.modals.theme = &ActiveThemePicker{Selected: selected}
	m.input.autocompleteOpen = false
	m.updateLayout()
}

func (m *tuiModel) applyThemeMode(mode ThemeMode) {
	if mode != ThemeModeAuto && mode != ThemeModeDark && mode != ThemeModeLight {
		m.addSystemEntry(fmt.Sprintf("Unknown theme mode %q. Options: `auto`, `dark`, `light`.", mode))
		return
	}
	SetThemeMode(mode)
	m.applyThemeStyles()
	m.refreshViewportContent()
	m.updateLayout()
	if m.settingsStore == nil {
		m.addSystemEntry(fmt.Sprintf("Theme mode set to `%s` for this run; persistence is unavailable.", mode))
		return
	}
	if err := m.settingsStore.SaveTUITheme(string(mode)); err != nil {
		m.addSystemEntry(fmt.Sprintf("Theme mode set to `%s` for this run, but could not be saved: %v", mode, err))
		return
	}
	m.addSystemEntry(fmt.Sprintf("Theme mode set to `%s` (active: %s).", mode, CurrentThemeMode()))
}

func (m *tuiModel) handleThemePickerKeyPress(keyStr string) (tea.Model, tea.Cmd) {
	if m.modals.theme == nil {
		return m, nil
	}
	switch strings.ToLower(keyStr) {
	case "esc", "q":
		m.modals.theme = nil
		m.updateLayout()
		return m, nil
	case "up", "k":
		if m.modals.theme.Selected > 0 {
			m.modals.theme.Selected--
		} else {
			m.modals.theme.Selected = len(themeModes) - 1
		}
		m.updateLayout()
		return m, nil
	case "down", "j":
		if m.modals.theme.Selected < len(themeModes)-1 {
			m.modals.theme.Selected++
		} else {
			m.modals.theme.Selected = 0
		}
		m.updateLayout()
		return m, nil
	case "enter":
		selected := m.modals.theme.Selected
		m.modals.theme = nil
		m.applyThemeMode(themeModes[selected])
		return m, nil
	default:
		return m, nil
	}
}
