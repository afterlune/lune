package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/atotto/clipboard"
)

func (m *tuiModel) handleShortcutModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0. Shortcut Help Modal handling
	if m.shortcutsOpen {
		switch keyStr {
		case "esc", "?", "q", "enter":
			m.shortcutsOpen = false
			m.updateLayout()
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m *tuiModel) handleAgentsPanelKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	if m.agentsPanelOpen {
		ids := m.agentMetricIDs()
		switch keyStr {
		case "esc":
			m.closeAgentsPanel()
			return m, nil
		case "backspace", "left":
			if m.agentsPanelDetail {
				m.agentsPanelDetail = false
				m.agentsPanelDetailID = ""
				m.updateLayout()
			}
			return m, nil
		case "up", "ctrl+p":
			if len(ids) > 0 {
				if m.agentsPanelSelected > 0 {
					m.agentsPanelSelected--
				} else {
					m.agentsPanelSelected = len(ids) - 1
				}
				if m.agentsPanelDetail {
					m.agentsPanelDetailID = ids[m.agentsPanelSelected]
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(ids) > 0 {
				if m.agentsPanelSelected < len(ids)-1 {
					m.agentsPanelSelected++
				} else {
					m.agentsPanelSelected = 0
				}
				if m.agentsPanelDetail {
					m.agentsPanelDetailID = ids[m.agentsPanelSelected]
				}
				m.updateLayout()
			}
			return m, nil
		case "enter", "right":
			if m.agentsPanelDetail {
				m.agentsPanelDetail = false
				m.agentsPanelDetailID = ""
			} else if len(ids) > 0 && m.agentsPanelSelected >= 0 && m.agentsPanelSelected < len(ids) {
				m.agentsPanelDetail = true
				m.agentsPanelDetailID = ids[m.agentsPanelSelected]
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
	if m.sessionsModalOpen {
		if m.sessionsConfirmDelete {
			switch keyStr {
			case "y", "Y":
				targetID := m.sessionsDeletingID
				m.sessionsConfirmDelete = false
				m.sessionsDeletingID = ""
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
				for _, s := range m.sessionsItems {
					if s.ID != targetID {
						updated = append(updated, s)
					}
				}
				m.sessionsItems = updated
				m.filterSessions()
				m.addSystemEntry("Deleted session: " + targetID)
				m.updateLayout()
				return m, nil
			case "n", "N", "esc":
				m.sessionsConfirmDelete = false
				m.sessionsDeletingID = ""
				m.updateLayout()
				return m, nil
			default:
				return m, nil
			}
		}

		switch keyStr {
		case "esc", "ctrl+o":
			m.sessionsModalOpen = false
			m.sessionsFilter = ""
			m.sessionsConfirmDelete = false
			m.sessionsDeletingID = ""
			m.updateLayout()
			return m, nil
		case "ctrl+d", "delete":
			if len(m.sessionsFiltered) > 0 && m.sessionsIndex >= 0 && m.sessionsIndex < len(m.sessionsFiltered) {
				target := m.sessionsFiltered[m.sessionsIndex]
				if target.ID == m.sessionID {
					m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
					m.updateLayout()
					return m, nil
				}
				m.sessionsConfirmDelete = true
				m.sessionsDeletingID = target.ID
				m.updateLayout()
				return m, nil
			}
			return m, nil
		case "up", "ctrl+p":
			if len(m.sessionsFiltered) > 0 {
				if m.sessionsIndex > 0 {
					m.sessionsIndex--
				} else {
					m.sessionsIndex = len(m.sessionsFiltered) - 1
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(m.sessionsFiltered) > 0 {
				if m.sessionsIndex < len(m.sessionsFiltered)-1 {
					m.sessionsIndex++
				} else {
					m.sessionsIndex = 0
				}
				m.updateLayout()
			}
			return m, nil
		case "enter":
			if len(m.sessionsFiltered) > 0 && m.sessionsIndex >= 0 && m.sessionsIndex < len(m.sessionsFiltered) {
				targetID := m.sessionsFiltered[m.sessionsIndex].ID
				m.sessionsModalOpen = false
				m.sessionsFilter = ""
				if targetID == m.sessionID {
					m.addSystemEntry("Already in session: " + targetID)
					m.updateLayout()
					return m, nil
				}
				return m.switchToSession(targetID)
			}
			m.sessionsModalOpen = false
			m.sessionsFilter = ""
			m.updateLayout()
			return m, nil
		case "backspace":
			if len(m.sessionsFilter) > 0 {
				runes := []rune(m.sessionsFilter)
				m.sessionsFilter = string(runes[:len(runes)-1])
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
				m.sessionsFilter += text
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
	if m.toolPagerOpen {
		toolIndices := m.getToolEntryIndices()
		toolCount := len(toolIndices)
		if toolCount == 0 {
			m.toolPagerOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.toolPagerIndex < 0 {
			m.toolPagerIndex = 0
		} else if m.toolPagerIndex >= toolCount {
			m.toolPagerIndex = toolCount - 1
		}

		entry := m.entries[toolIndices[m.toolPagerIndex]]
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
		case "esc", "q", "ctrl+t":
			m.toolPagerOpen = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.toolPagerScrollY > 0 {
				m.toolPagerScrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.toolPagerScrollY < maxScroll {
				m.toolPagerScrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.toolPagerScrollY = max(0, m.toolPagerScrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.toolPagerScrollY = min(maxScroll, m.toolPagerScrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.toolPagerScrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "[", "p", "ctrl+p":
			if m.toolPagerIndex > 0 {
				m.toolPagerIndex--
			} else {
				m.toolPagerIndex = toolCount - 1
			}
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "]", "n", "ctrl+n":
			if m.toolPagerIndex < toolCount-1 {
				m.toolPagerIndex++
			} else {
				m.toolPagerIndex = 0
			}
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "c", "ctrl+c":
			outText := entry.ToolOutput
			_ = clipboard.WriteAll(outText)
			count := utf8.RuneCountInString(outText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars of tool output to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		return m, nil
	}
	return m, nil
}

func (m *tuiModel) handleDiffModalKeyPress(msg tea.KeyPressMsg, keyStr string) (tea.Model, tea.Cmd) {
	// 0.3 Workspace Diff Viewer Modal handling
	if m.diffModalOpen {
		fileCount := len(m.diffFiles)
		if fileCount == 0 {
			m.diffModalOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.diffFileIndex < 0 {
			m.diffFileIndex = 0
		} else if m.diffFileIndex >= fileCount {
			m.diffFileIndex = fileCount - 1
		}

		file := m.diffFiles[m.diffFileIndex]
		totalLines := len(file.Lines)
		visibleLines := m.diffModalVisibleLines()
		maxScroll := max(0, totalLines-visibleLines)

		switch keyStr {
		case "esc", "q", "ctrl+d":
			m.diffModalOpen = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.diffScrollY > 0 {
				m.diffScrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.diffScrollY < maxScroll {
				m.diffScrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.diffScrollY = max(0, m.diffScrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.diffScrollY = min(maxScroll, m.diffScrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.diffScrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "tab", "]", "n", "ctrl+n":
			if m.diffFileIndex < fileCount-1 {
				m.diffFileIndex++
			} else {
				m.diffFileIndex = 0
			}
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "shift+tab", "[", "p", "ctrl+p":
			if m.diffFileIndex > 0 {
				m.diffFileIndex--
			} else {
				m.diffFileIndex = fileCount - 1
			}
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			targetIdx := int(keyStr[0] - '1')
			if targetIdx < fileCount {
				m.diffFileIndex = targetIdx
				m.diffScrollY = 0
				m.updateLayout()
			}
			return m, nil

		case "a":
			var allDiffs []string
			for _, f := range m.diffFiles {
				allDiffs = append(allDiffs, f.RawDiff)
			}
			fullText := strings.Join(allDiffs, "\n\n")
			_ = clipboard.WriteAll(fullText)
			count := utf8.RuneCountInString(fullText)
			m.copyFeedback = fmt.Sprintf("Copied all diffs (%d chars) to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})

		case "c", "ctrl+c":
			diffText := file.RawDiff
			_ = clipboard.WriteAll(diffText)
			count := utf8.RuneCountInString(diffText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars of diff to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		return m, nil
	}
	return m, nil
}
