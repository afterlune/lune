package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

const maxCommandSuggestions = 8

func (m *tuiModel) refreshPanels() {
	followLatest := m.autoScroll
	if m.lastTranscriptSessionID != "" && m.lastTranscriptSessionID != m.sessionID {
		followLatest = true
		m.autoScroll = true
		m.unreadEntries = 0
	}
	if followLatest {
		m.unreadEntries = 0
	} else if len(m.entries) > m.lastRenderedEntryCount {
		m.unreadEntries += len(m.entries) - m.lastRenderedEntryCount
	}
	oldOffset := m.chat.YOffset()
	lines := m.transcriptLines()
	content, roles := transcriptContent(lines)
	if m.lightLayout() {
		m.lightVisualLines = wrapTranscriptContent(content, max(1, m.chat.Width()))
		m.lightVisualRoles = visualTranscriptRoles(lines, m.lightVisualLines)
		m.lightCodeRanges = m.inlineCodeRanges(content)
	} else {
		m.lightVisualLines = nil
		m.lightVisualRoles = nil
		m.lightCodeRanges = nil
	}
	m.chat.SetContent(content)
	colors := m.themeColors()
	m.chat.StyleLineFunc = nil
	if !m.lightLayout() {
		m.chat.StyleLineFunc = func(line int) lipgloss.Style {
			role := "assistant"
			if line >= 0 && line < len(roles) {
				role = roles[line]
			}
			style := transcriptRoleStyle(role, colors)
			return style
		}
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

func visualTranscriptRoles(lines []transcriptLine, visual []transcriptVisualLine) []string {
	roles := make([]string, len(visual))
	if len(lines) == 0 {
		return roles
	}
	starts := make([]int, len(lines))
	position := 0
	for index, line := range lines {
		starts[index] = position
		position += len(line.text) + 1
	}
	logical := 0
	for index, line := range visual {
		for logical+1 < len(starts) && line.start >= starts[logical+1] {
			logical++
		}
		roles[index] = lines[logical].role
	}
	return roles
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
	composerInputWidth := inputWidth
	if m.lightLayout() {
		composerInputWidth = max(1, inputWidth-2)
	}
	m.input.SetWidth(composerInputWidth)
	if m.question != nil {
		m.question.input.KeyMap = m.input.KeyMap
		m.question.input.SetWidth(m.questionInputWidth())
		applyTextareaTheme(&m.question.input, m.resolvedTheme())
	}
	m.updateMCPInteractionWidth(inputWidth)
	dividerCount := 1
	composerExtra := 0
	scrollPillRows := 0
	if m.lightLayout() {
		dividerCount = 0
		if !m.planReview && !(m.questionOpen && m.question != nil) {
			composerExtra = 2
		}
		if m.scrolledAwayFromBottom() {
			scrollPillRows = 1
		}
	}
	chromeRows := 1 + 2 // session header, then a blank separator and live activity row
	bodyHeight := m.availableBodyHeight(contentWidth, lipgloss.Height(m.renderFooter(inputWidth)), dividerCount) - composerExtra - scrollPillRows - chromeRows
	bodyHeight = max(1, bodyHeight)
	m.chat.SoftWrap = true
	m.chat.SetWidth(inputWidth)
	m.chat.SetHeight(bodyHeight)
	m.activity.SetHeight(max(1, bodyHeight))
	m.activity.SetWidth(inputWidth)
}

func (m *tuiModel) availableBodyHeight(contentWidth, footerHeight, dividerCount int) int {
	inputHeight := m.input.Height()
	if m.planReview {
		inputHeight = 0
	} else if m.questionOpen && m.question != nil {
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

func (m *tuiModel) scrolledAwayFromBottom() bool {
	return m.lightLayout() && !m.detailsOpen && !m.chat.AtBottom()
}

func (m *tuiModel) handleScrollPillClick(x, y int) bool {
	if !m.scrolledAwayFromBottom() {
		return false
	}
	pillY := 1 + m.chat.Height()
	if m.searchOpen {
		pillY++
	}
	pillWidth := lipgloss.Width(scrollPillContent("esc"))
	left := max(0, (m.width-pillWidth)/2)
	if y != pillY || x < left || x >= left+pillWidth {
		return false
	}
	m.jumpToLatest()
	return true
}

func applyTextareaTheme(input *textarea.Model, theme themeName) {
	colors := colorsFor(theme)
	if input.Placeholder == defaultInputPlaceholder || input.Placeholder == lightInputPlaceholder {
		if theme == themeLight {
			input.Placeholder = lightInputPlaceholder
		} else {
			input.Placeholder = defaultInputPlaceholder
		}
	}
	styles := textarea.DefaultStyles(theme != themeLight)
	styleFor := func(foreground string) lipgloss.Style {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(foreground))
		if theme == themeLight {
			style = style.Background(lipgloss.Color(codexLight.Surface))
		}
		return style
	}
	apply := func(state *textarea.StyleState) {
		state.Base = styleFor(colors.Foreground)
		state.Text = styleFor(colors.Foreground)
		state.LineNumber = styleFor(colors.Muted)
		state.CursorLineNumber = styleFor(colors.Muted)
		state.CursorLine = styleFor(colors.Foreground)
		state.EndOfBuffer = styleFor(colors.Muted)
		state.Placeholder = styleFor(colors.Muted)
		prompt := colors.Accent
		if theme == themeLight {
			prompt = codexLight.Subtle
		}
		state.Prompt = styleFor(prompt)
		state.Selection = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(lipgloss.Color(colors.Secondary))
	}
	apply(&styles.Focused)
	apply(&styles.Blurred)
	styles.Cursor.Color = lipgloss.Color(colors.Accent)
	input.SetStyles(styles)
}

func (m *tuiModel) commandSuggestionHeight() int {
	if m.planReview {
		return lipgloss.Height(m.renderWorkspaceRow(m.renderPlanReview(), max(1, m.width)))
	}
	if m.shortcutsOpen {
		return lipgloss.Height(m.renderShortcuts())
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
	case m.shortcutsOpen:
		return m.renderShortcuts()
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
	light := m.lightLayout()
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", centerWidth))
	header := m.renderWorkspaceRow(m.renderSessionHeader(centerWidth), centerWidth)
	input := m.renderWorkspaceRow(m.renderInputView(), centerWidth)
	if m.questionOpen && m.question != nil {
		input = m.renderWorkspaceRow(m.renderQuestion(), centerWidth)
	}
	suggestions := m.renderCommandSuggestions()
	parts := []string{header, body}
	if m.searchOpen {
		parts = append(parts, m.renderWorkspaceRow(m.renderTranscriptSearch(), centerWidth))
	}
	pillShown := m.scrolledAwayFromBottom()
	if pillShown {
		parts = append(parts, m.renderWorkspaceRow(renderScrollPill(centerWidth, "esc"), centerWidth))
	}
	parts = append(parts, strings.Repeat(" ", centerWidth), m.renderWorkspaceRow(m.renderLiveActivity(centerWidth), centerWidth))
	if !light {
		parts = append(parts, m.renderWorkspaceRow(divider, centerWidth))
	}
	if m.planReview && !m.questionOpen {
		parts = append(parts, m.renderWorkspaceRow(m.renderPlanReview(), centerWidth))
	} else if light {
		composer := m.renderInputView()
		if m.questionOpen && m.question != nil {
			composer = m.renderQuestion()
			parts = append(parts, composer)
		} else {
			parts = append(parts, renderLightComposerBand(composer, centerWidth))
		}
		if auxiliary := m.renderAuxiliaryPanel(suggestions); !m.questionOpen && auxiliary != "" && auxiliary != suggestions {
			parts = append(parts, m.renderWorkspaceRow(auxiliary, centerWidth))
		}
	} else {
		parts = append(parts, input)
		if auxiliary := m.renderAuxiliaryPanel(suggestions); !m.questionOpen && auxiliary != "" && auxiliary != suggestions {
			parts = append(parts, m.renderWorkspaceRow(auxiliary, centerWidth))
		}
	}
	parts = append(parts, m.renderWorkspaceRow(m.renderFooter(centerWidth), centerWidth))
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.ForegroundColor = backgroundColor(colors.Foreground)
	if m.preferences.Theme == themeLight {
		view.BackgroundColor = backgroundColor(codexLight.Canvas)
	}
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	if cursor := m.activeCursor(); cursor != nil && !m.planReview && !m.keymapOpen && (!m.sessionPicker || m.renamingSession) && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker {
		cursor.Y += 1 + lipgloss.Height(body)
		if m.searchOpen {
			cursor.Y++
		}
		if pillShown {
			cursor.Y++
		}
		cursor.Y += 2 // activity separator and status row
		if light {
			if m.questionOpen && m.question != nil {
				cursor.X += 2
				cursor.Y += m.question.cursorRow
			} else {
				cursor.X++
				cursor.Y++
			}
		} else {
			cursor.Y += lipgloss.Height(divider)
			if m.questionOpen && m.question != nil {
				cursor.X++
				cursor.Y += m.question.cursorRow
			}
		}
		view.Cursor = cursor
	}
	return view
}

func renderLightComposerBand(content string, width int) string {
	width = max(1, width)
	background := lipgloss.NewStyle().Background(lipgloss.Color(codexLight.Surface))
	whitespace := lipgloss.WithWhitespaceStyle(background)
	rows := []string{renderFullWidthBlankLine(width, lipgloss.Color(codexLight.Surface))}
	for _, line := range strings.Split(content, "\n") {
		styled := background.Render(" ") + line
		rows = append(rows, lipgloss.PlaceHorizontal(width, lipgloss.Left, styled, whitespace))
	}
	rows = append(rows, renderFullWidthBlankLine(width, lipgloss.Color(codexLight.Surface)))
	return strings.Join(rows, "\n")
}

func (m *tuiModel) renderSessionHeader(width int) string {
	title := safeWindowTitle(m.windowTitle)
	if title == "" {
		title = defaultWindowTitle
	}
	model := strings.TrimSpace(m.sessionSettings.ModelProfile)
	if model == "" {
		model = "default model"
	}
	workspace := m.workspace
	if workspace == "" {
		workspace = "workspace"
	}
	colors := m.themeColors()
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Success)).Bold(true).Render("● luneAgent")
	name := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Bold(true).Render(title)
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(workspace + " · " + model)
	return fitTerminalText(brand+"  "+name+"  "+meta, max(1, width))
}

func (m *tuiModel) renderLiveActivity(width int) string {
	if m.hasPendingUserQuestion() || m.pendingApproval != nil {
		text := "⚠ Waiting for your answer (see prompt above)"
		if m.pendingApproval != nil {
			text = "⚠ Waiting for permission approval (see prompt above)"
		}
		color := m.themeColors().Warning
		return fitTerminalText(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(text), max(1, width))
	}
	if !m.running {
		return ""
	}
	action := m.currentLiveAction()
	frame := titleSpinner[m.windowTitleFrame%len(titleSpinner)]
	label := fmt.Sprintf("%s Working · %s (%s · Esc to cancel)", frame, action, m.taskElapsed())
	colors := m.themeColors()
	color := colors.Accent
	if m.lightLayout() {
		color = codexLight.Mode
	}
	return fitTerminalText(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label), max(1, width))
}

func (m *tuiModel) currentLiveAction() string {
	for index := len(m.activities) - 1; index >= 0; index-- {
		activity := m.activities[index]
		if activity.status != "running" {
			continue
		}
		title := toolActivityTitle(activity, m.workspace)
		for prefix, replacement := range map[string]string{
			"Read ": "Reading ", "Wrote ": "Writing ", "Edited ": "Editing ", "Listed ": "Listing ",
			"Searched ": "Searching ", "Fetched ": "Fetching ", "Run ": "Running ",
		} {
			if strings.HasPrefix(title, prefix) {
				return replacement + strings.TrimPrefix(title, prefix)
			}
		}
		return title
	}
	for _, activity := range m.teamActivities {
		if activity.status == "running" {
			return "Coordinating team agents"
		}
	}
	if m.modelStreamTaskID != "" {
		return "Thinking"
	}
	return "Working"
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
	if m.lightLayout() {
		return m.renderLightFooter(width)
	}
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
	case m.planReview:
		status = "Plan review · Read-only permissions"
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
		permissionHint := m.keyLabel("cycle_permissions") + " modes"
		if m.planMode {
			permissionHint = "permissions locked"
		}
		status = "Ready · " + m.keyLabel("send") + " send · " + m.keyLabel("input_newline") + " newline · " + permissionHint + " · " +
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
	statusLine := statusStyle.Render(fitTerminalText(status, width))
	if team := m.teamStatusLabel(); team != "" {
		separator := " · "
		teamStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.teamStatusColor(colors.Success, colors.Muted)))
		available := max(0, width-lipgloss.Width(separator)-lipgloss.Width(team))
		statusLine = statusStyle.Render(fitTerminalText(status, available)) + separator + teamStyle.Render(team)
	}
	if context := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); context != "" {
		metadata += " · " + context
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		muted.Render(fitTerminalText(metadata, width)),
		statusLine,
	)
}

