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
	question := core.UserQuestionRequest{RunID: "run", InvocationID: "wait-1", CallID: "call-1", ExecutionID: "execution-1", Questions: []core.UserQuestionItem{{ID: "q1", Question: "Which environment?", Options: []core.UserQuestionOption{{Label: "Local", Description: "Use the workspace"}, {Label: "Staging", Description: "Use the shared environment"}}}}}
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
	if ui.question.notes || ui.question.input.Focused() || ui.View().Cursor != nil {
		t.Fatal("choice list should own focus when the question opens")
	}
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
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answers[0].Answer != "Staging" {
		t.Fatalf("answers=%#v", runtime.resolvedQuestions)
	}
	if ui.question != nil || ui.questionOpen {
		t.Fatal("answered question remained open")
	}
}

func TestQuestionFreeTextFailureAndRecoveryKeepDraft(t *testing.T) {
	ui, _ := questionFixture()
	ui.input.SetValue("ordinary composer draft")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	ui.Update(tea.PasteMsg{Content: "Use the test machine"})
	_, cmd := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	result := cmd().(questionAnsweredMsg)
	if result.answer != "Local\n\nUse the test machine" {
		t.Fatalf("free text=%q", result.answer)
	}
	result.err = errors.New("a different answer is already recorded for this user question")
	_, refresh := ui.Update(result)
	if refresh == nil || ui.question.input.Value() != "Use the test machine" || ui.input.Value() != "ordinary composer draft" {
		t.Fatal("failure lost a draft or did not refresh the request")
	}
	ui.Update(refresh())
	if ui.question.input.Value() != "Use the test machine" || len(ui.entries) != 0 {
		t.Fatal("same question reset the draft or spammed transcript")
	}
}

func TestQuestionNewIdentityReplacesOldQuestionAndEscapeInterruptsIt(t *testing.T) {
	ui, runtime := questionFixture()
	ui.question.input.SetValue("old draft")
	next := *runtime.pendingRequests[0].UserQuestion
	next.CallID = "call-2"
	next.InvocationID = "wait-2"
	next.Questions[0].Question = "Which region?"
	runtime.pendingRequests = []core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &next}}
	ui.continueTask("/continue")
	if !sameQuestion(ui.question.request, next) || ui.question.input.Value() != "" {
		t.Fatal("new question reused stale wait or draft")
	}
	_, interrupt := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if interrupt == nil || !ui.question.interrupting {
		t.Fatal("escape did not start interrupting the paused task")
	}
	ui.Update(interrupt())
	if ui.questionOpen || ui.question != nil || runtime.owner.discarded != 1 {
		t.Fatal("escape did not discard the paused question task")
	}
}

func TestQuestionNumericLabelIsNotRemapped(t *testing.T) {
	ui, runtime := questionFixture()
	q := *ui.pendingRequest.UserQuestion
	q.Questions[0].Options = []core.UserQuestionOption{{Label: "2"}, {Label: "3"}}
	ui.openQuestion(q)
	ui.pendingRequest.UserQuestion = &q
	_, cmd := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	ui.Update(cmd())
	if runtime.resolvedQuestions[0].Answers[0].Answer != "2" {
		t.Fatal("selected label was interpreted as an option number")
	}
}

func TestQuestionChoiceUIShowsEverySuggestedOptionWithoutOpeningNotes(t *testing.T) {
	for _, test := range []struct {
		name       string
		options    []core.UserQuestionOption
		wantChoice bool
	}{
		{name: "no options", wantChoice: true},
		{name: "one option", options: []core.UserQuestionOption{{Label: "Only suggestion"}}, wantChoice: true},
		{name: "two options", options: []core.UserQuestionOption{{Label: "Alpha"}, {Label: "Beta"}}, wantChoice: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ui := newTUIModel(&fakeResponder{}, false)
			ui.openQuestion(core.UserQuestionRequest{Questions: []core.UserQuestionItem{{ID: "q1", Question: "What should we do?", Options: test.options}}})
			view := plainTUIView(ui)
			if got := strings.Contains(view, "None of the above"); got != test.wantChoice {
				t.Fatalf("choice fallback visible = %v, want %v: %q", got, test.wantChoice, view)
			}
			if len(test.options) > 0 && (ui.question.notes || ui.question.input.Focused()) {
				t.Fatalf("question should open in choices state with notes hidden: %q", view)
			}
			if len(test.options) == 1 && !strings.Contains(view, "Only suggestion") {
				t.Fatalf("single option should be presented as a choice: %q", view)
			}
			if len(test.options) > 1 && (!strings.Contains(view, "Alpha") || !strings.Contains(view, "Beta")) {
				t.Fatalf("two-option question omitted choices: %q", view)
			}
		})
	}
}

