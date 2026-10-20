package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
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

	m.shortcutsOpen = false
	m.sessionsModalOpen = false
	m.autocompleteOpen = false
	m.toolPagerOpen = false
	m.diffModalOpen = true
	m.diffFiles = files
	m.diffFileIndex = 0
	m.diffScrollY = 0

	m.updateLayout()
	return m, nil
}

// handleReviewCommand handles the /review command.
func (m *tuiModel) handleReviewCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) > 1 {
		targetPath := strings.Join(parts[1:], " ")
		return m.openDiffModal(targetPath)
	}

	statusCmd := exec.CommandContext(context.Background(), "git", "status", "--short")
	statusCmd.Dir = m.workspace
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		m.addSystemEntry("Failed to check git status: " + err.Error())
		return m, nil
	}
	statusText := strings.TrimSpace(string(statusOut))
	if statusText == "" {
		m.addSystemEntry("No unstaged or uncommitted changes in workspace.")
		return m, nil
	}

	diffStatCmd := exec.CommandContext(context.Background(), "git", "diff", "--stat")
	diffStatCmd.Dir = m.workspace
	diffStatOut, _ := diffStatCmd.CombinedOutput()
	statText := strings.TrimSpace(string(diffStatOut))

	var lines []string
	lines = append(lines, "**Workspace Changes Review:**\n```text")
	lines = append(lines, statusText)
	lines = append(lines, "```")
	if statText != "" {
		lines = append(lines, "\n**Diff Statistics:**\n```text")
		lines = append(lines, statText)
		lines = append(lines, "```")
	}
	lines = append(lines, "\n*Tip: Press `Ctrl+D` or use `/diff [path]` to inspect full diff in the interactive diff viewer.*")
	m.addSystemEntry(strings.Join(lines, "\n"))
	return m, nil
}
