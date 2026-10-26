package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

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
	q.restoreDraft()
	return q
}

func (q *ActiveQuestion) restoreDraft() {
	if q.Request.Draft == nil {
		return
	}
	draft := q.Request.Draft
	for index, item := range q.Request.Questions {
		for _, response := range draft.Responses {
			if response.QuestionID != item.ID {
				continue
			}
			selected := response.SelectedOption
			if selected < 0 {
				selected = len(item.Options)
			} else {
				selected = min(selected, len(item.Options))
			}
			q.Drafts[index] = questionDraft{
				Selected: selected, Custom: response.CustomAnswer,
				NoteText: response.Notes, Confirmed: response.Confirmed,
			}
			break
		}
		if item.ID == draft.CurrentQuestionID {
			q.CurrentIndex = index
		}
	}
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

func (m *tuiModel) questionDraftChanged(q *ActiveQuestion) tea.Cmd {
	if q == nil {
		return nil
	}
	q.currentDraft().Confirmed = false
	q.Error = ""
	q.draftRevision++
	m.recordQuestionAnswers(q)
	return m.scheduleQuestionDraftSave(q)
}

type questionDraftSaveDueMsg struct {
	key               questionWaitKey
	bindingGeneration uint64
	revision          uint64
}

type questionDraftSavedMsg struct {
	key               questionWaitKey
	bindingGeneration uint64
	revision          uint64
	err               error
}

func (m *tuiModel) scheduleQuestionDraftSave(q *ActiveQuestion) tea.Cmd {
	if q == nil || q.Submitting || m.runtime == nil || q.draftRevision <= q.savedRevision {
		return nil
	}
	key, generation, revision := m.questionKey(q.Request), m.bindingGeneration, q.draftRevision
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg {
		return questionDraftSaveDueMsg{key: key, bindingGeneration: generation, revision: revision}
	})
}

func (m *tuiModel) questionDraftValue(q *ActiveQuestion) core.UserQuestionDraft {
	q.ensureDrafts()
	draft := core.UserQuestionDraft{Responses: make([]core.UserQuestionDraftResponse, 0, len(q.Request.Questions))}
	if q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Request.Questions) {
		draft.CurrentQuestionID = q.Request.Questions[q.CurrentIndex].ID
	}
	for index, item := range q.Request.Questions {
		answer := q.Drafts[index]
		selected := answer.Selected
		if selected >= len(item.Options) {
			selected = -1
		}
		draft.Responses = append(draft.Responses, core.UserQuestionDraftResponse{
			QuestionID: item.ID, SelectedOption: selected, Notes: answer.NoteText,
			CustomAnswer: answer.Custom, Confirmed: answer.Confirmed,
		})
	}
	return draft
}

func (m *tuiModel) saveQuestionDraftCommand(q *ActiveQuestion, revision uint64) tea.Cmd {
	if q == nil || m.runtime == nil {
		return nil
	}
	runtime := m.runtime
	ctx := m.boundContext
	if ctx == nil {
		ctx = context.Background()
	}
	key, generation := m.questionKey(q.Request), m.bindingGeneration
	draft := m.questionDraftValue(q)
	answer := core.UserQuestionAnswer{
		ExecutionID: q.Request.ExecutionID, RunID: q.Request.RunID,
		InvocationID: q.Request.InvocationID, CallID: q.Request.CallID,
	}
	return func() tea.Msg {
		m.questionDraftSaveMu.Lock()
		defer m.questionDraftSaveMu.Unlock()
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := runtime.SaveUserQuestionDraft(writeCtx, answer, draft)
		return questionDraftSavedMsg{key: key, bindingGeneration: generation, revision: revision, err: err}
	}
}

func (m *tuiModel) flushQuestionDraft(ctx context.Context) error {
	q := m.activeQuestion
	if q == nil || q.Submitting || q.draftRevision <= q.savedRevision || m.runtime == nil {
		return nil
	}
	runtime := m.runtime
	draft := m.questionDraftValue(q)
	answer := core.UserQuestionAnswer{
		ExecutionID: q.Request.ExecutionID, RunID: q.Request.RunID,
		InvocationID: q.Request.InvocationID, CallID: q.Request.CallID,
	}
	m.questionDraftSaveMu.Lock()
	defer m.questionDraftSaveMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := runtime.SaveUserQuestionDraft(flushCtx, answer, draft); err != nil {
		return err
	}
	q.savedRevision = q.draftRevision
	return nil
}

func (m *tuiModel) handleQuestionDraftSaveDue(msg questionDraftSaveDueMsg) tea.Cmd {
	q := m.activeQuestion
	if q == nil || msg.key != m.questionKey(q.Request) || msg.bindingGeneration != m.bindingGeneration ||
		msg.revision != q.draftRevision || q.Submitting || q.saveInFlight {
		return nil
	}
	q.saveInFlight = true
	return m.saveQuestionDraftCommand(q, msg.revision)
}

func (m *tuiModel) finishQuestionDraftSave(msg questionDraftSavedMsg) tea.Cmd {
	q := m.activeQuestion
	if q == nil || msg.key != m.questionKey(q.Request) || msg.bindingGeneration != m.bindingGeneration {
		return nil
	}
	q.saveInFlight = false
	if msg.err == nil {
		q.savedRevision = max(q.savedRevision, msg.revision)
	} else if !q.Submitting && msg.revision == q.draftRevision {
		q.Error = "Could not save question draft: " + msg.err.Error()
		m.updateLayout()
		return nil
	}
	if q.draftRevision > q.savedRevision && !q.Submitting {
		return m.scheduleQuestionDraftSave(q)
	}
	m.updateLayout()
	return nil
}

type questionSubmittedMsg struct {
	key               questionWaitKey
	bindingGeneration uint64
	err               error
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
	q.draftRevision++
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
	bindingGeneration := m.bindingGeneration
	return func() tea.Msg {
		if runtime == nil {
			return questionSubmittedMsg{key: key, bindingGeneration: bindingGeneration, err: errors.New("session runtime is unavailable")}
		}
		return questionSubmittedMsg{key: key, bindingGeneration: bindingGeneration, err: runtime.ResolveUserQuestion(context.Background(), answer)}
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
			m.updateLayout()
			return m.scheduleQuestionDraftSave(q)
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
