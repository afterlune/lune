package tui

import (
	"encoding/json"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func TestToolDetailsMergeLifecycleAndRetainPreview(t *testing.T) {
	for _, detailsFirst := range []bool{false, true} {
		ui := newTUIModel(nil, false)
		details := core.ToolCall{ID: "call", InvocationID: "inv", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`), Details: &publictool.ExecutionDetails{Output: "one\ntwo\nthree\nfour\nfive"}}
		life := core.ToolCall{ID: "call", InvocationID: "inv", Name: "read_file"}
		if detailsFirst {
			ui.updateToolActivity(details)
			ui.updateToolActivity(life)
		} else {
			ui.updateToolActivity(life)
			ui.updateToolActivity(details)
		}
		text, _ := ui.transcriptContent()
		if len(ui.entries) != 1 || !strings.Contains(text, "main.go") || strings.Contains(text, "  │ one") || strings.Contains(text, "  │ five") || !strings.Contains(text, "f4 then enter") {
			t.Fatalf("preview=%q entries=%#v", text, ui.entries)
		}
		ui.toggleFocusedActivity()
		text, _ = ui.transcriptContent()
		if !strings.Contains(text, "five") {
			t.Fatal("expanded output missing")
		}
	}
}

func TestBackgroundCommandUpdatesOriginalRecordAndShowsExit(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.updateToolActivity(core.ToolCall{ID: "start", InvocationID: "inv", Name: "command", Started: true, Arguments: json.RawMessage(`{"command":"go test ./..."}`)})
	output := publictool.CommandOutput{Command: publictool.CommandInfo{ID: "cmd", InvocationID: "inv", Command: "go test ./...", Status: publictool.CommandRunning}, Output: "running"}
	ui.updateToolActivity(core.ToolCall{ID: "start", InvocationID: "inv", Name: "command", Details: &publictool.ExecutionDetails{Command: &output}})
	if ui.activities[0].status != "running" {
		t.Fatal("start receipt treated as completed command")
	}
	code := 1
	output.Command.ExitCode = &code
	output.Command.Status = publictool.CommandFailed
	output.Output = "FAIL"
	ui.updateToolActivity(core.ToolCall{ID: "poll", InvocationID: "poll-inv", Name: "command_status", Details: &publictool.ExecutionDetails{Command: &output}})
	text, _ := ui.transcriptContent()
	if len(ui.entries) != 1 || !strings.Contains(text, "Failed (exit 1) go test ./...") {
		t.Fatalf("timeline=%q", text)
	}
	stale := output
	stale.Command.Status = publictool.CommandRunning
	ui.updateToolActivity(core.ToolCall{ID: "start", InvocationID: "inv", Name: "command", Details: &publictool.ExecutionDetails{Command: &stale}})
	if ui.activities[0].status != "failed" {
		t.Fatal("late start receipt reverted the completed command")
	}
}

func TestToolOutputCannotMoveTerminalCursor(t *testing.T) {
	got := safeToolText("before\x1b[2J\x1b]0;title\aafter\rnext")
	if strings.ContainsAny(got, "\x1b\a\r") || got != "beforeafter\nnext" {
		t.Fatalf("unsafe text=%q", got)
	}
}

func TestToolTimelineKeepsDifferentExecutionsSeparate(t *testing.T) {
	ui := newTUIModel(nil, false)
	for _, executionID := range []string{"first", "second"} {
		ui.updateToolActivity(core.ToolCall{ExecutionID: executionID, InvocationID: "c4", ID: "provider-call", Name: "read_file"})
	}
	if len(ui.entries) != 2 || len(ui.activities) != 2 {
		t.Fatal("tools from different executions were merged")
	}
}
