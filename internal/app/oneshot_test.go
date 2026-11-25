package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type oneShotTestRuntime struct {
	result      core.TurnResult
	ready       chan struct{}
	interrupted chan struct{}
	once        sync.Once
}

func (r *oneShotTestRuntime) WaitTurn(ctx context.Context, _ string) (core.TurnResult, error) {
	select {
	case <-r.ready:
		return r.result, nil
	case <-ctx.Done():
		return core.TurnResult{}, ctx.Err()
	}
}
func (r *oneShotTestRuntime) Interrupt(context.Context, string) (bool, error) {
	r.once.Do(func() { close(r.interrupted); close(r.ready) })
	return true, nil
}
func oneShotText(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": text}}}}})
	return b
}
func deltaEvent(text string) core.Event {
	return core.Event{Kind: core.EventModelStream, ExecutionID: "exec", AgentID: "lead", Stream: &core.ModelStreamEvent{Type: "text_delta", Purpose: "main", Delta: text}}
}
func finishedEvent() core.Event {
	return core.Event{Kind: core.EventTaskFinished, ExecutionID: "exec", Status: core.TaskCompleted}
}

type notifyingWriter struct {
	bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (w *notifyingWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.once.Do(func() { close(w.first) })
	return n, err
}
func (w *notifyingWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

type countingOneShotWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingOneShotWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(p)
}

func (w *countingOneShotWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

type observingOneShotWriter struct {
	bytes.Buffer
	writes chan string
}

func (w *observingOneShotWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.writes <- string(p)
	return n, err
}

func (w *observingOneShotWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func TestOneShotOutputBeforeCompletionAndNoLostTail(t *testing.T) {
	for _, resultFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "result_first", false: "events_first"}[resultFirst], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := &oneShotTestRuntime{result: core.TurnResult{Status: core.TaskCompleted, Response: oneShotText("hello world")}, ready: make(chan struct{}), interrupted: make(chan struct{})}
			if resultFirst {
				close(r.ready)
			}
			events := make(chan core.Event)
			w := &notifyingWriter{first: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- awaitOneShotTurn(ctx, r, "session", "exec", events, func() {}, w, io.Discard) }()
			select {
			case events <- deltaEvent("hello "):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-w.first:
			case <-ctx.Done():
				t.Fatal("first delta was buffered until completion")
			}
			select {
			case err := <-done:
				t.Fatalf("returned before tail: %v", err)
			default:
			}
			events <- deltaEvent("world")
			events <- finishedEvent()
			if !resultFirst {
				close(r.ready)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if got := w.String(); got != "hello world\n" {
				t.Fatalf("stdout=%q", got)
			}
		})
	}
}

