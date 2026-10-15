package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type recordingPlanRuntime struct {
	*fakeSessionRuntime
	mode core.PlanImplementationMode
}

func (r *recordingPlanRuntime) ImplementPlan(_ context.Context, _ string, mode core.PlanImplementationMode, _ ...core.PlanAuthorization) (core.PlanImplementation, error) {
	r.mode = mode
	return core.PlanImplementation{
		SessionID:  r.sessionID,
		Submission: core.TurnSubmission{ExecutionID: "plan-execution"},
	}, nil
}

func planReviewFixture() (*tuiModel, *recordingPlanRuntime) {
	client := &fakeResponder{}
	runtime := &recordingPlanRuntime{fakeSessionRuntime: newFakeSessionRuntime(client, "session-1", client.runTask)}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.runtimeSessionID = ui.sessionID
	ui.runtime = runtime
	if err := ui.setPlanMode(true); err != nil {
		panic(err)
	}
	ui.planReview = true
	ui.proposedPlan = &core.ProposedPlan{Text: "## Build it"}
	ui.planSourceExecutionID = "source-execution"
	ui.entries = []tuiEntry{{role: "assistant", text: "## Build it"}}
	return ui, runtime
}

func TestTUIRecognizedPlanHidesWrapperAndOpensThreeChoices(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	raw := "Intro\n<proposed_plan>\n## Summary\nBuild the feature.\n</proposed_plan>\nDone"
	response, err := json.Marshal(map[string]any{"output": []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": raw},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	planText := "## Summary\nBuild the feature."
	ui.finishTask(taskResult{response: response, coreResult: core.TurnResult{
		ExecutionID: "execution-1", Status: core.TaskCompleted, ProposedPlan: &core.ProposedPlan{Text: planText},
	}})

	if len(ui.entries) != 1 || ui.entries[0].text != "Intro\n\n## Summary\nBuild the feature.\n\nDone" {
		t.Fatalf("plan transcript = %#v", ui.entries)
	}
	if !ui.planReview || ui.planReviewIndex != 0 {
		t.Fatalf("plan review state = %v/%d, want open with first option selected", ui.planReview, ui.planReviewIndex)
	}
	for _, choice := range planReviewChoices {
		if !strings.Contains(ui.renderPlanReview(), choice) {
			t.Errorf("plan review panel omitted %q: %q", choice, ui.renderPlanReview())
		}
	}
	if strings.Contains(ui.renderPlanReview(), "<proposed_plan>") {
		t.Fatal("plan tags leaked into the review panel")
	}
}

func TestTUIStreamingPlanReconciliationRemovesWrapper(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-1"
	raw := "<proposed_plan>\nDo the work.\n</proposed_plan>"
	ui.entries = []tuiEntry{{role: "assistant", text: raw}}
	ui.modelStreamLast = []int{0}
	ui.finishTask(taskResult{
		response:   json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"<proposed_plan>\nDo the work.\n</proposed_plan>"}]}]}`),
		coreResult: core.TurnResult{Status: core.TaskCompleted, ProposedPlan: &core.ProposedPlan{Text: "Do the work."}},
	})
	if got := ui.entries[0].text; got != "Do the work." {
		t.Fatalf("reconciled streamed plan = %q", got)
	}
}

func TestTUIPlanTagsAreHiddenDuringStreamingAndChooserUsesVisiblePlanBlock(t *testing.T) {
	ui := newStreamingTUI()
	raw := "Intro\n<proposed_plan>\nDo the work.\n</proposed_plan>\nDone"
	for _, character := range raw {
		streamModelText(ui, "task-1", "lead", "response-1", string(character))
		ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
		if strings.Contains(plainTUIView(ui), "proposed_plan") {
			t.Fatalf("plan wrapper leaked during stream at %q: %q", string(character), plainTUIView(ui))
		}
	}
	streamModelBoundary(ui, "task-1", "completed")
	response, err := json.Marshal(map[string]any{"output": []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": raw},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ui.finishTask(taskResult{response: response, coreResult: core.TurnResult{
		ExecutionID: "execution-1", Status: core.TaskCompleted,
	}})
	if len(ui.entries) != 1 || ui.entries[0].text != "Intro\n\nDo the work.\n\nDone" {
		t.Fatalf("cleaned plan transcript = %#v", ui.entries)
	}
	if !ui.planReview || ui.proposedPlan == nil || ui.proposedPlan.Text != "Do the work." {
		t.Fatalf("valid streamed plan did not open review: review=%v plan=%#v", ui.planReview, ui.proposedPlan)
	}
}

func TestInvalidStreamedPlanTagIsRestoredWhenNoFinalResponseExists(t *testing.T) {
	ui := newStreamingTUI()
	raw := "<proposed_plan>unfinished"
	streamModelText(ui, "task-1", "lead", "response-1", raw)
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	if strings.Contains(plainTUIView(ui), "proposed_plan") {
		t.Fatal("partial plan wrapper leaked before the stream ended")
	}
	ui.cancelled = true
	ui.finishTask(taskResult{coreResult: core.TurnResult{Status: core.TaskCancelled}})
	if len(ui.entries) != 1 || ui.entries[0].text != raw {
		t.Fatalf("cancelled invalid plan stream = %#v, want original text restored", ui.entries)
	}
	if ui.planReview {
		t.Fatal("invalid plan wrapper opened review choices")
	}
}

func TestTUIPlanChoicesDispatchAndKeepReadOnly(t *testing.T) {
	for _, test := range []struct {
		name  string
		index int
		mode  core.PlanImplementationMode
	}{
		{name: "same session", index: 0, mode: core.PlanImplementSameContext},
		{name: "new session", index: 1, mode: core.PlanImplementNewContext},
	} {
		t.Run(test.name, func(t *testing.T) {
			ui, runtime := planReviewFixture()
			ui.planReviewIndex = test.index
			updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			if command == nil {
				t.Fatal("selecting an implementation option returned no command")
			}
			runTUICommands(updated, command)
			if runtime.mode != test.mode {
				t.Fatalf("implementation mode = %q, want %q", runtime.mode, test.mode)
			}
			if ui.planMode || ui.currentPermissionMode() != core.PermissionWorkspace || ui.client.PermissionMode() != core.PermissionWorkspace {
				t.Fatalf("implementation did not exit Plan and restore permission: plan=%v active=%q", ui.planMode, ui.currentPermissionMode())
			}
		})
	}

	ui, runtime := planReviewFixture()
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.planReviewIndex != 2 {
		t.Fatalf("selected option = %d, want stay read-only", ui.planReviewIndex)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.planReview || ui.proposedPlan != nil || !ui.planMode || runtime.mode != "" {
		t.Fatalf("stay state = review:%v plan:%v planMode:%v implementation:%q", ui.planReview, ui.proposedPlan, ui.planMode, runtime.mode)
	}
	if len(ui.entries) != 1 || ui.entries[0].text != "## Build it" {
		t.Fatalf("staying read-only discarded plan text: %#v", ui.entries)
	}
}

func TestTUIPlanImplementationDoesNotStartIfPermissionRestoreFails(t *testing.T) {
	ui, runtime := planReviewFixture()
	client := ui.client.(*fakeResponder)
	client.permissionErr = errors.New("permission store unavailable")

	_, command := ui.implementProposedPlan(core.PlanImplementSameContext)
	if command != nil || !ui.planMode || !ui.planReview || runtime.mode != "" || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("implementation ran despite restore failure: command=%v plan=%v review=%v runtime=%q permission=%q",
			command != nil, ui.planMode, ui.planReview, runtime.mode, client.PermissionMode())
	}
}

func TestTUISubmitsPlanModeWithReadOnlyPermission(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	if err := ui.setPlanMode(true); err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), ui.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	ui.runtime = runtime
	ui.runtimeSessionID = ui.sessionID
	ui.input.SetValue("Plan a change")
	updated, command := ui.submit()
	if command == nil {
		t.Fatal("plan task did not submit")
	}
	runTUICommands(updated, command)
	if len(client.inputs) != 1 || !client.inputs[0].PlanMode {
		t.Fatalf("submitted task = %#v, want PlanMode true", client.inputs)
	}
	if client.PermissionMode() != core.PermissionReadOnly || ui.permissionModeBeforePlan != core.PermissionWorkspace {
		t.Fatalf("Plan mode permissions = active:%q saved:%q, want read-only with Workspace saved", client.PermissionMode(), ui.permissionModeBeforePlan)
	}
}

func TestTUIPlanReviewEscapeStaysReadOnlyAndFeedbackRemainsAvailable(t *testing.T) {
	ui, _ := planReviewFixture()
	ui.queued = []core.QueuedTurnState{{ExecutionID: "queued"}}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.planReview || !ui.planMode || len(ui.queued) != 1 {
		t.Fatalf("escape changed plan or queue state: review=%v plan=%v queued=%d", ui.planReview, ui.planMode, len(ui.queued))
	}

	ui, _ = planReviewFixture()
	ui.input.SetValue("Add a rollback step")
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil || ui.planReview {
		t.Fatalf("feedback did not leave review for revision: review=%v command=%v", ui.planReview, command != nil)
	}
}

func TestInvalidPlanTagsRemainOrdinaryAssistantText(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	raw := "<proposed_plan>unfinished"
	response, err := json.Marshal(map[string]any{"output": []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": raw},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ui.finishTask(taskResult{response: response, coreResult: core.TurnResult{Status: core.TaskCompleted}})
	if ui.planReview || len(ui.entries) != 1 || ui.entries[0].text != raw {
		t.Fatalf("invalid plan markup was changed: review=%v entries=%#v", ui.planReview, ui.entries)
	}
}
