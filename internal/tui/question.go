package tui

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

var questionInlineMarkdown = regexp.MustCompile("`([^`]+)`|\\*\\*(.+?)\\*\\*")

type questionPanel struct {
	request      core.UserQuestionRequest
	current      int
	responses    []core.UserQuestionDraftResponse
	answers      []core.UserQuestionResponse
	revision     uint64
	writer       *questionDraftWriter
	displayText  string
	background   string
	selected     int
	notes        bool
	noteDraft    string
	customDraft  string
	input        textarea.Model
	submitting   bool
	interrupting bool
	fromComposer bool
	err          string
	cursorRow    int
}

type questionAnsweredMsg struct {
	sessionID string
	question  core.UserQuestionRequest
	answer    string
	answers   []core.UserQuestionResponse
	err       error
}

type questionInterruptedMsg struct {
	sessionID string
	question  core.UserQuestionRequest
	discarded bool
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
	if len(request.Questions) == 0 {
		return
	}
	m.clearQuestionPreparingTool(request.ExecutionID)
	if m.question != nil && sameQuestion(m.question.request, request) {
		parts := splitQuestionDisplay(request.Questions[m.question.current].Question)
		m.question.request = request
		m.question.selected = min(m.question.selected, len(request.Questions[m.question.current].Options))
		m.question.displayText = parts.question

		m.question.background = parts.background
		setQuestionInputPrompt(m.question)
		m.questionOpen = true
		m.updateLayout()
		return
	}
	input := textarea.New()
	input.KeyMap = m.input.KeyMap
	input.Placeholder = ""
	input.Prompt = "› Add notes: "
	input.ShowLineNumbers = false
	input.DynamicHeight = false
	input.MinHeight = 1
	input.MaxHeight = 1
	input.SetVirtualCursor(false)
	input.SetWidth(m.questionInputWidth())
	applyTextareaTheme(&input, m.resolvedTheme())
	if len(request.Questions) == 0 {
		return
	}
	parts := splitQuestionDisplay(request.Questions[0].Question)
	m.question = &questionPanel{request: request, displayText: parts.question, background: parts.background, input: input, writer: &questionDraftWriter{}, responses: make([]core.UserQuestionDraftResponse, len(request.Questions)), answers: make([]core.UserQuestionResponse, len(request.Questions))}
	seenBackground := map[string]bool{}
	for _, item := range request.Questions {
		background := splitQuestionDisplay(item.Question).background
		if background != "" && !seenBackground[background] {
			m.addEntry("assistant", background, false)
			seenBackground[background] = true
		}
	}

	for i, item := range request.Questions {
		m.question.responses[i] = core.UserQuestionDraftResponse{QuestionID: item.ID}
		m.question.answers[i].QuestionID = item.ID
	}
	if request.Draft != nil {
		for _, d := range request.Draft.Responses {
			for i, item := range request.Questions {
				if item.ID == d.QuestionID {
					m.question.responses[i] = d
					m.question.answers[i].Answer = draftAnswer(item, d)
				}
			}
		}
		for i, item := range request.Questions {
			if item.ID == request.Draft.CurrentQuestionID {
				m.question.current = i
			}
		}
	}
	m.question.loadCurrent()
	setQuestionInputPrompt(m.question)
	if !m.question.notes {
		m.question.input.Blur()
	}
	m.questionOpen = true
	m.status = ""
	m.updateLayout()
}

func setQuestionInputPrompt(q *questionPanel) {
	if q == nil {
		return
	}
	q.input.Prompt = "› Add notes: "
}

func (q *questionPanel) setSelection(selected int) tea.Cmd {
	if q == nil {
		return nil
	}
	count := len(q.item().Options)
	selected = max(0, min(count, selected))
	if q.notes {
		if q.selected == count {
			q.customDraft = q.input.Value()
		} else {
			q.noteDraft = q.input.Value()
		}
	}
	q.selected = selected
	setQuestionInputPrompt(q)
	if q.notes {
		if selected == count {
			q.input.SetValue(q.customDraft)
		} else {
			q.input.SetValue(q.noteDraft)
		}
	}
	return nil
}

