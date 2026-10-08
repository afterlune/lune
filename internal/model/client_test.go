package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	buffer := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

type testToolExecutor struct {
	definitions []ResponseTool
	calls       []OutputFunctionCall
}

type cancelingToolExecutor struct {
	*testToolExecutor
	cancel context.CancelFunc
}

func (e *cancelingToolExecutor) ExecuteCall(_ context.Context, call OutputFunctionCall) InputFunctionCallOutput {
	e.calls = append(e.calls, call)
	e.cancel()
	return InputFunctionCallOutput{CallID: call.CallID, Output: NewToolOutputText("completed before interruption")}
}

func (e *testToolExecutor) Definitions() []ResponseTool {
	return e.definitions
}

func (e *testToolExecutor) ExecuteCall(_ context.Context, call OutputFunctionCall) InputFunctionCallOutput {
	e.calls = append(e.calls, call)
	return InputFunctionCallOutput{CallID: call.CallID, Output: NewToolOutputText("tool result")}
}

func newTestExecutor() *testToolExecutor {
	strict := true
	return &testToolExecutor{definitions: []ResponseTool{
		FunctionTool{
			Name:       "read_file",
			Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
			Strict:     &strict,
		},
	}}
}

func TestReadAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"apikey":" test-key "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := readAPIKey(path)
	if err != nil {
		t.Fatalf("readAPIKey: %v", err)
	}
	if key != "test-key" {
		t.Fatalf("key = %q, want trimmed key", key)
	}

	if err := os.WriteFile(path, []byte(`{"apikey":" "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAPIKey(path); err == nil {
		t.Fatal("expected an empty API key to fail")
	}
}

func TestNewResponseInputAcceptsGenericInputAndPreservesUnknownParts(t *testing.T) {
	input, err := NewResponseInput("hello")
	if err != nil {
		t.Fatalf("NewResponseInput string: %v", err)
	}
	if input.Text == nil || *input.Text != "hello" {
		t.Fatalf("unexpected string input: %#v", input)
	}

	payload := []map[string]any{{
		"type": "message",
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": "inspect this"},
			{"type": "input_audio", "payload": map[string]any{"codec": "raw", "data": "opaque"}},
		},
	}}
	input, err = NewResponseInput(payload)
	if err != nil {
		t.Fatalf("NewResponseInput structured payload: %v", err)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal generic input: %v", err)
	}
	if !strings.Contains(string(encoded), `"type":"input_audio"`) || !strings.Contains(string(encoded), `"codec":"raw"`) {
		t.Fatalf("generic content was not preserved: %s", encoded)
	}

	if _, err := NewResponseInput(map[string]string{"type": "message"}); err == nil {
		t.Fatal("expected an object that is not a Responses input item list to fail")
	}
}

func TestRespondRunsFunctionCallAndSendsFullHistory(t *testing.T) {
	executor := newTestExecutor()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}

		var request ResponseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Model != DeepSeekFlashModel {
			t.Errorf("model = %q", request.Model)
		}
		if len(request.Tools) != 1 {
			t.Errorf("tools = %d, want 1", len(request.Tools))
		}
		if request.Input == nil || request.Input.Items == nil {
			t.Errorf("expected item-list input, got %#v", request.Input)
		}

		if requestCount == 1 {
			if got := len(request.Input.Items); got != 1 {
				t.Errorf("initial history has %d items, want 1", got)
			}
			_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"deepseek-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking."}]},{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"}]}`)
			return
		}
		if requestCount == 2 {
			if got := len(request.Input.Items); got != 4 {
				t.Errorf("continued history has %d items, want 4", got)
			} else {
				if _, ok := request.Input.Items[1].(InputMessage); !ok {
					t.Errorf("assistant response decoded as %T", request.Input.Items[1])
				}
				if _, ok := request.Input.Items[2].(InputFunctionCall); !ok {
					t.Errorf("function call decoded as %T", request.Input.Items[2])
				}
				if output, ok := request.Input.Items[3].(InputFunctionCallOutput); !ok || output.CallID != "call_1" {
					t.Errorf("tool output decoded as %#v", request.Input.Items[3])
				}
			}
			_, _ = fmt.Fprint(w, `{"id":"resp_2","object":"response","created_at":2,"status":"completed","model":"deepseek-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}]}`)
			return
		}
		t.Errorf("unexpected extra request %d", requestCount)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newClient("test-key", server.URL+"/responses", server.Client(), executor)
	input, err := NewResponseInput("read a file")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Respond(context.Background(), input)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if response.ID != "resp_2" || requestCount != 2 {
		t.Fatalf("response=%#v requests=%d", response, requestCount)
	}
	if len(executor.calls) != 1 || executor.calls[0].CallID != "call_1" {
		t.Fatalf("unexpected tool calls: %#v", executor.calls)
	}
}

