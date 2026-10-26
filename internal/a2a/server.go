package a2a

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/afterlune/luneCore"
)

// SessionRuntime abstracts the Lune session runtime operations needed by the A2A server.
type SessionRuntime interface {
	Close() error
	SubmitTurn(context.Context, core.TurnRequest, core.SubmissionMode) (core.TurnSubmission, error)
	WaitTurn(context.Context, string) (core.TurnResult, error)
	PendingRequests(context.Context) ([]core.PendingRequest, error)
	ResolveApproval(string, bool) error
	ResolveUserQuestion(context.Context, core.UserQuestionAnswer) error
	Interrupt(string) bool
	SetUserInteractionAvailable(bool)
}

// Backend abstracts session creation and runtime opening on top of core.Core.
type Backend interface {
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	OpenSessionRuntime(context.Context, string) (SessionRuntime, error)
}

type coreBackend struct {
	client *core.Core
}

// NewCoreBackend wraps *core.Core as an A2A Backend.
func NewCoreBackend(client *core.Core) Backend {
	return &coreBackend{client: client}
}

func (b *coreBackend) CreateSession(ctx context.Context, meta json.RawMessage) (core.Session, error) {
	return b.client.CreateSession(ctx, meta)
}

func (b *coreBackend) OpenSessionRuntime(ctx context.Context, sessionID string) (SessionRuntime, error) {
	return b.client.OpenSessionRuntime(ctx, sessionID)
}

type waitOutcome struct {
	result core.TurnResult
	err    error
}

type managedTask struct {
	mu          sync.Mutex
	id          string
	contextID   string
	sessionID   string
	executionID string
	state       string
	runtime     SessionRuntime
	pending     *core.PendingRequest
	statusText  string
	statusData  map[string]any
	outputText  string
	waitCh      chan waitOutcome
}

// Server exposes Lune SessionRuntime over the A2A 1.0 JSON-RPC + Agent Card HTTP endpoints.
type Server struct {
	backend Backend
	token   string
	seq     atomic.Uint64
	mu      sync.RWMutex
	tasks   map[string]*managedTask
}

