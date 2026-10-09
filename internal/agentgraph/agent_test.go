package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
	graph "github.com/afterlune/luneGraph"
	"github.com/afterlune/luneGraph/checkpoint"
	"github.com/afterlune/luneGraph/checkpoint/memory"
	"github.com/afterlune/luneGraph/checkpoint/sqlite"
)

type scriptedRequester struct {
	responses []*model.Response
	requests  []model.ResponseRequest
	next      int
}

func (r *scriptedRequester) CreateResponse(_ context.Context, request model.ResponseRequest) (*model.Response, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var saved model.ResponseRequest
	if err := json.Unmarshal(encoded, &saved); err != nil {
		return nil, err
	}
	r.requests = append(r.requests, saved)
	if r.next >= len(r.responses) {
		return nil, errors.New("no scripted response left")
	}
	response := r.responses[r.next]
	r.next++
	return response, nil
}

type cancelOnceRequester struct {
	started chan struct{}
	calls   int
}

func (r *cancelOnceRequester) CreateResponse(ctx context.Context, _ model.ResponseRequest) (*model.Response, error) {
	r.calls++
	if r.calls == 1 {
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return decodeResponse(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]}`)
}

type recordingTool struct{ calls int }

func (recordingTool) Definition() model.FunctionTool {
	return model.FunctionTool{
		Name:        "read_file",
		Description: "Read a workspace file.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
	}
}

func (t *recordingTool) Run(context.Context, json.RawMessage) tool.Result {
	t.calls++
	return tool.Result{Output: "file contents"}
}

type namedRecordingTool struct {
	name  string
	calls int
}

func (t *namedRecordingTool) Definition() model.FunctionTool {
	return model.FunctionTool{
		Name:        t.name,
		Description: "A test tool.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
	}
}

func (t *namedRecordingTool) Run(context.Context, json.RawMessage) tool.Result {
	t.calls++
	return tool.Result{Output: t.name + " completed"}
}

func TestAgentGraphExecutesMultipleToolCallsFromOneResponse(t *testing.T) {
	readFile := &namedRecordingTool{name: "read_file"}
	writeFile := &namedRecordingTool{name: "write_file"}
	registry, err := tool.NewRegistry(readFile, writeFile)
	if err != nil {
		t.Fatal(err)
	}
	requester := &scriptedRequester{responses: []*model.Response{
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"read_1","name":"read_file","arguments":"{}"},{"type":"function_call","call_id":"write_1","name":"write_file","arguments":"{}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`),
	}}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	input, err := model.NewResponseInput("read and then write")
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[State](graph.JSONClone[State])
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Start(context.Background(), "multiple-tools", nil, input, TaskOptions{}, graph.Options[State]{Store: store})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != graph.StatusCompleted || result.Checkpoint.Final == nil {
		t.Fatalf("result = %#v, want a completed task", result)
	}
	if readFile.calls != 1 || writeFile.calls != 1 {
		t.Fatalf("tool calls = read_file:%d write_file:%d, want one each", readFile.calls, writeFile.calls)
	}
	if len(requester.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requester.requests))
	}
}

