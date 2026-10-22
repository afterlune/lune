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
	return m.togglePlanMode()
}

// togglePlanMode switches between normal and Plan mode. Plan mode locks tool
// permissions to read-only and restores the previous mode when it ends.
func (m *tuiModel) togglePlanMode() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Plan mode is unavailable without a Core client.")
		return m, nil
	}
	if m.taskBusy() {
		m.addSystemEntry("Plan mode can only be changed after the current and queued tasks finish.")
		return m, nil
	}
	if m.hasPausedTask() {
		m.addSystemEntry("Plan mode can only be changed after paused tasks are resumed or discarded.")
		return m, nil
	}
	if m.activeQuestion != nil || m.activeApproval != nil || m.activePlanReview != nil {
		m.addSystemEntry("Plan mode can only be changed after the pending interaction is resolved.")
		return m, nil
	}

	if m.planMode {
		if err := m.client.SetPermissionMode(m.savedMode); err != nil {
			m.addSystemEntry("Failed to restore permissions; Plan mode remains active: " + err.Error())
			return m, nil
		}
		m.planMode = false
		m.permissionMode = m.savedMode
		m.addSystemEntry("Exited **Plan mode** (permissions restored).")
	} else {
		savedMode := m.client.PermissionMode()
		if err := m.client.SetPermissionMode(core.PermissionReadOnly); err != nil {
			m.addSystemEntry("Failed to enter Plan mode: " + err.Error())
			return m, nil
		}
		m.savedMode = savedMode
		m.permissionMode = core.PermissionReadOnly
		m.planMode = true
		m.addSystemEntry("Switched to **Plan mode** (permissions locked to Read-only).")
	}
	m.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

// handlePermissionsCommand handles the /permissions command.
func (m *tuiModel) handlePermissionsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Permission management is unavailable.")
		return m, nil
	}
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
		return m, nil
	}
	return m.openPermissionPicker()
}

// validateTuiModelProfile fails fast for a `-m` profile that does not exist, even
// while the session is still a draft.
func validateTuiModelProfile(client *core.Core, profile string) error {
	if client == nil {
		return fmt.Errorf("core client is unavailable")
	}
	for _, candidate := range client.ListModelProfiles() {
		if strings.EqualFold(candidate.Profile, profile) {
			return nil
		}
	}
	return fmt.Errorf("unknown model profile %q", profile)
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
			if m.sessionID == "" {
				m.pendingModelProfile = targetProfile
				m.currentModelProfile = targetProfile
				m.addSystemEntry(fmt.Sprintf("Model `%s` will be used when the session starts with your first message.", targetProfile))
				m.updateLayout()
				return m, nil
			}
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
		return m.openModelPicker()
	}
	return m, nil
}