// NewServer constructs an A2A HTTP Server for Lune.
func NewServer(backend Backend, token string) *Server {
	return &Server{
		backend: backend,
		token:   strings.TrimSpace(token),
		tasks:   make(map[string]*managedTask),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/.well-known/agent-card.json" || r.URL.Path == "/agent-card.json"):
		s.handleAgentCard(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case r.Method == http.MethodPost && (r.URL.Path == "/" || r.URL.Path == "/a2a"):
		if !s.authorize(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="lune-a2a"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		s.handleJSONRPC(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) authorize(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1:18091"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, host)
	card := map[string]any{
		"name":               "Lune Agent",
		"description":        "Autonomous AI coding and workspace execution agent",
		"version":            "0.1.0",
		"protocolVersion":    "1.0",
		"url":                baseURL,
		"preferredTransport": "JSONRPC",
		"supportedInterfaces": []map[string]any{
			{
				"url":             baseURL,
				"protocolBinding": "JSONRPC",
				"protocolVersion": "1.0",
			},
		},
		"capabilities": map[string]any{
			"streaming":              false,
			"pushNotifications":      false,
			"stateTransitionHistory": true,
			"cancellation":           true,
		},
		"defaultInputModes":  []string{"text/plain", "application/json"},
		"defaultOutputModes": []string{"text/plain", "application/json"},
		"skills": []map[string]any{
			{
				"id":          "workspace_analysis",
				"name":        "Workspace Analysis",
				"description": "Read, search, and analyze repository files and architecture",
			},
			{
				"id":          "code_execution",
				"name":        "Code Execution & Verification",
				"description": "Edit files, run commands, and execute verification tests with approval gates",
			},
		},
	}
	if s.token != "" {
		card["securitySchemes"] = map[string]any{
			"bearerAuth": map[string]any{
				"httpAuthSecurityScheme": map[string]any{"scheme": "bearer"},
			},
		}
		card["securityRequirements"] = []map[string]any{
			{"schemes": map[string]any{"bearerAuth": map[string]any{}}},
		}
	}
	writeJSON(w, http.StatusOK, card)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "invalid JSON payload")
		return
	}
	switch req.Method {
	case "SendMessage", "message/send":
		s.handleSendMessage(w, r.Context(), req)
	case "GetTask", "tasks/get":
		s.handleGetTask(w, r.Context(), req)
	case "CancelTask", "tasks/cancel":
		s.handleCancelTask(w, r.Context(), req)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("unsupported method %q", req.Method))
	}
}

type incomingPart struct {
	Text string         `json:"text"`
	Data map[string]any `json:"data"`
}

type incomingMessage struct {
	MessageID string         `json:"messageId"`
	TaskID    string         `json:"taskId"`
	TaskIDAlt string         `json:"task_id"`
	ContextID string         `json:"contextId"`
	CtxIDAlt  string         `json:"context_id"`
	Parts     []incomingPart `json:"parts"`
}

type sendMessageParams struct {
	Message incomingMessage `json:"message"`
}

func (m incomingMessage) effectiveTaskID() string {
	if m.TaskID != "" {
		return m.TaskID
	}
	return m.TaskIDAlt
}

func (m incomingMessage) effectiveContextID() string {
	if m.ContextID != "" {
		return m.ContextID
	}
	return m.CtxIDAlt
}

func (m incomingMessage) extractInput() (string, map[string]any) {
	var texts []string
	var data map[string]any
	for _, p := range m.Parts {
		if strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
		if len(p.Data) > 0 && data == nil {
			data = p.Data
		}
	}
	return strings.Join(texts, "\n"), data
}

func (s *Server) handleSendMessage(w http.ResponseWriter, ctx context.Context, req rpcRequest) {
	var params sendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid SendMessage params")
		return
	}
	text, dataPart := params.Message.extractInput()
	if text == "" && len(dataPart) == 0 {
		writeRPCError(w, req.ID, -32602, "message parts must include text or data")
		return
	}

	if taskID := params.Message.effectiveTaskID(); taskID != "" {
		s.mu.RLock()
		task := s.tasks[taskID]
		s.mu.RUnlock()
		if task == nil {
			writeRPCError(w, req.ID, -32001, "task not found")
			return
		}
		if err := s.resumeTaskInput(ctx, task, text, dataPart); err != nil {
			writeRPCError(w, req.ID, -32002, err.Error())
			return
		}
		writeRPCResult(w, req.ID, map[string]any{"task": s.snapshotTask(task)})
		return
	}

	sess, err := s.backend.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		writeRPCError(w, req.ID, -32000, fmt.Sprintf("create session: %v", err))
		return
	}
	rt, err := s.backend.OpenSessionRuntime(ctx, sess.ID)
	if err != nil {
		writeRPCError(w, req.ID, -32000, fmt.Sprintf("open session runtime: %v", err))
		return
	}
	rt.SetUserInteractionAvailable(true)

	input, err := core.ParseTaskInput(text)
	if err != nil {
		_ = rt.Close()
		writeRPCError(w, req.ID, -32602, fmt.Sprintf("parse task input: %v", err))
		return
	}
	sub, err := rt.SubmitTurn(ctx, core.TurnRequest{Input: input}, core.StartIfIdle)
	if err != nil {
		_ = rt.Close()
		writeRPCError(w, req.ID, -32000, fmt.Sprintf("submit turn: %v", err))
		return
	}

	taskID := fmt.Sprintf("lune-task-%d", s.seq.Add(1))
	contextID := params.Message.effectiveContextID()
	if contextID == "" {
		contextID = sess.ID
	}
	task := &managedTask{
		id:          taskID,
		contextID:   contextID,
		sessionID:   sess.ID,
		executionID: sub.ExecutionID,
		state:       "TASK_STATE_WORKING",
		runtime:     rt,
		waitCh:      make(chan waitOutcome, 1),
	}
	s.mu.Lock()
	s.tasks[taskID] = task
	s.mu.Unlock()

	go func() {
		res, waitErr := rt.WaitTurn(context.Background(), sub.ExecutionID)
		task.waitCh <- waitOutcome{result: res, err: waitErr}
	}()

	s.advanceTask(ctx, task)
	writeRPCResult(w, req.ID, map[string]any{"task": s.snapshotTask(task)})
}

