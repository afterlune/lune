package tui

import (
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestPausedTaskDetailsExplainCauseAndNextActionWithoutRecovering(t *testing.T) {
	client := &fakeResponder{pending: true}
	runtime := newFakeSessionRuntime(client, "session-1", client.runTask)
	runtime.status = core.TaskPaused
	runtime.pendingRequests = []core.PendingRequest{{
		Kind:    core.PendingExecutionBlocker,
		Blocker: &core.ExecutionBlocker{Tool: "command", Reason: "not authorized"},
	}}
	ui := newTUIModel(client, true)
	ui.sessionID = "session-1"
	ui.runtime = runtime
	ui.runtimeSessionID = "session-1"

	command := ui.inspectPausedTask(core.BudgetMaxTokens)
	if command == nil {
		t.Fatal("paused task inspection did not return a command")
	}
	ui.Update(command().(pausedTaskDetailsMsg))

	transcript := strings.Join(entryTexts(ui.entries), "\n")
	for _, expected := range []string{"token budget was reached", "Execution blocked by command: not authorized", "/plan replan", "/discard"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("paused task explanation missing %q: %q", expected, transcript)
		}
	}
	if client.resumes != 0 {
		t.Fatalf("showing paused task details resumed the task %d times", client.resumes)
	}
}

func TestPausedQuestionIsRestoredAsAnAnswerTarget(t *testing.T) {
	client := &fakeResponder{pending: true}
	runtime := newFakeSessionRuntime(client, "session-1", client.runTask)
	runtime.status = core.TaskPaused
	runtime.pendingRequests = []core.PendingRequest{{
		Kind:         core.PendingUserQuestion,
		UserQuestion: &core.UserQuestionRequest{Questions: []core.UserQuestionItem{{ID: "q1", Question: "Choose a number", Options: []core.UserQuestionOption{{Label: "1"}, {Label: "2"}}}}},
	}}
	ui := newTUIModel(client, true)
	ui.sessionID = "session-1"
	ui.runtime = runtime
	ui.runtimeSessionID = "session-1"

	ui.Update(ui.inspectPausedTask("")().(pausedTaskDetailsMsg))
	if ui.pendingRequest == nil || ui.pendingRequest.UserQuestion == nil {
		t.Fatal("paused user question was not restored as the active answer target")
	}
	transcript := plainTUIView(ui)
	if !strings.Contains(transcript, "Choose a number") || !strings.Contains(transcript, "enter to submit answer") {
		t.Fatalf("paused question was not explained in the transcript: %q", transcript)
	}
	if !strings.Contains(transcript, "1. 1") || !strings.Contains(transcript, "2. 2") {
		t.Fatalf("paused question options were not numbered: %q", transcript)
	}
	if client.resumes != 0 {
		t.Fatalf("restoring the question resumed the task %d times", client.resumes)
	}
}

func TestPausedQuestionAcceptsNumberedChoice(t *testing.T) {
	client := &fakeResponder{pending: true}
	runtime := newFakeSessionRuntime(client, "session-1", client.runTask)
	runtime.status = core.TaskPaused
	question := core.UserQuestionRequest{ExecutionID: "execution-1", Questions: []core.UserQuestionItem{{ID: "q1", Question: "Choose a color", Options: []core.UserQuestionOption{{Label: "blue"}, {Label: "green"}}}}}
	ui := newTUIModel(client, true)
	ui.sessionID = "session-1"
	ui.runtime = runtime
	ui.runtimeSessionID = "session-1"
	ui.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion, UserQuestion: &question}

	_, command := ui.answerUserQuestion("1")
	if command != nil {
		ui.Update(command())
	}
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answers[0].Answer != "blue" {
		t.Fatalf("numeric answer was not mapped to the first option: %#v", runtime.resolvedQuestions)
	}
}
