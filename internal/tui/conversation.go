package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	messageCollapseLines   = 6
	messagePreviewLines    = 3
	messageCollapseRunes   = 600
	wideConversationWidth  = 112
	wideConversationHeight = 24
)

func (m *tuiModel) modernConversation() bool {
	return m.preferences.Theme == themeDark || m.preferences.Theme == themeLight
}

func (m *tuiModel) wideConversation() bool {
	return m.modernConversation() && m.width >= wideConversationWidth && m.height >= wideConversationHeight
}

func (m *tuiModel) ensureMessageCollapseKeys() {
	turn := 0
	for index := range m.entries {
		entry := &m.entries[index]
		if entry.role != "you" {
			continue
		}
		if entry.collapseKey == "" {
			sum := sha256.Sum256([]byte(entry.text))
			entry.collapseKey = fmt.Sprintf("%s:%d:%x", m.sessionID, turn, sum[:8])
		}
		turn++
	}
}

func plainConversationText(markdown string) string {
	lines := strings.Split(markdown, "\n")
	inCode := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			lines[index] = ""
			continue
		}
		if inCode {
			lines[index] = "  " + line
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "### "):
			line = strings.TrimPrefix(trimmed, "### ")
		case strings.HasPrefix(trimmed, "## "):
			line = strings.TrimPrefix(trimmed, "## ")
		case strings.HasPrefix(trimmed, "# "):
			line = strings.TrimPrefix(trimmed, "# ")
		case strings.HasPrefix(trimmed, "> "):
			line = strings.TrimPrefix(trimmed, "> ")
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			line = "• " + trimmed[2:]
		}
		line = strings.ReplaceAll(line, "**", "")
		line = stripPairedAsterisks(line)
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "~~", "")
		line = strings.ReplaceAll(line, "`", "")
		lines[index] = line
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func stripPairedAsterisks(line string) string {
	for {
		start := strings.IndexByte(line, '*')
		if start < 0 {
			return line
		}
		relativeEnd := strings.IndexByte(line[start+1:], '*')
		if relativeEnd < 0 {
			return line
		}
		end := start + 1 + relativeEnd
		line = line[:start] + line[start+1:end] + line[end+1:]
	}
}

func wrapConversationText(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	return strings.Split(lipgloss.Wrap(text, max(1, width), " "), "\n")
}

func (m *tuiModel) renderConversation() (string, map[int]string, []string) {
	width := max(1, m.chat.Width())
	colors := colorsFor(m.preferences.Theme)
	lines := make([]string, 0, len(m.entries)*3)
	clickTargets := make(map[int]string)
	foldable := make([]string, 0)

	appendLine := func(line, target string) {
		if strings.ContainsAny(line, "\r\n") {
			for _, part := range strings.Split(strings.ReplaceAll(line, "\r\n", "\n"), "\n") {
				lines = append(lines, part)
				if target != "" {
					clickTargets[len(lines)-1] = target
				}
			}
			return
		}
		lines = append(lines, line)
		if target != "" {
			clickTargets[len(lines)-1] = target
		}
	}

	for _, entry := range m.entries {
		// Live tool activity is shown in the activity pane or compact activity
		// line. Keep historical tool events in the transcript after a restore.
		if entry.role == "tool" && entry.activityID != "" &&
			(m.modernConversation() || m.studioLayout() || m.detailsOpen) {
			continue
		}
		if strings.TrimSpace(entry.text) == "" {
			continue
		}
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}

		if entry.role == "you" {
			text := plainConversationText(entry.text)
			blockWidth := width
			rightAlign := m.width >= 96
			if rightAlign {
				blockWidth = max(1, width*3/4)
			}
			wrapped := wrapConversationText(text, blockWidth)
			isFoldable := len(wrapped) > messageCollapseLines || len([]rune(text)) > messageCollapseRunes
			if isFoldable {
				foldable = append(foldable, entry.collapseKey)
			}
			expanded := isFoldable && m.preferences.ExpandedMessages[entry.collapseKey]
			shown := wrapped
			if isFoldable && !expanded {
				shown = wrapped[:min(messagePreviewLines, len(wrapped))]
			}
			indent := 0
			if rightAlign {
				indent = width - blockWidth
			}
			marker := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.User)).Bold(true).Render("▸ YOU")
			if rightAlign {
				marker = lipgloss.PlaceHorizontal(blockWidth, lipgloss.Right, marker)
				marker = strings.Repeat(" ", indent) + marker
			}
			clickTarget := ""
			if isFoldable {
				clickTarget = entry.collapseKey
			}
			appendLine(marker, clickTarget)
			for _, line := range shown {
				if rightAlign {
					line = strings.Repeat(" ", indent) + lipgloss.PlaceHorizontal(blockWidth, lipgloss.Right, line)
				}
				appendLine(line, "")
			}
			if isFoldable {
				label := "Show less"
				if !expanded {
					label = fmt.Sprintf("Show more · %d lines", len(wrapped))
				}
				label = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(label)
				if rightAlign {
					label = strings.Repeat(" ", indent) + "  " + label
				}
				appendLine(label, entry.collapseKey)
			}
			continue
		}

		text := plainConversationText(entry.text)
		prefix := ""
		prefixStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		switch entry.role {
		case "assistant":
		case "tool":
			prefix = "  ↳ "
		case "notice":
			prefix = "· "
		case "permission":
			prefix = "! "
			prefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Warning))
		case "error":
			prefix = "! "
			prefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))
		case "queued":
			prefix = "Queued · "
		}
		if prefix != "" {
			text = prefixStyle.Render(prefix) + text
		}
		for _, line := range wrapConversationText(text, width) {
			appendLine(line, "")
		}
	}

	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(
			"Start a task, or use /resume to browse saved sessions."))
	}
	return strings.Join(lines, "\n"), clickTargets, foldable
}

