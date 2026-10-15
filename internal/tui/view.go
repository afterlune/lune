package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

const maxCommandSuggestions = 8

func (m *tuiModel) refreshPanels() {
	followLatest := m.chat.AtBottom()
	if m.lastTranscriptSessionID != "" && m.lastTranscriptSessionID != m.sessionID {
		followLatest = true
		m.unreadEntries = 0
	}
	if followLatest {
		m.unreadEntries = 0
	} else if len(m.entries) > m.lastRenderedEntryCount {
		m.unreadEntries += len(m.entries) - m.lastRenderedEntryCount
	}
	oldOffset := m.chat.YOffset()
	content, roles := m.transcriptContent()
	m.chat.SetContent(content)
	colors := m.themeColors()
	m.chat.StyleLineFunc = func(line int) lipgloss.Style {
		role := "assistant"
		if line >= 0 && line < len(roles) {
			role = roles[line]
		}
		return transcriptRoleStyle(role, colors)
	}
	m.chat.HighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color(colors.Secondary)).Foreground(lipgloss.Color(colors.Foreground))
	m.chat.SelectedHighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color(colors.Accent)).Foreground(lipgloss.Color(colors.Background))
	if followLatest {
		m.chat.GotoBottom()
	} else {
		m.chat.SetYOffset(oldOffset)
	}
	m.lastTranscriptSessionID = m.sessionID
	m.lastRenderedEntryCount = len(m.entries)
	if m.searchOpen {
		m.searchMatches = findTranscriptMatches(content, m.searchQuery)
	}
	m.applyTranscriptHighlights()
	m.activity.SetContent(m.activityDetailsContent())
}

func (m *tuiModel) updateLayout() {
	if m.width <= 0 {
		m.width = 80
	}
	if m.height <= 0 {
		m.height = 24
	}
	contentWidth := max(1, m.width)
	inputWidth := contentWidth
	m.input.SetWidth(inputWidth)
	if m.question != nil {
		m.question.input.KeyMap = m.input.KeyMap
		m.question.input.SetWidth(max(1, inputWidth-2))
		applyTextareaTheme(&m.question.input, m.resolvedTheme())
	}
	m.updateMCPInteractionWidth(inputWidth)
	bodyHeight := m.availableBodyHeight(contentWidth, lipgloss.Height(m.renderFooter(inputWidth)), 1)
	m.chat.SoftWrap = true
	m.chat.SetWidth(inputWidth)
	m.chat.SetHeight(bodyHeight)
	m.activity.SetHeight(max(1, bodyHeight))
	m.activity.SetWidth(inputWidth)
}

func (m *tuiModel) availableBodyHeight(contentWidth, footerHeight, dividerCount int) int {
	inputHeight := m.input.Height()
	if m.questionOpen && m.question != nil {
		inputHeight = lipgloss.Height(m.renderQuestion())
	}
	bodyHeight := max(1, m.height-dividerCount-inputHeight-m.commandSuggestionHeight()-footerHeight)
	if m.searchOpen {
		bodyHeight = max(1, bodyHeight-1)
	}
	if m.pendingMCPInteraction != nil {
		bodyHeight = max(1, bodyHeight-lipgloss.Height(m.renderMCPInteraction()))
	}
	if m.keymapOpen {
		bodyHeight = max(1, bodyHeight-m.keymapEditorHeight())
	}
	return bodyHeight
}

func applyTextareaTheme(input *textarea.Model, theme themeName) {
	colors := colorsFor(theme)
	styles := textarea.DefaultStyles(theme != themeLight)
	apply := func(state *textarea.StyleState) {
		state.Base = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground))
		state.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground))
		state.LineNumber = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.CursorLineNumber = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.CursorLine = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground))
		state.EndOfBuffer = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent))
		state.Selection = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(lipgloss.Color(colors.Secondary))
	}
	apply(&styles.Focused)
	apply(&styles.Blurred)
	styles.Cursor.Color = lipgloss.Color(colors.Accent)
	input.SetStyles(styles)
}