func (m *tuiModel) renderLightFooter(width int) string {
	width = max(1, min(width, max(1, m.width-1)))
	model := strings.TrimSpace(m.sessionSettings.ModelProfile)
	if model == "" {
		model = "default model"
	}
	model += " " + reasoningEffortLabel(m.sessionSettings.ReasoningEffort)
	cwd := m.workspace
	if cwd == "" {
		cwd = "workspace"
	}
	right := permissionModeLabel(m.currentPermissionMode()) + " · " + m.keyLabel("cycle_permissions") + " to cycle"
	if m.planMode {
		right = "Plan mode · Read-only"
	} else if m.running {
		right = permissionModeLabel(m.currentPermissionMode()) + " · locked while working"
	}
	right = fitTerminalText(right, width)
	leftBudget := max(0, width-lipgloss.Width(right)-2)
	model = fitTerminalText(model, min(24, leftBudget))
	leftBudget -= lipgloss.Width(model) + 2
	cwd = fitTerminalText(cwd, min(lipgloss.Width(cwd), max(0, leftBudget)))
	leftBudget -= lipgloss.Width(cwd) + 3
	team := m.teamStatusLabel()
	if team != "" {
		team = fitTerminalText(team, max(0, leftBudget))
		leftBudget -= lipgloss.Width(team) + 3
	}
	summary := latestUserTaskSummary(m.entries)
	if summary == "" {
		summary = "Ready"
	}
	summary = fitTerminalText(summary, max(0, leftBudget))
	left := lightMutedStyle().Render(model) + "  " + lightTextStyle().Render(cwd)
	if team != "" {
		color := m.teamStatusColor(codexLight.Success, codexLight.Muted)
		left += " · " + lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(team)
	}
	if summary != "" {
		left += " · " + lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.TaskSummary)).Render(summary)
	}
	rightStyle := lightTextStyle()
	if m.planMode {
		rightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Mode))
	}
	rightStyled := rightStyle.Render(right)
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(rightStyled))
	primary := left + strings.Repeat(" ", gap) + rightStyled
	if lipgloss.Width(primary) > width {
		primary = fitTerminalText(primary, width)
	}

	hint := "? shortcuts"
	if m.shortcutsOpen {
		hint = "Esc closes keyboard shortcuts"
	}
	if len(m.queued) > 0 {
		hint = fmt.Sprintf("%d queued messages available. enter/esc latest · ? shortcuts", len(m.queued))
	}
	if m.running {
		hint = "Working · Esc interrupts"
		if len(m.queued) > 0 {
			hint = fmt.Sprintf("%d queued messages available. enter/esc latest · ? shortcuts", len(m.queued))
		}
	}
	if context := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); context != "" {
		hint += " · " + context
	}
	if m.running && m.modelStreamFirstTokenRecorded {
		hint += " · first token " + m.modelStreamFirstTokenLatency.Round(time.Millisecond).String()
	}
	if len(m.attachments) > 0 {
		hint += fmt.Sprintf(" · %d images", len(m.attachments))
	}
	secondary := lightMutedStyle().Render(fitTerminalText(hint, width))
	return strings.Join([]string{primary, secondary}, "\n")
}

