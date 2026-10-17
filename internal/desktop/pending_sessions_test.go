package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestSummarizePendingRequestsOmitsRequestContent(t *testing.T) {
	requests := []core.PendingRequest{
		{Kind: core.PendingApproval, Approval: &core.ApprovalRequest{Tool: "exec_command", Arguments: json.RawMessage(`{"secret":"approval"}`)}},
		{Kind: core.PendingUserQuestion, UserQuestion: &core.UserQuestionRequest{Questions: []core.UserQuestionItem{{Question: "secret question"}}}},
		{Kind: core.PendingUserInteraction, Interaction: &core.UserInteractionRequest{ServerName: "docs", Request: json.RawMessage(`{"secret":"interaction"}`)}},
		{Kind: core.PendingToolRecovery, ToolRecovery: &core.ToolRecoveryRequest{Tool: "write_file", Arguments: json.RawMessage(`{"secret":"tool"}`)}},
		{Kind: core.PendingModelRecoveryRequest, ModelRecovery: &core.PendingModelRecovery{}},
		{Kind: core.PendingExecutionBlocker, Blocker: &core.ExecutionBlocker{Tool: "command", Reason: "secret blocker reason"}},
	}

	summaries := summarizePendingRequests(requests)
	if len(summaries) != len(requests) {
		t.Fatalf("summary count = %d, want %d: %#v", len(summaries), len(requests), summaries)
	}
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("summary leaked request content: %s", encoded)
	}
	if summaries[0].Tool != "exec_command" || summaries[2].Tool != "docs" || summaries[3].Tool != "write_file" || summaries[5].Tool != "command" {
		t.Fatalf("tool/server labels = %#v", summaries)
	}
}

func TestListPendingSessionsWithoutWorkspaceReturnsEmptyList(t *testing.T) {
	service := &DesktopService{}
	summaries, err := service.ListPendingSessions()
	if err != nil {
		t.Fatal(err)
	}
	if summaries == nil || len(summaries) != 0 {
		t.Fatalf("pending sessions = %#v, want a non-nil empty list", summaries)
	}
}

type pendingQuestionDesktopProvider struct{}

func (pendingQuestionDesktopProvider) Name() string { return "host" }

func (pendingQuestionDesktopProvider) CreateResponse(_ context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return &publicmodel.ModelResponse{
		Status: "completed",
		Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{
			CallID: "ask-call", Name: "ask_user",
			Arguments: `{"questions":[{"id":"confirm","question":"Private pending question?","options":[{"label":"Yes"}]}]}`,
		}},
	}, nil
}

func (provider pendingQuestionDesktopProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, profile publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	response, err := provider.CreateResponse(ctx, request, profile)
	if err != nil {
		return nil, err
	}
	if emit != nil {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamCompleted, Response: response}); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func TestListPendingSessionsIncludesBackgroundSessionsWithoutQuestionText(t *testing.T) {
	service, agent, firstSessionID := newDesktopServiceForTest(t, pendingQuestionDesktopProvider{})
	firstIndex := 0
	if _, err := service.Submit(SubmitInput{Text: "first task", UserMessageIndex: &firstIndex}); err != nil {
		t.Fatalf("submit first task: %v", err)
	}
	waitForPendingSessionCount(t, service, 1)

	secondSession, err := agent.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if _, err := service.OpenSession(secondSession.ID); err != nil {
		t.Fatalf("open second session: %v", err)
	}
	secondIndex := 0
	if _, err := service.Submit(SubmitInput{Text: "second task", UserMessageIndex: &secondIndex}); err != nil {
		t.Fatalf("submit second task: %v", err)
	}
	waitForPendingSessionCount(t, service, 2)

	summaries, err := service.ListPendingSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("pending sessions = %#v, want both local sessions", summaries)
	}
	seen := map[string]bool{}
	for _, summary := range summaries {
		seen[summary.ID] = true
		if len(summary.Requests) != 1 || summary.Requests[0].Kind != string(core.PendingUserQuestion) {
			t.Errorf("request summary for %q = %#v", summary.ID, summary.Requests)
		}
	}
	if !seen[firstSessionID] || !seen[secondSession.ID] {
		t.Fatalf("summary session IDs = %#v", seen)
	}
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Private pending question") {
		t.Fatalf("pending index leaked question content: %s", encoded)
	}
}

func waitForPendingSessionCount(t *testing.T, service *DesktopService, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		summaries, err := service.ListPendingSessions()
		if err == nil && len(summaries) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	summaries, err := service.ListPendingSessions()
	t.Fatalf("pending session count = %d, want %d; summaries=%#v error=%v", len(summaries), want, summaries, err)
}