func (m *tuiModel) handleQuestionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	q := m.question
	if q.submitting {
		if q.writer != nil && q.writer.submitted && m.matchesKey("send", key) {
			return m, m.retryQuestionSubmission()
		}
		return m, nil
	}
	if q.interrupting {
		return m, nil
	}
	if key.String() == "esc" || key.String() == "ctrl+c" {
		q.interrupting = true
		q.err = "Interrupting paused task…"
		return m, m.discardQuestionTask(q.request)
	}
	count := len(q.item().Options)
	switch {
	case !q.notes && (key.Code == tea.KeyLeft || key.Code == tea.KeyRight):
		q.captureCurrent(false)
		if key.Code == tea.KeyLeft {
			q.current = max(0, q.current-1)
		} else {
			q.current = min(len(q.request.Questions)-1, q.current+1)
		}
		q.loadCurrent()
		m.updateLayout()
		return m, m.saveQuestionDraft(false)
	case key.Code == tea.KeyTab || m.matchesKey("question_notes", key):
		if q.notes {
			if q.selected == count {
				q.customDraft = q.input.Value()
			} else {
				q.noteDraft = q.input.Value()
			}
			q.notes = false
			q.input.Blur()
		} else {
			q.notes = true
			if q.selected == count {
				q.input.SetValue(q.customDraft)
			} else {
				q.input.SetValue(q.noteDraft)
			}
		}
		if q.notes {
			setQuestionInputPrompt(q)
			m.updateLayout()
			return m, q.input.Focus()
		}
		m.updateLayout()
		return m, nil
	case key.Code == tea.KeyUp || m.matchesKey("question_previous", key):
		if q.notes {
			if q.selected == count {
				q.customDraft = q.input.Value()
			} else {
				q.noteDraft = q.input.Value()
			}
			q.notes = false
			q.input.Blur()
		}
		command := q.setSelection(q.selected - 1)
		q.err = ""
		m.updateLayout()
		return m, command
	case key.Code == tea.KeyDown || m.matchesKey("question_next", key):
		if q.notes {
			if q.selected == count {
				q.customDraft = q.input.Value()
			} else {
				q.noteDraft = q.input.Value()
			}
			q.notes = false
			q.input.Blur()
		}
		command := q.setSelection(q.selected + 1)
		q.err = ""
		m.updateLayout()
		return m, command
	case m.matchesKey("send", key):
		if !q.notes {
			if q.selected < count {
				return m.answerUserQuestion(questionChoiceAnswer(q))
			}
			q.notes = true
			q.input.SetValue(q.customDraft)
			q.err = ""
			setQuestionInputPrompt(q)
			m.updateLayout()
			return m, q.input.Focus()
		}
		answer := strings.TrimSpace(q.input.Value())
		if q.selected == count {
			if answer == "" {
				q.err = "Enter an answer in notes."
				m.updateLayout()
				return m, nil
			}
			q.customDraft = answer
			return m.answerUserQuestion(answer)
		}
		q.noteDraft = answer
		return m.answerUserQuestion(questionChoiceAnswer(q))
	default:
		if !q.notes {
			return m, nil
		}
		if key.Code == tea.KeyEnter {
			return m, nil
		}
		if strings.ContainsAny(key.Text, "\r\n") {
			return m, nil
		}
		var cmd tea.Cmd
		q.input, cmd = q.input.Update(key)
		if q.selected == count {
			q.customDraft = q.input.Value()
		} else {
			q.noteDraft = q.input.Value()
		}
		m.updateLayout()
		return m, cmd
	}
}

func questionChoiceAnswer(q *questionPanel) string {
	if q == nil || q.selected < 0 || q.selected >= len(q.item().Options) {
		return ""
	}
	answer := q.item().Options[q.selected].Label
	notes := q.noteDraft
	if q.notes && q.selected < len(q.item().Options) {
		notes = q.input.Value()
	}
	if notes = strings.TrimSpace(notes); notes != "" {
		answer += "\n\n" + notes
	}
	return answer
}

type questionTextParts struct {
	background string
	question   string
}

var questionParagraphBreak = regexp.MustCompile(`\n(?:[ \t]*\n)+`)