func latestUserTaskSummary(entries []tuiEntry) string {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].role == "you" {
			return strings.Join(strings.Fields(entries[index].text), " ")
		}
	}
	return ""
}

func (m *tuiModel) renderShortcuts() string {
	lines := []string{
		"Keyboard shortcuts · Esc close",
		"  Enter send · Shift+Enter newline · Ctrl+A select all",
		"  Shift+Tab cycle permissions · /plan toggle read-only planning",
		"  F4 focus tool activity · " + m.keyLabel("activity_toggle_latest") + " expand latest/selected tool",
		"  PgUp/PgDn scroll · Esc returns to latest and interrupts while working",
		"  /keymap edit key bindings",
	}
	return strings.Join(lines, "\n")
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
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
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
	lines = append(lines, m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" choose · Esc stay read-only")
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
	if m.planMode {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("Permission mode · Read-only (locked by Plan)\nExit Plan mode with /plan before changing permissions.")
	}
	mode := core.PermissionReadOnly
	if m.client != nil {
		mode = m.currentPermissionMode()
	}
	lines := []string{"Permission mode · current: " + permissionModeLabel(mode)}
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
	content := m.chat.View()
	if m.lightLayout() {
		return m.styleLightTranscript(content)
	}
	return content
}

func (m *tuiModel) styleLightTranscript(content string) string {
	rows := strings.Split(content, "\n")
	colors := m.themeColors()
	for rowIndex := range rows {
		visualIndex := m.chat.YOffset() + rowIndex
		if visualIndex < 0 || visualIndex >= len(m.lightVisualLines) {
			rows[rowIndex] = lipgloss.PlaceHorizontal(max(1, m.width), lipgloss.Left, "",
				lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(lipgloss.Color(codexLight.Canvas))))
			continue
		}
		visual := m.lightVisualLines[visualIndex]
		role := "assistant"
		if visualIndex < len(m.lightVisualRoles) {
			role = m.lightVisualRoles[visualIndex]
		}
		plainLine := ansi.Strip(rows[rowIndex])
		lineRanges := make([]lipgloss.Range, 0, 8)
		for _, code := range m.lightCodeRanges {
			start := max(code[0], visual.start)
			end := min(code[1], visual.end)
			if start >= end {
				continue
			}
			left := lipgloss.Width(visual.text[:start-visual.start])
			right := lipgloss.Width(visual.text[:end-visual.start])
			lineRanges = append(lineRanges, lipgloss.NewRange(left, right, inlineCodeStyle()))
		}
		lineRanges = append(lineRanges, lightToolLineRanges(plainLine, role)...)
		lineRanges = append(lineRanges, lightTokenRanges(plainLine)...)
		for _, match := range m.searchMatches {
			start, end := max(match[0], visual.start), min(match[1], visual.end)
			if start < end {
				left := lipgloss.Width(visual.text[:start-visual.start])
				right := lipgloss.Width(visual.text[:end-visual.start])
				lineRanges = append(lineRanges, lipgloss.NewRange(left, right, m.chat.HighlightStyle))
			}
		}
		selection := m.transcriptSelection
		if selection.active {
			start, end := max(selection.start, visual.start), min(selection.end, visual.end)
			if start < end {
				left := lipgloss.Width(visual.text[:start-visual.start])
				right := lipgloss.Width(visual.text[:end-visual.start])
				lineRanges = append(lineRanges, lipgloss.NewRange(left, right, m.chat.SelectedHighlightStyle))
			}
		}
		sort.SliceStable(lineRanges, func(left, right int) bool { return lineRanges[left].Start < lineRanges[right].Start })
		lineRanges = nonOverlappingStyleRanges(lineRanges)
		foreground := colors.Foreground
		background := codexLight.Canvas
		if role == "you" {
			foreground, background = codexLight.Primary, codexLight.Surface
		} else if role == "tool_done" {
			foreground = codexLight.Muted
		} else if role == "error" {
			foreground = codexLight.Error
		}
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(foreground)).Background(lipgloss.Color(background)).Render(plainLine)
		if len(lineRanges) > 0 {
			styled = lipgloss.StyleRanges(styled, lineRanges...)
		}
		rows[rowIndex] = lipgloss.PlaceHorizontal(max(1, m.width), lipgloss.Left, styled,
			lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(lipgloss.Color(background))))
	}
	return strings.Join(rows, "\n")
}

