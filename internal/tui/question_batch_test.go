package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func TestQuestionBatchConfirmsLocallyRestoresDraftAndSubmitsOnce(t *testing.T) {
	ui, runtime := questionFixture()
	wait := *ui.pendingRequest.UserQuestion
	wait.Questions = append(wait.Questions, core.UserQuestionItem{ID: "q2", Question: "Which scope?", Options: []core.UserQuestionOption{{Label: "All"}, {Label: "Storage"}}})
	ui.question = nil
	ui.pendingRequest.UserQuestion = &wait
	runtime.pendingRequests[0].UserQuestion = &wait
	ui.openQuestion(wait)
	_, cmd := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("missing confirmation save")
	}
	ui.Update(cmd())
	if len(runtime.resolvedQuestions) != 0 || ui.question.current != 1 || !ui.question.responses[0].Confirmed {
		t.Fatal("partial batch resumed model or lost confirmation")
	}
	pending, _ := runtime.PendingRequests(context.Background())
	restored := newTUIModel(&fakeResponder{}, true)
	restored.sessionID = ui.sessionID
	restored.runtime = runtime
	restored.pendingRequest = &pending[0]
	restored.openQuestion(*pending[0].UserQuestion)
	if restored.question.current != 1 || !restored.question.responses[0].Confirmed {
		t.Fatal("partial batch did not restore")
	}
	runtime.resolveQuestionErr = fmt.Errorf("transient resolver error")
	restored.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, cmd = restored.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	result := cmd()
	if result.(questionAnsweredMsg).err == nil {
		t.Fatal("resolver failure was not surfaced")
	}
	restored.Update(result)
	if !restored.question.submitting || !strings.Contains(restored.question.err, "press enter to retry") {
		t.Fatal("failed batch was not locked for safe retry")
	}
	_, retry := restored.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if retry == nil {
		t.Fatal("missing retry command")
	}
	restored.Update(retry())
	if len(runtime.resolvedQuestions) != 2 || len(runtime.resolvedQuestions[0].Answers) != 2 || runtime.resolvedQuestions[0].Answers[1].Answer != "Storage" || runtime.resolvedQuestions[1].Answers[1].Answer != "Storage" {
		t.Fatalf("answers=%#v", runtime.resolvedQuestions)
	}
	summaries := 0
	for _, entry := range restored.entries {
		if entry.role == "question_summary" {
			summaries++
			if !strings.Contains(entry.text, "Questions 2/2 answered") {
				t.Fatal(entry.text)
			}
		}
		if entry.role == "you" {
			t.Fatal("batch rendered as user message")
		}
	}
	if summaries != 1 {
		t.Fatalf("summaries=%d", summaries)
	}
}

func TestQuestionBatchHistoryReconstructsSummary(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"questions": []core.UserQuestionItem{{ID: "target", Question: "Target?", Options: []core.UserQuestionOption{{Label: "Local"}}}}})
	answers, _ := json.Marshal([]core.UserQuestionResponse{{QuestionID: "target", Answer: "Local"}})
	history, _ := json.Marshal([]any{map[string]any{"type": "function_call", "name": "ask_user", "call_id": "call", "arguments": string(args)}, map[string]any{"type": "function_call_output", "call_id": "call", "output": string(answers)}})
	entries := transcriptEntries(history)
	if len(entries) != 1 || entries[0].role != "question_summary" || !strings.Contains(entries[0].text, "answer: Local") {
		t.Fatalf("history=%#v", entries)
	}
}

func TestLightPanelGeometryFitsLongBatchChoices(t *testing.T) {
	for _, size := range []struct{ width, height int }{{160, 50}, {100, 30}, {48, 18}, {24, 12}} {
		ui, _ := questionFixture()
		ui.preferences.Theme = themeLight
		ui.applyResolvedTheme()
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		for i := 0; i < 20; i++ {
			ui.question.request.Questions[0].Options = append(ui.question.request.Questions[0].Options, core.UserQuestionOption{Label: "Long choice", Description: "A long explanation that must stay on a single row"})
		}
		ui.question.selected = len(ui.question.item().Options)
		ui.updateLayout()
		view := ui.View()
		if lipgloss.Width(view.Content) > size.width || lipgloss.Height(view.Content) > size.height {
			t.Fatalf("%dx%d overflow", size.width, size.height)
		}
		if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(view.Content, ""), "None of the above") && size.width >= 48 {
			t.Fatalf("selected item not visible: %s", view.Content)
		}
		if strings.Contains(tuiScreenshotANSI.ReplaceAllString(view.Content, ""), "? shortcuts") {
			t.Fatal("question duplicated global footer")
		}
	}
}
