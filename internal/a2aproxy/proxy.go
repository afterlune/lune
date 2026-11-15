package a2aproxy

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxJSONBodyBytes = 2 << 20

var errManagerClosed = errors.New("A2A proxy is closing")

// Worker is one native Lune A2A server process. The proxy owns at most one
// worker, and does not retry a request after a transport error.
type Worker interface {
	RoundTrip(context.Context, string, string, http.Header, []byte) (int, http.Header, []byte, error)
	Done() <-chan struct{}
	Stop(context.Context) error
}

type WorkerFactory func(context.Context) (Worker, error)

type Config struct {
	Token         string
	PublicURL     string
	IdleTimeout   time.Duration
	WorkerFactory WorkerFactory
	Now           func() time.Time
}

type taskRecord struct {
	state      string
	terminalAt time.Time
}

type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	cfg    Config

	startMu      sync.Mutex
	mu           sync.Mutex
	worker       Worker
	tasks        map[string]taskRecord
	lastActivity time.Time
	inFlight     int
	closed       bool
}

func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	cfg.Token = strings.TrimSpace(cfg.Token)
	if len(cfg.Token) < 32 {
		return nil, errors.New("LUNE_A2A_TOKEN must contain at least 32 characters")
	}
	if cfg.WorkerFactory == nil {
		return nil, errors.New("Lune worker factory is required")
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = "http://lune-a2a:18091"
	}
	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil || publicURL.Scheme != "http" || publicURL.Hostname() == "" || publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" || publicURL.Path != "" && publicURL.Path != "/" {
		return nil, errors.New("LUNE_A2A_PUBLIC_URL must be an HTTP origin without credentials, path, query, or fragment")
	}
	root, cancel := context.WithCancel(ctx)
	return &Manager{
		ctx: root, cancel: cancel, cfg: cfg,
		tasks: make(map[string]taskRecord), lastActivity: cfg.Now(),
	}, nil
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/.well-known/agent-card.json" || r.URL.Path == "/agent-card.json"):
		m.writeAgentCard(w)
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		m.writeHealth(w)
	case r.Method == http.MethodPost && (r.URL.Path == "/" || r.URL.Path == "/a2a"):
		m.handleJSONRPC(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *Manager) writeAgentCard(w http.ResponseWriter) {
	baseURL := strings.TrimRight(m.cfg.PublicURL, "/")
	card := map[string]any{
		"name":               "Lune",
		"description":        "Autonomous AI coding and workspace execution agent",
		"version":            "0.1.0",
		"protocolVersion":    "1.0",
		"url":                baseURL,
		"preferredTransport": "JSONRPC",
		"supportedInterfaces": []map[string]any{{
			"url": baseURL, "protocolBinding": "JSONRPC", "protocolVersion": "1.0",
		}},
		"capabilities": map[string]any{
			"streaming": false, "pushNotifications": false,
			"stateTransitionHistory": false, "cancellation": true,
		},
		"defaultInputModes":  []string{"text/plain", "application/json"},
		"defaultOutputModes": []string{"text/plain", "application/json"},
		"skills": []map[string]any{
			{"id": "workspace_analysis", "name": "Workspace Analysis", "description": "Read, search, and analyze files in the mounted workspace"},
			{"id": "code_execution", "name": "Code Execution & Verification", "description": "Edit workspace files and run permitted commands"},
		},
		"securitySchemes": map[string]any{
			"bearerAuth": map[string]any{
				"httpAuthSecurityScheme": map[string]any{"scheme": "bearer"},
			},
		},
		"securityRequirements": []map[string]any{{"schemes": map[string]any{"bearerAuth": map[string]any{}}}},
	}
	writeJSON(w, http.StatusOK, card)
}

func (m *Manager) writeHealth(w http.ResponseWriter) {
	m.mu.Lock()
	state := "idle"
	if m.worker != nil {
		select {
		case <-m.worker.Done():
		default:
			state = "running"
		}
	}
	if m.closed {
		state = "closing"
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "worker": state})
}

func (m *Manager) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	if !m.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lune-a2a"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBodyBytes+1))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(body) > maxJSONBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	worker, err := m.acquireWorker(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Lune worker unavailable; request was not retried"})
		return
	}
	defer m.releaseWorker()

	upstreamHeaders := r.Header.Clone()
	// The bearer token terminates at this proxy. The child binds only to
	// loopback, so forwarding the external credential would expose it to tools.
	upstreamHeaders.Del("Authorization")
	status, headers, response, err := worker.RoundTrip(r.Context(), r.URL.RequestURI(), r.Host, upstreamHeaders, body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Lune response was uncertain; request was not retried"})
		return
	}
	m.observeTask(body, response)
	copyResponseHeaders(w.Header(), headers)
	w.WriteHeader(status)
	_, _ = w.Write(response)
}

