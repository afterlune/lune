package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

type recordingPlanRuntime struct {
	*fakeSessionRuntime
	mode          core.PlanImplementationMode
	authorization core.PlanAuthorization
}

func (r *recordingPlanRuntime) ImplementPlan(_ context.Context, _ string, mode core.PlanImplementationMode, authorization ...core.PlanAuthorization) (core.PlanImplementation, error) {
	r.mode = mode
	if len(authorization) > 0 {
		r.authorization = authorization[0]
	}
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

	if len(ui.entries) != 3 || ui.entries[0].text != "Intro" || ui.entries[1].role != "plan" || ui.entries[1].text != planText || ui.entries[2].text != "Done" {
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

func TestTUIPlanReviewFitsTerminalSizesWithoutComposer(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 30}, {80, 24}, {48, 18}} {
		ui, _ := planReviewFixture()
		ui.input.SetValue("hidden draft")
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := ui.View()
		if got := lipgloss.Width(view.Content); got > size.width {
			t.Errorf("%dx%d plan review width = %d", size.width, size.height, got)
		}
		if got := lipgloss.Height(view.Content); got > size.height {
			t.Errorf("%dx%d plan review height = %d", size.width, size.height, got)
		}
		if view.Cursor != nil || strings.Contains(view.Content, "hidden draft") {
			t.Errorf("%dx%d plan review exposed composer: cursor=%v", size.width, size.height, view.Cursor)
		}
	}
}

