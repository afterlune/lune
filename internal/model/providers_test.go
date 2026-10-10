package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeepSeekProviderCreateResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ds-key" {
			t.Errorf("authorization = %q", got)
		}
		var request ResponseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "deepseek-test" || request.Stream {
			t.Errorf("request model/stream = %q/%v", request.Model, request.Stream)
		}
		_, _ = fmt.Fprint(w, `{"id":"resp-1","object":"response","status":"completed","model":"deepseek-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
	}))
	defer server.Close()
	provider := newResponsesProvider(ProviderDeepSeek, "ds-key", server.URL+"/responses", server.Client())
	profile := ModelProfile{Provider: ProviderDeepSeek, Model: "deepseek-test"}
	response, err := provider.CreateResponse(context.Background(), ResponseRequest{Input: NewResponseInputText("hi")}, profile)
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if response.ID != "resp-1" || response.Usage.TotalTokens != 6 {
		t.Fatalf("response = %#v", response)
	}
}

func TestDeepSeekProviderStreamsNormalizedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q", got)
		}
		var request ResponseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream {
			t.Errorf("stream request = %#v, error = %v", request, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"hi\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp-stream\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"deepseek-test\",\"output\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n")
	}))
	defer server.Close()
	provider := newResponsesProvider(ProviderDeepSeek, "ds-key", server.URL, server.Client())
	var events []ModelStreamEvent
	response, err := provider.StreamResponse(context.Background(), ResponseRequest{Input: NewResponseInputText("hi")}, ModelProfile{Model: "deepseek-test"}, func(event ModelStreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamResponse: %v", err)
	}
	if response.ID != "resp-stream" || len(events) != 2 || events[0].Delta != "hi" || events[0].Type != ModelStreamTextDelta {
		t.Fatalf("response=%#v events=%#v", response, events)
	}
}

func TestAnthropicMessagesMapsToolsImagesUsageAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicAPIVersion {
			t.Errorf("anthropic-version = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		encoded, _ := json.Marshal(request)
		for _, expected := range []string{`"model":"claude-test"`, `"max_tokens":2048`, `"type":"tool_use"`, `"type":"tool_result"`, `"type":"image"`, `"input_schema"`} {
			if !strings.Contains(string(encoded), expected) {
				t.Errorf("request missing %s: %s", expected, encoded)
			}
		}
		_, _ = fmt.Fprint(w, `{"id":"msg-1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"reading"},{"type":"tool_use","id":"toolu-1","name":"read_file","input":{"path":"a.go"}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`)
	}))
	defer server.Close()
	provider := newAnthropicMessagesProvider(ProviderAnthropic, "anthropic-key", server.URL+"/v1/messages", server.Client())
	profile := ModelProfile{Provider: ProviderAnthropic, Model: "claude-test", MaxOutputTokens: 2048}
	request := ResponseRequest{
		Instructions: "Inspect safely.",
		Input: NewResponseInputItems(
			InputMessage{Role: RoleUser, Content: NewMessageContentParts(
				InputText{Text: "inspect this"}, InputImage{ImageURL: "data:image/png;base64,YWJj"},
			)},
			UnknownInputItem{Raw: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I will inspect it."}]}`)},
			UnknownInputItem{Raw: json.RawMessage(`{"type":"function_call","call_id":"toolu-previous","name":"read_file","arguments":"{\"path\":\"a.go\"}"}`)},
			InputFunctionCallOutput{CallID: "toolu-previous", Output: NewToolOutputText("package main")},
		),
		Tools: []ResponseTool{FunctionTool{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	response, err := provider.CreateResponse(context.Background(), request, profile)
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if response.ID != "msg-1" || len(functionCalls(response.Output)) != 1 {
		t.Fatalf("normalized response = %#v", response)
	}
	if response.Usage.InputTokensDetails.CachedTokens != 3 || response.Usage.InputTokensDetails.CacheCreationTokens != 2 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestAnthropicMessagesRejectsUnsupportedModalitiesExplicitly(t *testing.T) {
	request := ResponseRequest{Input: NewResponseInputItems(InputMessage{
		Role:    RoleUser,
		Content: NewMessageContentParts(UnknownContentPart{Raw: json.RawMessage(`{"type":"input_audio","data":"opaque"}`)}),
	})}
	_, err := toAnthropicRequest(request, ModelProfile{Model: "claude-test", MaxOutputTokens: 100}, false)
	if err == nil || !strings.Contains(err.Error(), "cannot map content part") {
		t.Fatalf("unsupported modality error = %v", err)
	}
}

func TestAnthropicMessagesStreamsTextAndToolDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["stream"] != true {
			t.Errorf("stream = %v", request["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`event: message_start` + "\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-stream\",\"model\":\"claude-test\",\"usage\":{\"input_tokens\":5}}}\n\n",
			`event: content_block_start` + "\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			`event: content_block_delta` + "\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			`event: content_block_stop` + "\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			`event: message_delta` + "\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n",
			`event: message_stop` + "\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			_, _ = fmt.Fprint(w, event)
		}
	}))
	defer server.Close()
	provider := newAnthropicMessagesProvider(ProviderAnthropic, "anthropic-key", server.URL, server.Client())
	var deltas []string
	response, err := provider.StreamResponse(context.Background(), ResponseRequest{Input: NewResponseInputText("hi")}, ModelProfile{Model: "claude-test", MaxOutputTokens: 128}, func(event ModelStreamEvent) error {
		if event.Type == ModelStreamTextDelta {
			deltas = append(deltas, event.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamResponse: %v", err)
	}
	if response.ID != "msg-stream" || response.Status != "completed" || len(deltas) != 1 || deltas[0] != "hello" {
		t.Fatalf("response=%#v deltas=%#v", response, deltas)
	}
}
