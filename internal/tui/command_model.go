package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/slashcmd"
	"github.com/afterlune/lune/internal/toolchains"
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

// togglePlanMode switches between normal and Plan mode without changing permissions.
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
		m.planMode = false
		m.addSystemEntry("Exited **Plan mode**.")
	} else {
		if m.runtime != nil {
			if goal, err := m.runtime.GetGoal(context.Background()); err == nil && goal != nil && goal.Status == core.GoalActive {
				if _, err := m.runtime.PauseGoal(context.Background()); err != nil {
					m.addSystemEntry("Failed to pause goal before entering Plan mode: " + err.Error())
					return m, nil
				}
				m.refreshGoal()
			}
		}
		m.planMode = true
		m.addSystemEntry("Switched to **Plan mode** (read-only tools).")
	}
	m.input.autocompleteOpen = false
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
		mode, err := slashcmd.ParsePermissionMode(parts[1])
		if err != nil {
			m.addSystemEntry("Unknown permission mode. Choose: `" + strings.Join(slashcmd.PermissionModeNames(), "`, `") + "`.")
			return m, nil
		}
		if err := m.client.SetPermissionMode(mode); err != nil {
			m.addSystemEntry("Failed to set permission mode: " + err.Error())
		} else {
			m.permissionMode = mode
			m.addSystemEntry(fmt.Sprintf("Permission mode set to `%s`.", mode))
		}
		return m, nil
	}
	return m.openPermissionPicker()
}

// handleToolchainsCommand handles the /toolchains command.
func (m *tuiModel) handleToolchainsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Toolchain management is unavailable.")
		return m, nil
	}
	subcmd := ""
	if len(parts) > 1 {
		subcmd = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	switch subcmd {
	case "", "list":
		current := m.client.CommandToolchainPaths()
		if len(current) == 0 {
			m.addSystemEntry("No command toolchains configured for this workspace. Use `/toolchains detect` or `/toolchains refresh`.")
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Configured Toolchains (%d):**", len(current)))
		for _, p := range current {
			lines = append(lines, fmt.Sprintf("- `%s`", p))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "detect":
		candidates := toolchains.Detect()
		if len(candidates) == 0 {
			m.addSystemEntry("No host toolchains detected on PATH.")
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Detected Host Toolchains (%d):**", len(candidates)))
		for _, c := range candidates {
			rootInfo := c.Root
			if rootInfo == "" {
				rootInfo = "(skipped: " + c.Note + ")"
			}
			lines = append(lines, fmt.Sprintf("- **%s**: `%s` (root: `%s`)", c.Name, c.Executable, rootInfo))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "refresh":
		roots := toolchains.AutoDetectRoots()
		if err := m.client.SetCommandToolchainPaths(roots); err != nil {
			m.addSystemEntry("Failed to update toolchains in Core: " + err.Error())
			return m, nil
		}
		if userStore, err := settings.OpenUser(); err == nil {
			_ = userStore.SaveWorkspaceCommandToolchainPaths(m.client.Workspace(), roots)
			_ = userStore.Close()
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Refreshed Toolchains (%d):**", len(roots)))
		for _, p := range roots {
			lines = append(lines, fmt.Sprintf("- `%s`", p))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	default:
		m.addSystemEntry("Unknown toolchains subcommand. Usage: `/toolchains [list|detect|refresh]`.")
		return m, nil
	}
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
