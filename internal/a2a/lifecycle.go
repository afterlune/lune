package a2a

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

const maxActiveTasks = 16
const maxRetainedTasks = 128
const taskRetention = time.Hour
const taskPollInterval = 100 * time.Millisecond

func terminal(state string) bool {
	return state == "TASK_STATE_COMPLETED" || state == "TASK_STATE_FAILED" || state == "TASK_STATE_CANCELED"
}

func (s *Server) pruneLocked() {
	now := s.now()
	type completed struct {
		id string
		at time.Time
	}
	var retained []completed
	for id, t := range s.tasks {
		t.mu.Lock()
		at := t.finishedAt
		t.mu.Unlock()
		if !at.IsZero() {
			if now.Sub(at) >= taskRetention {
				s.removeTaskLocked(id)
			} else {
				retained = append(retained, completed{id, at})
			}
		}
	}
	for len(retained) > maxRetainedTasks {
		oldest := 0
		for i := range retained {
			if retained[i].at.Before(retained[oldest].at) {
				oldest = i
			}
		}
		s.removeTaskLocked(retained[oldest].id)
		retained = append(retained[:oldest], retained[oldest+1:]...)
	}
}
func (s *Server) findTask(id string) *managedTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	return s.tasks[id]
}

func (s *Server) submitTask(ctx context.Context, contextID string, input core.TaskInput) (*managedTask, error) {
	return s.submitMessage(ctx, contextID, "", input)
}

