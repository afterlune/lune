package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) handleShortcutModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0. Shortcut Help Modal handling
	if m.modals.shortcuts {
		switch keyStr {
		case "esc", "?", "q", "enter":
			m.modals.shortcuts = false
			m.updateLayout()
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m *tuiModel) handleAgentsPanelKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	if m.modals.agents.open {
		ids := m.agentMetricIDs()
		switch keyStr {
		case "esc":
			m.closeAgentsPanel()
			return m, nil
		case "backspace", "left":
			if m.modals.agents.detail {
				m.modals.agents.detail = false
				m.modals.agents.detailID = ""
				m.updateLayout()
			}
			return m, nil
		case "up", "ctrl+p":
			if len(ids) > 0 {
				if m.modals.agents.selected > 0 {
					m.modals.agents.selected--
				} else {
					m.modals.agents.selected = len(ids) - 1
				}
				if m.modals.agents.detail {
					m.modals.agents.detailID = ids[m.modals.agents.selected]
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(ids) > 0 {
				if m.modals.agents.selected < len(ids)-1 {
					m.modals.agents.selected++
				} else {
					m.modals.agents.selected = 0
				}
				if m.modals.agents.detail {
					m.modals.agents.detailID = ids[m.modals.agents.selected]
				}
				m.updateLayout()
			}
			return m, nil
		case "enter", "right":
			if m.modals.agents.detail {
				m.modals.agents.detail = false
				m.modals.agents.detailID = ""
			} else if len(ids) > 0 && m.modals.agents.selected >= 0 && m.modals.agents.selected < len(ids) {
				m.modals.agents.detail = true
				m.modals.agents.detailID = ids[m.modals.agents.selected]
			}
			m.updateLayout()
			return m, nil
		default:
			return m, nil
		}
	}
	return m, nil
}

func (m *tuiModel) handleSessionsModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0.1 Session Switcher Modal handling
	if m.modals.sessions.open {
		if m.modals.sessions.confirmDelete {
			switch keyStr {
			case "y", "Y":
				if m.taskBusy() {
					m.modals.sessions.confirmDelete = false
					m.modals.sessions.deletingID = ""
					m.addSystemEntry("Cannot delete sessions while a task is running.")
					m.updateLayout()
					return m, nil
				}
				targetID := m.modals.sessions.deletingID
				m.modals.sessions.confirmDelete = false
				m.modals.sessions.deletingID = ""
				if targetID == m.sessionID {
					m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
					m.updateLayout()
					return m, nil
				}
				if err := m.client.DeleteSession(context.Background(), targetID); err != nil {
					m.addSystemEntry("Failed to delete session: " + err.Error())
					m.updateLayout()
					return m, nil
				}
				var updated []core.Session
				for _, s := range m.modals.sessions.items {
					if s.ID != targetID {
						updated = append(updated, s)
					}
				}
				m.modals.sessions.items = updated
				m.filterSessions()
				m.addSystemEntry("Deleted session: " + targetID)
				m.updateLayout()
				return m, nil
			case "n", "N", "esc":
				m.modals.sessions.confirmDelete = false
				m.modals.sessions.deletingID = ""
				m.updateLayout()
				return m, nil
			default:
				return m, nil
			}
		}

		switch keyStr {
		case "esc":
			m.modals.sessions.open = false
			m.modals.sessions.filter = ""
			m.modals.sessions.confirmDelete = false
			m.modals.sessions.deletingID = ""
			m.modals.sessions.deleteMode = false
			m.updateLayout()
			return m, nil
		case "ctrl+d", "delete":
			if len(m.modals.sessions.filtered) > 0 && m.modals.sessions.index >= 0 && m.modals.sessions.index < len(m.modals.sessions.filtered) {
				if m.taskBusy() {
					m.addSystemEntry("Cannot delete sessions while a task is running.")
					m.updateLayout()
					return m, nil
				}
				target := m.modals.sessions.filtered[m.modals.sessions.index]
				if target.ID == m.sessionID {
					m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
					m.updateLayout()
					return m, nil
				}
				m.modals.sessions.confirmDelete = true
				m.modals.sessions.deletingID = target.ID
				m.updateLayout()
				return m, nil
			}
			return m, nil
		case "up", "ctrl+p":
			if len(m.modals.sessions.filtered) > 0 {
				if m.modals.sessions.index > 0 {
					m.modals.sessions.index--
				} else {
					m.modals.sessions.index = len(m.modals.sessions.filtered) - 1
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(m.modals.sessions.filtered) > 0 {
				if m.modals.sessions.index < len(m.modals.sessions.filtered)-1 {
					m.modals.sessions.index++
				} else {
					m.modals.sessions.index = 0
				}
				m.updateLayout()
			}
			return m, nil
		case "enter":
			if len(m.modals.sessions.filtered) > 0 && m.modals.sessions.index >= 0 && m.modals.sessions.index < len(m.modals.sessions.filtered) {
				targetID := m.modals.sessions.filtered[m.modals.sessions.index].ID
				if m.modals.sessions.deleteMode {
					if m.taskBusy() {
						m.addSystemEntry("Cannot delete sessions while a task is running.")
						m.updateLayout()
						return m, nil
					}
					if targetID == m.sessionID {
						m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
						m.updateLayout()
						return m, nil
					}
					m.modals.sessions.confirmDelete = true
					m.modals.sessions.deletingID = targetID
					m.updateLayout()
					return m, nil
				}
				m.modals.sessions.open = false
				m.modals.sessions.filter = ""
				if targetID == m.sessionID {
					m.addSystemEntry("Already in session: " + targetID)
					m.updateLayout()
					return m, nil
				}
				return m.switchToSession(targetID)
			}
			m.modals.sessions.open = false
			m.modals.sessions.filter = ""
			m.updateLayout()
			return m, nil
		case "backspace":
			if len(m.modals.sessions.filter) > 0 {
				runes := []rune(m.modals.sessions.filter)
				m.modals.sessions.filter = string(runes[:len(runes)-1])
				m.filterSessions()
				m.updateLayout()
			}
			return m, nil
		default:
			text := msg.Key().Text
			if text == "" && msg.Key().Code >= ' ' && msg.Key().Code <= '~' && msg.Key().Mod == 0 {
				text = string(msg.Key().Code)
			}
			if text != "" {
				m.modals.sessions.filter += text
				m.filterSessions()
				m.updateLayout()
				return m, nil
			}
		}
		return m, nil
	}
	return m, nil
}

func (m *tuiModel) handleToolPagerModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0.2 Tool Output Pager Modal handling
	if m.modals.toolPager.open {
		toolIndices := m.getToolEntryIndices()
		toolCount := len(toolIndices)
		if toolCount == 0 {
			m.modals.toolPager.open = false
			m.updateLayout()
			return m, nil
		}
		if m.modals.toolPager.index < 0 {
			m.modals.toolPager.index = 0
		} else if m.modals.toolPager.index >= toolCount {
			m.modals.toolPager.index = toolCount - 1
		}

		entry := m.entries[toolIndices[m.modals.toolPager.index]]
		trimmedOut := strings.TrimRight(entry.ToolOutput, "\r\n")
		var rawLines []string
		if strings.TrimSpace(trimmedOut) == "" {
			rawLines = []string{}
		} else {
			rawLines = strings.Split(trimmedOut, "\n")
		}
		totalLines := len(rawLines)
		visibleLines := m.toolPagerVisibleLines()
		maxScroll := max(0, totalLines-visibleLines)

		switch keyStr {
		case "esc", "q":
			m.modals.toolPager.open = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.modals.toolPager.scrollY > 0 {
				m.modals.toolPager.scrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.modals.toolPager.scrollY < maxScroll {
				m.modals.toolPager.scrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.modals.toolPager.scrollY = max(0, m.modals.toolPager.scrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.modals.toolPager.scrollY = min(maxScroll, m.modals.toolPager.scrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.modals.toolPager.scrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.modals.toolPager.scrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "[", "p", "ctrl+p":
			if m.modals.toolPager.index > 0 {
				m.modals.toolPager.index--
			} else {
				m.modals.toolPager.index = toolCount - 1
			}
			m.modals.toolPager.scrollY = 0
			m.updateLayout()
			return m, nil

		case "]", "n", "ctrl+n":
			if m.modals.toolPager.index < toolCount-1 {
				m.modals.toolPager.index++
			} else {
				m.modals.toolPager.index = 0
			}
			m.modals.toolPager.scrollY = 0
			m.updateLayout()
			return m, nil

		case "c", "ctrl+c":
			cmd := m.copyTextToClipboardAs(entry.ToolOutput, func(count int) string {
				return fmt.Sprintf("Copied %d chars of tool output to clipboard", count)
			})
			m.updateLayout()
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

func (m *tuiModel) handleDiffModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0.3 Workspace Diff Viewer Modal handling
	if m.modals.diff.open {
		fileCount := len(m.modals.diff.files)
		if fileCount == 0 {
			m.modals.diff.open = false
			m.updateLayout()
			return m, nil
		}
		if m.modals.diff.fileIndex < 0 {
			m.modals.diff.fileIndex = 0
		} else if m.modals.diff.fileIndex >= fileCount {
			m.modals.diff.fileIndex = fileCount - 1
		}

		file := m.modals.diff.files[m.modals.diff.fileIndex]
		totalLines := len(file.Lines)
		visibleLines := m.diffModalVisibleLines()
		maxScroll := max(0, totalLines-visibleLines)

		switch keyStr {
		case "esc", "q", "ctrl+d":
			m.modals.diff.open = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.modals.diff.scrollY > 0 {
				m.modals.diff.scrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.modals.diff.scrollY < maxScroll {
				m.modals.diff.scrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.modals.diff.scrollY = max(0, m.modals.diff.scrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.modals.diff.scrollY = min(maxScroll, m.modals.diff.scrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.modals.diff.scrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.modals.diff.scrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "tab", "]", "n", "ctrl+n":
			if m.modals.diff.fileIndex < fileCount-1 {
				m.modals.diff.fileIndex++
			} else {
				m.modals.diff.fileIndex = 0
			}
			m.modals.diff.scrollY = 0
			m.updateLayout()
			return m, nil

		case "shift+tab", "[", "p", "ctrl+p":
			if m.modals.diff.fileIndex > 0 {
				m.modals.diff.fileIndex--
			} else {
				m.modals.diff.fileIndex = fileCount - 1
			}
			m.modals.diff.scrollY = 0
			m.updateLayout()
			return m, nil

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			targetIdx := int(keyStr[0] - '1')
			if targetIdx < fileCount {
				m.modals.diff.fileIndex = targetIdx
				m.modals.diff.scrollY = 0
				m.updateLayout()
			}
			return m, nil

		case "a":
			var allDiffs []string
			for _, f := range m.modals.diff.files {
				allDiffs = append(allDiffs, f.RawDiff)
			}
			fullText := strings.Join(allDiffs, "\n\n")
			cmd := m.copyTextToClipboardAs(fullText, func(count int) string {
				return fmt.Sprintf("Copied all diffs (%d chars) to clipboard", count)
			})
			m.updateLayout()
			return m, cmd

		case "c", "ctrl+c":
			cmd := m.copyTextToClipboardAs(file.RawDiff, func(count int) string {
				return fmt.Sprintf("Copied %d chars of diff to clipboard", count)
			})
			m.updateLayout()
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}
