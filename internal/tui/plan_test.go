package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestPlanTagStripping(t *testing.T) {
	raw := "Here is the proposal:\n<proposed_plan>\n1. Step One\n2. Step Two\n</proposed_plan>\nLet me know!"
	extracted, cleaned := tuiMarkdown.ExtractProposedPlan(raw)
	if strings.Contains(extracted, "<proposed_plan>") || strings.Contains(extracted, "</proposed_plan>") {
		t.Fatalf("extracted plan contains raw tags: %q", extracted)
	}
	if !strings.Contains(extracted, "1. Step One") {
		t.Fatalf("expected plan content in extracted, got: %q", extracted)
	}
	if strings.Contains(cleaned, "<proposed_plan>") || strings.Contains(cleaned, "</proposed_plan>") {
		t.Fatalf("cleaned surrounding contains raw tags: %q", cleaned)
	}

	stripped := tuiMarkdown.StripProposedPlan(raw)
	if strings.Contains(stripped, "<proposed_plan>") || strings.Contains(stripped, "</proposed_plan>") {
		t.Fatalf("tuiMarkdown.StripProposedPlan leaked raw tags: %q", stripped)
	}

	// Test that inline code mentioning <proposed_plan> in markdown tables or prose is preserved
	tableWithInlineTag := "| # | 问题 | 证据 |\n|---|---|---|\n| 4 | `<proposed_plan>` 处理两套实现 | markdown.go |\n\n## 五、候选方向\n\n<proposed_plan>\n## 目标\n修复缺陷\n</proposed_plan>"
	extractedPlan, restText := tuiMarkdown.ExtractProposedPlan(tableWithInlineTag)
	if extractedPlan != "## 目标\n修复缺陷" {
		t.Fatalf("unexpected extracted plan: %q", extractedPlan)
	}
	if !strings.Contains(restText, "`<proposed_plan>` 处理两套实现") {
		t.Fatalf("inline code in table was swallowed: %q", restText)
	}
	if !strings.Contains(restText, "## 五、候选方向") {
		t.Fatalf("subsequent markdown sections were swallowed: %q", restText)
	}
	strippedText := tuiMarkdown.StripProposedPlan(tableWithInlineTag)
	if !strings.Contains(strippedText, "`<proposed_plan>` 处理两套实现") || !strings.Contains(strippedText, "## 五、候选方向") {
		t.Fatalf("tuiMarkdown.StripProposedPlan swallowed table row or sections: %q", strippedText)
	}
	if strings.Contains(strippedText, "## 目标") {
		t.Fatalf("tuiMarkdown.StripProposedPlan should remove the plan block body: %q", strippedText)
	}

	// Test renderEntry for KindPlan and KindAssistant
	planEntry := TranscriptEntry{
		Kind: KindPlan,
		Text: "<proposed_plan>\nImplement auth service\n</proposed_plan>",
	}
	renderedPlan := ansi.Strip(renderEntry(planEntry, 80))
	if strings.Contains(renderedPlan, "<proposed_plan>") || strings.Contains(renderedPlan, "</proposed_plan>") {
		t.Fatalf("renderEntry(KindPlan) leaked plan tags: %q", renderedPlan)
	}
	if !strings.Contains(renderedPlan, "Implement auth service") {
		t.Fatalf("renderEntry(KindPlan) missing text: %q", renderedPlan)
	}

	asstEntry := TranscriptEntry{
		Kind: KindAssistant,
		Text: "Notice <proposed_plan> in text</proposed_plan>",
	}
	renderedAsst := ansi.Strip(renderEntry(asstEntry, 80))
	if strings.Contains(renderedAsst, "<proposed_plan>") || strings.Contains(renderedAsst, "</proposed_plan>") {
		t.Fatalf("renderEntry(KindAssistant) leaked plan tags: %q", renderedAsst)
	}
}

func TestGreetingWithDanglingPlanTagDoesNotOpenPlanReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.run.currentStreamText = "你好！我是 Plan 模式下的规划助手。你说明目标后，我会先阅读相关代码，再用 <proposed_plan> 给你完整方案。"
	m.finalizeTurnOutput("", nil, "exec-1")

	if m.activePlanReview != nil {
		t.Fatalf("expected no plan review for a greeting, got: %+v", m.activePlanReview)
	}
	var assistantText string
	for _, entry := range m.entries {
		if entry.Kind == KindPlan {
			t.Fatalf("expected no plan entry for a greeting, got: %q", entry.Text)
		}
		if entry.Kind == KindAssistant {
			assistantText = entry.Text
		}
	}
	if strings.Contains(assistantText, "<proposed_plan>") {
		t.Fatalf("expected raw plan tag to be removed, got: %q", assistantText)
	}
	if !strings.Contains(assistantText, "给你完整方案。") {
		t.Fatalf("expected the reply's prose to be preserved, got: %q", assistantText)
	}
}

func TestCompleteCoreProposedPlanOpensPlanReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	plan := "## 实施步骤\n1. 修改 runtime.go\n2. 运行测试"
	m.finalizeTurnOutput(plan, nil, "exec-2")

	if m.activePlanReview == nil {
		t.Fatalf("expected a plan review for a complete Core-provided plan")
	}
	if m.activePlanReview.Text != plan {
		t.Fatalf("unexpected plan review text: %q", m.activePlanReview.Text)
	}
	foundPlan := false
	for _, entry := range m.entries {
		if entry.Kind == KindPlan && entry.Text == plan {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("expected a plan transcript entry, got: %+v", m.entries)
	}
}

func TestCompatProposedPlanFinalizationHidesEndMarker(t *testing.T) {
	m := newTUIModel(nil, nil, "compat-plan")
	m.run.currentStreamText = "Before.\n<proposed_plan>## Steps\n1. Inspect<proposed_plan_end/>\nAfter."
	m.finalizeTurnOutput("## Steps\n1. Inspect", nil, "exec-compat-plan")

	if m.activePlanReview == nil || m.activePlanReview.Text != "## Steps\n1. Inspect" {
		t.Fatalf("expected compatibility plan review, got %#v", m.activePlanReview)
	}
	if len(m.entries) != 3 || m.entries[0].Kind != KindAssistant || m.entries[0].Text != "Before." ||
		m.entries[1].Kind != KindPlan || m.entries[1].Text != "## Steps\n1. Inspect" ||
		m.entries[2].Kind != KindAssistant || m.entries[2].Text != "After." {
		t.Fatalf("plan and surrounding text were not ordered around the plan panel: %#v", m.entries)
	}
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "proposed_plan_end") || strings.Contains(entry.Text, "<proposed_plan>") {
			t.Fatalf("plan marker leaked into transcript: %#v", m.entries)
		}
	}

	// A repeated finalization must update the three existing blocks in place.
	m.run.currentStreamText = "Before.\n<proposed_plan>## Steps\n1. Inspect<proposed_plan_end/>\nAfter."
	m.finalizeTurnOutput("## Steps\n1. Inspect", nil, "exec-compat-plan")
	if len(m.entries) != 3 || m.entries[2].Text != "After." {
		t.Fatalf("repeated plan finalization duplicated transcript blocks: %#v", m.entries)
	}
}

func TestPlanMarkupInTextWithoutCorePlanDoesNotOpenReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.run.currentStreamText = "Here you go:\n<proposed_plan>1. Do it</proposed_plan>"
	m.finalizeTurnOutput("", nil, "exec-3")

	if m.activePlanReview != nil {
		t.Fatalf("expected no review when the Core returned no ProposedPlan, got: %+v", m.activePlanReview)
	}
	for _, entry := range m.entries {
		if entry.Kind == KindPlan {
			t.Fatalf("expected no plan entry without a Core plan, got: %q", entry.Text)
		}
		if entry.Kind == KindAssistant && strings.Contains(entry.Text, "<proposed_plan>") {
			t.Fatalf("expected raw plan markup to be removed, got: %q", entry.Text)
		}
	}
}

func TestResumedAnswerIsAppendedAfterQuestionAnswer(t *testing.T) {
	const executionID = "exec-question"
	m := newTUIModel(nil, nil, "question-order")
	m.entries = []TranscriptEntry{
		{
			ID:          assistantOutputID(executionID),
			ExecutionID: executionID,
			Kind:        KindAssistant,
			Text:        "Initial analysis",
		},
		{Kind: KindPlan, Text: "Initial plan"},
	}
	m.recordQuestionAnswer(&ActiveQuestion{Request: core.UserQuestionRequest{
		ID:          "question-batch",
		ExecutionID: executionID,
		Questions:   []core.UserQuestionItem{{Question: "Which scope?"}},
	}}, 0, AnsweredQuestionItem{Question: "Which scope?", Answer: "TUI"})

	m.run.currentStreamText = "Resumed answer"
	m.finalizeTurnOutput("", nil, executionID)

	if len(m.entries) != 4 {
		t.Fatalf("expected the resumed answer to append one entry, got %#v", m.entries)
	}
	if m.entries[0].Text != "Initial analysis" || m.entries[1].Kind != KindPlan {
		t.Fatalf("the pre-question transcript was modified: %#v", m.entries)
	}
	if m.entries[2].Kind != KindQuestionAnswered || m.entries[2].ExecutionID != executionID {
		t.Fatalf("question answer did not remain the resume boundary: %#v", m.entries[2])
	}
	if m.entries[3].Kind != KindAssistant || m.entries[3].Text != "Resumed answer" || m.entries[3].ExecutionID != executionID {
		t.Fatalf("resumed answer was not rendered after the question card: %#v", m.entries)
	}

	// A later completion event must update the resumed response instead of
	// creating a second assistant block.
	m.run.currentStreamText = "Canonical resumed answer"
	m.finalizeTurnOutput("", nil, executionID)
	if len(m.entries) != 4 || m.entries[3].Text != "Canonical resumed answer" {
		t.Fatalf("resumed response was not deduplicated after finalization: %#v", m.entries)
	}
}

