package desktop

import (
	"context"
	"sync"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

func TestSessionControlsAreIndependentInSharedCore(t *testing.T) {
	s, agent, first := newDesktopServiceForTest(t, immediateDesktopProvider{})
	second, err := agent.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenSession(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissionMode(first, "full-access"); err != nil {
		t.Fatal(err)
	}
	controls, err := s.SetPlanMode(first, true)
	if err != nil {
		t.Fatal(err)
	}
	if !controls.PlanMode || controls.EffectivePermissionMode != "read-only" || controls.PermissionMode != "full-access" {
		t.Fatalf("controls=%+v", controls)
	}
	if agent.PermissionMode() != core.PermissionWorkspace {
		t.Fatal("session policy changed shared Core")
	}
	if _, err = s.SetPermissionMode(second.ID, "ask"); err != nil {
		t.Fatal(err)
	}
	state, err := s.OpenSession(first)
	if err != nil {
		t.Fatal(err)
	}
	if state.Controls != controls {
		t.Fatalf("switch lost controls: %+v", state.Controls)
	}
	if _, err = s.SetPermissionMode(first, "workspace"); err == nil {
		t.Fatal("Plan permission was mutable")
	}
	restored, err := s.SetPlanMode(first, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.EffectivePermissionMode != "full-access" {
		t.Fatalf("restored=%+v", restored)
	}
	state, err = s.RefreshSession(second.ID)
	if err != nil || state.Controls.PermissionMode != "ask" || state.Controls.PlanMode {
		t.Fatalf("second=%+v err=%v", state.Controls, err)
	}
	if _, err = s.Submit("missing", SubmitInput{Text: "wrong target"}); err == nil {
		t.Fatal("accepted missing session")
	}
	if s.Interrupt(first, "old-execution") {
		t.Fatal("interrupted stale execution")
	}
}

func TestFailedPlanImplementationPreservesControls(t *testing.T) {
	s, _, id := newDesktopServiceForTest(t, immediateDesktopProvider{})
	if _, err := s.SetPlanMode(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImplementPlan(id, "missing", "same_context"); err == nil {
		t.Fatal("accepted nonexistent plan")
	}
	state, err := s.RefreshSession(id)
	if err != nil || !state.Controls.PlanMode {
		t.Fatalf("controls=%+v err=%v", state.Controls, err)
	}
}

func TestControlsRejectChangesDuringAcceptedWork(t *testing.T) {
	p := &gatedDesktopProvider{entered: make(chan string, 1), release: make(chan struct{})}
	s, _, id := newDesktopServiceForTest(t, p)
	defer close(p.release)
	submission, err := s.Submit(id, SubmitInput{Text: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	<-p.entered
	if _, err = s.SetPermissionMode(id, "full-access"); err == nil {
		t.Fatal("changed running permission")
	}
	if _, err = s.SetPlanMode(id, true); err == nil {
		t.Fatal("changed running Plan state")
	}
	if s.Interrupt(id, submission.ExecutionID+"-stale") {
		t.Fatal("stale interrupt succeeded")
	}
}

func TestCloseWorkspaceCancelsRunningAndQueuedTurns(t *testing.T) {
	provider := &gatedDesktopProvider{entered: make(chan string, 1), release: make(chan struct{})}
	service, _, sessionID := newDesktopServiceForTest(t, provider)
	if _, err := service.Submit(sessionID, SubmitInput{Text: "running"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model request did not start")
	}
	if _, err := service.Submit(sessionID, SubmitInput{Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.CloseWorkspace(true) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CloseWorkspace: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CloseWorkspace did not return")
	}
}

func TestCloseWorkspaceRefusesBusyWithoutInterrupt(t *testing.T) {
	provider := &gatedDesktopProvider{entered: make(chan string, 1), release: make(chan struct{})}
	service, _, sessionID := newDesktopServiceForTest(t, provider)
	if _, err := service.Submit(sessionID, SubmitInput{Text: "running"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model request did not start")
	}
	if err := service.CloseWorkspace(false); err == nil {
		t.Fatal("closed a busy workspace without interruption")
	}
	if _, err := service.RefreshSession(sessionID); err != nil {
		t.Fatalf("workspace unusable after refused close: %v", err)
	}
	close(provider.release)
	if err := service.CloseWorkspace(true); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}
}

func TestConcurrentRuntimeAttachmentAndCompletionClosesCleanly(t *testing.T) {
	s, agent, _ := newDesktopServiceForTest(t, immediateDesktopProvider{})
	var wg sync.WaitGroup
	for range 12 {
		saved, err := agent.CreateSession(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.attachSessionRuntime(&desktopSessionContext{core: agent, sessionID: id, workspace: s.workspace}); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.Submit(id, SubmitInput{Text: "complete"}); err != nil {
				t.Error(err)
			}
		}(saved.ID)
	}
	wg.Wait()
}
