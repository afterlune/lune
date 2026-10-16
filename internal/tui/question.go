package tui

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

var questionInlineMarkdown = regexp.MustCompile("`([^`]+)`|\\*\\*(.+?)\\*\\*")

type questionPanel struct {
	request      core.UserQuestionRequest
	selected     int
	notes        bool
	noteDraft    string
	customDraft  string
	input        textarea.Model
	submitting   bool
	fromComposer bool
	scroll       int
	manualScroll bool
	err          string
	cursorRow    int
}

type questionAnsweredMsg struct {
	sessionID string
	question  core.UserQuestionRequest
	answer    string
	err       error
}

func sameQuestion(a, b core.UserQuestionRequest) bool {
	return a.RunID == b.RunID && a.InvocationID == b.InvocationID && a.CallID == b.CallID && a.ExecutionID == b.ExecutionID
}

func (m *tuiModel) clearQuestionPreparingTool(executionID string) {
	removedKey := ""
	changed := false
	for index := len(m.activities) - 1; index >= 0; index-- {
		activity := m.activities[index]
		if activity.status != "running" {
			continue
		}
		if executionID != "" && strings.HasPrefix(activity.groupID, "execution:") && activity.groupID != "execution:"+executionID {
			continue
		}
		title := toolActivityTitle(activity, m.workspace)
		if !toolSummaryPreparing(activity.summary) && !strings.Contains(title, "(preparing)") {
			continue
		}
		removedKey = activity.callID
		m.activities = append(m.activities[:index], m.activities[index+1:]...)
		changed = true
		for entryIndex := len(m.entries) - 1; entryIndex >= 0; entryIndex-- {
			entry := m.entries[entryIndex]
			if entry.role == "tool" && (entry.activityID == removedKey || removedKey == "" && toolSummaryPreparing(entry.text)) {
				m.entries = append(m.entries[:entryIndex], m.entries[entryIndex+1:]...)
			}
		}
		break
	}
	seen := make(map[*streamToolCall]bool)
	for _, call := range m.streamToolCalls {
		if call == nil || seen[call] || call.taskID != m.modelStreamTaskID || call.agentID != "lead" ||
			executionID != "" && call.executionID != "" && call.executionID != executionID {
			continue
		}
		seen[call] = true
		if !call.preparing || call.finished || removedKey != "" && call.activityID != removedKey {
			continue
		}
		call.preparing = false
		call.finished = true
		call.hidden = true
		m.removeStreamToolActivity(call)
		changed = true
	}
	if changed {
		m.clearTranscriptSelection()
	}
}

func (m *tuiModel) openQuestion(request core.UserQuestionRequest) {
	m.clearQuestionPreparingTool(request.ExecutionID)
	if m.question != nil && sameQuestion(m.question.request, request) {
		previousSelection := m.question.selected
		previousCount := len(m.question.request.Options)
		m.question.request = request
		m.question.selected = min(m.question.selected, len(request.Options))
		if previousCount == 0 && len(request.Options) > 0 {
			m.question.notes = false
			m.question.input.Blur()
		} else if len(request.Options) == 0 || m.question.selected == len(request.Options) {
			m.question.notes = true
			m.question.input.Focus()
		} else if previousSelection == previousCount && previousCount > 0 {
			m.question.notes = false
			m.question.input.Blur()
		}
		setQuestionInputPrompt(m.question)
		m.questionOpen = true
		m.updateLayout()
		return
	}
	input := textarea.New()
	input.KeyMap = m.input.KeyMap
	input.Placeholder = ""
	input.Prompt = "› Note: "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 4
	input.SetVirtualCursor(false)
	input.SetWidth(m.questionInputWidth())
	applyTextareaTheme(&input, m.resolvedTheme())
	m.question = &questionPanel{request: request, input: input, notes: len(request.Options) == 0}
	setQuestionInputPrompt(m.question)
	if m.question.notes {
		m.question.input.Focus()
	} else {
		m.question.input.Blur()
	}
	m.questionOpen = true
	m.status = "Waiting for your answer."
	m.updateLayout()
}

func setQuestionInputPrompt(q *questionPanel) {
	if q == nil {
		return
	}
	if len(q.request.Options) == 0 || q.selected == len(q.request.Options) {
		q.input.Prompt = "› Type your answer: "
		return
	}
	q.input.Prompt = "› Note: "
}

