package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func questionFixture() (*tuiModel, *fakeSessionRuntime) {
	client := &fakeResponder{pending: true}
	runtime := newFakeSessionRuntime(client, "session-1", client.runTask)
	runtime.status = core.TaskPaused
	question := core.UserQuestionRequest{RunID: "run", InvocationID: "wait-1", CallID: "call-1", ExecutionID: "execution-1", Question: "Which environment?", Options: []core.UserQuestionOption{{Label: "Local", Description: "Use the workspace"}, {Label: "Staging", Description: "Use the shared environment"}}}
	runtime.pendingRequests = []core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &question}}
	ui := newTUIModel(client, true)
	ui.sessionID = "session-1"
	ui.runtimeSessionID = ui.sessionID
	ui.runtime = runtime
	ui.Update(ui.inspectPausedTask("")())
	return ui, runtime
}

func TestQuestionNavigationDoesNotSubmitAndEnterSubmitsOnce(t *testing.T) {
	ui, runtime := questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if len(runtime.resolvedQuestions) != 0 {
		t.Fatal("navigation submitted an answer")
	}
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil || !ui.question.submitting {
		t.Fatal("answer was not submitted asynchronously")
	}
	if _, again := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})); again != nil {
		t.Fatal("duplicate enter submitted twice")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answer != "Staging" {
		t.Fatalf("answers=%#v", runtime.resolvedQuestions)
	}
	if ui.question != nil || ui.questionOpen {
		t.Fatal("answered question remained open")
	}
}

func TestQuestionFreeTextFailureAndRecoveryKeepDraft(t *testing.T) {
	ui, _ := questionFixture()
	ui.input.SetValue("ordinary composer draft")
	ui.Update(tea.PasteMsg{Content: "Use the test machine"})
	_, cmd := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	result := cmd().(questionAnsweredMsg)
	if result.answer != "Use the test machine" {
		t.Fatalf("free text=%q", result.answer)
	}
	result.err = errors.New("a different answer is already recorded for this user question")
	_, refresh := ui.Update(result)
	if refresh == nil || ui.question.input.Value() != result.answer || ui.input.Value() != "ordinary composer draft" {
		t.Fatal("failure lost a draft or did not refresh the request")
	}
	ui.Update(refresh())
	if ui.question.input.Value() != result.answer || len(ui.entries) != 0 {
		t.Fatal("same question reset the draft or spammed transcript")
	}
}

func TestQuestionNewIdentityReplacesOldQuestionAndEscapePreservesIt(t *testing.T) {
	ui, runtime := questionFixture()
	ui.question.input.SetValue("old draft")
	next := *runtime.pendingRequests[0].UserQuestion
	next.CallID = "call-2"
	next.InvocationID = "wait-2"
	next.Question = "Which region?"
	runtime.pendingRequests = []core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &next}}
	ui.continueTask("/continue")
	if !sameQuestion(ui.question.request, next) || ui.question.input.Value() != "" {
		t.Fatal("new question reused stale wait or draft")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.questionOpen || ui.pendingRequest == nil {
		t.Fatal("escape answered or discarded the question")
	}
	ui.continueTask("/continue")
	if !ui.questionOpen || !strings.Contains(plainTUIView(ui), "Which region?") {
		t.Fatal("continue did not reopen the question")
	}
}

func TestQuestionNumericLabelIsNotRemapped(t *testing.T) {
	ui, runtime := questionFixture()
	q := *ui.pendingRequest.UserQuestion
	q.Options = []core.UserQuestionOption{{Label: "2"}, {Label: "3"}}
	ui.openQuestion(q)
	ui.pendingRequest.UserQuestion = &q
	_, cmd := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	ui.Update(cmd())
	if runtime.resolvedQuestions[0].Answer != "2" {
		t.Fatal("selected label was interpreted as an option number")
	}
}