func TestOneShotCoalescesDeltasAndFlushesBeforeToolStatus(t *testing.T) {
	const answer = "one two three four"
	r := &oneShotTestRuntime{
		result:      core.TurnResult{Status: core.TaskCompleted, Response: oneShotText(answer)},
		ready:       make(chan struct{}),
		interrupted: make(chan struct{}),
	}
	close(r.ready)
	events := make(chan core.Event, 8)
	for _, delta := range []string{"one ", "two ", "three ", "four"} {
		events <- deltaEvent(delta)
	}
	events <- core.Event{Kind: core.EventToolStarted, ExecutionID: "exec", Tool: core.ToolCall{Name: "read_file"}}
	events <- finishedEvent()
	var stdout, stderr countingOneShotWriter
	if err := awaitOneShotTurn(context.Background(), r, "session", "exec", events, func() {}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != answer+"\n" {
		t.Fatalf("stdout=%q", got)
	}
	if stdout.writes >= 4 {
		t.Fatalf("stream deltas caused %d stdout writes; expected coalescing", stdout.writes)
	}
	if !strings.Contains(stderr.String(), "Running read_file") {
		t.Fatalf("tool status was not written: %q", stderr.String())
	}
}

func TestOneShotFlushesPendingDeltaWithinFrameInterval(t *testing.T) {
	r := &oneShotTestRuntime{
		result:      core.TurnResult{Status: core.TaskCompleted, Response: oneShotText("first second")},
		ready:       make(chan struct{}),
		interrupted: make(chan struct{}),
	}
	close(r.ready)
	events := make(chan core.Event)
	stdout := &observingOneShotWriter{writes: make(chan string, 4)}
	done := make(chan error, 1)
	go func() {
		done <- awaitOneShotTurn(context.Background(), r, "session", "exec", events, func() {}, stdout, io.Discard)
	}()
	events <- deltaEvent("first ")
	if got := <-stdout.writes; got != "first " {
		t.Fatalf("first delta write=%q", got)
	}
	events <- deltaEvent("second")
	select {
	case got := <-stdout.writes:
		if got != "second" {
			t.Fatalf("buffered delta write=%q", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("buffered delta was not flushed within the frame interval")
	}
	events <- finishedEvent()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "first second\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestOneShotFiltersOtherStreamsAndFallsBack(t *testing.T) {
	for _, streamMain := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "stream"}[streamMain], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := &oneShotTestRuntime{result: core.TurnResult{Status: core.TaskCompleted, Response: oneShotText("answer")}, ready: make(chan struct{}), interrupted: make(chan struct{})}
			close(r.ready)
			events := make(chan core.Event, 16)
			for _, kind := range []string{"worker", "memory", "compaction", "reasoning", "other_execution"} {
				ev := deltaEvent("private " + kind)
				switch kind {
				case "worker":
					ev.AgentID = "worker"
				case "memory", "compaction":
					ev.Stream.Purpose = kind
				case "reasoning":
					ev.Stream.Type = "reasoning_delta"
				case "other_execution":
					ev.ExecutionID = "other"
				}
				events <- ev
			}
			if streamMain {
				events <- deltaEvent("answer")
			}
			events <- finishedEvent()
			var out bytes.Buffer
			if err := awaitOneShotTurn(ctx, r, "session", "exec", events, func() {}, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != "answer\n" {
				t.Fatalf("stdout=%q", got)
			}
		})
	}
}

func TestOneShotReconcilesBestEffortStream(t *testing.T) {
	for _, test := range []struct {
		name    string
		deltas  []string
		want    string
		warning bool
	}{
		{name: "missing_tail", deltas: []string{"hello "}, want: "hello world\n"},
		{name: "missing_middle", deltas: []string{"hello ", "ld"}, want: "hello ld\nhello world\n", warning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := &oneShotTestRuntime{result: core.TurnResult{Status: core.TaskCompleted, Response: oneShotText("hello world")}, ready: make(chan struct{}), interrupted: make(chan struct{})}
			close(r.ready)
			events := make(chan core.Event, len(test.deltas)+1)
			for _, delta := range test.deltas {
				events <- deltaEvent(delta)
			}
			events <- finishedEvent()
			var stdout, stderr bytes.Buffer
			if err := awaitOneShotTurn(ctx, r, "session", "exec", events, func() {}, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != test.want {
				t.Fatalf("stdout=%q, want %q", got, test.want)
			}
			if got := strings.Contains(stderr.String(), "complete answer follows"); got != test.warning {
				t.Fatalf("stderr=%q, want warning=%v", stderr.String(), test.warning)
			}
		})
	}
}

func TestOneShotReconcilesOnlyFinalRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := &oneShotTestRuntime{result: core.TurnResult{Status: core.TaskCompleted, Response: oneShotText("hello world")}, ready: make(chan struct{}), interrupted: make(chan struct{})}
	close(r.ready)
	events := make(chan core.Event, 3)
	for i, text := range []string{"Checking files...\n", "hello "} {
		event := deltaEvent(text)
		event.Stream.RequestID = []string{"narration", "answer"}[i]
		events <- event
	}
	events <- finishedEvent()
	var stdout, stderr bytes.Buffer
	if err := awaitOneShotTurn(ctx, r, "session", "exec", events, func() {}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "Checking files...\nhello world\n" {
		t.Fatalf("stdout=%q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

type shortOneShotWriter struct{}

func (shortOneShotWriter) Write([]byte) (int, error) { return 0, nil }

type failedOneShotWriter struct{}

func (failedOneShotWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOneShotAbortsOnOutputOrStreamFailure(t *testing.T) {
	for _, kind := range []string{"write", "short_write", "closed_stream", "failed_stream"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := &oneShotTestRuntime{ready: make(chan struct{}), interrupted: make(chan struct{})}
			events := make(chan core.Event, 1)
			var out io.Writer = io.Discard
			switch kind {
			case "write":
				out = failedOneShotWriter{}
				events <- deltaEvent("answer")
			case "short_write":
				out = shortOneShotWriter{}
				events <- deltaEvent("answer")
			case "closed_stream":
				close(events)
			case "failed_stream":
				events <- core.Event{Kind: core.EventRuntimeStreamFailed, Err: "database unavailable"}
			}
			stopped := false
			err := awaitOneShotTurn(ctx, r, "session", "exec", events, func() { stopped = true }, out, io.Discard)
			if err == nil || !stopped {
				t.Fatalf("err=%v stopped=%v", err, stopped)
			}
			if kind == "short_write" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatal(err)
			}
			if kind == "write" && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
			select {
			case <-r.interrupted:
			default:
				t.Fatal("task was not interrupted")
			}
		})
	}
}
func TestOneShotTerminalStatuses(t *testing.T) {
	for _, status := range []core.TaskStatus{core.TaskPaused, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded} {
		t.Run(string(status), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := &oneShotTestRuntime{result: core.TurnResult{Status: status}, ready: make(chan struct{})}
			close(r.ready)
			events := make(chan core.Event, 1)
			ev := finishedEvent()
			ev.Status = status
			events <- ev
			err := awaitOneShotTurn(ctx, r, "session", "exec", events, func() {}, io.Discard, io.Discard)
			if err == nil {
				t.Fatal("expected error")
			}
			if status == core.TaskPaused && !strings.Contains(err.Error(), "task paused") {
				t.Fatal(err)
			}
		})
	}
}

// The first Write blocks until the test releases it. This exercises real Core
// backpressure without using sleeps to guess whether the consumer is slow.
type gatedOneShotWriter struct {
	bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *gatedOneShotWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.Write(p)
}
func (w *gatedOneShotWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

type burstOneShotProvider struct{ count int }

func (p burstOneShotProvider) Name() string { return "host" }
func (p burstOneShotProvider) CreateResponse(context.Context, publicmodel.ModelRequest, publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: strings.Repeat("字", p.count)}}}}}, nil
}
func (p burstOneShotProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, profile publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	for i := 0; i < p.count; i++ {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamTextDelta, Delta: "字"}); err != nil {
			return nil, err
		}
	}
	return p.CreateResponse(ctx, request, profile)
}

func TestRunOneShotPermissionModes(t *testing.T) {
	for _, mode := range []core.PermissionMode{"", core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAuto, core.PermissionFullAccess} {
		t.Run(string(mode), func(t *testing.T) {
			client, err := core.New(core.Config{Workspace: t.TempDir(), StateDir: t.TempDir(), ModelConfig: &publicmodel.Config{
				Providers: map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}}, Profiles: map[string]publicmodel.ModelProfile{"host": {Provider: "host", Model: "test"}}, DefaultProfile: "host",
			}, ModelProviders: map[string]publicmodel.Provider{"host": burstOneShotProvider{count: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(context.Background())
			if err := RunOneShot(client, "hello", CLIFlags{PlanMode: true, PermissionMode: mode}, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			// An unattended run cannot answer approvals, so it defaults to workspace.
			want := mode
			if want == "" {
				want = core.PermissionWorkspace
			}
			if got := client.PermissionMode(); got != want {
				t.Fatalf("permission=%q, want %q", got, want)
			}
		})
	}
}

func TestRunOneShotWithSlowOutput(t *testing.T) {
	const count = 1000
	client, err := core.New(core.Config{Workspace: t.TempDir(), ModelConfig: &publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}}, Profiles: map[string]publicmodel.ModelProfile{"host": {Provider: "host", Model: "test"}}, DefaultProfile: "host",
	}, ModelProviders: map[string]publicmodel.Provider{"host": burstOneShotProvider{count: count}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	writer := &gatedOneShotWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(writer.release) })
	done := make(chan error, 1)
	go func() { done <- RunOneShot(client, "hello", CLIFlags{}, writer, io.Discard) }()
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no streaming output before completion")
	}
	select {
	case err := <-done:
		t.Fatalf("completed while output was blocked: %v", err)
	default:
	}
	release.Do(func() { close(writer.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not drain")
	}
	if got := writer.String(); got != strings.Repeat("字", count)+"\n" {
		t.Fatalf("received %d bytes, want %d", len(got), count*3+1)
	}
}
