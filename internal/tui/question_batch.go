package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type questionDraftWriter struct {
	sync.Mutex
	saved      uint64
	closed     bool
	submitted  bool
	submission core.UserQuestionAnswer
}
type questionDraftTick struct {
	wait     core.UserQuestionRequest
	revision uint64
}
type questionDraftSavedMsg struct {
	wait    core.UserQuestionRequest
	advance bool
	next    int
	err     error
}

func (m *tuiModel) retryQuestionSubmission() tea.Cmd {
	q := m.question
	if q == nil || q.writer == nil {
		return nil
	}
	runtime, sessionID, wait, writer := m.runtime, m.sessionID, q.request, q.writer
	return func() tea.Msg {
		writer.Lock()
		defer writer.Unlock()
		if !writer.submitted || writer.closed {
			return nil
		}
		err := runtime.ResolveUserQuestion(context.Background(), writer.submission)
		if err == nil {
			writer.closed = true
		}
		return questionAnsweredMsg{sessionID: sessionID, question: wait, answers: writer.submission.Answers, err: err}
	}
}

func (q *questionPanel) item() core.UserQuestionItem { return q.request.Questions[q.current] }
func draftAnswer(item core.UserQuestionItem, d core.UserQuestionDraftResponse) string {
	if d.SelectedOption >= len(item.Options) {
		return strings.TrimSpace(d.CustomAnswer)
	}
	answer := item.Options[d.SelectedOption].Label
	if strings.TrimSpace(d.Notes) != "" {
		answer += "\n\n" + strings.TrimSpace(d.Notes)
	}
	return answer
}
func (q *questionPanel) captureCurrent(confirm bool) {
	if q.notes {
		if q.selected == len(q.item().Options) {
			q.customDraft = q.input.Value()
		} else {
			q.noteDraft = q.input.Value()
		}
	}
	d := &q.responses[q.current]
	changed := d.SelectedOption != q.selected || d.Notes != q.noteDraft || d.CustomAnswer != q.customDraft
	d.SelectedOption = q.selected
	d.Notes = q.noteDraft
	d.CustomAnswer = q.customDraft
	if changed {
		d.Confirmed = false
	}
	if confirm {
		d.Confirmed = true
	}
	q.answers[q.current] = core.UserQuestionResponse{QuestionID: q.item().ID, Answer: draftAnswer(q.item(), *d)}
}
func (q *questionPanel) loadCurrent() {
	d := q.responses[q.current]
	q.selected = min(d.SelectedOption, len(q.item().Options))
	q.noteDraft = d.Notes
	q.customDraft = d.CustomAnswer
	q.displayText = splitQuestionDisplay(q.item().Question).question
	q.notes = len(q.item().Options) == 0
	q.err = ""
	q.input.Blur()
	q.input.SetValue(q.noteDraft)
	if q.notes {
		q.input.SetValue(q.customDraft)
		q.input.Focus()
	}
}
func (q *questionPanel) unanswered() int {
	n := 0
	for _, d := range q.responses {
		if !d.Confirmed {
			n++
		}
	}
	return n
}
func (m *tuiModel) saveQuestionDraft(confirm bool) tea.Cmd {
	q := m.question
	if q == nil {
		return nil
	}
	q.captureCurrent(confirm)
	q.revision++
	revision := q.revision
	wait := q.request
	writer := q.writer
	runtime := m.runtime
	sessionID := m.sessionID
	next := min(q.current+1, len(q.request.Questions)-1)
	all := q.unanswered() == 0
	if q.current == len(q.request.Questions)-1 && !all {
		for i, d := range q.responses {
			if !d.Confirmed {
				next = i
				break
			}
		}
	}
	draft := core.UserQuestionDraft{CurrentQuestionID: q.item().ID, Responses: append([]core.UserQuestionDraftResponse(nil), q.responses...)}
	if confirm && !all {
		draft.CurrentQuestionID = q.request.Questions[next].ID
	}
	answers := append([]core.UserQuestionResponse(nil), q.answers...)
	identity := core.UserQuestionAnswer{ExecutionID: wait.ExecutionID, RunID: wait.RunID, InvocationID: wait.InvocationID, CallID: wait.CallID, Answers: answers}
	return func() tea.Msg {
		writer.Lock()
		defer writer.Unlock()
		result := questionDraftSavedMsg{wait: wait, advance: confirm, next: next}
		if writer.closed || revision <= writer.saved {
			return result
		}
		if !writer.submitted {
			if err := runtime.SaveUserQuestionDraft(context.Background(), identity, draft); err != nil {
				result.err = err
				return result
			}
			writer.saved = revision
			if !confirm || !all {
				return result
			}
			writer.submitted = true
			writer.submission = identity
		} else if confirm {
			identity = writer.submission
		}
		err := runtime.ResolveUserQuestion(context.Background(), identity)
		if err == nil {
			writer.closed = true
		}
		answer := ""
		if len(answers) == 1 {
			answer = answers[0].Answer
		}
		return questionAnsweredMsg{sessionID: sessionID, question: wait, answer: answer, answers: answers, err: err}
	}
}
func (m *tuiModel) addQuestionSummary(wait core.UserQuestionRequest, answers []core.UserQuestionResponse) {
	m.addEntry("question_summary", questionSummaryText(wait, answers), false)
}

func questionSummaryText(wait core.UserQuestionRequest, answers []core.UserQuestionResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Questions %d/%d answered", len(answers), len(wait.Questions))
	for i, a := range answers {
		if i >= len(wait.Questions) {
			break
		}
		fmt.Fprintf(&b, "\n  • %s\n    answer: %s", splitQuestionDisplay(wait.Questions[i].Question).question, a.Answer)
	}
	return b.String()
}