func (s *Server) submitMessage(ctx context.Context, contextID, messageID string, input core.TaskInput) (*managedTask, error) {
	select {
	case <-s.admission:
		defer func() { s.admission <- struct{}{} }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.admissionCtx.Done():
		return nil, errors.New("A2A server is closing")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.admissionCtx, cancel)
	defer stop()
	defer cancel()
	// Capacity and context reuse checks are serialized by admission. The task
	// table mutex protects only in-memory state, never backend calls.
	inputHash := fmt.Sprintf("%x", sha256.Sum256(input.Raw))
	if messageID != "" {
		s.mu.RLock()
		for _, task := range s.tasks {
			if task.messageID == messageID {
				s.mu.RUnlock()
				if task.inputHash != inputHash || (contextID != "" && task.contextID != contextID) {
					return nil, errors.New("messageId has already been used for different input")
				}
				return task, nil
			}
		}
		s.mu.RUnlock()
	}
	if err := s.checkAdmission(contextID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var sess core.Session
	var err error
	if contextID == "" {
		sess, err = s.backend.CreateSession(ctx, json.RawMessage("null"))
	} else {
		sess, err = s.backend.GetSession(ctx, contextID)
	}
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	rt, err := s.backend.OpenSessionRuntime(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	task := &managedTask{id: "lune-task-" + rand.Text(), contextID: sess.ID, sessionID: sess.ID,
		messageID: messageID, inputHash: inputHash, state: "TASK_STATE_WORKING", runtime: rt, changed: make(chan struct{})}
	cleanup := func() {
		rt.SetUserInteractionAvailable(false)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = rt.Close(cleanupCtx)
	}
	if err := s.saveTask(task); err != nil {
		cleanup()
		return nil, err
	}
	rt.SetUserInteractionAvailable(true)
	sub, err := rt.SubmitTurn(ctx, core.TurnRequest{Input: input}, core.StartIfIdle)
	if err != nil {
		cleanup()
		if s.stateDir == "" {
			return nil, err
		}
		task.runtime = nil
		task.state, task.statusText, task.finishedAt = "TASK_STATE_FAILED", "Core submission was not confirmed; inspect the session before retrying", s.now()
	} else {
		task.executionID = sub.ExecutionID
	}
	if err := s.saveTask(task); err != nil {
		slog.Error("persist A2A submission outcome", "task_id", task.id, "error", err)
	}
	s.mu.Lock()
	s.tasks[task.id] = task
	if task.runtime != nil {
		s.wg.Add(1)
		go s.observeTask(task)
	}
	s.mu.Unlock()
	return task, nil
}

func (s *Server) checkAdmission(contextID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("A2A server is closing")
	}
	s.pruneLocked()
	active := 0
	for _, t := range s.tasks {
		t.mu.Lock()
		live := !terminal(t.state)
		sid := t.sessionID
		t.mu.Unlock()
		if live {
			active++
			if contextID != "" && contextID == sid {
				return core.ErrSessionBusy
			}
		}
	}
	if active >= maxActiveTasks {
		return errors.New("active task limit reached")
	}
	return nil
}
func (t *managedTask) notifyLocked() { close(t.changed); t.changed = make(chan struct{}) }
func (s *Server) finishTask(t *managedTask, state, message string, data map[string]any, output string) bool {
	t.mu.Lock()
	if terminal(t.state) {
		t.mu.Unlock()
		return true
	}
	t.mu.Unlock()
	// Release the old handle and its capability before admitting another task in
	// the same session. Callers serialize finalization with opMu.
	t.runtime.SetUserInteractionAvailable(false)
	cleanupCtx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if err := t.runtime.Close(cleanupCtx); err != nil {
		slog.Warn("close A2A session runtime", "session_id", t.sessionID, "error", err)
		return false
	}
	t.mu.Lock()
	t.state = state
	t.statusText = message
	t.statusData = data
	t.outputText = output
	t.pending = nil
	t.finishedAt = s.now()
	if err := s.saveTask(t); err != nil {
		slog.Error("persist A2A terminal outcome", "task_id", t.id, "error", err)
	}
	t.notifyLocked()
	t.mu.Unlock()
	s.mu.Lock()
	s.pruneLocked()
	s.mu.Unlock()
	return true
}
func (s *Server) observeTask(t *managedTask) {
	defer s.wg.Done()
	ticker := time.NewTicker(taskPollInterval)
	defer ticker.Stop()
	for {
		if err := acquireTaskOperation(s.ctx, t); err != nil {
			return
		}
		done := t.detached || s.refreshTask(t)
		t.opMu.Unlock()
		if done {
			return
		}
		select {
		case <-ticker.C:
		case <-s.ctx.Done():
			return
		}
	}
}
func (s *Server) refreshTask(t *managedTask) bool {
	t.mu.Lock()
	done := terminal(t.state)
	t.mu.Unlock()
	if done {
		return true
	}
	// A tool resolution may have committed before Recover failed. Keep the host
	// interaction available so retry repeats Recover, never the side-effect decision.
	if t.toolDecision != "" {
		return false
	}
	state, err := t.runtime.State(s.ctx)
	if err != nil {
		return false
	}
	reqs, err := t.runtime.PendingRequests(s.ctx)
	if err != nil {
		return false
	}
	if len(reqs) > 0 && !t.cancelRequested {
		p := reqs[0]
		if p.Kind == core.PendingExecutionBlocker {
			return s.finishTask(t, "TASK_STATE_FAILED", "Execution requires replanning", map[string]any{"kind": p.Kind, "payload": p.Blocker}, "")
		}
		if err := s.parkTask(t, p); err != nil {
			return s.finishTask(t, "TASK_STATE_FAILED", err.Error(), nil, "")
		}
		return false
	}
	status := state.Task.Status
	if state.Task.ExecutionID != "" && state.Task.ExecutionID != t.executionID {
		status = core.TaskCompleted
	}
	switch status {
	case core.TaskCompleted, core.TaskFailed, core.TaskCancelled:
		result, err := t.runtime.WaitTurn(s.ctx, t.executionID)
		if s.ctx.Err() != nil {
			return false
		}
		switch {
		case result.Status == core.TaskCancelled || errors.Is(err, context.Canceled):
			return s.finishTask(t, "TASK_STATE_CANCELED", "Task canceled", nil, "")
		case err != nil || result.Status == core.TaskFailed:
			message := "Task failed"
			if err != nil {
				message = err.Error()
			}
			return s.finishTask(t, "TASK_STATE_FAILED", message, nil, "")
		default:
			return s.finishTask(t, "TASK_STATE_COMPLETED", "", nil, extractTurnOutputText(result.Response))
		}
	case core.TaskPaused:
		return s.finishTask(t, "TASK_STATE_FAILED", "Core session is paused; resume through a host with replanning or budget controls", map[string]any{"kind": "paused", "context_id": t.contextID}, "")
	}
	return false
}
func pendingID(p core.PendingRequest) string {
	switch p.Kind {
	case core.PendingApproval:
		if p.Approval != nil {
			return p.Approval.ID
		}
	case core.PendingUserQuestion:
		if p.UserQuestion != nil {
			return p.UserQuestion.ID
		}
	case core.PendingModelRecoveryRequest:
		if p.ModelRecovery != nil {
			return p.ModelRecovery.CallID
		}
	case core.PendingToolRecovery:
		if p.ToolRecovery != nil {
			return p.ToolRecovery.InvocationID
		}
	case core.PendingUserInteraction:
		if p.Interaction != nil {
			return p.Interaction.ID
		}
	}
	return ""
}
func (s *Server) parkTask(t *managedTask, p core.PendingRequest) error {
	id := pendingID(p)
	if id == "" {
		return fmt.Errorf("unsupported or invalid pending request %q", p.Kind)
	}
	var payload any
	switch p.Kind {
	case core.PendingApproval:
		payload = map[string]any{"tool": p.Approval.Tool, "arguments": p.Approval.Arguments, "path_grant": p.Approval.PathGrant, "auto_review_reason": p.Approval.AutoReviewReason, "fingerprint": p.Approval.Fingerprint}
	case core.PendingUserQuestion:
		payload = map[string]any{"questions": p.UserQuestion.Questions, "draft": p.UserQuestion.Draft}
	case core.PendingModelRecoveryRequest:
		payload = map[string]any{"failure": p.ModelRecovery.Failure}
	case core.PendingToolRecovery:
		payload = map[string]any{"tool": p.ToolRecovery.Tool, "arguments": p.ToolRecovery.Arguments, "agent_id": p.ToolRecovery.AgentID}
	case core.PendingUserInteraction:
		payload = map[string]any{"server": p.Interaction.ServerName, "request": p.Interaction.Request}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending != nil && pendingID(*t.pending) == id && t.pending.Kind == p.Kind {
		return nil
	}
	t.pending = &p
	t.state = "TASK_STATE_INPUT_REQUIRED"
	t.statusText = fmt.Sprintf("Input required: %s", p.Kind)
	t.statusData = map[string]any{"kind": p.Kind, "request_id": id, "payload": payload}
	if p.Kind == core.PendingApproval {
		t.statusData["decisions"] = []string{"allow_once", "allow_session", "deny"}
	}
	t.notifyLocked()
	return nil
}
func (s *Server) resumeTaskInput(ctx context.Context, t *managedTask, text string, data map[string]any) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.admissionCtx, cancel)
	defer stop()
	defer cancel()
	if err := acquireTaskOperation(ctx, t); err != nil {
		return err
	}
	defer t.opMu.Unlock()
	t.mu.Lock()
	if t.cancelRequested || t.state != "TASK_STATE_INPUT_REQUIRED" || t.pending == nil {
		t.mu.Unlock()
		return errors.New("task is not waiting for input")
	}
	p := *t.pending
	t.mu.Unlock()
	if len(data) > 0 {
		if id, ok := data["request_id"].(string); !ok || id != pendingID(p) {
			return errors.New("missing or stale request_id")
		}
	}
	decision := strings.ToLower(strings.TrimSpace(text))
	if v, ok := data["decision"].(string); ok {
		decision = v
	}
	var err error
	switch p.Kind {
	case core.PendingApproval:
		var d publictool.ApprovalDecision
		switch decision {
		case "allow_once", "y", "yes", "true", "1", "approve", "approved", "allow", "allowed", "允许", "同意", "确认", "通过":
			d = publictool.ApprovalAllowOnce
		case "allow_session":
			d = publictool.ApprovalAllowSession
		case "deny", "n", "no", "false", "拒绝":
			d = publictool.ApprovalDeny
		default:
			return errors.New("decision must be allow_once, allow_session, or deny")
		}
		err = t.runtime.ResolveApprovalDecision(ctx, p.Approval.ID, d)
	case core.PendingUserQuestion:
		if len(data) == 0 && len(p.UserQuestion.Questions) != 1 {
			return errors.New("multiple questions require structured answers")
		}
		answers, _ := data["answers"].(map[string]any)
		if len(data) > 0 && answers == nil {
			return errors.New("answers must be an object keyed by question ID")
		}
		err = t.runtime.ResolveUserQuestion(ctx, buildUserQuestionAnswer(*p.UserQuestion, text, answers))
	case core.PendingUserInteraction:
		response, ok := data["response"]
		if !ok {
			return errors.New("response is required")
		}
		encoded, e := json.Marshal(response)
		if e != nil {
			return e
		}
		err = t.runtime.ResolveUserInteraction(p.Interaction.ID, encoded)
	case core.PendingModelRecoveryRequest:
		if decision == "cancel" {
			return s.cancelTaskLocked(ctx, t)
		}
		if decision != "retry" {
			return errors.New("decision must be retry or cancel")
		}
		_, err = t.runtime.Recover(ctx)
	case core.PendingToolRecovery:
		if decision == "cancel" {
			return s.cancelTaskLocked(ctx, t)
		}
		var d core.ToolRecoveryDecision
		switch decision {
		case "retry":
			d = core.ToolRecoveryRetry
		case "skip":
			d = core.ToolRecoverySkip
		default:
			return errors.New("decision must be retry, skip, or cancel")
		}
		if t.toolDecision != "" && t.toolDecision != decision {
			return errors.New("tool recovery decision already committed; retry the same decision")
		}
		if t.toolDecision == "" {
			err = t.runtime.ResolveToolRecovery(ctx, p.ToolRecovery.InvocationID, d)
			if err == nil {
				t.toolDecision = decision
			}
		}
		if err == nil {
			_, err = t.runtime.Recover(ctx)
		}
	default:
		return fmt.Errorf("unsupported pending request %q", p.Kind)
	}
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.pending = nil
	t.toolDecision = ""
	t.state = "TASK_STATE_WORKING"
	t.statusData = nil
	t.statusText = ""
	t.notifyLocked()
	t.mu.Unlock()
	return nil
}
func (s *Server) cancelTask(ctx context.Context, t *managedTask) error {
	if err := acquireTaskOperation(ctx, t); err != nil {
		return err
	}
	defer t.opMu.Unlock()
	return s.cancelTaskLocked(ctx, t)
}
func (s *Server) cancelTaskLocked(ctx context.Context, t *managedTask) error {
	t.mu.Lock()
	done := terminal(t.state)
	t.mu.Unlock()
	if done {
		return nil
	}
	accepted, err := t.runtime.Interrupt(ctx, t.executionID)
	if err != nil {
		return err
	}
	if accepted {
		t.cancelRequested = true
		t.toolDecision = ""
		t.mu.Lock()
		t.state = "TASK_STATE_WORKING"
		t.pending = nil
		t.statusData = nil
		t.statusText = "Cancellation requested"
		t.notifyLocked()
		t.mu.Unlock()
		return nil
	}
	state, err := t.runtime.State(ctx)
	if err != nil {
		return err
	}
	if state.Task.ExecutionID != t.executionID || state.Task.Status != core.TaskPaused {
		return nil
	}
	discarded, err := t.runtime.DiscardPaused(ctx)
	if err != nil {
		return err
	}
	if discarded {
		if !s.finishTask(t, "TASK_STATE_CANCELED", "Task canceled", nil, "") {
			return errors.New("task discarded but runtime cleanup failed; retry cancellation")
		}
		return nil
	}
	return nil // A completed execution is reconciled by the observer.
}
func (s *Server) awaitTask(ctx context.Context, t *managedTask)     { s.await(ctx, t, false) }
func (s *Server) awaitTerminal(ctx context.Context, t *managedTask) { s.await(ctx, t, true) }
func (s *Server) await(ctx context.Context, t *managedTask, onlyTerminal bool) {
	timer := time.NewTimer(s.responseWait)
	defer timer.Stop()
	for {
		t.mu.Lock()
		ready := terminal(t.state) || (!onlyTerminal && t.state == "TASK_STATE_INPUT_REQUIRED")
		changed := t.changed
		t.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-timer.C:
			return
		}
	}
}