func splitQuestionDisplay(raw string) questionTextParts {
	text := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return questionTextParts{}
	}
	if mark := strings.LastIndexAny(text, "?？"); mark >= 0 {
		start := 0
		prefix := text[:mark]
		if boundary := strings.LastIndexAny(prefix, ".!。！？\n"); boundary >= 0 {
			_, size := utf8.DecodeRuneInString(prefix[boundary:])
			start = boundary + size
		}
		return questionTextParts{
			background: strings.TrimSpace(text[:start]),
			question:   strings.TrimSpace(text[start:]),
		}
	}
	paragraphs := questionParagraphBreak.Split(text, -1)
	if len(paragraphs) == 1 {
		lines := strings.Split(text, "\n")
		if len(lines) > 1 {
			return questionTextParts{
				background: strings.TrimSpace(strings.Join(lines[:len(lines)-1], "\n")),
				question:   strings.TrimSpace(lines[len(lines)-1]),
			}
		}
		return questionTextParts{question: text}
	}
	question := strings.TrimSpace(paragraphs[len(paragraphs)-1])
	background := strings.TrimSpace(strings.Join(paragraphs[:len(paragraphs)-1], "\n\n"))
	return questionTextParts{background: background, question: question}
}

func (m *tuiModel) renderQuestion() string {
	q := m.question
	if q == nil {
		return ""
	}
	width := max(1, m.width)
	if m.lightLayout() {
		width = m.lightPanelWidth()
	}
	innerWidth := max(1, width-4)
	bg := color.Color(lipgloss.Color(codexLight.Surface))
	colors := m.themeColors()
	if !m.lightLayout() {
		bg = m.questionSurface()
	}
	background := lipgloss.NewStyle().Background(bg)
	compact := m.lightLayout() && m.height < 10
	rows := []string{}
	if !compact {
		rows = append(rows, fillQuestionRow("", width, bg))
	}
	questionTitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Muted)).Background(bg)
	questionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#1C64C8")).Background(bg)
	if !m.lightLayout() {
		questionTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Background(bg)
		questionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Background(bg)
	}
	rows = append(rows, fillQuestionRow(background.Render("  ")+questionTitleStyle.Render(fmt.Sprintf("Question %d/%d (%d unanswered)", q.current+1, len(q.request.Questions), q.unanswered())), width, bg))
	prompt := fitTerminalText(singleLineQuestionSummary(q.displayText), innerWidth)
	rows = append(rows, fillQuestionRow(background.Render("  ")+questionStyle.Render(prompt), width, bg))
	if !compact {
		rows = append(rows, fillQuestionRow("", width, bg))
	}

	count := len(q.item().Options)
	labels := make([]string, count+1)
	descriptions := make([]string, count+1)
	for index, option := range q.item().Options {
		labels[index] = fmt.Sprintf("%d. %s", index+1, singleLineQuestionSummary(option.Label))
		descriptions[index] = singleLineQuestionSummary(option.Description)
	}
	labels[count] = fmt.Sprintf("%d. None of the above", count+1)
	descriptions[count] = "Optionally, add details in notes (tab)"
	labelWidth := 1
	for _, label := range labels {
		labelWidth = max(labelWidth, lipgloss.Width(label))
	}
	rowWidth := max(1, innerWidth-2) // selection marker
	gapWidth := min(2, max(0, rowWidth-2))
	labelWidth = min(labelWidth, max(1, rowWidth-gapWidth-1))
	descriptionWidth := max(0, rowWidth-labelWidth-gapWidth)
	visible := len(labels)
	start := 0
	if m.lightLayout() {
		fixed := 7
		if compact {
			fixed = 3
		}
		if q.notes {
			fixed++
		}
		if q.err != "" {
			fixed++
		}
		visible = min(len(labels), max(1, m.height-fixed))
		start = max(0, min(q.selected-visible/2, len(labels)-visible))
	}
	for index := start; index < start+visible; index++ {
		label := labels[index]
		selected := index == q.selected
		rowBG := bg
		marker, labelColor, descriptionColor := "  ", colors.Foreground, colors.Muted
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(labelColor)).Background(rowBG)
		descriptionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(descriptionColor)).Background(rowBG)
		markerStyle := labelStyle
		if selected {
			marker = "› "
			if m.lightLayout() {
				rowBG = lipgloss.Color("#A4CDFB")
				labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary)).Background(rowBG).Bold(true)
				descriptionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#24292F")).Background(rowBG)
				markerStyle = labelStyle
			} else {
				rowBG = lipgloss.Color(colors.Accent)
				labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(rowBG).Bold(true)
				descriptionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(rowBG)
				markerStyle = labelStyle
			}
		}
		label = truncateQuestionText(label, labelWidth)
		row := lipgloss.NewStyle().Background(rowBG).Render("  ") + markerStyle.Render(marker) + labelStyle.Render(label)
		padding := max(0, labelWidth-lipgloss.Width(label))
		if padding > 0 {
			row += lipgloss.NewStyle().Background(rowBG).Render(strings.Repeat(" ", padding))
		}
		if descriptionWidth > 0 {
			description := truncateQuestionText(descriptions[index], descriptionWidth)
			row += lipgloss.NewStyle().Background(rowBG).Render(strings.Repeat(" ", gapWidth)) + descriptionStyle.Render(description)
		}
		rows = append(rows, fillQuestionRow(row, width, rowBG))
	}
	q.cursorRow = 0
	if q.notes {
		q.cursorRow = len(rows)
		input := ansi.Truncate(q.input.View(), innerWidth, "...")
		rows = append(rows, fillQuestionRow(background.Render("  ")+input, width, bg))
	}
	if q.err != "" {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error)).Background(bg)
		rows = append(rows, fillQuestionRow(background.Render("  ")+style.Render(fitTerminalText(q.err, innerWidth)), width, bg))
	}
	if m.lightLayout() {
		if !compact {
			rows = append(rows, fillQuestionRow("", width, bg))
		}
		hint := m.renderQuestionHint(max(1, width-2))
		rows = append(rows, fillQuestionRow(background.Render("  ")+hint, width, bg))
		if !compact {
			rows = append(rows, fillQuestionRow("", width, bg))
		}
		return strings.Join(rows, "\n")
	}
	rows = append(rows, fillQuestionRow("", width, bg))
	return strings.Join(rows, "\n") + "\n" + m.renderQuestionHint(width)
}

