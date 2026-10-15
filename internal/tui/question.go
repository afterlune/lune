package tui

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

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
		if len(request.Options) < 2 {
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
	input.SetVirtualCursor(true)
	input.SetWidth(max(1, m.width-2))
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
		q.manualScroll = false
	case hasChoices && !q.notes && m.matchesKey("question_next", key):
		q.selected = min(len(q.request.Options), q.selected+1)
		q.manualScroll = false
	case m.matchesKey("send", key):
		answer := strings.TrimSpace(q.input.Value())
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
					return m, nil
				}
			}
			if key.Text == "" {
				return m, nil
			}
			q.notes = true
			q.selected = len(q.request.Options)
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
	colors := m.themeColors()
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	lines := []string{muted.Render("Question"), accent.Render(wrapDisplayText(q.request.Question, max(1, m.width-2)))}
	count := len(q.request.Options)
	hasChoices := count >= 2
	focusLine := 0
	if hasChoices {
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
			row = wrapDisplayText(row, max(1, m.width-2))
			if i == q.selected {
				focusLine = len(strings.Split(strings.Join(lines, "\n"), "\n"))
				row = accent.Render(row)
			}
			lines = append(lines, row)
			if description != "" {
				lines = append(lines, muted.Render(wrapDisplayText("     "+description, max(1, m.width-2))))
			}
		}
	}
	if q.notes {
		lines = append(lines, q.input.View())
		focusLine = len(strings.Split(strings.Join(lines, "\n"), "\n")) - 1
	}
	if q.err != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error)).Render(wrapDisplayText(q.err, max(1, m.width-2))))
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
	body := strings.Split(strings.Join(lines, "\n"), "\n")
	budget := max(1, m.height-7)
	offset := q.scroll
	if !q.manualScroll {
		offset = max(0, focusLine-budget+1)
	}
	totalLines := len(body)
	offset = min(offset, max(0, totalLines-budget))
	body = body[offset:min(len(body), offset+budget)]
	if offset > 0 || offset+budget < totalLines {
		hint = "PgUp/PgDn scroll · " + hint
	}
	content := strings.Join(append(body, muted.Render(fitTerminalText(hint, max(1, m.width-2)))), "\n")
	return lipgloss.NewStyle().Width(m.width).Padding(0, 1).Background(m.questionSurface()).Render(content)
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