func (q *questionPanel) setSelection(selected int) tea.Cmd {
	if q == nil || len(q.request.Options) == 0 {
		return nil
	}
	count := len(q.request.Options)
	selected = max(0, min(count, selected))
	wasCustom := q.selected == count
	willBeCustom := selected == count
	if wasCustom != willBeCustom {
		if wasCustom {
			q.customDraft = q.input.Value()
			q.input.SetValue(q.noteDraft)
		} else {
			q.noteDraft = q.input.Value()
			q.input.SetValue(q.customDraft)
		}
	}
	q.selected = selected
	setQuestionInputPrompt(q)
	if willBeCustom {
		q.notes = true
		return q.input.Focus()
	}
	if wasCustom {
		q.notes = false
		q.input.Blur()
		return nil
	}
	return nil
}

func (m *tuiModel) handleQuestionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	q := m.question
	if key.String() == "esc" {
		m.questionOpen = false
		m.updateLayout()
		return m, nil
	}
	if q.submitting {
		return m, nil
	}
	if key.String() == "ctrl+c" {
		if q.input.HasSelection() {
			if err := writeClipboard(q.input.SelectedText()); err != nil {
				q.err = err.Error()
			}
			return m, nil
		}
		m.questionOpen = false
		m.updateLayout()
		return m, nil
	}
	hasChoices := len(q.request.Options) > 0
	switch {
	case m.matchesKey("chat_scroll_up", key):
		q.manualScroll = true
		q.scroll = max(0, q.scroll-max(1, m.height/3))
	case m.matchesKey("chat_scroll_down", key):
		q.manualScroll = true
		q.scroll += max(1, m.height/3)
	case m.matchesKey("question_notes", key):
		if !hasChoices || q.selected == len(q.request.Options) {
			return m, nil
		}
		q.notes = !q.notes
		q.manualScroll = false
		if q.notes {
			q.input.SetValue(q.noteDraft)
			setQuestionInputPrompt(q)
			m.updateLayout()
			return m, q.input.Focus()
		}
		q.noteDraft = q.input.Value()
		q.input.Blur()
		m.updateLayout()
		return m, nil
	case hasChoices && m.matchesKey("question_previous", key):
		command := q.setSelection(q.selected - 1)
		q.err = ""
		q.manualScroll = false
		m.updateLayout()
		return m, command
	case hasChoices && m.matchesKey("question_next", key):
		command := q.setSelection(q.selected + 1)
		q.err = ""
		q.manualScroll = false
		m.updateLayout()
		return m, command
	case m.matchesKey("send", key):
		if hasChoices && q.selected < len(q.request.Options) {
			return m.answerUserQuestion(questionChoiceAnswer(q))
		}
		answer := strings.TrimSpace(q.input.Value())
		if answer == "" {
			q.notes = true
			q.err = "Type your own answer before submitting."
			setQuestionInputPrompt(q)
			m.updateLayout()
			return m, q.input.Focus()
		}
		return m.answerUserQuestion(answer)
	default:
		if !q.notes {
			if hasChoices {
				if n, err := strconv.Atoi(key.String()); err == nil && n > 0 && n <= len(q.request.Options)+1 {
					q.err = ""
					q.manualScroll = false
					if n <= len(q.request.Options) {
						q.setSelection(n - 1)
						setQuestionInputPrompt(q)
						m.updateLayout()
						return m.answerUserQuestion(questionChoiceAnswer(q))
					}
					command := q.setSelection(len(q.request.Options))
					if q.selected == len(q.request.Options) {
						m.updateLayout()
						return m, command
					}
				}
				return m, nil
			}
			if key.Text == "" {
				return m, nil
			}
			q.notes = true
			q.err = ""
			if hasChoices && q.selected < len(q.request.Options) {
				q.noteDraft = q.input.Value()
			}
			setQuestionInputPrompt(q)
			q.input.Focus()
		}
		var cmd tea.Cmd
		q.input, cmd = q.input.Update(key)
		m.updateLayout()
		return m, cmd
	}
	m.updateLayout()
	return m, nil
}

