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
	if ui.question.notes || ui.question.input.Focused() {
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

func TestQuestionChoiceUIShowsEverySuggestedOption(t *testing.T) {
	for _, test := range []struct {
		name       string
		options    []core.UserQuestionOption
		wantChoice bool
	}{
		{name: "no options"},
		{name: "one option", options: []core.UserQuestionOption{{Label: "Only suggestion"}}, wantChoice: true},
		{name: "two options", options: []core.UserQuestionOption{{Label: "Alpha"}, {Label: "Beta"}}, wantChoice: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ui := newTUIModel(&fakeResponder{}, false)
			ui.openQuestion(core.UserQuestionRequest{Question: "What should we do?", Options: test.options})
			view := plainTUIView(ui)
			if got := strings.Contains(view, "None of the above"); got != test.wantChoice {
				t.Fatalf("choice fallback visible = %v, want %v: %q", got, test.wantChoice, view)
			}
			wantInputFocused := !test.wantChoice
			if ui.question.notes != wantInputFocused || ui.question.input.Focused() != wantInputFocused {
				t.Fatalf("input focus = %v/%v, want %v: %q", ui.question.notes, ui.question.input.Focused(), wantInputFocused, view)
			}
			if len(test.options) == 0 && (!ui.question.notes || !ui.question.input.Focused()) {
				t.Fatalf("free-text question should focus its input: %q", view)
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

func TestSingleOptionQuestionAcceptsFreeTextWithoutSelectingOption(t *testing.T) {
	ui, runtime := questionFixture()
	question := *ui.pendingRequest.UserQuestion
	question.Options = []core.UserQuestionOption{{Label: "Only suggestion"}}
	ui.pendingRequest.UserQuestion = &question
	ui.question.request = question
	ui.question.setSelection(1)
	ui.question.input.SetValue("Use the standard approach")

	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("free-text answer was not submitted")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answer != "Use the standard approach" {
		t.Fatalf("resolved answer = %#v, want free text without the lone suggestion", runtime.resolvedQuestions)
	}
}

func TestQuestionArrowsNavigateWhileNotesAndCustomAnswerAreFocused(t *testing.T) {
	ui, runtime := questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	ui.question.input.SetValue("keep this note")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.question.selected != 1 || !ui.question.notes {
		t.Fatalf("notes focus blocked navigation: selection=%d notes=%v", ui.question.selected, ui.question.notes)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.question.selected != 2 || !ui.question.notes || ui.question.input.Prompt != "› Type your answer: " {
		t.Fatalf("None of the above did not focus custom input: selection=%d prompt=%q", ui.question.selected, ui.question.input.Prompt)
	}
	ui.question.input.SetValue("custom response")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if ui.question.selected != 1 || ui.question.notes || ui.question.input.Value() != "keep this note" {
		t.Fatalf("leaving custom answer did not collapse and restore notes: selection=%d notes=%v value=%q", ui.question.selected, ui.question.notes, ui.question.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.question.input.Value() != "custom response" {
		t.Fatalf("custom draft was lost: %q", ui.question.input.Value())
	}
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("custom answer was not submitted")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answer != "custom response" {
		t.Fatalf("custom answer = %#v", runtime.resolvedQuestions)
	}
}

func TestQuestionNumberShortcutSubmitsChoiceAndNoneFocusesInput(t *testing.T) {
	ui, runtime := questionFixture()
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: '1', Text: "1"}))
	if command == nil {
		t.Fatal("numeric choice did not submit")
	}
	ui.Update(command())
	if len(runtime.resolvedQuestions) != 1 || runtime.resolvedQuestions[0].Answer != "Local" {
		t.Fatalf("numeric choice answer=%#v", runtime.resolvedQuestions)
	}

	ui, _ = questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: '3', Text: "3"}))
	if ui.question.selected != len(ui.question.request.Options) || !ui.question.notes || ui.question.input.Prompt != "› Type your answer: " {
		t.Fatalf("numeric custom choice did not focus input: %#v", ui.question)
	}
	if !strings.Contains(plainTUIView(ui), "› Type your answer:") {
		t.Fatal("custom answer prompt was not rendered under None of the above")
	}
	ui.question.input.SetValue("typed custom answer")
	if _, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: '1', Text: "1"})); command != nil || ui.question.input.Value() != "typed custom answer1" {
		t.Fatal("digit typed in the focused custom answer was not treated as text")
	}
}

