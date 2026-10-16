package tui

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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
	selected     int
	notes        bool
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

func (m *tuiModel) openQuestion(request core.UserQuestionRequest) {
	if m.question != nil && sameQuestion(m.question.request, request) {
		m.question.request = request
		m.question.selected = min(m.question.selected, len(request.Options))
		if len(request.Options) < 2 || m.question.selected == len(request.Options) {
			m.question.notes = true
			m.question.input.Focus()
		}
		m.questionOpen = true
		m.updateLayout()
		return
	}
	input := textarea.New()
	input.KeyMap = m.input.KeyMap
	input.Placeholder = "Add notes or type your own answer"
	input.Prompt = "› "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 4
	input.SetVirtualCursor(false)
	input.SetWidth(m.questionInputWidth())
	applyTextareaTheme(&input, m.resolvedTheme())
	m.question = &questionPanel{request: request, input: input, notes: len(request.Options) < 2}
	if m.question.notes {
		m.question.input.Focus()
	}
	m.questionOpen = true
	m.status = "Waiting for your answer."
	m.updateLayout()
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
	hasChoices := len(q.request.Options) >= 2
	switch {
	case m.matchesKey("chat_scroll_up", key):
		q.manualScroll = true
		q.scroll = max(0, q.scroll-max(1, m.height/3))
	case m.matchesKey("chat_scroll_down", key):
		q.manualScroll = true
		q.scroll += max(1, m.height/3)
	case m.matchesKey("question_notes", key):
		if !hasChoices {
			return m, nil
		}
		q.notes = !q.notes
		q.manualScroll = false
		if q.notes {
			return m, q.input.Focus()
		}
		q.input.Blur()
		return m, nil
	case hasChoices && !q.notes && m.matchesKey("question_previous", key):
		q.selected = max(0, q.selected-1)
		q.err = ""
		q.manualScroll = false
	case hasChoices && !q.notes && m.matchesKey("question_next", key):
		q.selected = min(len(q.request.Options), q.selected+1)
		q.err = ""
		q.manualScroll = false
		if q.selected == len(q.request.Options) {
			q.notes = true
			m.updateLayout()
			return m, q.input.Focus()
		}
	case m.matchesKey("send", key):
		answer := strings.TrimSpace(q.input.Value())
		if answer == "" && (!hasChoices || q.selected == len(q.request.Options)) {
			q.notes = true
			q.err = "Type your own answer before submitting."
			m.updateLayout()
			return m, q.input.Focus()
		}
		if hasChoices && q.selected < len(q.request.Options) {
			label := q.request.Options[q.selected].Label
			if answer != "" {
				answer = label + "\n\n" + answer
			} else {
				answer = label
			}
		}
		return m.answerUserQuestion(answer)
	default:
		if !q.notes {
			if hasChoices {
				if n, err := strconv.Atoi(key.String()); err == nil && n > 0 && n <= len(q.request.Options)+1 {
					q.selected = n - 1
					q.err = ""
					q.manualScroll = false
					if q.selected == len(q.request.Options) {
						q.notes = true
						m.updateLayout()
						return m, q.input.Focus()
					}
					return m, nil
				}
			}
			if key.Text == "" {
				return m, nil
			}
			q.notes = true
			q.err = ""
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
	appendText := func(value, lineIndent string, style lipgloss.Style) {
		if light {
			lines = append(lines, wrapAndPadLines(value, lineIndent, m.width, style, bg)...)
			return
		}
		for _, line := range strings.Split(wrapDisplayText(value, width), "\n") {
			lines = append(lines, style.Render(line))
		}
	}
	questionTitle := "⚠ Question"
	questionTitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Warning)).Bold(true)
	appendText(questionTitle, indent, questionTitleStyle)
	appendText(q.request.Question, indent, textStyle)
	count := len(q.request.Options)
	hasChoices := count >= 2
	focusLine := 0
	if hasChoices {
		if light {
			lines = append(lines, renderFullWidthBlankLine(m.width, bg))
		} else {
			lines = append(lines, "")
		}
		// Keep the selected choice visible even on short terminals.
		for i := 0; i <= count; i++ {
			label, description := "None of the above", "Type your own answer in notes"
			if i < count {
				label = q.request.Options[i].Label
				description = q.request.Options[i].Description
			}
			prefix := "  "
			if i == q.selected {
				prefix = "› "
			}
			row := fmt.Sprintf("%s%d. %s", prefix, i+1, label)
			style := descriptionStyle
			if i == q.selected {
				focusLine = len(lines)
				style = accent
			}
			appendText(row, indent, style)
			if description != "" {
				if light {
					appendText(description, "   ", descriptionStyle)
				} else {
					appendText("     "+description, "", muted)
				}
			}
		}
	}
	if q.notes {
		notesLine = len(lines)
		var inputRows []string
		if light {
			inputRows = wrapStyledQuestionInput(q.input.View(), indent, m.width, bg)
		} else {
			inputRows = strings.Split(q.input.View(), "\n")
		}
		lines = append(lines, inputRows...)
		focusLine = max(0, len(lines)-1)
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
			hint = m.keyLabel("question_previous") + "/" + m.keyLabel("question_next") + " select · " + m.keyLabel("question_notes") + " choices · " + m.keyLabel("send") + " submit · Esc hide"
		} else {
			hint = m.keyLabel("question_previous") + "/" + m.keyLabel("question_next") + " select · " + m.keyLabel("question_notes") + " notes · " + m.keyLabel("send") + " submit · Esc hide"
		}
	}
	if q.submitting {
		hint = "Submitting answer…"
	}
	body := lines
	budget := max(1, m.height-7)
	offset := q.scroll
	if !q.manualScroll {
		offset = max(0, focusLine-budget+1)
	}
	totalLines := len(body)
	offset = min(offset, max(0, totalLines-budget))
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
	if offset > 0 || offset+budget < totalLines {
		hint = "PgUp/PgDn scroll · " + hint
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

func (m *tuiModel) questionInputWidth() int {
	return max(1, m.width-2)
}

type questionInlineStyleRange struct {
	start int
	end   int
	style lipgloss.Style
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
		start, end := match[2], match[3]
		if start < 0 {
			style = bold
			start, end = match[4], match[5]
		}
		styledStart := plain.Len()
		plain.WriteString(text[start:end])
		ranges = append(ranges, questionInlineStyleRange{start: styledStart, end: plain.Len(), style: style})
		last = match[1]
	}
	plain.WriteString(text[last:])
	return plain.String(), ranges
}

func wrappedLineOffset(plain, line string, offset int) int {
	if line == "" || strings.HasPrefix(plain[offset:], line) {
		return offset
	}
	for offset < len(plain) {
		r, size := utf8.DecodeRuneInString(plain[offset:])
		if !unicode.IsSpace(r) {
			break
		}
		offset += size
		if strings.HasPrefix(plain[offset:], line) {
			return offset
		}
	}
	if index := strings.Index(plain[offset:], line); index >= 0 {
		return offset + index
	}
	return offset
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