func TestLiveProposedPlanStaysVisibleWhenClosingTagArrives(t *testing.T) {
	m := newTUIModel(nil, nil, "live-plan")
	m.width, m.height = 100, 30
	m.run.running = true
	m.run.taskStartedAt = time.Now()
	m.updateLayout()

	for _, stream := range []string{
		"Intro.\n## Section <proposed_plan>\n## Steps\n1. Fin",
		"Intro.\n## Section <proposed_plan>\n## Steps\n1. Finish\n<proposed_plan_end/>\nAfter.",
	} {
		m.run.currentStreamText = stream
		m.view.invalidateContent()
		m.refreshViewportContent()
		view := ansi.Strip(m.viewport.View())
		if !strings.Contains(view, "Proposed Plan") || !strings.Contains(view, "Steps") || !strings.Contains(view, "1. Fin") || strings.Contains(view, "proposed_plan_end") {
			t.Fatalf("live plan disappeared while the stream reached a complete block:\n%s", view)
		}
		if strings.HasSuffix(stream, "After.") && strings.Index(view, "Proposed Plan") >= strings.Index(view, "After.") {
			t.Fatalf("text after the plan was rendered before the plan panel:\n%s", view)
		}
	}
	if m.activePlanReview != nil {
		t.Fatal("live plan output must not open the review card before the turn completes")
	}
	for _, entry := range m.entries {
		if entry.Kind == KindPlan {
			t.Fatalf("live rendering should not persist a plan entry before finalization: %#v", m.entries)
		}
	}
}

func TestCancelPlanReviewKeepsPlanModeAndPlan(t *testing.T) {
	plan := "1. Inspect the implementation\n2. Run focused tests"
	m := newTUIModel(nil, nil, "plan-cancel")
	m.planMode = true
	m.permissionMode = core.PermissionReadOnly
	m.entries = []TranscriptEntry{{Kind: KindPlan, Text: plan}}
	m.activePlanReview = &ActivePlanReview{ExecutionID: "plan-review", Text: plan, Selected: 2}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.activePlanReview != nil {
		t.Fatal("cancel should close the plan review card")
	}
	if !m.planMode || m.permissionMode != core.PermissionReadOnly {
		t.Fatalf("cancel should keep Plan mode and read-only permission, plan=%v permission=%q", m.planMode, m.permissionMode)
	}
	if len(m.entries) < 2 || m.entries[0].Text != plan || !strings.Contains(strings.ToLower(m.entries[len(m.entries)-1].Text), "continue discussing") {
		t.Fatalf("expected original plan and continuation guidance to remain in transcript: %#v", m.entries)
	}
}

func TestEscapePlanReviewKeepsPlanMode(t *testing.T) {
	m := newTUIModel(nil, nil, "plan-escape")
	m.planMode = true
	m.permissionMode = core.PermissionReadOnly
	m.activePlanReview = &ActivePlanReview{ExecutionID: "plan-review", Text: "Keep planning", Selected: 0}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activePlanReview != nil || !m.planMode || m.permissionMode != core.PermissionReadOnly {
		t.Fatalf("Esc should close only the review card, review=%v plan=%v permission=%q", m.activePlanReview, m.planMode, m.permissionMode)
	}
	if !strings.Contains(strings.ToLower(m.entries[len(m.entries)-1].Text), "continue discussing") {
		t.Fatal("expected Esc cancellation to leave continuation guidance")
	}
}

func TestShiftTabTogglesPlanModeWithGuards(t *testing.T) {
	workspace := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	m := newTUIModel(client, nil, "shift-tab-plan")
	m.width, m.height = 100, 30
	m.updateLayout()
	normalMode := m.permissionMode

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if !m.planMode || m.permissionMode != core.PermissionReadOnly || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("Shift+Tab should enter Plan mode with read-only permissions, plan=%v model=%s client=%s", m.planMode, m.permissionMode, client.PermissionMode())
	}
	if m.savedMode != normalMode {
		t.Fatalf("expected saved permission %s, got %s", normalMode, m.savedMode)
	}

	m.handleSlashCommand("/permissions")
	if m.modals.permission != nil {
		t.Fatal("permissions picker must stay unavailable while Plan mode is active")
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.planMode || m.permissionMode != normalMode || client.PermissionMode() != normalMode {
		t.Fatalf("Shift+Tab should exit Plan mode and restore %s, plan=%v model=%s client=%s", normalMode, m.planMode, m.permissionMode, client.PermissionMode())
	}

	m.run.running = true
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.planMode {
		t.Fatal("Shift+Tab must not enter Plan mode while a task is running")
	}
	m.run.running = false

	m.activeQuestion = &ActiveQuestion{}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.planMode {
		t.Fatal("Shift+Tab must not enter Plan mode while an interaction is pending")
	}
	if len(m.entries) == 0 || !strings.Contains(strings.ToLower(m.entries[len(m.entries)-1].Text), "pending interaction") {
		t.Fatalf("expected a pending-interaction guard message, got %#v", m.entries)
	}
}
