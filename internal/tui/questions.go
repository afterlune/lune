package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type questionWaitKey struct {
	SessionID, ExecutionID, RunID, InvocationID, CallID string
}

type questionSubmissionState uint8

const (
	questionSubmitting questionSubmissionState = iota + 1
	questionAccepted
)

func recommendedQuestionOptionIndex(options []core.UserQuestionOption) int {
	for index, option := range options {
		if option.Recommended {
			return index
		}
	}
	return 0
}

func newActiveQuestion(request core.UserQuestionRequest) *ActiveQuestion {
	q := &ActiveQuestion{Request: request}
	q.ensureDrafts()
	return q
}

func (q *ActiveQuestion) ensureDrafts() {
	for len(q.Drafts) < len(q.Request.Questions) {
		item := q.Request.Questions[len(q.Drafts)]
		q.Drafts = append(q.Drafts, questionDraft{Selected: recommendedQuestionOptionIndex(item.Options)})
	}
}

func (q *ActiveQuestion) currentDraft() *questionDraft {
	if len(q.Request.Questions) == 0 {
		return nil
	}
	q.ensureDrafts()
	q.CurrentIndex = max(0, min(q.CurrentIndex, len(q.Request.Questions)-1))
	return &q.Drafts[q.CurrentIndex]
}

func (d questionDraft) answerText(item core.UserQuestionItem) string {
	answer := "None of the above"
	if d.Selected >= 0 && d.Selected < len(item.Options) {
		answer = item.Options[d.Selected].Label
	} else if custom := strings.TrimSpace(d.Custom); custom != "" {
		answer = custom
	}
	if note := strings.TrimSpace(d.NoteText); note != "" {
		if answer == "None of the above" {
			answer = note
		} else {
			answer += " (" + note + ")"
		}
	}
	return answer
}

func (q *ActiveQuestion) confirmedAnswers() []core.UserQuestionResponse {
	q.ensureDrafts()
	var answers []core.UserQuestionResponse
	for i, item := range q.Request.Questions {
		if q.Drafts[i].Confirmed {
			answers = append(answers, core.UserQuestionResponse{QuestionID: item.ID, Answer: q.Drafts[i].answerText(item)})
		}
	}
	return answers
}

func (m *tuiModel) questionDraftChanged(q *ActiveQuestion) {
	q.currentDraft().Confirmed = false
	q.Error = ""
	m.recordQuestionAnswers(q)
}

type questionSubmittedMsg struct {
	key questionWaitKey
	err error
}

func (m *tuiModel) questionKey(request core.UserQuestionRequest) questionWaitKey {
	callID := request.CallID
	if callID == "" {
		callID = request.ID
	}
	return questionWaitKey{m.sessionID, request.ExecutionID, request.RunID, request.InvocationID, callID}
}

func (m *tuiModel) questionInFlight(executionID string) bool {
	for key, state := range m.run.questionSubmissions {
		if key.SessionID == m.sessionID && key.ExecutionID == executionID && state == questionSubmitting {
			return true
		}
	}
	return false
}

func (m *tuiModel) recordQuestionAnswers(q *ActiveQuestion) {
	q.ensureDrafts()
	var answers []AnsweredQuestionItem
	for i, item := range q.Request.Questions {
		if q.Drafts[i].Confirmed {
			answers = append(answers, AnsweredQuestionItem{Question: item.Question, Answer: q.Drafts[i].answerText(item)})
		}
	}
	encoded, _ := json.Marshal(m.questionKey(q.Request))
	batchID := string(encoded)
	for i := range m.entries {
		entry := &m.entries[i]
		if entry.Kind != KindQuestionAnswered || entry.QuestionBatchID != batchID {
			continue
		}
		if len(answers) == 0 {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
		} else {
			entry.ExecutionID = q.Request.ExecutionID
			entry.AnsweredQuestions = answers
		}
		m.view.invalidateBlocks()
		return
	}
	if len(answers) == 0 {
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		Kind: KindQuestionAnswered, ExecutionID: q.Request.ExecutionID, QuestionBatchID: batchID,
		QuestionsTotal: len(q.Request.Questions), AnsweredQuestions: answers,
	})
	m.view.invalidateBlocks()
}

