package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	publictool "github.com/afterlune/luneCore/tool"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

type fakeRuntime struct {
	mu              sync.Mutex
	pending         []core.PendingRequest
	resolvedID      string
	approved        bool
	answered        *core.UserQuestionAnswer
	interrupted     string
	releaseCh       chan core.TurnResult
	result          *core.TurnResult
	failResolve     bool
	recoverFailures int
	toolResolves    int
	submitted       core.TurnRequest
	decision        publictool.ApprovalDecision
	closeCount      int
}

func newFakeRuntime(initialPending ...core.PendingRequest) *fakeRuntime {
	return &fakeRuntime{
		pending:   append([]core.PendingRequest(nil), initialPending...),
		releaseCh: make(chan core.TurnResult, 1),
	}
}

func (f *fakeRuntime) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closeCount++; return nil }

func (f *fakeRuntime) SubmitTurn(_ context.Context, request core.TurnRequest, _ core.SubmissionMode) (core.TurnSubmission, error) {
	f.mu.Lock()
	f.submitted = request
	f.result = nil
	f.mu.Unlock()
	return core.TurnSubmission{ExecutionID: "exec-1"}, nil
}

func (f *fakeRuntime) State(context.Context) (core.RuntimeState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case r := <-f.releaseCh:
		f.result = &r
	default:
	}
	status := core.TaskRunning
	if f.result != nil {
		status = f.result.Status
	}
	return core.RuntimeState{Task: core.TaskState{Status: status, ExecutionID: "exec-1"}}, nil
}
func (f *fakeRuntime) WaitTurn(ctx context.Context, _ string) (core.TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.result != nil {
		return *f.result, nil
	}
	return core.TurnResult{Status: core.TaskRunning}, nil
}
func (f *fakeRuntime) Recover(context.Context, ...core.ExecutionOptions) (core.TurnSubmission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recoverFailures > 0 {
		f.recoverFailures--
		return core.TurnSubmission{}, fmt.Errorf("retry temporarily unavailable")
	}
	f.pending = nil
	f.result = nil
	return core.TurnSubmission{ExecutionID: "exec-1"}, nil
}
func (f *fakeRuntime) DiscardPaused(context.Context) (bool, error) { return false, nil }
func (f *fakeRuntime) ResolveToolRecovery(context.Context, string, core.ToolRecoveryDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolResolves++
	f.pending = nil
	return nil
}
func (f *fakeRuntime) ResolveUserInteraction(string, json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = nil
	return nil
}
func (f *fakeRuntime) ResolveApprovalDecision(id string, d publictool.ApprovalDecision) error {
	f.mu.Lock()
	f.decision = d
	fail := f.failResolve
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("resolve failed")
	}
	return f.ResolveApproval(id, d != publictool.ApprovalDeny)
}

func (f *fakeRuntime) PendingRequests(_ context.Context) ([]core.PendingRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.PendingRequest(nil), f.pending...), nil
}

func (f *fakeRuntime) ResolveApproval(id string, allowed bool) error {
	f.mu.Lock()
	f.resolvedID = id
	f.approved = allowed
	f.pending = nil
	f.mu.Unlock()
	if allowed {
		f.releaseCh <- core.TurnResult{
			ExecutionID: "exec-1",
			Status:      core.TaskCompleted,
			Response:    json.RawMessage(`"approval granted and command executed"`),
		}
	} else {
		f.releaseCh <- core.TurnResult{
			ExecutionID: "exec-1",
			Status:      core.TaskCancelled,
		}
	}
	return nil
}

func (f *fakeRuntime) ResolveUserQuestion(_ context.Context, answer core.UserQuestionAnswer) error {
	f.mu.Lock()
	f.answered = &answer
	f.pending = nil
	f.mu.Unlock()
	f.releaseCh <- core.TurnResult{
		ExecutionID: "exec-1",
		Status:      core.TaskCompleted,
		Response:    json.RawMessage(`"question answered: " + answer.Answers[0].Answer`),
	}
	return nil
}

func (f *fakeRuntime) Interrupt(executionID string) bool {
	f.mu.Lock()
	f.interrupted = executionID
	f.pending = nil
	f.mu.Unlock()
	select {
	case f.releaseCh <- core.TurnResult{ExecutionID: executionID, Status: core.TaskCancelled}:
	default:
	}
	return true
}

