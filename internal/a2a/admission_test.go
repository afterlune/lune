package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

type stalledAdmissionBackend struct {
	fakeBackend
	entered        chan struct{}
	release        chan struct{}
	respectContext bool
}

func (b *stalledAdmissionBackend) CreateSession(ctx context.Context, _ json.RawMessage) (core.Session, error) {
	close(b.entered)
	if b.respectContext {
		select {
		case <-ctx.Done():
			return core.Session{}, ctx.Err()
		case <-b.release:
		}
	} else {
		<-b.release
	}
	return core.Session{}, errors.New("admission released")
}

func TestShutdownCanCancelStalledAdmission(t *testing.T) {
	for _, cooperative := range []bool{true, false} {
		name := "ignores context"
		if cooperative {
			name = "respects context"
		}
		t.Run(name, func(t *testing.T) {
			backend := &stalledAdmissionBackend{entered: make(chan struct{}), release: make(chan struct{}), respectContext: cooperative}
			s := NewServer(backend, "")
			submissionDone := make(chan error, 1)
			input, _ := core.NewTaskInput("work")
			go func() { _, err := s.submitTask(context.Background(), "", input); submissionDone <- err }()
			<-backend.entered
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			closed := make(chan error, 1)
			go func() { closed <- s.Close(ctx) }()
			select {
			case err := <-closed:
				if cooperative && err != nil {
					t.Errorf("shutdown should cancel admission: %v", err)
				}
				if !cooperative && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("shutdown error=%v", err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Error("shutdown blocked behind admission beyond its deadline")
				close(backend.release)
				<-submissionDone
				<-closed
				return
			}
			close(backend.release)
			if err := <-submissionDone; err == nil {
				t.Error("stalled admission succeeded after shutdown")
			}
		})
	}
}

func TestStalledAdmissionDoesNotBlockTaskLookup(t *testing.T) {
	backend := &stalledAdmissionBackend{entered: make(chan struct{}), release: make(chan struct{})}
	s := NewServer(backend, "")
	input, _ := core.NewTaskInput("work")
	done := make(chan struct{})
	go func() { _, _ = s.submitTask(context.Background(), "", input); close(done) }()
	<-backend.entered
	lookup := make(chan struct{})
	go func() { s.findTask("missing"); close(lookup) }()
	select {
	case <-lookup:
	case <-time.After(200 * time.Millisecond):
		t.Error("task lookup blocked by unrelated admission I/O")
	}
	close(backend.release)
	<-done
	<-lookup
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type acceptedAdmissionRuntime struct {
	*fakeRuntime
	accepted chan struct{}
	release  chan struct{}
}

func (r *acceptedAdmissionRuntime) SubmitTurn(ctx context.Context, request core.TurnRequest, mode core.SubmissionMode) (core.TurnSubmission, error) {
	submission, err := r.fakeRuntime.SubmitTurn(ctx, request, mode)
	close(r.accepted)
	<-r.release // Model a turn accepted just before shutdown cancels admission.
	return submission, err
}

type acceptedAdmissionBackend struct {
	fakeBackend
	runtime SessionRuntime
}

func (b *acceptedAdmissionBackend) OpenSessionRuntime(context.Context, string) (SessionRuntime, error) {
	return b.runtime, nil
}

func TestShutdownIncludesTurnAcceptedDuringAdmission(t *testing.T) {
	rt := &acceptedAdmissionRuntime{fakeRuntime: newFakeRuntime(), accepted: make(chan struct{}), release: make(chan struct{})}
	s := NewServer(&acceptedAdmissionBackend{runtime: rt}, "")
	input, _ := core.NewTaskInput("work")
	submitted := make(chan *managedTask, 1)
	submissionErr := make(chan error, 1)
	go func() {
		task, err := s.submitTask(context.Background(), "", input)
		submitted <- task
		submissionErr <- err
	}()
	<-rt.accepted
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx) }()
	<-s.admissionCtx.Done()
	close(rt.release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	task := <-submitted
	if err := <-submissionErr; err != nil {
		t.Fatal(err)
	}
	task.mu.Lock()
	state := task.state
	task.mu.Unlock()
	rt.mu.Lock()
	closeCount := rt.closeCount
	rt.mu.Unlock()
	if state != "TASK_STATE_CANCELED" || closeCount != 1 {
		t.Fatalf("accepted turn escaped shutdown: state=%s closes=%d", state, closeCount)
	}
}

func TestCloseDeadlineWhileTaskOperationIsHeld(t *testing.T) {
	s := NewServer(&fakeBackend{rt: newFakeRuntime()}, "")
	task := submitFake(t, s)
	task.opMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Close(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("close error=%v", err)
		}
	case <-time.After(time.Second):
		task.opMu.Unlock()
		<-done
		t.Fatal("Close remained blocked on task operation past its deadline")
	}
	task.opMu.Unlock()
}
