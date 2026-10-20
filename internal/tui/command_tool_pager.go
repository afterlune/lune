package tui

import (
	tea "charm.land/bubbletea/v2"
)

// getToolEntryIndices returns the indices of all KindTool entries in m.entries.
func (m *tuiModel) getToolEntryIndices() []int {
	var indices []int
	for i, entry := range m.entries {
		if entry.Kind == KindTool {
			indices = append(indices, i)
		}
	}
	return indices
}

// toolPagerVisibleLines calculates the visible line capacity of the tool pager modal.
func (m *tuiModel) toolPagerVisibleLines() int {
	if m.height <= 0 {
		return 10
	}
	lines := m.height - 12
	if lines > 18 {
		lines = 18
	}
	if lines < 5 {
		lines = 5
	}
	return lines
}

// openToolPager opens the floating pager modal to inspect full tool output.
func (m *tuiModel) openToolPager(targetIndex int) (tea.Model, tea.Cmd) {
	indices := m.getToolEntryIndices()
	if len(indices) == 0 {
		m.addSystemEntry("No tool executions in current session.")
		m.updateLayout()
		return m, nil
	}

	m.shortcutsOpen = false
	m.sessionsModalOpen = false
	m.autocompleteOpen = false
	m.toolPagerOpen = true
	m.toolPagerScrollY = 0

	if targetIndex < 0 || targetIndex >= len(indices) {
		m.toolPagerIndex = len(indices) - 1
	} else {
		m.toolPagerIndex = targetIndex
	}

	m.updateLayout()
	return m, nil
}
