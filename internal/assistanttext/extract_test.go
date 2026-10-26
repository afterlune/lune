package assistanttext

import (
	"encoding/json"
	"testing"
)

func TestExtractOnlyReturnsAssistantMessageText(t *testing.T) {
	response := json.RawMessage(`{"output":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"private reasoning"}]},
		{"type":"function_call","name":"read_file","arguments":"{}"},
		{"type":"compaction","data":{"text":"control payload"}},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"user input"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":" first "},{"type":"refusal","text":"ignored"}]},
		{"type":"message","content":"second"}
	]}`)
	got, err := Extract(response)
	if err != nil {
		t.Fatal(err)
	}
	if got != "first\n\nsecond" {
		t.Fatalf("Extract() = %q, want assistant text only", got)
	}
}

func TestContentTextSupportsRestoredUserAndAssistantContent(t *testing.T) {
	content := json.RawMessage(`[{"type":"input_text","text":"user"},{"type":"output_text","text":"assistant"}]`)
	if got := ContentText(content, false); got != "assistant" {
		t.Fatalf("assistant content = %q", got)
	}
	if got := ContentText(content, true); got != "userassistant" {
		t.Fatalf("history content = %q", got)
	}
}