func questionChoiceAnswer(q *questionPanel) string {
	if q == nil || q.selected < 0 || q.selected >= len(q.request.Options) {
		return ""
	}
	answer := q.request.Options[q.selected].Label
	if notes := strings.TrimSpace(q.input.Value()); notes != "" {
		answer += "\n\n" + notes
	}
	return answer
}

func (m *tuiModel) renderQuestion() string {
	q := m.question
	if q == nil {
		return ""
	}
	light := m.lightLayout()
	colors := m.themeColors()
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	textStyle := accent
	descriptionStyle := muted
	indent := ""
	bg := lipgloss.Color(codexLight.Surface)
	width := max(1, m.width-2)
	if light {
		indent = "  "
		textStyle = lightTextStyle()
		descriptionStyle = lightMutedStyle()
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.TaskSummary))
		muted = lightMutedStyle()
	}
	lines := make([]string, 0, len(q.request.Options)*2+5)
	notesLine := -1
	focusLine := 0
	appendText := func(value, lineIndent string, style lipgloss.Style) {
		if light {
			lines = append(lines, wrapAndPadLines(value, lineIndent, m.width, style, bg)...)
			return
		}
		plain, ranges := parseQuestionMarkdown(value, bg)
		atomic := questionAtomicRanges(ranges)
		contentWidth := max(1, width-lipgloss.Width(lineIndent))
		for _, segment := range wrapTextSegments(plain, contentWidth, atomic) {
			content := renderQuestionTextRange(plain, segment.start, segment.end, style, nil)
			lines = append(lines, style.Render(lineIndent)+content)
		}
	}
	appendSingleLine := func(value, lineIndent string, style lipgloss.Style) {
		plain, ranges := parseQuestionMarkdown(value, bg)
		totalWidth := width
		if light {
			totalWidth = m.width
		}
		contentWidth := max(1, totalWidth-lipgloss.Width(lineIndent))
		truncated := ansi.StringWidth(plain) > contentWidth
		end := len(plain)
		if truncated {
			end = len(ansi.Truncate(plain, max(0, contentWidth-1), ""))
		}
		styleRanges := ranges
		if !light {
			styleRanges = nil
		}
		content := renderQuestionTextRange(plain, 0, end, style, styleRanges)
		if truncated {
			content += style.Render("…")
		}
		if light {
			background := lipgloss.NewStyle().Background(bg)
			styled := background.Render(lineIndent) + content
			lines = append(lines, lipgloss.PlaceHorizontal(m.width, lipgloss.Left, styled,
				lipgloss.WithWhitespaceStyle(background)))
			return
		}
		lines = append(lines, style.Render(lineIndent)+content)
	}
	appendQuestionInput := func() {
		var inputRows []string
		if light {
			inputRows = wrapStyledQuestionInput(q.input.View(), indent, m.width, bg)
		} else {
			inputRows = strings.Split(q.input.View(), "\n")
		}
		lines = append(lines, inputRows...)
		focusLine = max(0, len(lines)-1)
	}
	appendBlankLine := func() {
		if light {
			lines = append(lines, renderFullWidthBlankLine(m.width, bg))
			return
		}
		lines = append(lines, "")
	}
	questionTitle := "⚠ Question"
	questionTitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Warning)).Bold(true)
	appendText(questionTitle, indent, questionTitleStyle)
	appendText(q.request.Question, indent, textStyle)
	count := len(q.request.Options)
	hasChoices := count > 0
	if !hasChoices {
		appendBlankLine()
	}
	if hasChoices {
		appendBlankLine()
		// Keep the selected choice visible even on short terminals.
		for i := 0; i <= count; i++ {
			label, description := "None of the above", ""
			if i < count {
				label = q.request.Options[i].Label
				description = q.request.Options[i].Description
			}
			prefix := "  "
			if i == q.selected {
				prefix = "› "
			}
			row := fmt.Sprintf("%s%d. %s", prefix, i+1, label)
			if i == count && i != q.selected {
				row += " (type custom answer)"
			} else if i < count && i != q.selected && strings.TrimSpace(description) != "" {
				row += " · " + singleLineQuestionSummary(description)
			}
			style := descriptionStyle
			if i == q.selected {
				focusLine = len(lines)
				style = accent
			}
			appendSingleLine(row, indent, style)
			if i == q.selected && description != "" {
				appendText(description, "    ", descriptionStyle)
				appendBlankLine()
			}
			if i == count && i == q.selected && q.notes {
				notesLine = len(lines)
				appendQuestionInput()
			}
		}
	}
	if q.notes && (!hasChoices || q.selected < count) {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			appendBlankLine()
		}
		notesLine = len(lines)
		appendQuestionInput()
	}
	if q.err != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))
		if light {
			appendText(q.err, indent, errStyle)
		} else {
			appendText(q.err, "", errStyle)
		}
	}
	hint := m.keyLabel("send") + " submit · Esc hide"
	if hasChoices {
		if q.notes {
			hint = m.keyLabel("question_previous") + "/" + m.keyLabel("question_next") + " select · " + m.keyLabel("send") + " submit · Esc hide"
			if q.selected < count {
				hint = m.keyLabel("question_previous") + "/" + m.keyLabel("question_next") + " select · " + m.keyLabel("question_notes") + " choices · " + m.keyLabel("send") + " submit · Esc hide"
			}
		} else {
			hint = m.keyLabel("question_previous") + "/" + m.keyLabel("question_next") + " select · number keys submit · " + m.keyLabel("question_notes") + " notes · " + m.keyLabel("send") + " submit · Esc hide"
		}
	}
	if q.submitting {
		hint = "Submitting answer…"
	}
	body := lines
	totalLines := len(body)
	budget := m.questionBodyBudget(light, hint, indent, muted, bg)
	questionOffset := func(budget int) int {
		offset := q.scroll
		if !q.manualScroll {
			offset = max(0, focusLine-budget+1)
		}
		return min(offset, max(0, totalLines-budget))
	}
	offset := questionOffset(budget)
	if offset > 0 || offset+budget < totalLines {
		hint = "PgUp/PgDn scroll · " + hint
		budget = m.questionBodyBudget(light, hint, indent, muted, bg)
		offset = questionOffset(budget)
	}
	body = body[offset:min(len(body), offset+budget)]
	if notesLine >= 0 {
		cursorY := 0
		if cursor := q.input.Cursor(); cursor != nil {
			cursorY = cursor.Y
		}
		q.cursorRow = notesLine - offset + cursorY
		if light {
			q.cursorRow++
		}
	} else {
		q.cursorRow = 0
	}
	if light {
		content := append([]string(nil), body...)
		content = append(content, renderFullWidthBlankLine(m.width, bg))
		content = append(content, wrapAndPadLines(hint, indent, m.width, muted, bg)...)
		content = append([]string{renderFullWidthBlankLine(m.width, bg)}, content...)
		content = append(content, renderFullWidthBlankLine(m.width, bg))
		return strings.Join(content, "\n")
	}
	content := append(body, muted.Render(fitTerminalText(hint, max(1, m.width-2))))
	return lipgloss.NewStyle().Width(m.width).Padding(0, 1).Background(m.questionSurface()).Render(strings.Join(content, "\n"))
}