func nonOverlappingStyleRanges(ranges []lipgloss.Range) []lipgloss.Range {
	filtered := ranges[:0]
	lastEnd := -1
	for _, current := range ranges {
		if current.Start < lastEnd || current.End <= current.Start {
			continue
		}
		filtered = append(filtered, current)
		lastEnd = current.End
	}
	return filtered
}

func (m *tuiModel) inlineCodeRanges(content string) [][2]int {
	var ranges [][2]int
	searchFrom := 0
	for _, entry := range m.entries {
		for _, value := range inlineCodeValues(entry.text) {
			if value == "" || searchFrom >= len(content) {
				continue
			}
			position := strings.Index(content[searchFrom:], value)
			if position < 0 {
				position = strings.Index(content, value)
				if position < 0 {
					continue
				}
			} else {
				position += searchFrom
			}
			ranges = append(ranges, [2]int{position, position + len(value)})
			searchFrom = position + len(value)
		}
	}
	return ranges
}

func inlineCodeValues(text string) []string {
	var values []string
	for index := 0; index < len(text); {
		start := strings.IndexByte(text[index:], '`')
		if start < 0 {
			break
		}
		start += index
		if strings.HasPrefix(text[start:], "```") {
			end := strings.Index(text[start+3:], "```")
			if end < 0 {
				break
			}
			index = start + 3 + end + 3
			continue
		}
		end := strings.IndexByte(text[start+1:], '`')
		if end < 0 {
			break
		}
		end += start + 1
		value := text[start+1 : end]
		if value != "" && !strings.ContainsAny(value, "\r\n") {
			values = append(values, value)
		}
		index = end + 1
	}
	return values
}

