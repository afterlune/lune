package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type integrationProvider struct {
	mu       sync.Mutex
	calls    int
	input    string
	block    bool
	question bool
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (p *integrationProvider) Name() string { return "host" }
func (p *integrationProvider) CreateResponse(ctx context.Context, r publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	input, _ := json.Marshal(r.Input)
	p.input = string(input)
	p.mu.Unlock()
	if p.block {
		p.once.Do(func() { close(p.entered) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	if n == 1 {
		if p.question {
			return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{
				CallID: "ask", Name: "ask_user", Arguments: `{"questions":[{"id":"target","question":"Which target?","options":[{"label":"staging"},{"label":"local"}]}]}`,
			}}}, nil
		}
		return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{CallID: "write", Name: "write_file", Arguments: `{"path":"forbidden.txt","content":"not allowed"}`}}}, nil
	}
	return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: "done"}}}}}, nil
}

func TestDurableTaskResumesQuestionAfterCoreRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	provider := &integrationProvider{question: true}
	cfg := core.Config{Workspace: t.TempDir(), StateDir: t.TempDir(), ModelConfig: &publicmodel.Config{
		Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
		Profiles:       map[string]publicmodel.ModelProfile{"host": {Provider: "host", Model: "test"}},
		DefaultProfile: "host",
	}, ModelProviders: map[string]publicmodel.Provider{"host": provider}}
	dir := filepath.Join(cfg.StateDir, "a2a")
	open := func() (*core.Core, *Server) {
		t.Helper()
		client, err := core.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		server, err := OpenServer(ctx, NewCoreBackend(client), "", dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := server.Close(cleanup); err != nil {
				t.Error(err)
			}
			if err := client.Close(cleanup); err != nil {
				t.Error(err)
			}
		})
		return client, server
	}
	client, server := open()
	input, _ := core.ParseTaskInput("pick a target")
	task, err := server.submitMessage(ctx, "", "message-1", input)
	if err != nil {
		t.Fatal(err)
	}
	eventuallyTask(t, server, task, "TASK_STATE_INPUT_REQUIRED")
	if err := server.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}

	client, restored := open()
	found, err := restored.submitMessage(ctx, "", "message-1", input)
	if err != nil || found.id != task.id {
		t.Fatalf("restored task = %v, error = %v", found, err)
	}
	eventuallyTask(t, restored, found, "TASK_STATE_INPUT_REQUIRED")
	found.mu.Lock()
	requestID := pendingID(*found.pending)
	found.mu.Unlock()
	if err := restored.resumeTaskInput(ctx, found, "", map[string]any{"request_id": "stale", "answers": map[string]any{"target": "staging"}}); err == nil {
		t.Fatal("accepted a stale request ID")
	}
	if err := restored.resumeTaskInput(ctx, found, "", map[string]any{"request_id": requestID, "answers": map[string]any{"target": "staging"}}); err != nil {
		t.Fatal(err)
	}
	eventuallyTask(t, restored, found, "TASK_STATE_COMPLETED")
	if err := restored.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, restored = open()
	final := restored.findTask(task.id)
	if final == nil {
		t.Fatal("completed task was lost after restart")
	}
	if final.state != "TASK_STATE_COMPLETED" || final.outputText != "done" {
		t.Fatalf("restored completed task = %s, %q", final.state, final.outputText)
	}
}
func (p *integrationProvider) StreamResponse(ctx context.Context, r publicmodel.ModelRequest, m publicmodel.ModelProfile, _ func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.CreateResponse(ctx, r, m)
}
func integrationCore(t *testing.T, p *integrationProvider) *core.Core {
	t.Helper()
	c, e := core.New(core.Config{Workspace: t.TempDir(), StateDir: t.TempDir(), ModelConfig: &publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}}, Profiles: map[string]publicmodel.ModelProfile{"host": {Provider: "host", Model: "test"}}, DefaultProfile: "host",
	}, ModelProviders: map[string]publicmodel.Provider{"host": p}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := c.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return c
}
func TestA2ARealCoreReadOnlyAndContextContinuity(t *testing.T) {
	p := &integrationProvider{}
	c := integrationCore(t, p)
	if e := c.SetPermissionMode(core.PermissionReadOnly); e != nil {
		t.Fatal(e)
	}
	s := NewServer(NewCoreBackend(c), "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := s.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	input, _ := core.NewTaskInput("first message")
	task, e := s.submitTask(context.Background(), "", input)
	if e != nil {
		t.Fatal(e)
	}
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
	if _, e = os.Stat(filepath.Join(c.Workspace(), "forbidden.txt")); !os.IsNotExist(e) {
		t.Fatalf("read-only write was not blocked: %v", e)
	}
	nextInput, _ := core.NewTaskInput("second message")
	next, e := s.submitTask(context.Background(), task.contextID, nextInput)
	if e != nil {
		t.Fatal(e)
	}
	eventuallyTask(t, s, next, "TASK_STATE_COMPLETED")
	p.mu.Lock()
	history := p.input
	p.mu.Unlock()
	for _, want := range []string{"first message", "second message"} {
		if !strings.Contains(history, want) {
			t.Fatalf("lost history %q: %s", want, history)
		}
	}
}
func TestA2ACloseJoinsRealCoreCancellation(t *testing.T) {
	p := &integrationProvider{block: true, entered: make(chan struct{})}
	c := integrationCore(t, p)
	s := NewServer(NewCoreBackend(c), "")
	input, _ := core.NewTaskInput("wait")
	task, e := s.submitTask(context.Background(), "", input)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("provider not entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	task.mu.Lock()
	status := task.state
	task.mu.Unlock()
	if status != "TASK_STATE_CANCELED" {
		t.Fatal(status)
	}
	if _, e = s.submitTask(context.Background(), "", input); e == nil {
		t.Fatal("closed server accepted task")
	}
}

func TestA2AHTTPRequestDisconnectDoesNotCancelAcceptedCoreTurn(t *testing.T) {
	p := &integrationProvider{block: true, entered: make(chan struct{}), release: make(chan struct{})}
	c := integrationCore(t, p)
	if err := c.SetPermissionMode(core.PermissionReadOnly); err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewCoreBackend(c), "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := rpcRequest{ID: 1, Params: json.RawMessage(`{"message":{"parts":[{"text":"finish after disconnect"}]}}`)}
	recorder := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() { s.handleSendMessage(recorder, ctx, req); close(returned) }()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("turn was not accepted")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler ignored disconnect")
	}
	var response struct {
		Result struct {
			Task struct {
				ID string `json:"id"`
			} `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	task := s.findTask(response.Result.Task.ID)
	if task == nil {
		t.Fatal(recorder.Body.String())
	}
	close(p.release)
	eventuallyTask(t, s, task, "TASK_STATE_COMPLETED")
}