func TestQuestionChoiceRequiresTabBeforeNotesCanBeTyped(t *testing.T) {
	ui, _ := questionFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if ui.question.notes || strings.Contains(plainTUIView(ui), "Note:") {
		t.Fatal("typing in choice focus opened notes without Tab")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.question.notes || !strings.Contains(plainTUIView(ui), "Note:") {
		t.Fatal("Tab did not reveal the note input")
	}
}

func TestQuestionAccordionShowsFullActiveDescriptionAndOneLinePreviews(t *testing.T) {
	ui, _ := questionFixture()
	ui.width = 54
	ui.question.request.Options[0].Description = "The active explanation remains fully visible across multiple lines."
	ui.question.request.Options[1].Description = "Brief preview followed by many words that should be clipped from this inactive option."
	plain := plainTUIView(ui)
	if !strings.Contains(plain, "active explanation") {
		t.Fatalf("active description was collapsed: %q", plain)
	}
	if strings.Contains(plain, "many words that should be clipped") {
		t.Fatalf("inactive description expanded instead of truncating: %q", plain)
	}
	if !strings.Contains(plain, "Brief preview") || !strings.Contains(plain, "type custom answer") {
		t.Fatalf("compact option labels are incomplete: %q", plain)
	}
	if strings.Contains(plain, "Type your own answer in notes") || strings.Contains(plain, "Waiting for your answer (see prompt above)") {
		t.Fatalf("duplicate question guidance remained: %q", plain)
	}
}

func TestOpeningQuestionRemovesOnlyLastPreparingToolPlaceholder(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, true)
	ui.updateToolActivity(core.ToolCall{ExecutionID: "execution", ID: "read", Name: "read_file", Arguments: []byte(`{"path":"README.md"}`), Started: false})
	ui.updateToolActivity(core.ToolCall{ExecutionID: "execution", ID: "pending", Name: "unknown_tool", Arguments: []byte(`{}`), Started: true})
	ui.activities[len(ui.activities)-1].summary = "Tool request (preparing)"
	ui.entries[len(ui.entries)-1].text = "Tool request (preparing)"

	ui.openQuestion(core.UserQuestionRequest{ExecutionID: "execution", Question: "Continue?", Options: []core.UserQuestionOption{{Label: "Yes"}}})
	if len(ui.activities) != 1 || len(ui.entries) != 1 || !strings.Contains(ui.entries[0].text, "README.md") {
		t.Fatalf("question cleanup removed a real tool or kept the placeholder: activities=%#v entries=%#v", ui.activities, ui.entries)
	}

	ui = newTUIModel(&fakeResponder{}, true)
	ui.modelStreamTaskID = "task"
	ui.streamToolCalls = map[string]*streamToolCall{
		"pending": {taskID: "task", executionID: "execution", agentID: "lead", activityID: "stream-pending", preparing: true},
	}
	ui.entries = []tuiEntry{{role: "tool", activityID: "stream-pending", text: "Tool request (preparing)"}}
	ui.openQuestion(core.UserQuestionRequest{ExecutionID: "execution", Question: "Continue?"})
	call := ui.streamToolCalls["pending"]
	if call.preparing || !call.finished || !call.hidden || len(ui.entries) != 0 {
		t.Fatalf("stream placeholder remained live after question: call=%#v entries=%#v", call, ui.entries)
	}
}