func TestTUIPlanBodyAndConfirmationUseLightModeBlocks(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeLight
	ui.applyResolvedTheme()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ui.taskStartedAt = time.Now().Add(-40 * time.Second)
	planText := "## Overview\n- First implementation step\n1. Second implementation step"
	response, err := json.Marshal(map[string]any{"output": []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": planText},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ui.finishTask(taskResult{response: response, coreResult: core.TurnResult{
		ExecutionID: "execution-1", Status: core.TaskCompleted, ProposedPlan: &core.ProposedPlan{Text: planText},
	}})
	ui.refreshPanels()
	view := ui.View()
	plain := tuiScreenshotANSI.ReplaceAllString(view.Content, "")
	overviewAt := strings.Index(plain, "Overview")
	workedAt := strings.Index(plain, "Worked for 40s • ")
	confirmAt := strings.Index(plain, "Implement this plan?")
	if overviewAt < 0 || workedAt <= overviewAt || confirmAt <= workedAt {
		t.Fatalf("plan blocks are out of order or incomplete: %q", plain)
	}
	lines := parseTUIScreenshotLinesWithBackground(view.Content, codexLight.Primary, codexLight.Canvas)
	planHeading, planBullet, numberedPlanBullet, worked := false, false, false, false
	for _, runs := range lines {
		var line strings.Builder
		background := ""
		boldOverview := false
		mutedFooter := false
		for _, run := range runs {
			line.WriteString(run.text)
			if lipgloss.Width(run.text) > 0 {
				background = run.background
			}
			if strings.Contains(run.text, "Overview") && run.bold && strings.EqualFold(run.foreground, "#111827") {
				boldOverview = true
			}
			if strings.Contains(run.text, "Worked for 40s") && strings.EqualFold(run.foreground, codexLight.Muted) {
				mutedFooter = true
			}
		}
		if strings.Contains(line.String(), "Overview") {
			planHeading = strings.EqualFold(background, codexLight.Surface) && boldOverview
		}
		if strings.Contains(line.String(), "• First implementation step") {
			planBullet = strings.EqualFold(background, codexLight.Surface)
		}
		if strings.Contains(line.String(), "• Second implementation step") {
			numberedPlanBullet = strings.EqualFold(background, codexLight.Surface)
		}
		if strings.Contains(line.String(), "Worked for 40s") {
			worked = strings.EqualFold(background, codexLight.Canvas) && mutedFooter
		}
	}
	if !planHeading || !planBullet || !numberedPlanBullet || !worked {
		t.Fatalf("plan surface/footer styles missing: heading=%v bullet=%v numbered=%v footer=%v", planHeading, planBullet, numberedPlanBullet, worked)
	}

	panel := ui.renderPlanReview()
	plainPanel := tuiScreenshotANSI.ReplaceAllString(panel, "")
	for _, pair := range [][2]string{
		{"1. Yes, implement this plan", "Switch to Default and start coding"},
		{"2. Yes, clear context and implement", "Start a fresh thread (current context: unknown used)"},
		{"3. No, stay in Plan mode", "Continue planning with the model"},
	} {
		found := false
		for _, line := range strings.Split(plainPanel, "\n") {
			if strings.Contains(line, pair[0]) && strings.Contains(line, pair[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("Plan review option %q and description %q are not on one row: %q", pair[0], pair[1], plainPanel)
		}
	}
	if !strings.Contains(plainPanel, "enter select · esc back") {
		t.Errorf("Plan review hint is missing: %q", plainPanel)
	}
	selectedRow := false
	for _, runs := range parseTUIScreenshotLinesWithBackground(panel, codexLight.Primary, codexLight.Canvas) {
		var text strings.Builder
		width := 0
		allBlue := true
		for _, run := range runs {
			text.WriteString(run.text)
			width += lipgloss.Width(run.text)
			if lipgloss.Width(run.text) > 0 && !strings.EqualFold(run.background, "#BFDBFE") {
				allBlue = false
			}
		}
		if strings.Contains(text.String(), "› 1. Yes, implement this plan") {
			selectedRow = width == 100 && allBlue
		}
	}
	if !selectedRow {
		t.Fatal("Plan review selected option did not fill the terminal row in blue")
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
	if len(ui.entries) < 3 || ui.entries[0].text != "Intro" || ui.entries[1].role != "plan" || ui.entries[1].text != "Do the work." || ui.entries[2].text != "Done" {
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
			ui.input.SetValue("feedback must not be submitted")
			if view := ui.View().Content; strings.Contains(view, "feedback must not be submitted") {
				t.Fatalf("composer was visible during plan review: %q", view)
			}
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
	if ui.planReview || ui.proposedPlan == nil || ui.planSourceExecutionID != "source-execution" || !ui.planMode || runtime.mode != "" {
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

func TestTUIPlanReviewIsSelectionOnlyAndEscapeKeepsPendingPlan(t *testing.T) {
	ui, runtime := planReviewFixture()
	ui.queued = []core.QueuedTurnState{{ExecutionID: "queued"}}
	ui.planAuthorization.AllowedTools = []string{"read_file"}
	ui.input.SetValue("hidden draft")
	if view := ui.View().Content; strings.Contains(view, "hidden draft") || strings.Contains(view, "type feedback") {
		t.Fatalf("review exposed composer or feedback prompt: %q", view)
	}
	ui.Update(tea.PasteMsg{Content: "pasted feedback"})
	if ui.input.Value() != "hidden draft" {
		t.Fatalf("paste changed composer during review: %q", ui.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if ui.input.Value() != "hidden draft" {
		t.Fatalf("typed key changed composer during review: %q", ui.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.planReview || !ui.planMode || len(ui.queued) != 1 || ui.proposedPlan == nil || !containsString(ui.planAuthorization.AllowedTools, "read_file") {
		t.Fatalf("escape changed plan or queue state: review=%v plan=%v queued=%d", ui.planReview, ui.planMode, len(ui.queued))
	}

	ui.input.SetValue("/plan allow-network")
	ui.submit()
	if !ui.planAuthorization.AllowNetwork || ui.proposedPlan == nil {
		t.Fatalf("authorization after closing review = network:%v plan:%v", ui.planAuthorization.AllowNetwork, ui.proposedPlan)
	}
	ui.input.SetValue("/plan implement")
	updated, command := ui.submit()
	if command == nil {
		t.Fatal("implementation command was unavailable after closing review")
	}
	runTUICommands(updated, command)
	if runtime.mode != core.PlanImplementSameContext || !runtime.authorization.AllowNetwork {
		t.Fatalf("post-review implementation = mode:%q network:%v", runtime.mode, runtime.authorization.AllowNetwork)
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