func (m *tuiModel) commandSuggestionHeight() int {
	if m.planReview {
		return len(planReviewChoices) + 2
	}
	if m.queuePicker {
		return min(len(m.queued), 8) + 1
	}
	if m.sessionPicker {
		if m.renamingSession || m.deleteConfirm {
			return 2
		}
		return min(len(m.sessionList), 8) + 2
	}
	if m.modelPicker {
		return min(len(m.modelOptions), 8) + 2
	}
	if m.effortPicker {
		return len(reasoningEffortOptions) + 2
	}
	if m.permissionMenu {
		extraLines := 0
		if m.planMode {
			extraLines = 1
		}
		return len(tuiPermissionOptions) + 2 + extraLines
	}
	if len(m.commandSuggestions) == 0 {
		return 0
	}
	return 0
}

func (m *tuiModel) View() tea.View {
	view := m.renderSessionWorkspace(max(1, m.width))
	view.WindowTitle = m.windowTitleText()
	return view
}

func (m *tuiModel) renderAuxiliaryPanel(suggestions string) string {
	switch {
	case m.exitConfirm:
		return "Interrupt and cancel the queued turns [i] · wait for completion [w] · return [esc]"
	case m.pendingMCPInteraction != nil:
		return m.renderMCPInteraction()
	case m.keymapOpen:
		return m.renderKeymapEditor()
	case m.sessionPicker:
		return m.renderSessionPicker()
	case m.modelPicker:
		return m.renderModelPicker()
	case m.effortPicker:
		return m.renderEffortPicker()
	case m.queuePicker:
		return m.renderQueuePicker()
	case m.planReview:
		return m.renderPlanReview()
	case m.permissionMenu:
		return m.renderPermissionMenu()
	default:
		return suggestions
	}
}