func TestAgentGraphResumesBetweenModelAndToolSteps(t *testing.T) {
	readFile := &recordingTool{}
	registry, err := tool.NewCategorizedRegistry(tool.ToolRegistration{
		Tool:     readFile,
		Category: model.ToolCategory{Name: "files", Description: "Workspace files."},
	})
	if err != nil {
		t.Fatal(err)
	}
	requester := &scriptedRequester{responses: []*model.Response{
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"select_1","name":"select_tool_category","arguments":"{\"category\":\"files\"}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"read_1","name":"read_file","arguments":"{}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`),
	}}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	input, err := model.NewResponseInput([]map[string]any{{
		"type": "message",
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": "read this attachment"},
			{"type": "input_image", "image_url": "data:image/png;base64,AA=="},
			{"type": "input_audio", "data": "opaque-audio-payload"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[State](graph.JSONClone[State])
	if err != nil {
		t.Fatal(err)
	}
	options := graph.Options[State]{Store: store, MaxSteps: 1}
	paused, err := agent.Start(context.Background(), "agent-poc", nil, input, TaskOptions{}, options)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if paused.Status != graph.StatusBudget || len(paused.Checkpoint.Invocations) != 1 || paused.Checkpoint.Invocations[0].Node != toolNode {
		t.Fatalf("paused result = %#v, want a checkpoint at the tool node", paused)
	}

	options.MaxSteps = 100
	completed, err := agent.Recover(context.Background(), "agent-poc", nil, options)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if completed.Status != graph.StatusCompleted || completed.Checkpoint.Final == nil {
		t.Fatalf("completed result = %#v, want a completed checkpoint", completed)
	}
	if completed.Checkpoint.Final.FinalResponse == nil {
		t.Fatal("final response was not retained in graph state")
	}
	if readFile.calls != 1 {
		t.Fatalf("read_file executions = %d, want 1", readFile.calls)
	}
	if len(requester.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(requester.requests))
	}
	if len(requester.requests[0].Tools) != 1 {
		t.Fatalf("initial tools = %#v, want only category selector", requester.requests[0].Tools)
	}
	for _, index := range []int{1, 2} {
		if len(requester.requests[index].Tools) != 2 {
			t.Errorf("request %d tools = %#v, want selector and read_file", index+1, requester.requests[index].Tools)
		}
	}
	encodedHistory, err := json.Marshal(requester.requests[2].Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"input_audio", "opaque-audio-payload", "file contents"} {
		if !strings.Contains(string(encodedHistory), preserved) {
			t.Errorf("recovered history lost %q: %s", preserved, encodedHistory)
		}
	}
}

func TestAgentGraphRecoversAfterCancellation(t *testing.T) {
	registry, err := tool.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	requester := &cancelOnceRequester{started: make(chan struct{})}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	input, err := model.NewResponseInput("cancel then resume")
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[State](graph.JSONClone[State])
	if err != nil {
		t.Fatal(err)
	}
	options := graph.Options[State]{Store: store}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan struct {
		result graph.Result[State]
		err    error
	}, 1)
	go func() {
		result, err := agent.Start(ctx, "cancel-poc", nil, input, TaskOptions{}, options)
		resultCh <- struct {
			result graph.Result[State]
			err    error
		}{result: result, err: err}
	}()
	<-requester.started
	cancel()
	interrupted := <-resultCh
	if !errors.Is(interrupted.err, context.Canceled) || interrupted.result.Status != graph.StatusCancelled {
		t.Fatalf("interrupted result = %#v, error = %v", interrupted.result, interrupted.err)
	}

	recovered, err := agent.Recover(context.Background(), "cancel-poc", nil, options)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovered.Status != graph.StatusCompleted || requester.calls != 2 {
		t.Fatalf("recovered status=%q requests=%d, want completed after replay", recovered.Status, requester.calls)
	}
}

func TestAgentGraphRecoversAfterSQLiteStoreReopen(t *testing.T) {
	registry, err := tool.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	requester := &cancelOnceRequester{started: make(chan struct{})}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "checkpoints.db")
	store, err := sqlite.Open[State](context.Background(), dbPath, checkpoint.JSON[State]{})
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	input, err := model.NewResponseInput([]map[string]any{{
		"type": "message", "role": "user", "content": []map[string]any{
			{"type": "input_text", "text": "persist this task"},
			{"type": "input_audio", "data": "audio-payload"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan struct {
		result graph.Result[State]
		err    error
	}, 1)
	go func() {
		result, runErr := agent.Start(ctx, "sqlite-cancel", nil, input, TaskOptions{}, graph.Options[State]{Store: store})
		resultCh <- struct {
			result graph.Result[State]
			err    error
		}{result: result, err: runErr}
	}()
	<-requester.started
	cancel()
	interrupted := <-resultCh
	if !errors.Is(interrupted.err, context.Canceled) || interrupted.result.Status != graph.StatusCancelled {
		t.Fatalf("interrupted result = %#v, error = %v", interrupted.result, interrupted.err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened, err := sqlite.Open[State](context.Background(), dbPath, checkpoint.JSON[State]{})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	recoveredAgent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New after reopen: %v", err)
	}
	recovered, err := recoveredAgent.Recover(context.Background(), "sqlite-cancel", nil, graph.Options[State]{Store: reopened})
	if err != nil {
		t.Fatalf("Recover after reopen: %v", err)
	}
	if recovered.Status != graph.StatusCompleted || recovered.Checkpoint.Final == nil || recovered.Checkpoint.Final.FinalResponse == nil {
		t.Fatalf("recovered result = %#v, want completed final state", recovered)
	}
	if requester.calls != 2 {
		t.Fatalf("model requests = %d, want original call plus replay", requester.calls)
	}
	encoded, err := json.Marshal(recovered.Checkpoint.Final.History)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"persist this task", "input_audio", "audio-payload"} {
		if !strings.Contains(string(encoded), preserved) {
			t.Fatalf("recovered history lost %q: %s", preserved, encoded)
		}
	}
}

func TestAgentGraphPreservesHistoryAndEnforcesPerTaskToolRoundLimit(t *testing.T) {
	readFile := &recordingTool{}
	registry, err := tool.NewRegistry(readFile)
	if err != nil {
		t.Fatal(err)
	}
	requester := &scriptedRequester{responses: []*model.Response{
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"read_1","name":"read_file","arguments":"{}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"read_2","name":"read_file","arguments":"{}"}]}`),
	}}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	store, err := memory.New[State](graph.JSONClone[State])
	if err != nil {
		t.Fatal(err)
	}
	options := graph.Options[State]{Store: store}
	history, err := model.NewResponseInput([]map[string]any{{
		"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "previous turn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	input, err := model.NewResponseInput("current turn")
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Start(context.Background(), "round-limit", history, input, TaskOptions{MaxToolRounds: 1}, options)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != graph.StatusCompleted || result.Checkpoint.Final == nil {
		t.Fatalf("result = %#v, want completed final state", result)
	}
	final := result.Checkpoint.Final
	if final.TaskError == "" || !strings.Contains(final.TaskError, model.ErrToolCallLimit.Error()) {
		t.Fatalf("task error = %q, want tool-call limit error", final.TaskError)
	}
	if readFile.calls != 1 {
		t.Fatalf("read_file executions = %d, want 1 before the next batch is skipped", readFile.calls)
	}
	if len(requester.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requester.requests))
	}
	encodedRequest, err := json.Marshal(requester.requests[0].Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"previous turn", "current turn"} {
		if !strings.Contains(string(encodedRequest), content) {
			t.Errorf("request history lost %q: %s", content, encodedRequest)
		}
	}
	if !strings.Contains(string(mustMarshal(t, final.History)), "Skipped because the tool-call round limit was reached.") {
		t.Fatal("final history does not record the skipped tool call")
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPlanModeExposesExplorationToolsAndBlocksWrites(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "main.go"), []byte("package main\n// target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := tool.NewDefaultRegistry(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPermissionMode(tool.PermissionModeReadOnly); err != nil {
		t.Fatal(err)
	}
	requester := &scriptedRequester{responses: []*model.Response{
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"select_1","name":"select_tool_category","arguments":"{\"category\":\"files\"}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"function_call","call_id":"list_1","name":"list_dir","arguments":"{}"},{"type":"function_call","call_id":"glob_1","name":"glob","arguments":"{\"pattern\":\"**/*.go\"}"},{"type":"function_call","call_id":"grep_1","name":"grep","arguments":"{\"pattern\":\"target\"}"},{"type":"function_call","call_id":"write_1","name":"write_file","arguments":"{\"path\":\"blocked.txt\",\"content\":\"no\"}"}]}`),
		mustDecodeResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"plan ready"}]}]}`),
	}}
	agent, err := New(requester, registry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	input, err := model.NewResponseInput("inspect the repository")
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[State](graph.JSONClone[State])
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Start(context.Background(), "plan-exploration-tools", nil, input, TaskOptions{PlanMode: true}, graph.Options[State]{Store: store})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Status != graph.StatusCompleted || len(requester.requests) != 3 {
		t.Fatalf("result = %#v, model requests = %d", result, len(requester.requests))
	}

	visible := map[string]bool{}
	for _, definition := range requester.requests[1].Tools {
		if function, ok := definition.(model.FunctionTool); ok {
			visible[function.Name] = true
		}
	}
	for _, name := range []string{"list_dir", "glob", "grep", "read_file", model.CategorySelectorName} {
		if !visible[name] {
			t.Errorf("Plan mode did not expose %q: %#v", name, visible)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "command"} {
		if visible[name] {
			t.Errorf("Plan mode exposed %q", name)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked write created a file: %v", err)
	}
	encoded, err := json.Marshal(requester.requests[2].Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"src", "src/main.go", "src/main.go:2:// target", "Tool is not available in plan mode."} {
		if !strings.Contains(string(encoded), output) {
			t.Errorf("tool output %q missing from history: %s", output, encoded)
		}
	}
}

func decodeResponse(raw string) (*model.Response, error) {
	var response model.Response
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func mustDecodeResponse(t *testing.T, raw string) *model.Response {
	t.Helper()
	response, err := decodeResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
