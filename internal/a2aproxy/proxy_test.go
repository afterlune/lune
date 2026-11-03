package a2aproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "test-token-with-at-least-thirty-two-characters"

type fakeWorker struct {
	mu       sync.Mutex
	requests int
	stops    int
	lastAuth string
	done     chan struct{}
}

func newFakeWorker() *fakeWorker { return &fakeWorker{done: make(chan struct{})} }

func (w *fakeWorker) RoundTrip(_ context.Context, path, _ string, headers http.Header, body []byte) (int, http.Header, []byte, error) {
	w.mu.Lock()
	w.requests++
	w.lastAuth = headers.Get("Authorization")
	w.mu.Unlock()
	var response string
	switch {
	case strings.Contains(string(body), `"method":"GetTask"`):
		response = `{"jsonrpc":"2.0","id":1,"result":{"id":"task-1","status":{"state":"TASK_STATE_INPUT_REQUIRED"}}}`
	case strings.Contains(string(body), `"taskId":"task-1"`):
		response = `{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"task-1","status":{"state":"TASK_STATE_COMPLETED"}}}}`
	default:
		response = `{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"task-1","status":{"state":"TASK_STATE_WORKING"}}}}`
	}
	return http.StatusOK, http.Header{"Content-Type": []string{"application/json"}, "X-Upstream": []string{path}}, []byte(response), nil
}

func (w *fakeWorker) Done() <-chan struct{} { return w.done }

func (w *fakeWorker) Stop(context.Context) error {
	w.mu.Lock()
	w.stops++
	w.mu.Unlock()
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	return nil
}

func TestAgentCardAndHealthDoNotStartWorker(t *testing.T) {
	starts := 0
	manager := newTestManager(t, time.Now, func(context.Context) (Worker, error) {
		starts++
		return newFakeWorker(), nil
	})

	card := httptest.NewRecorder()
	manager.ServeHTTP(card, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))
	if card.Code != http.StatusOK || !strings.Contains(card.Body.String(), `"url":"http://lune-a2a:18091"`) {
		t.Fatalf("card status/body = %d %s", card.Code, card.Body.String())
	}
	health := httptest.NewRecorder()
	manager.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"worker":"idle"`) {
		t.Fatalf("health status/body = %d %s", health.Code, health.Body.String())
	}
	if starts != 0 {
		t.Fatalf("worker starts = %d, want 0", starts)
	}
}

func TestA2AProxyRequiresBearerAndStartsWorkerOnlyForAcceptedRequest(t *testing.T) {
	starts := 0
	manager := newTestManager(t, time.Now, func(context.Context) (Worker, error) {
		starts++
		return newFakeWorker(), nil
	})

	unauthorized := httptest.NewRecorder()
	manager.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}
	if starts != 0 {
		t.Fatalf("unauthorized request started %d workers", starts)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"parts":[{"text":"inspect"}]}}}`))
	request.Header.Set("Authorization", "Bearer "+testToken)
	manager.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", response.Code, response.Body.String())
	}
	if starts != 1 {
		t.Fatalf("worker starts = %d, want 1", starts)
	}
	worker, ok := manager.worker.(*fakeWorker)
	if !ok {
		t.Fatal("worker has unexpected implementation")
	}
	worker.mu.Lock()
	forwardedAuth := worker.lastAuth
	worker.mu.Unlock()
	if forwardedAuth != "" {
		t.Fatal("public bearer token was forwarded to the loopback worker")
	}
}

func TestPendingTaskKeepsWorkerAndTerminalTaskExpiresAfterOneHour(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	worker := newFakeWorker()
	manager := newTestManager(t, func() time.Time { return now }, func(context.Context) (Worker, error) {
		return worker, nil
	})
	post(t, manager, `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"parts":[{"text":"inspect"}]}}}`)

	now = now.Add(2 * time.Hour)
	if manager.ReapIdle() {
		t.Fatal("working task was reaped")
	}
	post(t, manager, `{"jsonrpc":"2.0","id":2,"method":"GetTask","params":{"id":"task-1"}}`)
	if manager.ReapIdle() {
		t.Fatal("input-required task was reaped")
	}
	post(t, manager, `{"jsonrpc":"2.0","id":3,"method":"SendMessage","params":{"message":{"taskId":"task-1","parts":[{"text":"continue"}]}}}`)

	now = now.Add(time.Hour - time.Second)
	if manager.ReapIdle() {
		t.Fatal("terminal task was reaped before its one-hour retention")
	}
	now = now.Add(time.Second)
	if !manager.ReapIdle() {
		t.Fatal("terminal worker was not reaped after one hour")
	}
	worker.mu.Lock()
	stops := worker.stops
	worker.mu.Unlock()
	if stops != 1 {
		t.Fatalf("worker stops = %d, want 1", stops)
	}
}

func TestNoWorkerResponseIsAutomaticallyReplayed(t *testing.T) {
	starts := 0
	manager := newTestManager(t, time.Now, func(context.Context) (Worker, error) {
		starts++
		return failingWorker{}, nil
	})
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0"}`))
		request.Header.Set("Authorization", "Bearer "+testToken)
		manager.ServeHTTP(response, request)
		if response.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", response.Code)
		}
	}
	if starts != 1 {
		t.Fatalf("the proxy retried or restarted an uncertain request; worker starts = %d", starts)
	}
}

func TestManagerRequiresLongRandomBearerToken(t *testing.T) {
	_, err := NewManager(context.Background(), Config{
		Token: "short", PublicURL: "http://lune-a2a:18091",
		WorkerFactory: func(context.Context) (Worker, error) { return newFakeWorker(), nil },
	})
	if err == nil {
		t.Fatal("NewManager accepted a short bearer token")
	}
}

type failingWorker struct{}

func (failingWorker) RoundTrip(context.Context, string, string, http.Header, []byte) (int, http.Header, []byte, error) {
	return 0, nil, nil, io.ErrUnexpectedEOF
}
func (failingWorker) Done() <-chan struct{}      { return make(chan struct{}) }
func (failingWorker) Stop(context.Context) error { return nil }

func newTestManager(t *testing.T, now func() time.Time, factory WorkerFactory) *Manager {
	t.Helper()
	manager, err := NewManager(context.Background(), Config{
		Token: testToken, PublicURL: "http://lune-a2a:18091", IdleTimeout: time.Hour,
		WorkerFactory: factory, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	return manager
}

func post(t *testing.T, manager *Manager, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	manager.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
	}
	return response
}