func lightToolLineRanges(line, role string) []lipgloss.Range {
	if strings.HasPrefix(line, "  └ ") {
		return []lipgloss.Range{lipgloss.NewRange(2, 4, lightSubtleStyle())}
	}
	if strings.HasPrefix(line, "    + ") {
		return []lipgloss.Range{lipgloss.NewRange(4, lipgloss.Width(line), lightSubtleStyle())}
	}
	markerWidth := 0
	marker := ""
	for _, candidate := range []string{"•", "›", "×", "◉", "◐", "◓", "◑", "◒", "|", "/", "-", "\\"} {
		if strings.HasPrefix(line, candidate+" ") {
			marker, markerWidth = candidate, lipgloss.Width(candidate)
			break
		}
	}
	if marker == "" {
		return nil
	}
	toolLine := role == "tool" || role == "tool_done" || role == "error" && marker == "×"
	if !toolLine {
		if marker == "•" || marker == "›" {
			markerStyle := lightSubtleStyle()
			if role == "you" {
				markerStyle = markerStyle.Background(lipgloss.Color(codexLight.Surface))
			}
			return []lipgloss.Range{lipgloss.NewRange(0, markerWidth, markerStyle)}
		}
		return nil
	}
	markerColor := codexLight.Success
	if marker == "×" {
		markerColor = codexLight.Error
	} else if marker == "›" {
		markerColor = codexLight.Primary
	}
	ranges := []lipgloss.Range{lipgloss.NewRange(0, markerWidth, lipgloss.NewStyle().Foreground(lipgloss.Color(markerColor)))}
	markerByteWidth := len(marker)
	plain := line[markerByteWidth+1:]
	for _, verb := range []string{"Ran", "Run", "Failed", "Read", "Wrote", "Edited", "Listed", "Found", "Searched", "Fetched", "Processed"} {
		if !strings.HasPrefix(plain, verb+" ") {
			continue
		}
		start := markerWidth + 1
		verbByteStart := markerByteWidth + 1
		verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary)).Bold(true)
		if verb == "Failed" {
			verbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error)).Bold(true)
		}
		ranges = append(ranges, lipgloss.NewRange(start, start+lipgloss.Width(verb), verbStyle))
		commandStart := verbByteStart + len(verb) + 1
		command := line[commandStart:]
		if verb == "Failed" && strings.HasPrefix(command, "(exit ") {
			if close := strings.Index(command, ") "); close >= 0 {
				statusEnd := commandStart + close + 1
				statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error))
				ranges = append(ranges, lipgloss.NewRange(lipgloss.Width(line[:commandStart]), lipgloss.Width(line[:statusEnd]), statusStyle))
				commandStart += close + 2
				command = line[commandStart:]
			}
		}
		if verb == "Ran" {
			if failedAt := strings.LastIndex(command, " · failed"); failedAt >= 0 {
				statusStart := commandStart + failedAt
				ranges = append(ranges, lipgloss.NewRange(
					lipgloss.Width(line[:statusStart]),
					lipgloss.Width(line),
					lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error)),
				))
			}
		}
		fields := strings.Fields(command)
		if len(fields) == 0 {
			break
		}
		first := strings.Index(command, fields[0])
		firstStart := commandStart + first
		ranges = append(ranges, lipgloss.NewRange(lipgloss.Width(line[:firstStart]), lipgloss.Width(line[:firstStart+len(fields[0])]), inlineCodeStyle()))
		cursor := first + len(fields[0])
		for _, field := range fields[1:] {
			relative := strings.Index(command[cursor:], field)
			if relative < 0 {
				continue
			}
			relative += cursor
			if strings.HasPrefix(field, "-") {
				fieldStart := commandStart + relative
				flagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Flag))
				ranges = append(ranges, lipgloss.NewRange(lipgloss.Width(line[:fieldStart]), lipgloss.Width(line[:fieldStart+len(field)]), flagStyle))
			}
			cursor = relative + len(field)
		}
		break
	}
	return ranges
}

func lightTokenRanges(line string) []lipgloss.Range {
	var ranges []lipgloss.Range
	for cursor := 0; cursor < len(line); {
		for cursor < len(line) && (line[cursor] == ' ' || line[cursor] == '\t') {
			cursor++
		}
		start := cursor
		for cursor < len(line) && line[cursor] != ' ' && line[cursor] != '\t' {
			cursor++
		}
		if start == cursor {
			break
		}
		word := strings.Trim(line[start:cursor], "()[]{}.,:;\"'")
		isHash := len(word) >= 7 && len(word) <= 40
		for _, character := range word {
			if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
				isHash = false
				break
			}
		}
		if !isHash && !strings.HasPrefix(word, "origin/") {
			continue
		}
		wordStart := start + strings.Index(line[start:cursor], word)
		wordEnd := wordStart + len(word)
		ranges = append(ranges, lipgloss.NewRange(lipgloss.Width(line[:wordStart]), lipgloss.Width(line[:wordEnd]), inlineCodeStyle()))
	}
	return ranges
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
