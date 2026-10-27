package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type modelProfileChoice struct {
	Profile     string
	Provider    string
	Model       string
	DisplayName string
}

type ActiveModelPicker struct {
	Items    []modelProfileChoice
	Selected int
}

type ActivePermissionPicker struct {
	Selected int
}

var permissionModes = []core.PermissionMode{
	core.PermissionReadOnly,
	core.PermissionWorkspace,
	core.PermissionAuto,
	core.PermissionFullAccess,
}

func (m *tuiModel) openModelPicker() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Model switching is unavailable.")
		return m, nil
	}
	profiles := m.client.ListModelProfiles()
	if len(profiles) == 0 {
		m.addSystemEntry("No model profiles are configured.")
		return m, nil
	}
	m.modals.open()
	m.modals.model = &ActiveModelPicker{}
	active := m.currentModelProfile
	if strings.TrimSpace(active) == "" {
		active = m.currentModel
	}
	for _, profile := range profiles {
		choice := modelProfileChoice{
			Profile:     profile.Profile,
			Provider:    profile.Provider,
			Model:       profile.Model,
			DisplayName: profile.DisplayName,
		}
		m.modals.model.Items = append(m.modals.model.Items, choice)
		if strings.EqualFold(profile.Profile, active) || (strings.TrimSpace(m.currentModelProfile) == "" && strings.EqualFold(profile.Model, active)) {
			m.modals.model.Selected = len(m.modals.model.Items) - 1
		}
	}
	m.input.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) openPermissionPicker() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Permission management is unavailable.")
		return m, nil
	}
	selected := 0
	for i, mode := range permissionModes {
		if mode == m.permissionMode {
			selected = i
			break
		}
	}
	m.modals.open()
	m.modals.permission = &ActivePermissionPicker{Selected: selected}
	m.input.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) handleProfilePickerKeyPress(keyStr string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(keyStr) {
	case "esc", "q":
		m.modals.model = nil
		m.modals.permission = nil
		m.updateLayout()
		return m, nil
	case "up", "k":
		if m.modals.model != nil {
			m.modals.model.Selected = (m.modals.model.Selected - 1 + len(m.modals.model.Items)) % len(m.modals.model.Items)
		} else if m.modals.permission != nil {
			m.modals.permission.Selected = (m.modals.permission.Selected - 1 + len(permissionModes)) % len(permissionModes)
		}
		m.updateLayout()
		return m, nil
	case "down", "j":
		if m.modals.model != nil {
			m.modals.model.Selected = (m.modals.model.Selected + 1) % len(m.modals.model.Items)
		} else if m.modals.permission != nil {
			m.modals.permission.Selected = (m.modals.permission.Selected + 1) % len(permissionModes)
		}
		m.updateLayout()
		return m, nil
	case "enter":
		if m.modals.model != nil {
			selected := m.modals.model.Selected
			if selected < 0 || selected >= len(m.modals.model.Items) {
				return m, nil
			}
			profile := m.modals.model.Items[selected].Profile
			m.modals.model = nil
			return m.handleModelCommand([]string{"/model", profile})
		}
		if m.modals.permission != nil {
			selected := m.modals.permission.Selected
			if selected < 0 || selected >= len(permissionModes) {
				return m, nil
			}
			mode := permissionModes[selected]
			m.modals.permission = nil
			if err := m.client.SetPermissionMode(mode); err != nil {
				m.addSystemEntry("Failed to set permission mode: " + err.Error())
			} else {
				m.permissionMode = mode
				m.addSystemEntry(fmt.Sprintf("Permission mode set to `%s`.", mode))
			}
			m.updateLayout()
			return m, nil
		}
	}
	return m, nil
}