func (m *tuiModel) questionBodyBudget(light bool, hint, indent string, muted lipgloss.Style, bg color.Color) int {
	footerHeight := lipgloss.Height(m.renderFooter(max(1, m.width)))
	dividerRows, panelChromeRows := 1, 1
	if light {
		hintRows := len(wrapAndPadLines(hint, indent, m.width, muted, bg))
		dividerRows, panelChromeRows = 0, 3+hintRows
	}
	scrollPillRows := 0
	if m.lightLayout() && m.scrolledAwayFromBottom() {
		scrollPillRows = 1
	}
	searchRows := 0
	if m.searchOpen {
		searchRows = 1
	}
	// Reserve the header, one transcript row, separator/activity rows, footer,
	// and the Question panel's own hint and padding before sizing its body.
	reserved := 1 + 1 + 2 + footerHeight + dividerRows + panelChromeRows + scrollPillRows + searchRows
	return max(1, m.height-reserved)
}

func singleLineQuestionSummary(description string) string {
	plain, _ := parseQuestionMarkdown(strings.TrimSpace(description), lipgloss.Color(codexLight.Surface))
	return strings.Join(strings.Fields(plain), " ")
}

func (m *tuiModel) questionInputWidth() int {
	return max(1, m.width-2)
}

type questionInlineStyleRange struct {
	start  int
	end    int
	style  lipgloss.Style
	atomic bool
}