func TestQuestionNoneOptionAcceptsFreeText(t *testing.T) {
	ui, runtime := questionFixture()
	question := *ui.pendingRequest.UserQuestion
	question.Questions[0].Options = []core.UserQuestionOption{{Label: "Only suggestion"}}
	ui.pendingRequest.UserQuestion = &question
	ui.question.request = question
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, openNotes := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	_ = openNotes
	if !ui.question.notes || !ui.question.input.Focused() {
		t.Fatal("Enter on None of the above did not open notes")
	}
	ui.question.input.SetValue("Use the standard approach")

	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("free-text answer was not submitted")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answers[0].Answer != "Use the standard approach" {
		t.Fatalf("resolved answer = %#v, want free text without the lone suggestion", runtime.resolvedQuestions)
	}
}

func TestQuestionArrowsAndTabLeaveNotesAndReturnToChoices(t *testing.T) {
	ui, runtime := questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	ui.question.input.SetValue("keep this note")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.question.selected != 1 || ui.question.notes || ui.question.input.Focused() {
		t.Fatalf("notes focus blocked navigation: selection=%d notes=%v", ui.question.selected, ui.question.notes)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.question.selected != 2 || ui.question.notes {
		t.Fatalf("None of the above selection unexpectedly focused notes: selection=%d notes=%v", ui.question.selected, ui.question.notes)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.question.notes || !ui.question.input.Focused() || ui.question.input.Prompt != "› Add notes: " {
		t.Fatalf("Tab on None did not focus notes: selection=%d prompt=%q", ui.question.selected, ui.question.input.Prompt)
	}
	ui.question.input.SetValue("custom response")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if ui.question.selected != 1 || ui.question.notes || ui.question.input.Focused() || ui.question.noteDraft != "keep this note" {
		t.Fatalf("Up did not return to choices and preserve notes: selection=%d notes=%v draft=%q", ui.question.selected, ui.question.notes, ui.question.noteDraft)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.question.notes || ui.question.input.Value() != "custom response" {
		t.Fatalf("custom draft was lost: %q", ui.question.input.Value())
	}
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("custom answer was not submitted")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answers[0].Answer != "custom response" {
		t.Fatalf("custom answer = %#v", runtime.resolvedQuestions)
	}
}

func TestQuestionChoiceRequiresTabBeforeNotesCanBeTyped(t *testing.T) {
	ui, _ := questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if ui.question.notes || strings.Contains(plainTUIView(ui), "Note:") {
		t.Fatal("typing in choice focus opened notes without Tab")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.question.notes || !strings.Contains(plainTUIView(ui), "Add notes:") {
		t.Fatal("Tab did not reveal the note input")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if ui.question.notes || ui.question.input.Focused() || ui.View().Cursor != nil {
		t.Fatal("Tab did not return focus to the choice list")
	}
}

func TestQuestionOptionsUseOneAlignedRowAndTruncateDescriptions(t *testing.T) {
	ui, _ := questionFixture()
	ui.width = 54
	ui.updateLayout()
	ui.question.request.Questions[0].Options[0].Description = "The active explanation remains fully visible across multiple lines."
	ui.question.request.Questions[0].Options[1].Description = "Brief preview followed by many words that should be clipped from this inactive option."
	plain := plainTUIView(ui)
	for _, label := range []string{"1. Local", "2. Staging", "3. None of the above"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("option %q is missing: %q", label, plain)
		}
	}
	if strings.Contains(plain, "many words that should be clipped") || strings.Contains(plain, "fully visible across multiple lines") {
		t.Fatalf("a long description was wrapped instead of truncated: %q", plain)
	}
	if !strings.Contains(plain, "The active explanation") || !strings.Contains(plain, "Optionally, add details...") {
		t.Fatalf("same-row descriptions are incomplete: %q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "1. Local") && !strings.Contains(line, "The active explanation") {
			t.Fatalf("option description was wrapped to a separate row: %q", line)
		}
	}
	if strings.Contains(plain, "Type your own answer in notes") || strings.Contains(plain, "Waiting for your answer (see prompt above)") {
		t.Fatalf("duplicate question guidance remained: %q", plain)
	}
}

func TestQuestionSplitsBackgroundIntoAgentMessage(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.openQuestion(core.UserQuestionRequest{Questions: []core.UserQuestionItem{{ID: "q1", Question: "失败时保留上下文。\n\n取消后如何处理？\n\n失败或取消后，标题怎么显示？", Options: []core.UserQuestionOption{{Label: "保留状态"}}}}})
	if ui.question.displayText != "失败或取消后，标题怎么显示？" {
		t.Fatalf("panel prompt = %q", ui.question.displayText)
	}
	if len(ui.entries) != 1 || ui.entries[0].role != "assistant" || !strings.Contains(ui.entries[0].text, "取消后如何处理？") {
		t.Fatalf("background context was not moved into an Agent message: %#v", ui.entries)
	}
	if strings.Contains(plainTUIView(ui), "Waiting for your answer") || strings.Contains(plainTUIView(ui), "Type your own answer in notes") {
		t.Fatal("legacy question guidance remained visible")
	}
	parts := splitQuestionDisplay("为避免丢失上下文，需要明确执行方向。\n已检查现有状态\n下一步落在哪条线")
	if parts.background != "为避免丢失上下文，需要明确执行方向。\n已检查现有状态" || parts.question != "下一步落在哪条线" {
		t.Fatalf("multi-line preamble split = %#v", parts)
	}
}

func TestOpeningQuestionRemovesOnlyLastPreparingToolPlaceholder(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, true)
	ui.updateToolActivity(core.ToolCall{ExecutionID: "execution", ID: "read", Name: "read_file", Arguments: []byte(`{"path":"README.md"}`), Started: false})
	ui.updateToolActivity(core.ToolCall{ExecutionID: "execution", ID: "pending", Name: "unknown_tool", Arguments: []byte(`{}`), Started: true})
	ui.activities[len(ui.activities)-1].summary = "Tool request (preparing)"
	ui.entries[len(ui.entries)-1].text = "Tool request (preparing)"

	ui.openQuestion(core.UserQuestionRequest{ExecutionID: "execution", Questions: []core.UserQuestionItem{{ID: "q1", Question: "Continue?", Options: []core.UserQuestionOption{{Label: "Yes"}}}}})
	if len(ui.activities) != 1 || len(ui.entries) != 1 || !strings.Contains(ui.entries[0].text, "README.md") {
		t.Fatalf("question cleanup removed a real tool or kept the placeholder: activities=%#v entries=%#v", ui.activities, ui.entries)
	}

	ui = newTUIModel(&fakeResponder{}, true)
	ui.modelStreamTaskID = "task"
	ui.streamToolCalls = map[string]*streamToolCall{
		"pending": {taskID: "task", executionID: "execution", agentID: "lead", activityID: "stream-pending", preparing: true},
	}
	ui.entries = []tuiEntry{{role: "tool", activityID: "stream-pending", text: "Tool request (preparing)"}}
	ui.openQuestion(core.UserQuestionRequest{ExecutionID: "execution", Questions: []core.UserQuestionItem{{ID: "q1", Question: "Continue?"}}})
	call := ui.streamToolCalls["pending"]
	if call.preparing || !call.finished || !call.hidden || len(ui.entries) != 0 {
		t.Fatalf("stream placeholder remained live after question: call=%#v entries=%#v", call, ui.entries)
	}
}
