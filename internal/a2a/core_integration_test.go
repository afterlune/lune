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
	mu      sync.Mutex
	calls   int
	input   string
	block   bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
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
		return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{CallID: "write", Name: "write_file", Arguments: `{"path":"forbidden.txt","content":"not allowed"}`}}}, nil
	}
	return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: "done"}}}}}, nil
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