func parseQuestionMarkdown(text string, bg color.Color) (string, []questionInlineStyleRange) {
	code := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.CodeText)).Background(lipgloss.Color("#E2E8F0"))
	bold := lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Background(bg).Bold(true)
	var plain strings.Builder
	var ranges []questionInlineStyleRange
	last := 0
	for _, match := range questionInlineMarkdown.FindAllStringSubmatchIndex(text, -1) {
		plain.WriteString(text[last:match[0]])
		style := code
		atomic := true
		start, end := match[2], match[3]
		if start < 0 {
			style = bold
			atomic = false
			start, end = match[4], match[5]
		}
		styledStart := plain.Len()
		plain.WriteString(text[start:end])
		ranges = append(ranges, questionInlineStyleRange{start: styledStart, end: plain.Len(), style: style, atomic: atomic})
		last = match[1]
	}
	plain.WriteString(text[last:])
	return plain.String(), ranges
}

func questionAtomicRanges(ranges []questionInlineStyleRange) [][2]int {
	var atomic [][2]int
	for _, styledRange := range ranges {
		if styledRange.atomic {
			atomic = append(atomic, [2]int{styledRange.start, styledRange.end})
		}
	}
	return atomic
}

func renderQuestionTextRange(plain string, start, end int, textStyle lipgloss.Style, ranges []questionInlineStyleRange) string {
	var rendered strings.Builder
	cursor := start
	for _, styledRange := range ranges {
		if styledRange.end <= start || styledRange.start >= end {
			continue
		}
		rangeStart := max(start, styledRange.start)
		rangeEnd := min(end, styledRange.end)
		if rangeStart > cursor {
			rendered.WriteString(textStyle.Render(plain[cursor:rangeStart]))
		}
		if rangeEnd > rangeStart {
			rendered.WriteString(styledRange.style.Render(plain[rangeStart:rangeEnd]))
			cursor = rangeEnd
		}
	}
	if cursor < end {
		rendered.WriteString(textStyle.Render(plain[cursor:end]))
	}
	return rendered.String()
}

func wrapStyledQuestionInput(value, indent string, totalWidth int, bg color.Color) []string {
	totalWidth = max(1, totalWidth)
	indentWidth := lipgloss.Width(indent)
	maxIndentWidth := max(0, totalWidth-1)
	if indentWidth > maxIndentWidth {
		indent = ansi.Truncate(indent, maxIndentWidth, "")
		indentWidth = lipgloss.Width(indent)
	}
	contentWidth := max(1, totalWidth-indentWidth)
	background := lipgloss.NewStyle().Background(bg)
	whitespace := lipgloss.WithWhitespaceStyle(background)
	styledIndent := background.Render(indent)
	wrapped := strings.Split(ansi.Wrap(value, contentWidth, ""), "\n")
	rows := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		rows = append(rows, lipgloss.PlaceHorizontal(totalWidth, lipgloss.Left, styledIndent+line, whitespace))
	}
	return rows
}

func (m *tuiModel) submitQuestionAnswer(question core.UserQuestionRequest, answer string) tea.Cmd {
	runtime, sessionID := m.runtime, m.sessionID
	return func() tea.Msg {
		err := runtime.ResolveUserQuestion(context.Background(), core.UserQuestionAnswer{ExecutionID: question.ExecutionID, RunID: question.RunID, InvocationID: question.InvocationID, CallID: question.CallID, Answer: answer})
		return questionAnsweredMsg{sessionID: sessionID, question: question, answer: answer, err: err}
	}
}

// A restrained surface follows the actual terminal background, including auto.
func (m *tuiModel) questionSurface() color.Color {
	var base color.Color = color.Black
	if m.resolvedTheme() == themeLight {
		base = color.White
	}
	if m.terminalBackground != nil {
		base = m.terminalBackground
	}
	foreground := lipgloss.Color(m.themeColors().Foreground)
	br, bg, bb, _ := base.RGBA()
	fr, fg, fb, _ := foreground.RGBA()
	return color.RGBA{R: uint8((br*95 + fr*5) / 100 >> 8), G: uint8((bg*95 + fg*5) / 100 >> 8), B: uint8((bb*95 + fb*5) / 100 >> 8), A: 255}
}