func TestRespondKeepsHistoryAcrossUserTurns(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var request ResponseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if requestCount == 1 {
			if len(request.Input.Items) != 1 {
				t.Errorf("first turn input items = %d, want 1", len(request.Input.Items))
			}
			_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First answer."}]}]}`)
			return
		}
		if requestCount == 2 {
			if len(request.Input.Items) != 3 {
				t.Errorf("second turn history has %d items, want 3", len(request.Input.Items))
			} else {
				if first, ok := request.Input.Items[0].(InputMessage); !ok || first.Role != RoleUser {
					t.Errorf("first item = %#v, want original user message", request.Input.Items[0])
				}
				if assistant, ok := request.Input.Items[1].(InputMessage); !ok || assistant.Role != RoleAssistant {
					t.Errorf("second item = %#v, want prior assistant message", request.Input.Items[1])
				}
				if second, ok := request.Input.Items[2].(InputMessage); !ok || second.Role != RoleUser {
					t.Errorf("third item = %#v, want new user message", request.Input.Items[2])
				}
			}
			_, _ = fmt.Fprint(w, `{"id":"resp_2","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Second answer."}]}]}`)
			return
		}
		t.Errorf("unexpected request %d", requestCount)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newClient("test-key", server.URL+"/responses", server.Client(), newTestExecutor())

	for _, text := range []string{"first question", "follow-up"} {
		input, err := NewResponseInput(text)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Respond(context.Background(), input); err != nil {
			t.Fatalf("Respond(%q): %v", text, err)
		}
	}
	if requestCount != 2 {
		t.Fatalf("requests = %d, want 2", requestCount)
	}
}

func TestRespondIsUnlimitedByDefault(t *testing.T) {
	executor := newTestExecutor()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount <= 9 {
			_, _ = fmt.Fprintf(w, `{"id":"resp_%d","object":"response","created_at":1,"status":"completed","model":"deepseek-flash","output":[{"type":"function_call","call_id":"call_%d","name":"read_file","arguments":"{}"}]}`, requestCount, requestCount)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"resp_10","object":"response","created_at":1,"status":"completed","model":"deepseek-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
	}))
	defer server.Close()

	client := newClient("test-key", server.URL+"/responses", server.Client(), executor)
	input, err := NewResponseInput("keep going")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Respond(context.Background(), input)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if response == nil || requestCount != 10 || len(executor.calls) != 9 {
		t.Fatalf("response=%#v requests=%d tool calls=%d", response, requestCount, len(executor.calls))
	}
}