func (m *tuiModel) submitQuestionBatch(q *ActiveQuestion) tea.Cmd {
	answers := q.confirmedAnswers()
	if q.Submitting || len(answers) == 0 || len(answers) != len(q.Request.Questions) {
		return nil
	}
	key := m.questionKey(q.Request)
	if m.run.questionSubmissions == nil {
		m.run.questionSubmissions = make(map[questionWaitKey]questionSubmissionState)
	}
	m.run.questionSubmissions[key] = questionSubmitting
	q.Submitting, q.Error = true, ""
	// WaitTurn can still be returning the pause that produced this question.
	// Its result must not finalize the resumed attempt under the same execution ID.
	delete(m.run.awaitingTurns, key.ExecutionID)
	delete(m.run.terminalEvents, key.ExecutionID)
	answer := core.UserQuestionAnswer{
		ExecutionID: q.Request.ExecutionID, RunID: q.Request.RunID,
		InvocationID: q.Request.InvocationID, CallID: q.Request.CallID,
		Answers: answers,
	}
	runtime := m.runtime
	return func() tea.Msg {
		if runtime == nil {
			return questionSubmittedMsg{key: key, err: errors.New("session runtime is unavailable")}
		}
		return questionSubmittedMsg{key: key, err: runtime.ResolveUserQuestion(context.Background(), answer)}
	}
}

func (m *tuiModel) finishQuestionSubmission(msg questionSubmittedMsg) tea.Cmd {
	if msg.key.SessionID != m.sessionID || m.run.questionSubmissions[msg.key] != questionSubmitting {
		return nil
	}
	q := m.activeQuestion
	if msg.err != nil {
		delete(m.run.questionSubmissions, msg.key)
		if q != nil && m.questionKey(q.Request) == msg.key {
			q.Submitting = false
			q.Error = "Failed to submit answers: " + msg.err.Error() + ". Press enter to retry."
		}
		m.updateLayout()
		return nil
	}
	m.run.questionSubmissions[msg.key] = questionAccepted
	if q != nil && m.questionKey(q.Request) == msg.key {
		m.activeQuestion = nil
	}
	m.restoreRuntimeState(context.Background())
	m.checkPendingState(context.Background())
	if m.run.taskSnapshot.ExecutionID == msg.key.ExecutionID {
		switch m.run.taskSnapshot.Status {
		case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
			// A fast resumed model may finish before this acknowledgement is
			// rendered. Fetch its canonical result even though it is now idle.
			m.run.runningExecution = msg.key.ExecutionID
			m.updateLayout()
			return m.awaitExecutionResult(msg.key.ExecutionID)
		}
	}
	if m.run.taskSnapshot.ExecutionID == msg.key.ExecutionID && m.run.taskSnapshot.Status == core.TaskPaused &&
		m.activeQuestion == nil && m.activeRecovery == nil && m.activeToolRecovery == nil {
		// Admission may precede the next task_started event. Do not display the
		// answered wait's pause banner or wait again on its old paused result.
		m.run.running = true
		m.run.runningExecution = msg.key.ExecutionID
		m.pausedCheckpoint = nil
		m.updateLayout()
		return tickClock()
	}
	if m.run.running {
		m.pausedCheckpoint = nil
	}
	m.updateLayout()
	return tea.Batch(tickClock(), m.waitForExecution(m.run.runningExecution))
}

func (m *tuiModel) staleTaskAttempt(executionID, taskID string) bool {
	return executionID == m.run.taskSnapshot.ExecutionID && taskID != "" &&
		m.run.taskSnapshot.TaskID != "" && taskID != m.run.taskSnapshot.TaskID
}

// A delayed pause belongs to an earlier attempt when Core has already resumed
// or completed the execution, or the answer is currently being admitted.
func (m *tuiModel) stalePause(executionID, taskID string) bool {
	if m.staleTaskAttempt(executionID, taskID) || m.questionInFlight(executionID) {
		return true
	}
	if m.runtime != nil {
		if state, err := m.runtime.State(context.Background()); err == nil &&
			state.Task.ExecutionID == executionID && state.Task.Status != core.TaskPaused {
			m.restoreRuntimeSnapshot(state)
			m.checkPendingState(context.Background())
			return true
		}
	}
	return false
}
