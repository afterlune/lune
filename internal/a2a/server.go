package a2a

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

// SessionRuntime abstracts the Lune session runtime operations needed by the A2A server.
type SessionRuntime interface {
	Close(context.Context) error
	SubmitTurn(context.Context, core.TurnRequest, core.SubmissionMode) (core.TurnSubmission, error)
	WaitTurn(context.Context, string) (core.TurnResult, error)
	PendingRequests(context.Context) ([]core.PendingRequest, error)
	ResolveApprovalDecision(context.Context, string, publictool.ApprovalDecision) error
	State(context.Context) (core.RuntimeState, error)
	Recover(context.Context, ...core.ExecutionOptions) (core.TurnSubmission, error)
	DiscardPaused(context.Context) (bool, error)
	ResolveToolRecovery(context.Context, string, core.ToolRecoveryDecision) error
	ResolveUserInteraction(string, json.RawMessage) error
	ResolveUserQuestion(context.Context, core.UserQuestionAnswer) error
	Interrupt(context.Context, string) (bool, error)
	SetUserInteractionAvailable(bool)
}

// Backend abstracts session creation and runtime opening on top of core.Core.
type Backend interface {
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	GetSession(context.Context, string) (core.Session, error)
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

func (b *coreBackend) GetSession(ctx context.Context, id string) (core.Session, error) {
	return b.client.GetSession(ctx, id)
}

type managedTask struct {
	mu              sync.Mutex
	opMu            sync.Mutex
	id              string
	contextID       string
	sessionID       string
	executionID     string
	state           string
	runtime         SessionRuntime
	pending         *core.PendingRequest
	statusText      string
	statusData      map[string]any
	outputText      string
	changed         chan struct{}
	finishedAt      time.Time
	toolDecision    string
	cancelRequested bool
}

// Server exposes Lune SessionRuntime over the A2A 1.0 JSON-RPC + Agent Card HTTP endpoints.
type Server struct {
	backend      Backend
	token        string
	mu           sync.RWMutex
	tasks        map[string]*managedTask
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	closed       bool
	now          func() time.Time
	responseWait time.Duration
	// Serialize admission without holding the task table lock during backend I/O.
	admission     chan struct{}
	admissionCtx  context.Context
	stopAdmission context.CancelFunc
}

// NewServer constructs an A2A HTTP Server for Lune.
func NewServer(backend Backend, token string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	admissionCtx, stopAdmission := context.WithCancel(context.Background())
	admission := make(chan struct{}, 1)
	admission <- struct{}{}
	return &Server{
		admission: admission, admissionCtx: admissionCtx, stopAdmission: stopAdmission,
		ctx: ctx, cancel: cancel, now: time.Now, responseWait: 15 * time.Second,
		backend: backend,
		token:   strings.TrimSpace(token),
		tasks:   make(map[string]*managedTask),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.mu.RLock()
		closed := s.closed
		s.mu.RUnlock()
		if closed {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server is closing"})
			return
		}
	}
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
		"name":               "Lune",
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
			"stateTransitionHistory": false,
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
	text, data := params.Message.extractInput()
	if text == "" && len(data) == 0 {
		writeRPCError(w, req.ID, -32602, "message parts must include text or data")
		return
	}
	if id := params.Message.effectiveTaskID(); id != "" {
		dataParts := 0
		for _, p := range params.Message.Parts {
			if len(p.Data) > 0 {
				dataParts++
			}
		}
		if dataParts > 1 {
			writeRPCError(w, req.ID, -32602, "continuation requires one data object")
			return
		}
		task := s.findTask(id)
		if task == nil {
			writeRPCError(w, req.ID, -32001, "task not found")
			return
		}
		if err := s.resumeTaskInput(ctx, task, text, data); err != nil {
			writeRPCError(w, req.ID, -32002, err.Error())
			return
		}
		s.awaitTask(ctx, task)
		writeRPCResult(w, req.ID, map[string]any{"task": s.snapshotTask(task)})
		return
	}
	// Preserve every initial part in order. JSON is user content, never control input.
	var parts []string
	for _, part := range params.Message.Parts {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
		if len(part.Data) > 0 {
			encoded, _ := json.Marshal(part.Data)
			parts = append(parts, "Structured data (JSON):\n"+string(encoded))
		}
	}
	input, err := core.ParseTaskInput(strings.Join(parts, "\n\n"))
	if err != nil {
		writeRPCError(w, req.ID, -32602, err.Error())
		return
	}
	task, err := s.submitTask(ctx, params.Message.effectiveContextID(), input)
	if err != nil {
		writeRPCError(w, req.ID, -32002, err.Error())
		return
	}
	s.awaitTask(ctx, task)
	writeRPCResult(w, req.ID, map[string]any{"task": s.snapshotTask(task)})
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
	task := s.findTask(params.ID)
	if task == nil {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	writeRPCResult(w, req.ID, s.snapshotTask(task))
}

func (s *Server) handleCancelTask(w http.ResponseWriter, ctx context.Context, req rpcRequest) {
	var params taskIDParams
	if json.Unmarshal(req.Params, &params) != nil || params.ID == "" {
		writeRPCError(w, req.ID, -32602, "invalid CancelTask params")
		return
	}
	task := s.findTask(params.ID)
	if task == nil {
		writeRPCError(w, req.ID, -32001, "task not found")
		return
	}
	if err := s.cancelTask(ctx, task); err != nil {
		writeRPCError(w, req.ID, -32002, err.Error())
		return
	}
	s.awaitTerminal(ctx, task)
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
