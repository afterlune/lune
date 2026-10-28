package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func testServer(t *testing.T, rt *fakeRuntime) *Server {
	t.Helper()
	s := NewServer(&fakeBackend{rt: rt}, "")
	s.responseWait = time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := s.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return s
}
func eventuallyTask(t *testing.T, s *Server, task *managedTask, state string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		task.mu.Lock()
		current := task.state
		changed := task.changed
		task.mu.Unlock()
		if current == state {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("state=%s want=%s", current, state)
		}
	}
}
func submitFake(t *testing.T, s *Server) *managedTask {
	t.Helper()
	input, _ := core.NewTaskInput("work")
	task, e := s.submitTask(context.Background(), "", input)
	if e != nil {
		t.Fatal(e)
	}
	return task
}
func TestDetachedTaskKeepsObservingCompletion(t *testing.T) {
	rt := newFakeRuntime()
	s := testServer(t, rt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := rpcRequest{ID: 1, Params: json.RawMessage(`{"message":{"parts":[{"text":"work"}]}}`)}
	w := httptest.NewRecorder()
	s.handleSendMessage(w, ctx, req)
	var response struct {
		Result struct {
			Task struct {
				ID string `json:"id"`
			} `json:"task"`
		} `json:"result"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	task := s.findTask(response.Result.Task.ID)
	if task == nil {
		t.Fatal(w.Body.String())
	}
	rt.releaseCh <- core.TurnResult{Status: core.TaskCompleted, Response: json.RawMessage(`"finished"`)}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	if s.snapshotTask(task)["artifacts"] == nil {
		t.Fatal("missing output")
	}
}
func TestReplyFailureAndStaleIDKeepPending(t *testing.T) {
	rt := newFakeRuntime(core.PendingRequest{Kind: core.PendingApproval, Approval: &core.ApprovalRequest{ID: "approve", Tool: "read_file", PathGrant: &publictool.PathGrant{Path: "/private", Access: publictool.PathRead}}})
	s := testServer(t, rt)
	task := submitFake(t, s)
	eventuallyTask(t, s, task, "TASK_STATE_INPUT_REQUIRED")
	task.mu.Lock()
	payload := task.statusData["payload"].(map[string]any)
	task.mu.Unlock()
	if payload["tool"] != "read_file" || payload["path_grant"].(*publictool.PathGrant).Path != "/private" {
		t.Fatalf("missing approval scope: %v", payload)
	}
	if e := s.resumeTaskInput(context.Background(), task, "", map[string]any{"request_id": "old", "decision": "allow_session"}); e == nil {
		t.Fatal("stale input accepted")
	}
	rt.mu.Lock()
	rt.failResolve = true
	rt.mu.Unlock()
	if e := s.resumeTaskInput(context.Background(), task, "allow_session", nil); e == nil {
		t.Fatal("resolve should fail")
	}
	eventuallyTask(t, s, task, "TASK_STATE_INPUT_REQUIRED")
	rt.mu.Lock()
	rt.failResolve = false
	rt.mu.Unlock()
	if e := s.resumeTaskInput(context.Background(), task, "yes", nil); e != nil {
		t.Fatal(e)
	}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	rt.mu.Lock()
	d := rt.decision
	rt.mu.Unlock()
	if d != publictool.ApprovalAllowOnce {
		t.Fatalf("yes gave %s", d)
	}
}
func TestInitialPartsPreservedAndContextReused(t *testing.T) {
	rt := newFakeRuntime()
	s := testServer(t, rt)
	req := rpcRequest{ID: 1, Params: json.RawMessage(`{"message":{"parts":[{"text":"first"},{"data":{"a":1}},{"text":"last"},{"data":{"b":2}}]}}`)}
	w := httptest.NewRecorder()
	s.handleSendMessage(w, context.Background(), req)
	rt.mu.Lock()
	encoded, _ := json.Marshal(rt.submitted.Input)
	rt.mu.Unlock()
	for _, want := range []string{"first", "last", `\"a\":1`, `\"b\":2`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing %s: %s", want, encoded)
		}
	}
	var response struct {
		Result struct {
			Task struct {
				ID string `json:"id"`
			} `json:"task"`
		} `json:"result"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	task := s.findTask(response.Result.Task.ID)
	if task.contextID != "sess-a2a-1" {
		t.Fatal(task.contextID)
	}
	input, _ := core.NewTaskInput("next")
	if _, e := s.submitTask(context.Background(), task.contextID, input); !errors.Is(e, core.ErrSessionBusy) {
		t.Fatalf("concurrent context=%v", e)
	}
	rt.releaseCh <- core.TurnResult{Status: core.TaskCompleted}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	next, e := s.submitTask(context.Background(), task.contextID, input)
	if e != nil {
		t.Fatal(e)
	}
	if next.sessionID != task.sessionID {
		t.Fatal("context changed")
	}
	if _, e = s.submitTask(context.Background(), "unknown", input); e == nil {
		t.Fatal("unknown context accepted")
	}
}
func TestRetentionOnlyEvictsTerminalTasks(t *testing.T) {
	s := NewServer(&fakeBackend{}, "")
	defer s.cancel()
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < maxRetainedTasks+2; i++ {
		id := string(rune(i + 1))
		s.tasks[id] = &managedTask{state: "TASK_STATE_COMPLETED", finishedAt: now.Add(-time.Duration(i) * time.Second)}
	}
	live := &managedTask{state: "TASK_STATE_WORKING"}
	s.tasks["live"] = live
	s.pruneLocked()
	if len(s.tasks) != maxRetainedTasks+1 || s.tasks["live"] != live {
		t.Fatal("retention limit failed")
	}
	now = now.Add(2 * time.Hour)
	s.pruneLocked()
	if len(s.tasks) != 1 {
		t.Fatal("expired tasks retained")
	}
}
func TestRecoveryKindsRemainUsable(t *testing.T) {
	for _, p := range []core.PendingRequest{
		{Kind: core.PendingModelRecoveryRequest, ModelRecovery: &core.PendingModelRecovery{CallID: "model"}},
		{Kind: core.PendingToolRecovery, ToolRecovery: &core.ToolRecoveryRequest{InvocationID: "tool"}},
		{Kind: core.PendingUserInteraction, Interaction: &core.UserInteractionRequest{ID: "mcp", Request: json.RawMessage(`{"method":"elicitation/create"}`)}},
	} {
		t.Run(string(p.Kind), func(t *testing.T) {
			rt := newFakeRuntime(p)
			s := testServer(t, rt)
			task := submitFake(t, s)
			eventuallyTask(t, s, task, "TASK_STATE_INPUT_REQUIRED")
			data := map[string]any{"request_id": pendingID(p), "decision": "retry", "response": map[string]any{"action": "accept"}}
			if e := s.resumeTaskInput(context.Background(), task, "", data); e != nil {
				t.Fatal(e)
			}
			rt.releaseCh <- core.TurnResult{Status: core.TaskCompleted}
			eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
		})
	}
}
func TestCompletionCannotBeOverwrittenByCancellation(t *testing.T) {
	rt := newFakeRuntime()
	s := testServer(t, rt)
	task := submitFake(t, s)
	rt.releaseCh <- core.TurnResult{Status: core.TaskCompleted}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	if e := s.cancelTask(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
}

func TestToolRecoveryRetriesAdvanceWithoutRepeatingDecision(t *testing.T) {
	rt := newFakeRuntime(core.PendingRequest{Kind: core.PendingToolRecovery, ToolRecovery: &core.ToolRecoveryRequest{InvocationID: "tool"}})
	rt.recoverFailures = 1
	s := testServer(t, rt)
	task := submitFake(t, s)
	eventuallyTask(t, s, task, "TASK_STATE_INPUT_REQUIRED")
	if e := s.resumeTaskInput(context.Background(), task, "retry", nil); e == nil {
		t.Fatal("expected recover failure")
	}
	task.opMu.Lock()
	finished := s.refreshTask(task)
	task.opMu.Unlock()
	if finished {
		t.Fatal("failed Recover lost the interaction")
	}
	if e := s.resumeTaskInput(context.Background(), task, "retry", nil); e != nil {
		t.Fatal(e)
	}
	rt.releaseCh <- core.TurnResult{Status: core.TaskCompleted}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	rt.mu.Lock()
	resolves := rt.toolResolves
	closes := rt.closeCount
	rt.mu.Unlock()
	if resolves != 1 || closes != 1 {
		t.Fatalf("resolves=%d closes=%d", resolves, closes)
	}
}
func TestCoreBlockerEndsTaskAndPreservesSession(t *testing.T) {
	rt := newFakeRuntime(core.PendingRequest{Kind: core.PendingExecutionBlocker, Blocker: &core.ExecutionBlocker{ID: "blocked", Reason: "needs authorization"}})
	s := testServer(t, rt)
	task := submitFake(t, s)
	eventuallyTask(t, s, task, "TASK_STATE_FAILED")
	task.mu.Lock()
	data := task.statusData
	task.mu.Unlock()
	if data["kind"] != core.PendingExecutionBlocker {
		t.Fatal(data)
	}
	rt.mu.Lock()
	interrupt := rt.interrupted
	closes := rt.closeCount
	rt.mu.Unlock()
	if interrupt != "" || closes != 1 {
		t.Fatalf("blocker was discarded: %s/%d", interrupt, closes)
	}
}
