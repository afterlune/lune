package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// lightLayoutGeometry is shared by rendering, cursor placement and hit testing.
type lightLayoutGeometry struct {
	margin, width, header, body, search, pill, activity, bottomY int
	bottom                                                       string
}

func (m *tuiModel) renderLightCommandSuggestions(maxLines int) string {
	if m.permissionMenu || len(m.commandSuggestions) == 0 || maxLines <= 0 {
		return ""
	}
	width := m.lightPanelWidth()
	visible := min(maxCommandSuggestions, maxLines)
	start, end := commandSuggestionRangeVisible(m.suggestionIndex, len(m.commandSuggestions), visible)
	left := 1
	for _, name := range m.commandSuggestions {
		left = max(left, lipgloss.Width(name))
	}
	left = min(left, max(1, width-5))
	var rows []string
	for i := start; i < end; i++ {
		name := m.commandSuggestions[i]
		description := ""
		for _, spec := range tuiCommands {
			if spec.name == name {
				description = spec.description
				break
			}
		}
		bg := codexLight.Canvas
		fg := codexLight.Primary
		muted := codexLight.Muted
		marker := "  "
		if i == m.suggestionIndex {
			bg = "#A4CDFB"
			muted = fg
			marker = "› "
		}
		label := truncateQuestionText(name, left)
		padding := strings.Repeat(" ", max(0, left-lipgloss.Width(label))+2)
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Bold(i == m.suggestionIndex)
		detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(muted)).Background(lipgloss.Color(bg))
		line := labelStyle.Render(marker+label+padding) + detailStyle.Render(truncateQuestionText(description, max(0, width-left-4)))
		rows = append(rows, fillQuestionRow(line, width, lipgloss.Color(bg)))
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) lightPanelWidth() int { return max(1, m.width-2*min(2, max(0, (m.width-1)/2))) }

func (m *tuiModel) lightBottom() string {
	if m.questionOpen && m.question != nil {
		return m.renderQuestion()
	}
	if m.planReview {
		return m.renderPlanReview()
	}
	parts := []string{renderLightComposerBand(m.renderInputView(), m.lightPanelWidth())}
	suggestions := m.renderCommandSuggestions()
	if aux := m.renderAuxiliaryPanel(suggestions); aux != "" && aux != suggestions {
		parts = append(parts, aux)
	}
	parts = append(parts, m.renderFooter(m.lightPanelWidth()))
	return strings.Join(parts, "\n")
}

func (m *tuiModel) lightGeometry() lightLayoutGeometry {
	g := lightLayoutGeometry{margin: min(2, max(0, (m.width-1)/2)), width: m.lightPanelWidth()}
	if m.lightStickyPrompt() != "" {
		g.header = 1
	}
	if m.searchOpen {
		g.search = 1
	}
	if m.scrolledAwayFromBottom() {
		g.pill = 1
	}
	if m.terminalState() == terminalRunning {
		g.activity = 2
	}
	g.bottom = m.lightBottom()
	g.body = max(0, m.height-g.header-g.search-g.pill-g.activity-lipgloss.Height(g.bottom))
	g.bottomY = g.header + g.body + g.search + g.pill + g.activity
	return g
}

func (m *tuiModel) updateLightLayout() {
	width := m.lightPanelWidth()
	m.input.SetWidth(max(1, width-2))
	if m.question != nil {
		m.question.input.SetWidth(max(1, width-4))
		applyTextareaTheme(&m.question.input, m.resolvedTheme())
	}
	m.updateMCPInteractionWidth(width)
	g := m.lightGeometry()
	m.chat.SoftWrap = false
	m.chat.SetWidth(width)
	m.chat.SetHeight(g.body)
	m.activity.SetWidth(width)
	m.activity.SetHeight(g.body)
}

func (m *tuiModel) lightStickyPrompt() string {
	offset := m.chat.YOffset()
	for i := min(offset-1, len(m.lightVisualRoles)-1); i >= 0; i-- {
		if m.lightVisualRoles[i] == "you" {
			start := i
			for start > 0 && m.lightVisualRoles[start-1] == "you" {
				start--
			}
			if start < len(m.lightVisualLines) {
				return fitTerminalText(m.lightVisualLines[start].text, m.lightPanelWidth())
			}
		}
	}
	return ""
}

func (m *tuiModel) renderLightWorkspace() tea.View {
	g := m.lightGeometry()
	parts := []string{}
	if g.header > 0 {
		parts = append(parts, lipgloss.NewStyle().Background(lipgloss.Color("#F9F9F9")).Width(g.width).Render(m.lightStickyPrompt()))
	}
	if g.body > 0 {
		body := m.renderBody(g.width)
		rows := fitWorkspaceColumn(body, g.width, g.body)
		if suggestions := m.renderCommandSuggestionsForHeight(g.body); suggestions != "" && m.renderAuxiliaryPanel(m.renderCommandSuggestions()) == m.renderCommandSuggestions() {
			popup := strings.Split(suggestions, "\n")
			for i, line := range popup {
				rows[len(rows)-len(popup)+i] = fitTerminalText(line, g.width)
			}
		}
		parts = append(parts, strings.Join(rows, "\n"))
	}
	if g.search > 0 {
		parts = append(parts, m.renderTranscriptSearch())
	}
	if g.pill > 0 {
		parts = append(parts, renderScrollPill(g.width, "esc"))
	}
	if g.activity > 0 {
		parts = append(parts, "", m.renderLiveActivity(g.width))
	}
	parts = append(parts, g.bottom)
	rows := strings.Split(strings.Join(parts, "\n"), "\n")
	clipped := max(0, len(rows)-m.height)
	if len(rows) > m.height {
		rows = rows[len(rows)-m.height:]
	}
	for i, line := range rows {
		rows[i] = strings.Repeat(" ", g.margin) + fitTerminalText(line, g.width)
	}
	view := tea.NewView(strings.Join(rows, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	view.ForegroundColor = lipgloss.Color(m.themeColors().Foreground)
	if m.preferences.Theme == themeLight {
		view.BackgroundColor = lipgloss.Color(codexLight.Canvas)
	}
	if cursor := m.activeCursor(); cursor != nil && !m.planReview && !m.keymapOpen && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker && (!m.sessionPicker || m.renamingSession) {
		cursor.X += g.margin
		cursor.Y += g.bottomY - clipped
		if m.questionOpen && m.question != nil {
			cursor.X += 2
			cursor.Y += m.question.cursorRow
		} else {
			cursor.X++
			cursor.Y++
		}
		if cursor.Y >= 0 && cursor.Y < m.height {
			view.Cursor = cursor
		}
	}
	return view
}
