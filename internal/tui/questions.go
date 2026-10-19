package tui

import (
	"context"
	"encoding/json"
	"errors"

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
	for key, state := range m.questionSubmissions {
		if key.SessionID == m.sessionID && key.ExecutionID == executionID && state == questionSubmitting {
			return true
		}
	}
	return false
}

func (m *tuiModel) recordQuestionAnswer(q *ActiveQuestion, index int, answer AnsweredQuestionItem) {
	encoded, _ := json.Marshal(m.questionKey(q.Request))
	batchID := string(encoded)
	for i := range m.entries {
		entry := &m.entries[i]
		if entry.Kind != KindQuestionAnswered || entry.QuestionBatchID != batchID {
			continue
		}
		if index < len(entry.AnsweredQuestions) {
			entry.AnsweredQuestions[index] = answer
		} else {
			entry.AnsweredQuestions = append(entry.AnsweredQuestions, answer)
		}
		m.invalidateEntryCache()
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		Kind: KindQuestionAnswered, QuestionBatchID: batchID,
		QuestionsTotal: len(q.Request.Questions), AnsweredQuestions: []AnsweredQuestionItem{answer},
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) submitQuestionBatch(q *ActiveQuestion) tea.Cmd {
	key := m.questionKey(q.Request)
	if m.questionSubmissions == nil {
		m.questionSubmissions = make(map[questionWaitKey]questionSubmissionState)
	}
	m.questionSubmissions[key] = questionSubmitting
	q.Submitting, q.Error = true, ""
	// WaitTurn can still be returning the pause that produced this question.
	// Its result must not finalize the resumed attempt under the same execution ID.
	delete(m.awaitingTurns, key.ExecutionID)
	delete(m.terminalEvents, key.ExecutionID)
	answer := core.UserQuestionAnswer{
		ExecutionID: q.Request.ExecutionID, RunID: q.Request.RunID,
		InvocationID: q.Request.InvocationID, CallID: q.Request.CallID,
		Answers: append([]core.UserQuestionResponse(nil), q.Answers...),
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
	if msg.key.SessionID != m.sessionID || m.questionSubmissions[msg.key] != questionSubmitting {
		return nil
	}
	q := m.activeQuestion
	if msg.err != nil {
		delete(m.questionSubmissions, msg.key)
		if q != nil && m.questionKey(q.Request) == msg.key {
			q.Submitting = false
			q.Error = "Failed to submit answers: " + msg.err.Error() + ". Press enter to retry."
		}
		m.updateLayout()
		return nil
	}
	m.questionSubmissions[msg.key] = questionAccepted
	if q != nil && m.questionKey(q.Request) == msg.key {
		m.activeQuestion = nil
	}
	m.restoreRuntimeState(context.Background())
	m.checkPendingState(context.Background())
	if m.taskSnapshot.ExecutionID == msg.key.ExecutionID {
		switch m.taskSnapshot.Status {
		case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
			// A fast resumed model may finish before this acknowledgement is
			// rendered. Fetch its canonical result even though it is now idle.
			m.runningExecution = msg.key.ExecutionID
			m.updateLayout()
			return m.awaitExecutionResult(msg.key.ExecutionID)
		}
	}
	if m.taskSnapshot.ExecutionID == msg.key.ExecutionID && m.taskSnapshot.Status == core.TaskPaused &&
		m.activeQuestion == nil && m.activeRecovery == nil && m.activeToolRecovery == nil {
		// Admission may precede the next task_started event. Do not display the
		// answered wait's pause banner or wait again on its old paused result.
		m.running = true
		m.runningExecution = msg.key.ExecutionID
		m.pausedCheckpoint = nil
		m.updateLayout()
		return tickClock()
	}
	if m.running {
		m.pausedCheckpoint = nil
	}
	m.updateLayout()
	return tea.Batch(tickClock(), m.waitForExecution(m.runningExecution))
}

func (m *tuiModel) staleTaskAttempt(executionID, taskID string) bool {
	return executionID == m.taskSnapshot.ExecutionID && taskID != "" &&
		m.taskSnapshot.TaskID != "" && taskID != m.taskSnapshot.TaskID
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