func fillQuestionRow(content string, width int, bg color.Color) string {
	background := lipgloss.NewStyle().Background(bg)
	return lipgloss.PlaceHorizontal(width, lipgloss.Left, ansi.Truncate(content, max(1, width), ""), lipgloss.WithWhitespaceStyle(background))
}

func truncateQuestionText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width-3, "") + "..."
}

func (m *tuiModel) renderQuestionHint(width int) string {
	colors := m.themeColors()
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Bold(true)
	if m.lightLayout() {
		muted = lightMutedStyle()
		key = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary)).Bold(true)
	}
	var hint strings.Builder
	hint.WriteString(key.Render("tab"))
	hint.WriteString(muted.Render(" to add notes | "))
	hint.WriteString(key.Render("enter"))
	hint.WriteString(muted.Render(" to submit answer | "))
	hint.WriteString(key.Render("esc"))
	hint.WriteString(muted.Render(" to interrupt"))
	if m.question != nil && len(m.question.request.Questions) > 1 {
		hint.WriteString(muted.Render(" | ←/→ questions"))
	}
	return fitTerminalText(hint.String(), width)
}

func singleLineQuestionSummary(description string) string {
	plain, _ := parseQuestionMarkdown(strings.TrimSpace(description), lipgloss.Color(codexLight.Surface))
	return strings.Join(strings.Fields(plain), " ")
}

func (m *tuiModel) questionInputWidth() int {
	if m.lightLayout() {
		return max(1, m.lightPanelWidth()-4)
	}
	return max(1, m.width-4)
}

type questionInlineStyleRange struct {
	start  int
	end    int
	style  lipgloss.Style
	atomic bool
}

func parseQuestionMarkdown(text string, bg color.Color) (string, []questionInlineStyleRange) {
	code := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.CodeText)).Background(lipgloss.Color("#E2E8F0"))
	bold := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary)).Background(bg).Bold(true)
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

func (m *tuiModel) submitQuestionAnswer(question core.UserQuestionRequest, answer string) tea.Cmd {
	q := m.question
	q.captureCurrent(true)
	q.answers[q.current].Answer = answer
	return m.saveQuestionDraft(true)
}

func (m *tuiModel) discardQuestionTask(question core.UserQuestionRequest) tea.Cmd {
	runtime, sessionID := m.runtime, m.sessionID
	return func() tea.Msg {
		if runtime == nil {
			return questionInterruptedMsg{sessionID: sessionID, question: question, err: fmt.Errorf("session runtime is unavailable")}
		}
		discarded, err := runtime.DiscardPaused(context.Background())
		return questionInterruptedMsg{sessionID: sessionID, question: question, discarded: discarded, err: err}
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
