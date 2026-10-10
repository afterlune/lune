package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/afterlune/lune/core"
)

type fakeResponder struct {
	inputs     []core.TaskRequest
	firstErr   error
	responses  int
	pending    bool
	resumes    int
	discarded  int
	compact    int
	compactErr error
	mode       core.PermissionMode
	sessions   map[string]core.Session
	last       string
	sequence   int
}

type blockingResponder struct {
	*fakeResponder
	started chan struct{}
}

type fakeResumableResponder = fakeResponder

func (f *fakeResponder) Workspace() string                 { return "/workspace" }
func (f *fakeResponder) SetEventHandler(core.EventHandler) {}

func (f *fakeResponder) RunTask(_ context.Context, request core.TaskRequest) (json.RawMessage, error) {
	f.inputs = append(f.inputs, request)
	f.responses++
	if f.responses == 1 && f.firstErr != nil {
		return nil, f.firstErr
	}
	return json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"reply"}]}]}`), nil
}

func (f *blockingResponder) RunTask(ctx context.Context, _ core.TaskRequest) (json.RawMessage, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeResponder) ResumeTask(context.Context, string) (json.RawMessage, error) {
	f.resumes++
	f.pending = false
	return json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"resumed"}]}]}`), nil
}

func (f *fakeResponder) HasPending(context.Context, string) (bool, error) { return f.pending, nil }
func (f *fakeResponder) PendingHistory(context.Context, string) (json.RawMessage, error) {
	return nil, nil
}
func (f *fakeResponder) PendingPlanMode(context.Context, string) (bool, error) { return false, nil }

func (f *fakeResponder) DiscardTask(context.Context, string) (bool, error) {
	if !f.pending {
		return false, nil
	}
	f.pending = false
	f.discarded++
	return true, nil
}

func (f *fakeResponder) Compact(context.Context, string) (int, error) {
	if f.compactErr != nil {
		return 0, f.compactErr
	}
	return f.compact, nil
}

func (f *fakeResponder) CreateSession(_ context.Context, history json.RawMessage) (core.Session, error) {
	f.sequence++
	id := fmt.Sprintf("session-%d", f.sequence)
	saved := core.Session{
		ID: id, Workspace: f.Workspace(), History: append(json.RawMessage(nil), history...),
		Settings: core.SessionSettings{ModelProfile: "lead"},
	}
	if f.sessions == nil {
		f.sessions = make(map[string]core.Session)
	}
	f.sessions[id] = saved
	f.last = id
	return saved, nil
}

func (f *fakeResponder) LatestSession(context.Context) (core.Session, bool, error) {
	saved, ok := f.sessions[f.last]
	return saved, ok, nil
}

func (f *fakeResponder) ListSessions(context.Context, int) ([]core.Session, error) {
	sessions := make([]core.Session, 0, len(f.sessions))
	for _, saved := range f.sessions {
		sessions = append(sessions, saved)
	}
	return sessions, nil
}

func (f *fakeResponder) GetSession(_ context.Context, id string) (core.Session, error) {
	saved, ok := f.sessions[id]
	if !ok {
		return core.Session{}, fmt.Errorf("unknown session %q", id)
	}
	return saved, nil
}

func (f *fakeResponder) ListModelProfiles() []core.ModelOption {
	return []core.ModelOption{
		{Profile: "lead", Provider: "test", Model: "lead-model"},
		{Profile: "review", Provider: "test", Model: "review-model"},
	}
}

func (f *fakeResponder) SetSessionSettings(_ context.Context, id string, settings core.SessionSettings) error {
	saved, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	saved.Settings = settings
	f.sessions[id] = saved
	return nil
}

func (f *fakeResponder) PermissionMode() core.PermissionMode {
	if f.mode == "" {
		return core.PermissionWorkspace
	}
	return f.mode
}

func (f *fakeResponder) SetPermissionMode(mode core.PermissionMode) error { f.mode = mode; return nil }
func (f *fakeResponder) ResolveApproval(string, bool) error               { return nil }
