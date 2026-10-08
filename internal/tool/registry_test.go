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

func TestExecuteCallPublishesSanitizedLifecycleEvents(t *testing.T) {
	registry, err := NewRegistry(loggingTestTool{})
	if err != nil {
		t.Fatal(err)
	}
	var events []ToolEvent
	registry.SetToolEventObserver(func(event ToolEvent) {
		events = append(events, event)
	})
	registry.ExecuteCall(context.Background(), model.OutputFunctionCall{
		CallID:    "call-456",
		Name:      "private_tool",
		Arguments: `{"secret":"private-tool-arguments"}`,
	})
	if len(events) != 2 {
		t.Fatalf("events = %#v, want start and finish", events)
	}
	if !events[0].Started || events[0].Name != "private_tool" || events[0].CallID != "call-456" {
		t.Errorf("start event = %#v", events[0])
	}
	finished := events[1]
	if finished.Started || finished.Name != "private_tool" || finished.CallID != "call-456" || !finished.Failed || finished.Canceled {
		t.Errorf("finish event = %#v", finished)
	}
	if finished.Duration < 0 {
		t.Errorf("finish duration = %s, want nonnegative duration", finished.Duration)
	}
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "private-tool-") {
			t.Errorf("event contains tool payload: %s", encoded)
		}
	}
}

func TestDefaultRegistryIncludesEveryBuiltInTool(t *testing.T) {
	registry, err := NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDefaultRegistry: %v", err)
	}
	want := map[string]bool{
		"command": false, "read_file": false, "write_file": false,
		"edit_file": false, "web_search": false, "web_fetch": false,
	}
	for _, definition := range registry.Definitions() {
		function, ok := definition.(model.FunctionTool)
		if !ok {
			t.Errorf("tool definition has type %T, want model.FunctionTool", definition)
			continue
		}
		if _, exists := want[function.Name]; !exists {
			t.Errorf("unexpected tool %q", function.Name)
			continue
		}
		want[function.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("default registry is missing %q", name)
		}
	}
}

func TestCategorizedRegistryPreservesCategoryAndToolOrder(t *testing.T) {
	first := loggingTestTool{}
	second := privateNamedTool{name: "second_tool"}
	registry, err := NewCategorizedRegistry(
		ToolRegistration{Tool: first, Category: model.ToolCategory{Name: "files", Description: "Workspace files."}},
		ToolRegistration{Tool: second, Category: model.ToolCategory{Name: "files", Description: "Workspace files."}},
		ToolRegistration{Tool: privateNamedTool{name: "web_tool"}, Category: model.ToolCategory{Name: "web", Description: "Web access."}},
	)
	if err != nil {
		t.Fatalf("NewCategorizedRegistry: %v", err)
	}

	categories := registry.Categories()
	if len(categories) != 2 || categories[0].Name != "files" || categories[1].Name != "web" {
		t.Fatalf("categories = %#v, want files then web", categories)
	}
	definitions := registry.DefinitionsForCategory("files")
	if len(definitions) != 2 {
		t.Fatalf("files definitions = %d, want 2", len(definitions))
	}
	firstDefinition, firstOK := definitions[0].(model.FunctionTool)
	secondDefinition, secondOK := definitions[1].(model.FunctionTool)
	if !firstOK || !secondOK || firstDefinition.Name != "private_tool" || secondDefinition.Name != "second_tool" {
		t.Fatalf("files definitions = %#v, want registration order", definitions)
	}
}

func TestCategorizedRegistryRejectsInconsistentCategoryMetadata(t *testing.T) {
	_, err := NewCategorizedRegistry(
		ToolRegistration{Tool: loggingTestTool{}, Category: model.ToolCategory{Name: "files", Description: "Workspace files."}},
		ToolRegistration{Tool: privateNamedTool{name: "second_tool"}, Category: model.ToolCategory{Name: "files", Description: "Different description."}},
	)
	if err == nil {
		t.Fatal("expected inconsistent category descriptions to fail")
	}
}

func TestRegistryDoesNotMixCategorizedAndUncategorizedTools(t *testing.T) {
	registry, err := NewRegistry(loggingTestTool{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterInCategory(model.ToolCategory{Name: "files", Description: "Workspace files."}, privateNamedTool{name: "second_tool"}); err == nil {
		t.Fatal("expected a categorized tool to be rejected after an uncategorized tool")
	}

	categorized, err := NewCategorizedRegistry(ToolRegistration{
		Tool:     loggingTestTool{},
		Category: model.ToolCategory{Name: "files", Description: "Workspace files."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := categorized.Register(privateNamedTool{name: "second_tool"}); err == nil {
		t.Fatal("expected an uncategorized tool to be rejected from a categorized registry")
	}
}

func TestDefaultRegistryCategorizesBuiltInTools(t *testing.T) {
	registry, err := NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDefaultRegistry: %v", err)
	}
	want := map[string][]string{
		"files":   {"read_file", "write_file", "edit_file"},
		"command": {"command"},
		"web":     {"web_search", "web_fetch"},
	}
	if categories := registry.Categories(); len(categories) != 3 || categories[0].Name != "files" || categories[1].Name != "command" || categories[2].Name != "web" {
		t.Fatalf("categories = %#v, want files, command, web", categories)
	}
	for category, expectedNames := range want {
		definitions := registry.DefinitionsForCategory(category)
		if len(definitions) != len(expectedNames) {
			t.Errorf("%s definitions = %d, want %d", category, len(definitions), len(expectedNames))
			continue
		}
		for index, expectedName := range expectedNames {
			definition, ok := definitions[index].(model.FunctionTool)
			if !ok || definition.Name != expectedName {
				t.Errorf("%s definition %d = %#v, want %q", category, index, definitions[index], expectedName)
			}
		}
	}
}

type privateNamedTool struct{ name string }

func (t privateNamedTool) Definition() model.FunctionTool {
	return model.FunctionTool{
		Name:       t.name,
		Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
	}
}

func (privateNamedTool) Run(context.Context, json.RawMessage) Result {
	return Result{Output: "ok"}
}