func (s *Server) resumeTaskInput(ctx context.Context, task *managedTask, text string, dataPart map[string]any) error {
	task.mu.Lock()
	if task.state != "TASK_STATE_INPUT_REQUIRED" || task.pending == nil {
		task.mu.Unlock()
		return errors.New("task is not waiting for user input")
	}
	pending := *task.pending
	task.pending = nil
	task.statusText = ""
	task.statusData = nil
	task.state = "TASK_STATE_WORKING"
	rt := task.runtime
	task.mu.Unlock()

	switch pending.Kind {
	case core.PendingApproval:
		if pending.Approval == nil {
			return errors.New("pending approval missing payload")
		}
		allowed := parseApprovalDecision(text, dataPart)
		if err := rt.ResolveApproval(pending.Approval.ID, allowed); err != nil {
			return fmt.Errorf("resolve approval: %w", err)
		}
	case core.PendingUserQuestion:
		if pending.UserQuestion == nil {
			return errors.New("pending user question missing payload")
		}
		answer := buildUserQuestionAnswer(*pending.UserQuestion, text, dataPart)
		if err := rt.ResolveUserQuestion(ctx, answer); err != nil {
			return fmt.Errorf("resolve user question: %w", err)
		}
	default:
		return fmt.Errorf("unsupported pending request kind %q", pending.Kind)
	}

	s.advanceTask(ctx, task)
	return nil
}

