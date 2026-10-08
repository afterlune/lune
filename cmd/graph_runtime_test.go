package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/agentgraph"
	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
	graph "github.com/afterlune/luneGraph"
	"github.com/afterlune/luneGraph/checkpoint/memory"
)

type graphScriptedRequester struct {
	responses []*model.Response
	requests  []model.ResponseRequest
	errors    []error
}

func (r *graphScriptedRequester) CreateResponse(_ context.Context, request model.ResponseRequest) (*model.Response, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var saved model.ResponseRequest
	if err := json.Unmarshal(encoded, &saved); err != nil {
		return nil, err
	}
	r.requests = append(r.requests, saved)
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		return nil, err
	}
	if len(r.responses) == 0 {
		return nil, context.Canceled
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response, nil
}

func TestGraphResponderClearsFailedModelTaskAndContinues(t *testing.T) {
	registry, err := tool.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	requester := &graphScriptedRequester{
		errors: []error{errors.New("temporary model error")},
		responses: []*model.Response{
			graphResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"next answer"}]}]}`),
		},
	}
	agent, err := agentgraph.New(requester, registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[agentgraph.State](graph.JSONClone[agentgraph.State])
	if err != nil {
		t.Fatal(err)
	}
	runtime := &graphResponder{agent: agent, store: store}
	failedInput, err := model.NewResponseInput("will fail")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Respond(context.Background(), failedInput); err == nil || !strings.Contains(err.Error(), "temporary model error") {
		t.Fatalf("first Respond error = %v, want temporary model error", err)
	}
	if pending, err := runtime.HasPending(); err != nil || pending {
		t.Fatalf("pending after model failure = %v, error = %v", pending, err)
	}
	nextInput, err := model.NewResponseInput("next task")
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtime.Respond(context.Background(), nextInput)
	if err != nil {
		t.Fatalf("second Respond: %v", err)
	}
	if responseText(response) != "next answer" {
		t.Fatalf("response = %q, want next answer", responseText(response))
	}
}

func TestGraphResponderCarriesHistoryBetweenCompletedTasks(t *testing.T) {
	registry, err := tool.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	requester := &graphScriptedRequester{responses: []*model.Response{
		graphResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}]}`),
		graphResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second answer"}]}]}`),
	}}
	agent, err := agentgraph.New(requester, registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.New[agentgraph.State](graph.JSONClone[agentgraph.State])
	if err != nil {
		t.Fatal(err)
	}
	runtime := &graphResponder{agent: agent, store: store}
	firstInput, err := model.NewResponseInput("first question")
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.Respond(context.Background(), firstInput)
	if err != nil {
		t.Fatalf("first Respond: %v", err)
	}
	if responseText(first) != "first answer" {
		t.Fatalf("first response = %q", responseText(first))
	}
	if pending, err := runtime.HasPending(); err != nil || pending {
		t.Fatalf("pending after completed first task = %v, error = %v", pending, err)
	}
	secondInput, err := model.NewResponseInput("second question")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Respond(context.Background(), secondInput)
	if err != nil {
		t.Fatalf("second Respond: %v", err)
	}
	if responseText(second) != "second answer" {
		t.Fatalf("second response = %q", responseText(second))
	}
	if len(requester.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requester.requests))
	}
	encoded, err := json.Marshal(requester.requests[1].Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first question", "first answer", "second question"} {
		if !strings.Contains(string(encoded), content) {
			t.Errorf("second request history lost %q: %s", content, encoded)
		}
	}
}

func graphResponse(t *testing.T, raw string) *model.Response {
	t.Helper()
	var response model.Response
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func responseText(response *model.Response) string {
	if response == nil {
		return ""
	}
	var text strings.Builder
	for _, item := range response.Output {
		message, ok := item.(model.OutputMessage)
		if !ok {
			continue
		}
		for _, part := range message.Content {
			if output, ok := part.(model.OutputText); ok {
				text.WriteString(output.Text)
			}
		}
	}
	return text.String()
}
