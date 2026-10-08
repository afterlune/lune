package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
)

type loggingTestTool struct{}

func (loggingTestTool) Definition() model.FunctionTool {
	return model.FunctionTool{
		Name:       "private_tool",
		Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
	}
}

func (loggingTestTool) Run(context.Context, json.RawMessage) Result {
	return Result{Output: "private-tool-output", Err: errors.New("private-tool-error")}
}

func TestExecuteCallLogsMetadataWithoutPayloads(t *testing.T) {
	previous := slog.Default()
	logs := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	registry, err := NewRegistry(loggingTestTool{})
	if err != nil {
		t.Fatal(err)
	}
	call := model.OutputFunctionCall{
		CallID:    "call-123",
		Name:      "private_tool",
		Arguments: `{"secret":"private-tool-arguments"}`,
	}
	registry.ExecuteCall(context.Background(), call)

	output := logs.String()
	for _, expected := range []string{"tool execution started", "tool execution finished", "tool=private_tool", "call_id=call-123", "failed=true"} {
		if !strings.Contains(output, expected) {
			t.Errorf("logs %q do not contain %q", output, expected)
		}
	}
	for _, private := range []string{"private-tool-arguments", "private-tool-output", "private-tool-error"} {
		if strings.Contains(output, private) {
			t.Errorf("logs contain private payload %q: %s", private, output)
		}
	}
}