func (m *tuiModel) renderSessionWorkspace(width int) tea.View {
	colors := m.themeColors()
	body := m.renderSessionWorkspaceBody(width)
	centerWidth := max(1, width)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", centerWidth))
	input := m.renderWorkspaceRow(m.renderInputView(), centerWidth)
	if m.questionOpen && m.question != nil {
		input = m.renderWorkspaceRow(m.renderQuestion(), centerWidth)
	}
	suggestions := m.renderCommandSuggestions()
	parts := []string{body}
	if m.searchOpen {
		parts = append(parts, m.renderWorkspaceRow(m.renderTranscriptSearch(), centerWidth))
	}
	parts = append(parts, m.renderWorkspaceRow(divider, centerWidth), input)
	if auxiliary := m.renderAuxiliaryPanel(suggestions); !m.questionOpen && auxiliary != "" && auxiliary != suggestions {
		parts = append(parts, m.renderWorkspaceRow(auxiliary, centerWidth))
	}
	parts = append(parts, m.renderWorkspaceRow(m.renderFooter(centerWidth), centerWidth))
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.ForegroundColor = backgroundColor(colors.Foreground)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	if cursor := m.activeCursor(); cursor != nil && !m.questionOpen && !m.keymapOpen && (!m.sessionPicker || m.renamingSession) && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker {
		cursor.Y += lipgloss.Height(body)
		if m.searchOpen {
			cursor.Y++
		}
		cursor.Y += lipgloss.Height(divider)
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderWorkspaceRow(content string, centerWidth int) string {
	content = lipgloss.NewStyle().Width(centerWidth).Render(content)
	lines := strings.Split(content, "\n")
	rows := make([]string, len(lines))
	for index, line := range lines {
		if lipgloss.Width(line) < centerWidth {
			line += strings.Repeat(" ", centerWidth-lipgloss.Width(line))
		} else if lipgloss.Width(line) > centerWidth {
			line = fitTerminalText(line, centerWidth)
		}
		rows[index] = line
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) renderFooter(width int) string {
	colors := m.themeColors()
	model := strings.TrimSpace(m.sessionSettings.ModelProfile)
	if model == "" {
		model = "default model"
	}
	metadata := model + " · " + reasoningEffortLabel(m.sessionSettings.ReasoningEffort)
	if m.client != nil {
		metadata += " · " + permissionModeLabel(m.currentPermissionMode())
	}
	if m.planMode {
		metadata += " · Plan"
	}

	status := m.status
	switch {
	case m.detailsOpen:
		status = "Activity & usage · " + m.keyLabel("toggle_activity") + " or Esc close"
	case m.activityFocus:
		status = "Tool activity · " + m.keyLabel("activity_previous") + "/" + m.keyLabel("activity_next") + " select · Enter expand · Esc return"
	case m.searchOpen:
		status = "Transcript search · " + m.keyLabel("search_previous") + "/" + m.keyLabel("search_next") + " match · Enter keep · Esc close"
	case m.pendingMCPInteraction != nil:
		status = m.mcpInteractionFooter()
	case m.mcpAuthCancel != nil:
		status = m.mcpAuthFooter()
	case m.pendingApproval != nil:
		status = "Approval required · " + m.keyLabel("approve_tool") + " allow once · " + m.keyLabel("deny_tool") + " deny · Esc pause"
	case m.questionOpen:
		status = "Waiting for your answer"
	case m.running:
		status = fmt.Sprintf("Working (%s) · %d queued · Esc interrupt", m.taskElapsed(), len(m.queued))
	case m.pending:
		status = "Paused · /continue resume · /discard"
	case m.status == "Ready":
		status = "Ready · " + m.keyLabel("send") + " send · " + m.keyLabel("input_newline") + " newline · " + m.keyLabel("cycle_permissions") + " modes · " +
			m.keyLabel("search_open") + " find · " + m.keyLabel("focus_activity") + " tools · /usage · /plan"
	}
	if m.vimMode {
		vimState := "VIM INSERT"
		if m.vimNormal {
			vimState = "VIM NORMAL"
		}
		status = vimState + " · " + status
	}
	if m.running && m.modelStreamFirstTokenRecorded {
		status += " · first token " + m.modelStreamFirstTokenLatency.Round(time.Millisecond).String()
	}
	if !m.chat.AtBottom() && !m.detailsOpen {
		latest := m.keyLabel("jump_chat_bottom") + " latest"
		if m.unreadEntries > 0 {
			latest = fmt.Sprintf("%d new · %s", m.unreadEntries, latest)
		}
		status += " · " + latest
	}
	if len(m.attachments) > 0 {
		status += fmt.Sprintf(" · %d image(s)", len(m.attachments))
	}

	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent))
	if context := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); context != "" {
		metadata += " · " + context
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		muted.Render(fitTerminalText(metadata, width)),
		statusStyle.Render(fitTerminalText(status, width)),
	)
}

func (m *tuiModel) renderTranscriptSearch() string {
	colors := m.themeColors()
	count := fmt.Sprintf("%d matches", len(m.searchMatches))
	if m.searchQuery == "" {
		count = "type to search"
	}
	text := "Find  " + m.searchQuery + "  ·  " + count
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(fitTerminalText(text, m.width))
}

