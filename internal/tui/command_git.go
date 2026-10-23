package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/workspacereview"
	core "github.com/afterlune/luneCore"
)

// diffModalVisibleLines calculates the visible line capacity of the diff modal.
func (m *tuiModel) diffModalVisibleLines() int {
	if m.height <= 0 {
		return 10
	}
	lines := m.height - 12
	if lines > 20 {
		lines = 20
	}
	if lines < 5 {
		lines = 5
	}
	return lines
}

// openDiffModal fetches workspace git diffs and opens the floating diff viewer modal.
func (m *tuiModel) openDiffModal(targetPath string) (tea.Model, tea.Cmd) {
	ws := m.workspace
	if ws == "" && m.client != nil {
		ws = m.client.Workspace()
	}

	var diffArgs []string
	diffArgs = append(diffArgs, "diff")
	if targetPath != "" {
		diffArgs = append(diffArgs, "--", targetPath)
	}

	cmd := exec.CommandContext(context.Background(), "git", diffArgs...)
	cmd.Dir = ws
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.addSystemEntry("Failed to run git diff: " + err.Error())
		m.updateLayout()
		return m, nil
	}

	diffText := strings.TrimSpace(string(out))
	if diffText == "" && targetPath == "" {
		stagedCmd := exec.CommandContext(context.Background(), "git", "diff", "--cached")
		stagedCmd.Dir = ws
		stagedOut, err := stagedCmd.CombinedOutput()
		if err == nil {
			diffText = strings.TrimSpace(string(stagedOut))
		}
	}

	if diffText == "" {
		if targetPath != "" {
			m.addSystemEntry(fmt.Sprintf("No changes found for: %s", targetPath))
		} else {
			m.addSystemEntry("No git changes detected in workspace.")
		}
		m.updateLayout()
		return m, nil
	}

	files := parseGitDiff(diffText)
	if len(files) == 0 {
		if targetPath != "" {
			m.addSystemEntry(fmt.Sprintf("No changes found for: %s", targetPath))
		} else {
			m.addSystemEntry("No git changes detected in workspace.")
		}
		m.updateLayout()
		return m, nil
	}

	m.modals.open()
	m.input.autocompleteOpen = false
	m.modals.diff.open = true
	m.modals.diff.files = files
	m.modals.diff.fileIndex = 0
	m.modals.diff.scrollY = 0

	m.updateLayout()
	return m, nil
}

// handleReviewCommand handles the /review command.
func (m *tuiModel) handleReviewCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Workspace review is unavailable while a task is running or queued.")
		return m, nil
	}
	workspace := m.workspace
	if strings.TrimSpace(workspace) == "" && m.client != nil {
		workspace = m.client.Workspace()
	}
	if workspace == "" {
		m.addSystemEntry("Code review is unavailable because the workspace path is empty.")
		return m, nil
	}
	targetPath := ""
	if len(parts) > 1 {
		targetPath = strings.Join(parts[1:], " ")
	}
	materials, changed, err := workspacereview.CollectMaterials(workspace, targetPath)
	if err != nil {
		m.addSystemEntry("Could not prepare workspace review: " + err.Error())
		return m, nil
	}
	if !changed {
		m.addSystemEntry("No text changes to review. Git-ignored and binary files are excluded.")
		return m, nil
	}
	if m.runtime == nil {
		m.addSystemEntry("Code review requires an active session runtime.")
		return m, nil
	}

	prompt := workspacereview.Prompt(materials, targetPath)
	return m.submitTurnWithOptions(prompt, core.PermissionReadOnly, false, false)
}
