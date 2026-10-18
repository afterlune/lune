package desktop

import (
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestListDesktopProcessesWithoutWorkspaceReturnsEmptySlices(t *testing.T) {
	service := &DesktopService{}
	result, err := service.ListDesktopProcesses()
	if err != nil {
		t.Fatal(err)
	}
	if result.Processes == nil || len(result.Processes) != 0 || result.UnavailableSessions == nil || len(result.UnavailableSessions) != 0 {
		t.Fatalf("process list = %#v, want initialized empty slices", result)
	}
}

func TestListDesktopProcessesReportsUnavailableSessions(t *testing.T) {
	service, _, sessionID := newDesktopServiceForTest(t, pendingQuestionDesktopProvider{})
	service.mu.Lock()
	delete(service.sessionContexts, sessionID)
	service.mu.Unlock()

	result, err := service.ListDesktopProcesses()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Processes) != 0 {
		t.Fatalf("processes = %#v, want no commands", result.Processes)
	}
	if len(result.UnavailableSessions) != 1 {
		t.Fatalf("unavailable sessions = %#v, want the missing local session", result.UnavailableSessions)
	}
	item := result.UnavailableSessions[0]
	if item.ID != sessionID || item.Status != "unavailable" || item.Reason != "此会话的运行时未加载。" {
		t.Fatalf("unavailable local session = %#v", item)
	}
}

func TestDesktopProcessRuntimeRejectsUnloadedSession(t *testing.T) {
	service := &DesktopService{workspace: `D:\workspace`, sessionContexts: map[string]*desktopSessionContext{}}
	if _, err := service.ReadDesktopProcessOutput("missing", "command", 0, false); err == nil {
		t.Fatal("ReadDesktopProcessOutput accepted an unloaded session")
	}
	if err := service.StopDesktopProcess("missing", "command"); err == nil {
		t.Fatal("StopDesktopProcess accepted an unloaded session")
	}
}

func TestToAgentEventIncludesCommandLifecycleFields(t *testing.T) {
	exitCode := 17
	event := toAgentEvent(core.Event{
		Kind: core.EventCommandFinished, SessionID: "session-1", ExecutionID: "execution-1",
		CommandID: "command-1", CommandStatus: core.CommandFailed, CommandExitCode: &exitCode,
	})
	if event.Kind != string(core.EventCommandFinished) || event.SessionID != "session-1" || event.CommandID != "command-1" || event.CommandStatus != "failed" || event.CommandExitCode == nil || *event.CommandExitCode != exitCode {
		t.Fatalf("mapped command event = %#v", event)
	}
}
