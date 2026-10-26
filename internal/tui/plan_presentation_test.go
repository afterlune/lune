package tui

import (
	"encoding/json"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestPlaceholderPlanPreservesReplyWithoutReview(t *testing.T) {
	for _, placeholder := range []string{"", " ", "...", "…", " …\n... "} {
		for _, prose := range []string{"", "实际分析内容。"} {
			t.Run(placeholder+prose, func(t *testing.T) {
				text := "<proposed_plan>" + placeholder + "</proposed_plan>\n" + prose
				m := newTUIModel(nil, nil, "placeholder-plan")
				m.width, m.height = 100, 40
				m.run.currentStreamText = text
				m.updateLayout()
				if strings.Contains(ansi.Strip(m.viewport.View()), "Proposed Plan") {
					t.Fatal("placeholder must not appear as a live plan")
				}
				m.finalizeTurnOutput(placeholder, nil, "placeholder-execution")
				if m.activePlanReview != nil {
					t.Fatal("placeholder must not offer implementation")
				}
				want := prose
				if want == "" {
					want = unusablePlanMessage
				}
				history, err := json.Marshal([]map[string]string{{"type": "message", "role": "assistant", "content": text}})
				if err != nil {
					t.Fatal(err)
				}
				for source, entries := range map[string][]TranscriptEntry{"completion": m.entries, "history": transcriptFromHistory(history)} {
					if len(entries) != 1 || entries[0].Kind != KindAssistant || entries[0].Text != want {
						t.Fatalf("%s must retain prose or report a missing plan: %#v", source, entries)
					}
				}
			})
		}
	}
}

func TestPlanOrderMatchesLiveCompletedAndRestoredResponse(t *testing.T) {
	const plan = "## 实施步骤\n1. 检查接口\n2. 修复显示"
	const text = "before-marker\n<proposed_plan>" + plan + "</proposed_plan>\nafter-marker"
	m := newTUIModel(nil, nil, "response-order")
	m.width, m.height = 100, 60
	m.run.currentStreamText = text
	m.updateLayout()
	live := strings.TrimSpace(ansi.Strip(m.view.liveContent))
	m.finalizeTurnOutput(plan, nil, "plan-execution")
	m.updateLayout()
	completed := strings.TrimSpace(ansi.Strip(m.viewport.GetContent()))
	if m.activePlanReview == nil {
		t.Fatal("a concrete plan in normal mode must still offer implementation")
	}
	history, err := json.Marshal([]map[string]string{{"type": "message", "role": "assistant", "content": text}})
	if err != nil {
		t.Fatal(err)
	}
	restored := newTUIModel(nil, nil, "restored-order")
	restored.width, restored.height = m.width, m.height
	restored.entries = transcriptFromHistory(history)
	restored.updateLayout()
	restoredText := strings.TrimSpace(ansi.Strip(restored.viewport.GetContent()))
	if live != completed || completed != restoredText {
		t.Fatalf("plan rendering changed between lifecycle stages:\nlive:\n%s\ncompleted:\n%s\nrestored:\n%s", live, completed, restoredText)
	}
	before, middle, after := strings.Index(completed, "before-marker"), strings.Index(completed, "Proposed Plan"), strings.Index(completed, "after-marker")
	if before < 0 || !(before < middle && middle < after) {
		t.Fatalf("response order lost:\n%s", completed)
	}
}

func TestPlanReviewFitsNarrowTerminalsWithoutAssumingMode(t *testing.T) {
	for _, width := range []int{24, 48, 80, 120} {
		view := ansi.Strip(renderPlanReviewPanel(&ActivePlanReview{}, width))
		if strings.Contains(view, "Plan mode") || strings.Contains(view, "Switch to Default") {
			t.Fatalf("review must also describe normal-mode proposals: %s", view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("review overflows width %d: %q", width, line)
			}
		}
		for _, word := range []string{"conversation", "fresh", "discussing", "implementing"} {
			if !strings.Contains(view, word) {
				t.Fatalf("review lost %q at width %d: %s", word, width, view)
			}
		}
	}
}

func TestNormalModePlanDismissalPreservesMode(t *testing.T) {
	m := newTUIModel(nil, nil, "normal-mode-review")
	m.permissionMode = core.PermissionFullAccess
	m.activePlanReview = &ActivePlanReview{Text: "Inspect the project"}
	m.cancelActivePlanReview()
	if m.activePlanReview != nil || m.planMode || m.permissionMode != core.PermissionFullAccess {
		t.Fatal("closing review must preserve normal mode and its permissions")
	}
	if strings.Contains(m.entries[len(m.entries)-1].Text, "Plan mode") {
		t.Fatal("closing a normal-mode review must not claim Plan mode is active")
	}
}