func (m *Manager) authorized(r *http.Request) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	provided := strings.TrimSpace(parts[1])
	return subtle.ConstantTimeCompare([]byte(provided), []byte(m.cfg.Token)) == 1
}

func (m *Manager) acquireWorker(ctx context.Context) (Worker, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errManagerClosed
	}
	if m.worker != nil {
		select {
		case <-m.worker.Done():
			m.worker = nil
			m.tasks = make(map[string]taskRecord)
		default:
			m.inFlight++
			m.lastActivity = m.cfg.Now()
			worker := m.worker
			m.mu.Unlock()
			return worker, nil
		}
	}
	m.mu.Unlock()

	worker, err := m.cfg.WorkerFactory(m.ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		_ = worker.Stop(ctx)
		return nil, errManagerClosed
	}
	m.worker = worker
	m.tasks = make(map[string]taskRecord)
	m.inFlight++
	m.lastActivity = m.cfg.Now()
	return worker, nil
}

func (m *Manager) releaseWorker() {
	m.mu.Lock()
	if m.inFlight > 0 {
		m.inFlight--
	}
	m.lastActivity = m.cfg.Now()
	m.mu.Unlock()
}

func (m *Manager) observeTask(request, response []byte) {
	id, state, ok := responseTask(request, response)
	if !ok {
		return
	}
	now := m.cfg.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.tasks[id]
	record.state = state
	if terminalState(state) {
		record.terminalAt = now
	} else {
		record.terminalAt = time.Time{}
	}
	m.tasks[id] = record
	m.lastActivity = now
}

func (m *Manager) ReapIdle() bool {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	worker := m.worker
	if worker == nil || m.closed || m.inFlight != 0 {
		m.mu.Unlock()
		return false
	}
	select {
	case <-worker.Done():
		m.worker = nil
		m.tasks = make(map[string]taskRecord)
		m.mu.Unlock()
		return false
	default:
	}
	idleSince := m.lastActivity
	for _, task := range m.tasks {
		if !terminalState(task.state) {
			m.mu.Unlock()
			return false
		}
		if task.terminalAt.After(idleSince) {
			idleSince = task.terminalAt
		}
	}
	if m.cfg.Now().Sub(idleSince) < m.cfg.IdleTimeout {
		m.mu.Unlock()
		return false
	}
	m.worker = nil
	m.tasks = make(map[string]taskRecord)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = worker.Stop(ctx)
	return true
}

func (m *Manager) RunReaper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.ReapIdle()
		}
	}
}

func (m *Manager) Close(ctx context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	m.mu.Lock()
	m.closed = true
	m.cancel()
	worker := m.worker
	m.worker = nil
	m.tasks = make(map[string]taskRecord)
	m.mu.Unlock()
	if worker == nil {
		return nil
	}
	return worker.Stop(ctx)
}

func responseTask(request, response []byte) (string, string, bool) {
	var requestData struct {
		Method string `json:"method"`
		Params struct {
			ID        string `json:"id"`
			TaskID    string `json:"taskId"`
			TaskIDAlt string `json:"task_id"`
			Message   struct {
				TaskID    string `json:"taskId"`
				TaskIDAlt string `json:"task_id"`
			} `json:"message"`
		} `json:"params"`
	}
	var responseData struct {
		Error  json.RawMessage `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(request, &requestData) != nil || json.Unmarshal(response, &responseData) != nil || len(responseData.Error) > 0 {
		return "", "", false
	}
	var taskEnvelope struct {
		Task json.RawMessage `json:"task"`
	}
	_ = json.Unmarshal(responseData.Result, &taskEnvelope)
	taskJSON := responseData.Result
	if len(taskEnvelope.Task) > 0 {
		taskJSON = taskEnvelope.Task
	}
	var task struct {
		ID     string `json:"id"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if json.Unmarshal(taskJSON, &task) != nil || task.Status.State == "" {
		return "", "", false
	}
	id := task.ID
	if id == "" {
		id = requestData.Params.ID
	}
	if id == "" {
		id = requestData.Params.TaskID
	}
	if id == "" {
		id = requestData.Params.TaskIDAlt
	}
	if id == "" {
		id = requestData.Params.Message.TaskID
	}
	if id == "" {
		id = requestData.Params.Message.TaskIDAlt
	}
	return id, task.Status.State, id != ""
}

func terminalState(state string) bool {
	state = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(state), "TASK_STATE_"))
	switch state {
	case "completed", "failed", "canceled", "rejected":
		return true
	default:
		return false
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Connection") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Keep-Alive") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
