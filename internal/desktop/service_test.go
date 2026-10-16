package desktop

import (
	"encoding/json"
	"testing"
)

func TestStripProposedPlanKeepsSurroundingResponse(t *testing.T) {
	got := stripProposedPlan("Before the plan.\n<proposed_plan>## Steps\n- Inspect files</proposed_plan>\nAfter the plan.")
	want := "Before the plan.\n\nAfter the plan."
	if got != want {
		t.Fatalf("stripProposedPlan() = %q, want %q", got, want)
	}
}

func TestStripProposedPlanLeavesIncompleteTagVisible(t *testing.T) {
	input := "The response contains <proposed_plan> but is incomplete"
	if got := stripProposedPlan(input); got != input {
		t.Fatalf("stripProposedPlan() = %q, want original", got)
	}
}

func TestTranscriptMessagesStripsProposedPlanFromAssistantHistory(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"Make a plan"}]},
		{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"Before the plan.\n<proposed_plan>## Steps\n- Inspect files</proposed_plan>\nAfter the plan."}]}
	]`)

	got := transcriptMessages(input)
	if len(got) != 2 {
		t.Fatalf("transcriptMessages() returned %d messages, want 2: %#v", len(got), got)
	}
	if got[0].Text != "Make a plan" {
		t.Fatalf("user transcript = %q, want original text", got[0].Text)
	}
	if got[1].Text != "Before the plan.\n\nAfter the plan." {
		t.Fatalf("assistant transcript = %q, want the plan block stripped", got[1].Text)
	}
}

func TestTranscriptMessagesOmitsAssistantMessageContainingOnlyAPlan(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"<proposed_plan>## Steps\n- Inspect files</proposed_plan>"}]}
	]`)

	if got := transcriptMessages(input); len(got) != 0 {
		t.Fatalf("transcriptMessages() = %#v, want no empty assistant message", got)
	}
}