// Close stops admission and joins task observers before Core may be closed.
func (s *Server) Close(ctx context.Context) error {
	// Also interrupt observer I/O if the shutdown budget expires while a task
	// operation holds opMu.
	stopDeadline := context.AfterFunc(ctx, s.cancel)
	defer stopDeadline()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopAdmission()
	// An admission may have accepted a Core turn just before cancellation. Join
	// its registration before taking the shutdown snapshot or waiting on wg.
	select {
	case <-s.admission:
		s.admission <- struct{}{}
	case <-ctx.Done():
		s.cancel()
		return ctx.Err()
	}
	s.mu.Lock()
	var tasks []*managedTask
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.Unlock()
	var closeErr error
	for _, t := range tasks {
		if s.stateDir != "" && t.runtime != nil {
			if err := acquireTaskOperation(ctx, t); err != nil {
				closeErr = errors.Join(closeErr, err)
				continue
			}
			t.mu.Lock()
			done := terminal(t.state)
			t.mu.Unlock()
			if done || t.detached {
				t.opMu.Unlock()
				continue
			}
			state, err := t.runtime.State(ctx)
			if err == nil && state.Task.Status == core.TaskPaused {
				t.detached = true
				t.runtime.SetUserInteractionAvailable(false)
				closeErr = errors.Join(closeErr, t.runtime.Close(ctx))
				t.opMu.Unlock()
				continue
			}
			t.opMu.Unlock()
		}
		closeErr = errors.Join(closeErr, s.cancelTask(ctx, t))
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		s.cancel()
		return closeErr
	case <-ctx.Done():
		s.cancel()
		return errors.Join(closeErr, ctx.Err())
	}
}

// Acquire only the handoff lock; the caller context also governs backend I/O.
func acquireTaskOperation(ctx context.Context, task *managedTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task.opMu.TryLock() {
		return nil
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if task.opMu.TryLock() {
				return nil
			}
		}
	}
}