func TestRespondLogsRequestMetadataWithoutPayloads(t *testing.T) {
	logs := captureLogs(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"private-response-content"}]}]}`)
	}))
	defer server.Close()
	client := newClient("private-api-key", server.URL, server.Client(), newTestExecutor())
	input, err := NewResponseInput("private-user-input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Respond(context.Background(), input); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	output := logs.String()
	for _, expected := range []string{"model request started", "model response received", "model=deepseek-flash", "request=1", "status=completed", "tool_calls=0"} {
		if !strings.Contains(output, expected) {
			t.Errorf("logs %q do not contain %q", output, expected)
		}
	}
	for _, private := range []string{"private-user-input", "private-response-content", "private-api-key"} {
		if strings.Contains(output, private) {
			t.Errorf("logs contain private payload %q: %s", private, output)
		}
	}
}

func TestRespondWithOptionsStopsAtConfiguredToolRoundLimit(t *testing.T) {
	executor := newTestExecutor()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		_, _ = fmt.Fprintf(w, `{"id":"resp_%d","object":"response","created_at":1,"status":"completed","model":"deepseek-flash","output":[{"type":"function_call","call_id":"call_%d","name":"read_file","arguments":"{}"}]}`, requestCount, requestCount)
	}))
	defer server.Close()

	client := newClient("test-key", server.URL+"/responses", server.Client(), executor)
	input, err := NewResponseInput("stop after two rounds")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.RespondWithOptions(context.Background(), input, RespondOptions{MaxToolRounds: 2})
	if !errors.Is(err, ErrToolCallLimit) {
		t.Fatalf("error = %v, want ErrToolCallLimit", err)
	}
	if response == nil || requestCount != 3 || len(executor.calls) != 2 {
		t.Fatalf("response=%#v requests=%d tool calls=%d", response, requestCount, len(executor.calls))
	}
	lastOutput, ok := client.history[len(client.history)-2].(InputFunctionCallOutput)
	if !ok || lastOutput.CallID != "call_3" {
		t.Fatalf("history before interruption marker = %#v, want skipped output for call_3", client.history[len(client.history)-2])
	}
}

func TestRespondWithOptionsAllowsFinalAnswerAtLimitBoundary(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 1 {
			_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"resp_2","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
	}))
	defer server.Close()
	client := newClient("test-key", server.URL, server.Client(), newTestExecutor())
	input, err := NewResponseInput("one tool call")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RespondWithOptions(context.Background(), input, RespondOptions{MaxToolRounds: 1}); err != nil {
		t.Fatalf("final answer at the limit returned an error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("requests = %d, want 2", requestCount)
	}
}

func TestRespondWithOptionsRejectsNegativeLimit(t *testing.T) {
	client := newClient("test-key", "", nil, newTestExecutor())
	input, err := NewResponseInput("hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RespondWithOptions(context.Background(), input, RespondOptions{MaxToolRounds: -1}); err == nil {
		t.Fatal("expected a negative tool-round limit to fail")
	}
}

func TestRespondPreservesHistoryWhenCanceledDuringRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-releaseHandler
	}))
	defer server.Close()
	defer close(releaseHandler)
	client := newClient("test-key", server.URL, server.Client(), newTestExecutor())
	input, err := NewResponseInput("cancel this")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Respond(ctx, input)
		result <- err
	}()
	<-requestStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Respond error = %v, want context.Canceled", err)
	}
	if len(client.history) != 2 {
		t.Fatalf("history items = %d, want user input and interruption marker", len(client.history))
	}
	marker, ok := client.history[1].(InputMessage)
	if !ok || marker.Role != RoleAssistant {
		t.Fatalf("history marker = %#v, want assistant interruption marker", client.history[1])
	}
}

func TestRespondPreservesCompletedAndSkippedToolCallsOnCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"},{"type":"function_call","call_id":"call_2","name":"read_file","arguments":"{}"}]}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	executor := &cancelingToolExecutor{testToolExecutor: newTestExecutor(), cancel: cancel}
	client := newClient("test-key", server.URL, server.Client(), executor)
	input, err := NewResponseInput("cancel between tools")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Respond(ctx, input)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Respond error = %v, want context.Canceled", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("executed calls = %d, want 1", len(executor.calls))
	}
	var outputs []InputFunctionCallOutput
	for _, item := range client.history {
		if output, ok := item.(InputFunctionCallOutput); ok {
			outputs = append(outputs, output)
		}
	}
	if len(outputs) != 2 || outputs[0].CallID != "call_1" || outputs[1].CallID != "call_2" {
		t.Fatalf("function call outputs = %#v, want outputs for both call IDs", outputs)
	}
	if len(client.history) == 0 {
		t.Fatal("interrupted history was not saved")
	}
	marker, ok := client.history[len(client.history)-1].(InputMessage)
	if !ok || marker.Role != RoleAssistant {
		t.Fatalf("final history item = %#v, want assistant interruption marker", client.history[len(client.history)-1])
	}
}

func TestRespondReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid key"}`)
	}))
	defer server.Close()

	client := newClient("test-key", server.URL+"/responses", server.Client(), newTestExecutor())
	input, err := NewResponseInput("hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Respond(context.Background(), input); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want HTTP 401", err)
	}
}