func (s *Server) advanceTask(ctx context.Context, task *managedTask) {
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case outcome := <-task.waitCh:
			s.finalizeTaskOutcome(ctx, task, outcome)
			return
		case <-ticker.C:
			reqs, err := task.runtime.PendingRequests(ctx)
			if err == nil && len(reqs) > 0 {
				s.parkTaskOnPending(task, reqs[0])
				return
			}
		case <-timeout.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) finalizeTaskOutcome(ctx context.Context, task *managedTask, outcome waitOutcome) {
	if outcome.result.Status == core.TaskPaused {
		reqs, err := task.runtime.PendingRequests(ctx)
		if err == nil && len(reqs) > 0 {
			s.parkTaskOnPending(task, reqs[0])
			return
		}
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	switch {
	case outcome.result.Status == core.TaskCancelled || errors.Is(outcome.err, context.Canceled):
		task.state = "TASK_STATE_CANCELED"
		task.statusText = "Task canceled"
	case outcome.err != nil || outcome.result.Status == core.TaskFailed:
		task.state = "TASK_STATE_FAILED"
		if outcome.err != nil {
			task.statusText = outcome.err.Error()
		} else {
			task.statusText = "Task failed"
		}
	default:
		task.state = "TASK_STATE_COMPLETED"
		task.outputText = extractTurnOutputText(outcome.result.Response)
	}
	_ = task.runtime.Close()
}

func (s *Server) parkTaskOnPending(task *managedTask, req core.PendingRequest) {
	task.mu.Lock()
	defer task.mu.Unlock()
	reqCopy := req
	task.pending = &reqCopy
	task.state = "TASK_STATE_INPUT_REQUIRED"
	switch req.Kind {
	case core.PendingApproval:
		toolName := ""
		approvalID := ""
		if req.Approval != nil {
			toolName = req.Approval.Tool
			approvalID = req.Approval.ID
		}
		task.statusText = fmt.Sprintf("Approval required for tool %q (ID: %s)", toolName, approvalID)
		task.statusData = map[string]any{
			"kind":        "approval",
			"approval_id": approvalID,
			"tool":        toolName,
		}
	case core.PendingUserQuestion:
		prompt := "User question requires an answer"
		callID := ""
		if req.UserQuestion != nil {
			callID = req.UserQuestion.CallID
			if len(req.UserQuestion.Questions) > 0 {
				prompt = req.UserQuestion.Questions[0].Question
			}
		}
		task.statusText = prompt
		task.statusData = map[string]any{
			"kind":    "user_question",
			"call_id": callID,
			"prompt":  prompt,
		}
	default:
		task.statusText = fmt.Sprintf("Paused on %s", req.Kind)
		task.statusData = map[string]any{"kind": string(req.Kind)}
	}
}

type taskIDParams struct {
	ID string `json:"id"`
}

func (s *Server) handleGetTask(w http.ResponseWriter, _ context.Context, req rpcRequest) {
	var params taskIDParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		writeRPCError(w, req.ID, -32602, "invalid GetTask params")
		return
	}
	s.mu.RLock()
	task := s.tasks[params.ID]
	s.mu.RUnlock()
	if task == nil {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	writeRPCResult(w, req.ID, s.snapshotTask(task))
}

func (s *Server) handleCancelTask(w http.ResponseWriter, _ context.Context, req rpcRequest) {
	var params taskIDParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		writeRPCError(w, req.ID, -32602, "invalid CancelTask params")
		return
	}
	s.mu.RLock()
	task := s.tasks[params.ID]
	s.mu.RUnlock()
	if task == nil {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	task.mu.Lock()
	execID := task.executionID
	rt := task.runtime
	task.state = "TASK_STATE_CANCELED"
	task.statusText = "Task canceled by client"
	task.pending = nil
	task.mu.Unlock()
	if rt != nil && execID != "" {
		rt.Interrupt(execID)
		_ = rt.Close()
	}
	writeRPCResult(w, req.ID, s.snapshotTask(task))
}

func (s *Server) snapshotTask(task *managedTask) map[string]any {
	task.mu.Lock()
	defer task.mu.Unlock()
	status := map[string]any{
		"state": task.state,
	}
	if task.statusText != "" || len(task.statusData) > 0 {
		var parts []map[string]any
		if task.statusText != "" {
			parts = append(parts, map[string]any{"text": task.statusText})
		}
		if len(task.statusData) > 0 {
			parts = append(parts, map[string]any{"data": task.statusData})
		}
		status["message"] = map[string]any{
			"messageId": task.id + "-status",
			"role":      "ROLE_AGENT",
			"parts":     parts,
		}
	}
	var artifacts []map[string]any
	if task.outputText != "" {
		artifacts = append(artifacts, map[string]any{
			"artifactId": task.id + "-output",
			"parts": []map[string]any{
				{"text": task.outputText},
			},
		})
	}
	return map[string]any{
		"id":        task.id,
		"contextId": task.contextID,
		"status":    status,
		"artifacts": artifacts,
	}
}

func parseApprovalDecision(text string, data map[string]any) bool {
	if data != nil {
		if v, ok := data["approved"].(bool); ok {
			return v
		}
		if v, ok := data["allowed"].(bool); ok {
			return v
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(text))
	switch normalized {
	case "y", "yes", "true", "1", "approve", "approved", "allow", "allowed", "同意", "允许", "确认", "通过":
		return true
	default:
		return false
	}
}

func buildUserQuestionAnswer(req core.UserQuestionRequest, text string, data map[string]any) core.UserQuestionAnswer {
	var answers []core.UserQuestionResponse
	for _, q := range req.Questions {
		ans := strings.TrimSpace(text)
		if data != nil {
			if mapped, ok := data[q.ID].(string); ok && strings.TrimSpace(mapped) != "" {
				ans = strings.TrimSpace(mapped)
			}
		}
		answers = append(answers, core.UserQuestionResponse{
			QuestionID: q.ID,
			Answer:     ans,
		})
	}
	return core.UserQuestionAnswer{
		ExecutionID:  req.ExecutionID,
		RunID:        req.RunID,
		InvocationID: req.InvocationID,
		CallID:       req.CallID,
		Answers:      answers,
	}
}

func extractTurnOutputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil && strings.TrimSpace(plain) != "" {
		return strings.TrimSpace(plain)
	}
	var envelope struct {
		Output []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Output) > 0 {
		var lines []string
		for _, item := range envelope.Output {
			if item.Type != "message" {
				continue
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(item.Content, &parts) == nil {
				for _, p := range parts {
					if p.Type == "output_text" && strings.TrimSpace(p.Text) != "" {
						lines = append(lines, strings.TrimSpace(p.Text))
					}
				}
			}
		}
		if len(lines) > 0 {
			return strings.Join(lines, "\n")
		}
	}
	return strings.TrimSpace(string(raw))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