func (m *tuiModel) toggleLatestLongMessage() bool {
	if len(m.foldableMessages) == 0 {
		m.status = "No long user message to expand or collapse."
		return false
	}
	key := m.foldableMessages[len(m.foldableMessages)-1]
	m.toggleMessageExpansion(key)
	return true
}

func (m *tuiModel) toggleMessageExpansion(key string) {
	if key == "" {
		return
	}
	if m.preferences.ExpandedMessages == nil {
		m.preferences.ExpandedMessages = make(map[string]bool)
	}
	expanded := !m.preferences.ExpandedMessages[key]
	if expanded {
		m.preferences.ExpandedMessages[key] = true
		m.status = "Long message expanded."
	} else {
		delete(m.preferences.ExpandedMessages, key)
		if len(m.preferences.ExpandedMessages) == 0 {
			m.preferences.ExpandedMessages = nil
		}
		m.status = "Long message collapsed."
	}
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status += " This change could not be saved."
	}
	m.refreshPanels()
	m.updateLayout()
}

func (m *tuiModel) forgetExpandedMessages(sessionID string) {
	if len(m.preferences.ExpandedMessages) == 0 {
		return
	}
	prefix := sessionID + ":"
	changed := false
	for key := range m.preferences.ExpandedMessages {
		if strings.HasPrefix(key, prefix) {
			delete(m.preferences.ExpandedMessages, key)
			changed = true
		}
	}
	if changed {
		if len(m.preferences.ExpandedMessages) == 0 {
			m.preferences.ExpandedMessages = nil
		}
		_ = saveUIPreferences(m.preferencesPath, m.preferences)
	}
}

func (m *tuiModel) handleConversationClick(message tea.MouseClickMsg) bool {
	if m.detailsOpen || m.keymapOpen || m.sessionPicker || m.modelPicker ||
		m.effortPicker || m.queuePicker || m.permissionMenu || m.renamingSession || message.Button != tea.MouseLeft {
		return false
	}
	chatWidth := m.chat.Width()
	if message.X >= chatWidth {
		return false
	}
	headerHeight := lipgloss.Height(m.renderHeader(max(1, m.width)))
	row := message.Y - headerHeight
	if row < 0 || row >= m.chat.Height() {
		return false
	}
	contentRow := row + m.chat.YOffset()
	if key := m.chatClickTargets[contentRow]; key != "" {
		m.toggleMessageExpansion(key)
		return true
	}
	return false
}

func (m *tuiModel) compactActivitySummary() string {
	if len(m.activities) == 0 {
		return ""
	}
	activity := m.activities[len(m.activities)-1]
	colors := colorsFor(m.preferences.Theme)
	icon := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Render(activityBadge(activity.status))
	return fitTerminalText(icon+" "+activity.summary+" · "+activity.status, max(1, m.width))
}

func (m *tuiModel) activitySidebarContent() string {
	colors := colorsFor(m.preferences.Theme)
	lines := []string{"Activity"}
	if len(m.activities) == 0 {
		lines = append(lines, "No tool activity yet.")
	} else {
		start := max(0, len(m.activities)-5)
		for _, activity := range m.activities[start:] {
			icon := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Render(activityBadge(activity.status))
			lines = append(lines, icon+" "+activity.summary+" · "+activity.status)
		}
	}
	lines = append(lines, "", "Usage")
	if m.sessionUsage.requests == 0 {
		lines = append(lines, "No model requests yet.")
	} else if m.sessionUsage.measured == 0 {
		lines = append(lines, "Usage unavailable")
	} else {
		lines = append(lines,
			"Input      "+compactTokens(m.sessionUsage.input),
			"Output     "+compactTokens(m.sessionUsage.output),
			"Reasoning  "+compactTokens(m.sessionUsage.reasoning),
			"Total      "+compactTokens(m.sessionUsage.total),
			"Cached     "+compactTokens(m.sessionUsage.cached),
		)
	}
	return strings.Join(lines, "\n")
}
