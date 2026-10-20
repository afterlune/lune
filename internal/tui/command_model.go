package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// handlePlanCommand handles the /plan command.
func (m *tuiModel) handlePlanCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) != 1 {
		m.addSystemEntry("Usage: `/plan` to toggle Plan mode.")
		return m, nil
	}
	m.planMode = !m.planMode
	if m.planMode {
		m.savedMode = m.permissionMode
		_ = m.client.SetPermissionMode(core.PermissionReadOnly)
		m.permissionMode = core.PermissionReadOnly
		m.addSystemEntry("Switched to **Plan mode** (permissions locked to Read-only).")
	} else {
		_ = m.client.SetPermissionMode(m.savedMode)
		m.permissionMode = m.savedMode
		m.addSystemEntry("Exited **Plan mode** (permissions restored).")
	}
	m.updateLayout()
	return m, nil
}

// handlePermissionsCommand handles the /permissions command.
func (m *tuiModel) handlePermissionsCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) > 1 {
		mode := core.PermissionMode(strings.ToLower(parts[1]))
		switch mode {
		case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAsk, core.PermissionFullAccess:
			if m.planMode {
				m.addSystemEntry("Cannot change permissions while **Plan mode** is active.")
			} else {
				if err := m.client.SetPermissionMode(mode); err != nil {
					m.addSystemEntry("Failed to set permission mode: " + err.Error())
				} else {
					m.permissionMode = mode
					m.addSystemEntry(fmt.Sprintf("Permission mode set to `%s`.", mode))
				}
			}
		default:
			m.addSystemEntry("Unknown permission mode. Choose: `read-only`, `workspace`, `ask`, `full-access`.")
		}
	} else {
		m.addSystemEntry(fmt.Sprintf("Current permission mode: `%s` (Plan mode: %v)", m.permissionMode, m.planMode))
	}
	return m, nil
}

// handleModelCommand handles the /model command.
func (m *tuiModel) handleModelCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Model switching is unavailable.")
		return m, nil
	}
	if len(parts) > 1 {
		targetProfile := parts[1]
		profiles := m.client.ListModelProfiles()
		valid := false
		// Prefer an exact profile ID match; model IDs may be shared by profiles.
		for _, p := range profiles {
			if strings.EqualFold(p.Profile, targetProfile) {
				valid = true
				targetProfile = p.Profile
				break
			}
		}
		if !valid {
			for _, p := range profiles {
				if strings.EqualFold(p.Model, targetProfile) {
					valid = true
					targetProfile = p.Profile
					break
				}
			}
		}
		if valid {
			err := m.mutateSessionSettings(func(settings *core.SessionSettings) {
				settings.ModelProfile = targetProfile
			})
			if err != nil {
				m.addSystemEntry("Failed to set model: " + err.Error())
			} else {
				m.currentModelProfile = targetProfile
				m.currentModel = m.client.ModelForSession(context.Background(), m.sessionID)
				m.refreshMainContextWindow()
				m.addSystemEntry(fmt.Sprintf("Active model switched to `%s`.", m.currentModelDisplayName()))
				m.updateLayout()
			}
		} else {
			var names []string
			for _, p := range profiles {
				names = append(names, p.Profile)
			}
			m.addSystemEntry(fmt.Sprintf("Unknown model profile `%s`. Available profiles: %s", parts[1], strings.Join(names, ", ")))
		}
	} else {
		profiles := m.client.ListModelProfiles()
		activeProfile := m.currentModelProfile
		if strings.TrimSpace(activeProfile) == "" {
			activeProfile = m.currentModel
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Current model:** `%s` (`%s`)", m.currentModelDisplayName(), activeProfile))
		lines = append(lines, "\n**Available model profiles:**")
		for _, p := range profiles {
			marker := "  "
			if strings.EqualFold(p.Profile, activeProfile) {
				marker = "* "
			}
			nameDesc := p.Model
			if p.DisplayName != "" && p.DisplayName != p.Model {
				nameDesc = fmt.Sprintf("%s / %s", p.Model, p.DisplayName)
			}
			lines = append(lines, fmt.Sprintf("%s`%s` (%s / %s)", marker, p.Profile, p.Provider, nameDesc))
		}
		lines = append(lines, "\nUsage: `/model <profile>` to switch.")
		m.addSystemEntry(strings.Join(lines, "\n"))
	}
	return m, nil
}