func fitTerminalText(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func (m *tuiModel) renderQueuePicker() string {
	colors := m.themeColors()
	lines := []string{"Core queue · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " details · " + m.keyLabel("queue_remove") + " cancel · Esc close"}
	start := max(0, m.queueIndex-4)
	end := min(len(m.queued), start+8)
	if end == len(m.queued) {
		start = max(0, end-8)
	}
	for index, item := range m.queued[start:end] {
		index += start
		line := fmt.Sprintf("  %d. %-8s execution %s", item.Position, item.Status, item.ExecutionID)
		if index == m.queueIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPlanReview() string {
	colors := m.themeColors()
	lines := []string{lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Warning)).Render("Plan ready · choose an action")}
	selected := min(max(m.planReviewIndex, 0), len(planReviewChoices)-1)
	for index, option := range planReviewChoices {
		line := "  " + option
		if index == selected {
			line = "› " + option
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" choose · type feedback to revise · Esc stay read-only")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderCommandSuggestions() string {
	return m.renderCommandSuggestionsForHeight(maxCommandSuggestions + 1)
}

func (m *tuiModel) renderCommandSuggestionsForHeight(maxLines int) string {
	colors := m.themeColors()
	if m.permissionMenu || len(m.commandSuggestions) == 0 || maxLines <= 0 {
		return ""
	}
	commandRows := min(maxCommandSuggestions, max(1, maxLines-1))
	if maxLines == 1 {
		commandRows = 1
	}
	start, end := commandSuggestionRangeVisible(m.suggestionIndex, len(m.commandSuggestions), commandRows)
	lines := make([]string, 0, min(maxLines, end-start+1))
	for index := start; index < end; index++ {
		command := m.commandSuggestions[index]
		line := "  " + command
		if index == m.suggestionIndex {
			line = "› " + command
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	if maxLines > 1 {
		hint := "  " + m.keyLabel("suggestion_previous") + "/" + m.keyLabel("suggestion_next") + " select · " + m.keyLabel("send") + " run"
		if len(m.commandSuggestions) > end-start {
			hint = fmt.Sprintf("  %d–%d of %d · %s", start+1, end, len(m.commandSuggestions), strings.TrimPrefix(hint, "  "))
		}
		lines = append(lines, hint)
	}
	return strings.Join(lines, "\n")
}

func commandSuggestionRange(index, total int) (int, int) {
	return commandSuggestionRangeVisible(index, total, maxCommandSuggestions)
}

func commandSuggestionRangeVisible(index, total, visible int) (int, int) {
	visible = min(total, max(0, visible))
	if visible <= 0 {
		return 0, 0
	}
	index = max(0, min(index, total-1))
	start := max(0, min(index-visible/2, total-visible))
	return start, start + visible
}

func (m *tuiModel) renderKeymapEditor() string {
	colors := m.themeColors()
	count := len(keymapActions) + 1
	rows := m.keymapEditorRows()
	start := max(0, m.keymapIndex-rows/2)
	end := min(count, start+rows)
	if end == count {
		start = max(0, end-rows)
	}
	heading := "Key bindings · ↑/↓ choose · Enter change · Esc apply and close"
	if m.keymapCapture != "" {
		heading = "Recording a binding · press one key combination · Esc cancels capture"
	}
	lines := []string{heading}
	for index := start; index < end; index++ {
		var label, chord string
		if index == len(keymapActions) {
			label, chord = "Restore defaults", ""
		} else {
			action := keymapActions[index]
			label = action.label
			chord = m.keymapDraft[action.id]
		}
		line := fmt.Sprintf("  %-38s %s", label, chord)
		if index == m.keymapIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	if m.keymapCapture == "" {
		lines = append(lines, "Esc is reserved for cancel/close · bindings apply when this menu closes")
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) keymapEditorRows() int {
	return min(9, max(1, m.height-10))
}

func (m *tuiModel) keymapEditorHeight() int {
	return m.keymapEditorRows() + 2
}

func (m *tuiModel) renderSessionPicker() string {
	colors := m.themeColors()
	if m.renamingSession {
		return strings.Join([]string{"Rename session · Enter save · empty clears · Esc cancel", "Title is limited to 120 characters."}, "\n")
	}
	if m.deleteConfirm {
		if m.sessionIndex >= 0 && m.sessionIndex < len(m.sessionList) {
			return strings.Join([]string{
				"Delete “" + sessionDisplayTitle(m.sessionList[m.sessionIndex]) + "” and all saved task state?",
				m.keyLabel("session_confirm_delete") + " confirm · " + m.keyLabel("session_cancel_delete") + "/Esc cancel",
			}, "\n")
		}
		return m.keyLabel("session_confirm_delete") + " confirm · " + m.keyLabel("session_cancel_delete") + "/Esc cancel"
	}
	if len(m.sessionList) == 0 {
		if m.showArchived {
			return "No saved sessions · " + m.keyLabel("session_toggle_archived") + " toggle · Esc cancel"
		}
		return "No active sessions · " + m.keyLabel("session_toggle_archived") + " show archived · Esc cancel"
	}
	viewKind := "active"
	if m.showArchived {
		viewKind = "active + archived"
	}
	lines := []string{"Sessions · " + viewKind + " · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " restore · " + m.keyLabel("session_archive") + " archive · " + m.keyLabel("session_rename") + " rename · " + m.keyLabel("session_delete") + " delete · " + m.keyLabel("session_toggle_archived") + " toggle"}
	start := max(0, m.sessionIndex-4)
	end := min(len(m.sessionList), start+8)
	if end == len(m.sessionList) {
		start = max(0, end-8)
	}
	for index, saved := range m.sessionList[start:end] {
		index += start
		preview := sessionDisplayTitle(saved)
		line := fmt.Sprintf("  %s  %s", saved.UpdatedAt.Local().Format("01-02 15:04"), preview)
		if saved.Archived {
			line += "  [archived]"
		}
		if saved.ID == m.sessionID {
			line += "  (current)"
		}
		if index == m.sessionIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderModelPicker() string {
	colors := m.themeColors()
	lines := []string{"Model profile · current: " + m.sessionSettings.ModelProfile}
	start := max(0, m.modelIndex-4)
	end := min(len(m.modelOptions), start+8)
	if end == len(m.modelOptions) {
		start = max(0, end-8)
	}
	for index, option := range m.modelOptions[start:end] {
		index += start
		line := fmt.Sprintf("  %-20s %s / %s", option.Profile, option.Provider, option.Model)
		if index == m.modelIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderEffortPicker() string {
	colors := m.themeColors()
	lines := []string{"Reasoning effort · current: " + reasoningEffortLabel(m.sessionSettings.ReasoningEffort)}
	for index, value := range reasoningEffortOptions {
		line := "  " + reasoningEffortLabel(value)
		if index == m.effortIndex {
			line = "› " + reasoningEffortLabel(value)
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPermissionMenu() string {
	colors := m.themeColors()
	mode := core.PermissionReadOnly
	if m.client != nil {
		mode = m.currentPermissionMode()
	}
	lines := []string{"Permission mode · current: " + permissionModeLabel(mode)}
	if m.planMode {
		lines = append(lines, "Plan caps tools at read-only; permission controls approval for visible tools such as web.")
	}
	for index, option := range tuiPermissionOptions {
		line := fmt.Sprintf("  %-12s %s", option.label, option.description)
		if index == m.permissionIndex {
			line = fmt.Sprintf("› %-12s %s", option.label, option.description)
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderBody(width int) string {
	if m.detailsOpen {
		return m.activity.View()
	}
	return m.chat.View()
}

func (m *tuiModel) renderSessionWorkspaceBody(width int) string {
	centerWidth := max(1, width)
	bodyHeight := m.chat.Height()
	center := m.renderBody(width)
	centerLines := fitWorkspaceColumn(center, centerWidth, bodyHeight)
	if suggestions := m.renderCommandSuggestions(); suggestions != "" && m.renderAuxiliaryPanel(suggestions) == suggestions {
		popupLines := strings.Split(m.renderCommandSuggestionsForHeight(len(centerLines)), "\n")
		start := len(centerLines) - len(popupLines)
		for index, line := range popupLines {
			line = fitTerminalText(line, centerWidth)
			centerLines[start+index] = line + strings.Repeat(" ", max(0, centerWidth-lipgloss.Width(line)))
		}
	}
	return strings.Join(centerLines, "\n")
}

func fitWorkspaceColumn(content string, width, height int) []string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		if lipgloss.Width(line) > width {
			line = fitTerminalText(line, width)
		}
		lines[index] = line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
	}
	return lines
}

func responseDisplayText(response json.RawMessage) (string, error) {
	if len(response) == 0 {
		return "", nil
	}
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	lines := make([]string, 0, len(envelope.Output))
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			return "", err
		}
		switch value.Type {
		case "message":
			if text := messageContentText(value.Content); text != "" {
				lines = append(lines, text)
			}
		case "reasoning", "function_call", "custom_tool_call":
		default:
			lines = append(lines, string(item))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}