func (f *fakeRuntime) SetUserInteractionAvailable(bool) {}

type fakeBackend struct {
	rt *fakeRuntime
}

func (b *fakeBackend) GetSession(_ context.Context, id string) (core.Session, error) {
	if id != "sess-a2a-1" {
		return core.Session{}, fmt.Errorf("unknown context")
	}
	return core.Session{ID: id}, nil
}

func (b *fakeBackend) CreateSession(_ context.Context, _ json.RawMessage) (core.Session, error) {
	return core.Session{ID: "sess-a2a-1"}, nil
}

func (b *fakeBackend) OpenSessionRuntime(_ context.Context, _ string) (SessionRuntime, error) {
	return b.rt, nil
}

func postRPC(t *testing.T, srv http.Handler, token, method string, params any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal rpc: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func TestAgentCardAndAuthBoundary(t *testing.T) {
	rt := newFakeRuntime()
	srv := NewServer(&fakeBackend{rt: rt}, "secret-token")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	cardReq := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	cardRec := httptest.NewRecorder()
	srv.ServeHTTP(cardRec, cardReq)
	if cardRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", cardRec.Code)
	}
	var card map[string]any
	if err := json.Unmarshal(cardRec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if card["protocolVersion"] != "1.0" {
		t.Fatalf("expected protocolVersion 1.0, got %v", card["protocolVersion"])
	}

	code, _ := postRPC(t, srv, "wrong-token", "SendMessage", map[string]any{})
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %d", code)
	}
}

func TestSendMessageApprovalInputRequiredToCompleted(t *testing.T) {
	rt := newFakeRuntime(core.PendingRequest{
		Kind: core.PendingApproval,
		Approval: &core.ApprovalRequest{
			ID:   "apr-42",
			Tool: "command",
		},
	})
	srv := NewServer(&fakeBackend{rt: rt}, "token-ok")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	code, resp := postRPC(t, srv, "token-ok", "SendMessage", map[string]any{
		"message": map[string]any{
			"parts": []map[string]any{{"text": "run tests"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	result := resp["result"].(map[string]any)
	task := result["task"].(map[string]any)
	taskID := task["id"].(string)
	status := task["status"].(map[string]any)
	if status["state"] != "TASK_STATE_INPUT_REQUIRED" {
		t.Fatalf("expected TASK_STATE_INPUT_REQUIRED, got %v", status["state"])
	}

	code, resp2 := postRPC(t, srv, "token-ok", "SendMessage", map[string]any{
		"message": map[string]any{
			"taskId": taskID,
			"parts":  []map[string]any{{"text": "approve"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 on approval reply, got %d", code)
	}
	task2 := resp2["result"].(map[string]any)["task"].(map[string]any)
	status2 := task2["status"].(map[string]any)
	if status2["state"] != "TASK_STATE_COMPLETED" {
		t.Fatalf("expected TASK_STATE_COMPLETED, got %v", status2["state"])
	}
	if !rt.approved || rt.resolvedID != "apr-42" {
		t.Fatalf("expected approval apr-42 to be approved")
	}
}

func TestCancelTaskInterruptsRuntime(t *testing.T) {
	rt := newFakeRuntime(core.PendingRequest{
		Kind: core.PendingApproval,
		Approval: &core.ApprovalRequest{
			ID:   "apr-cancel",
			Tool: "command",
		},
	})
	srv := NewServer(&fakeBackend{rt: rt}, "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	_, resp := postRPC(t, srv, "", "SendMessage", map[string]any{
		"message": map[string]any{
			"parts": []map[string]any{{"text": "long running build"}},
		},
	})
	taskID := resp["result"].(map[string]any)["task"].(map[string]any)["id"].(string)

	_, cancelResp := postRPC(t, srv, "", "CancelTask", map[string]any{"id": taskID})
	canceledTask := cancelResp["result"].(map[string]any)
	status := canceledTask["status"].(map[string]any)
	if status["state"] != "TASK_STATE_CANCELED" {
		t.Fatalf("expected TASK_STATE_CANCELED, got %v", status["state"])
	}
	if rt.interrupted != "exec-1" {
		t.Fatalf("expected Interrupt(exec-1), got %q", rt.interrupted)
	}
}
