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
	core.PermissionAsk,
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
	m.modelPicker = &ActiveModelPicker{}
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
		m.modelPicker.Items = append(m.modelPicker.Items, choice)
		if strings.EqualFold(profile.Profile, active) || (strings.TrimSpace(m.currentModelProfile) == "" && strings.EqualFold(profile.Model, active)) {
			m.modelPicker.Selected = len(m.modelPicker.Items) - 1
		}
	}
	m.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) openPermissionPicker() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Permission management is unavailable.")
		return m, nil
	}
	if m.planMode {
		m.addSystemEntry("Cannot change permissions while **Plan mode** is active.")
		return m, nil
	}
	selected := 0
	for i, mode := range permissionModes {
		if mode == m.permissionMode {
			selected = i
			break
		}
	}
	m.permissionPicker = &ActivePermissionPicker{Selected: selected}
	m.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) handleProfilePickerKeyPress(keyStr string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(keyStr) {
	case "esc", "q":
		m.modelPicker = nil
		m.permissionPicker = nil
		m.updateLayout()
		return m, nil
	case "up", "k":
		if m.modelPicker != nil {
			m.modelPicker.Selected = (m.modelPicker.Selected - 1 + len(m.modelPicker.Items)) % len(m.modelPicker.Items)
		} else if m.permissionPicker != nil {
			m.permissionPicker.Selected = (m.permissionPicker.Selected - 1 + len(permissionModes)) % len(permissionModes)
		}
		m.updateLayout()
		return m, nil
	case "down", "j":
		if m.modelPicker != nil {
			m.modelPicker.Selected = (m.modelPicker.Selected + 1) % len(m.modelPicker.Items)
		} else if m.permissionPicker != nil {
			m.permissionPicker.Selected = (m.permissionPicker.Selected + 1) % len(permissionModes)
		}
		m.updateLayout()
		return m, nil
	case "enter":
		if m.modelPicker != nil {
			selected := m.modelPicker.Selected
			if selected < 0 || selected >= len(m.modelPicker.Items) {
				return m, nil
			}
			profile := m.modelPicker.Items[selected].Profile
			m.modelPicker = nil
			return m.handleModelCommand([]string{"/model", profile})
		}
		if m.permissionPicker != nil {
			selected := m.permissionPicker.Selected
			if selected < 0 || selected >= len(permissionModes) {
				return m, nil
			}
			mode := permissionModes[selected]
			m.permissionPicker = nil
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
