package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/agentgraph"
	"github.com/afterlune/lune/internal/model"
	graph "github.com/afterlune/luneGraph"
	"github.com/afterlune/luneGraph/checkpoint"
)

func TestSessionTitleArchiveForkAndLatest(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	ctx := context.Background()

	history := json.RawMessage(`[{"type":"message","role":"user","content":"review this"}]`)
	source, err := client.CreateSession(ctx, history)
	if err != nil {
		t.Fatal(err)
	}
	settings := SessionSettings{ModelProfile: "review", ReasoningEffort: model.ReasoningEffortHigh}
	if err := client.SetSessionSettings(ctx, source.ID, settings); err != nil {
		t.Fatal(err)
	}
	if err := client.SetSessionTitle(ctx, source.ID, "  Review thread  "); err != nil {
		t.Fatal(err)
	}
	if err := client.SetSessionArchived(ctx, source.ID, true); err != nil {
		t.Fatal(err)
	}
	active, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	latest, found, err := client.LatestSession(ctx)
	if err != nil || !found || latest.ID != active.ID {
		t.Fatalf("LatestSession = %#v, found=%v, error=%v", latest, found, err)
	}
	ordinary, err := client.ListSessions(ctx, 30, false)
	if err != nil || len(ordinary) != 1 || ordinary[0].ID != active.ID {
		t.Fatalf("active sessions = %#v, error=%v", ordinary, err)
	}
	all, err := client.ListSessions(ctx, 30, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("all sessions = %#v, error=%v", all, err)
	}
	archived, err := client.GetSession(ctx, source.ID)
	if err != nil || archived.Title != "Review thread" || !archived.Archived {
		t.Fatalf("archived session = %#v, error=%v", archived, err)
	}

	fork, err := client.ForkSession(ctx, source.ID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if fork.ID == source.ID || string(fork.History) != string(history) || fork.Settings != settings || fork.Title != "" || fork.Archived {
		t.Fatalf("fork = %#v", fork)
	}
	if err := client.SetSessionTitle(ctx, fork.ID, " "); err != nil {
		t.Fatalf("clear title: %v", err)
	}
	if err := client.SetSessionTitle(ctx, fork.ID, strings.Repeat("界", 121)); err == nil {
		t.Fatal("title longer than 120 characters should be rejected")
	}
	cleared, err := client.GetSession(ctx, fork.ID)
	if err != nil || cleared.Title != "" {
		t.Fatalf("cleared title = %q, error=%v", cleared.Title, err)
	}
}

func TestResumeRejectsCheckpointWithoutTaskSettingsSnapshot(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	saved, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	runID := client.runtime.checkpointID(saved.ID)
	if err := client.checkpoints.Create(context.Background(), graph.Checkpoint[agentgraph.State]{
		FormatVersion: graph.CheckpointFormatVersion,
		RunID:         runID,
		MachineID:     "lune-agent-v1",
		Revision:      1,
		NextID:        2,
		Invocations: []graph.Invocation[agentgraph.State]{
			{ID: "old-invocation", Node: "model_request", Status: graph.InvocationReady, State: agentgraph.State{RunID: runID}},
		},
	}); err != nil {
		t.Fatalf("create old checkpoint: %v", err)
	}

	if _, err := client.ResumeTask(context.Background(), saved.ID); err == nil || !strings.Contains(err.Error(), "task settings version 0") {
		t.Fatalf("ResumeTask error = %v, want unsupported settings checkpoint error", err)
	}
	if state := client.State(); state.Status != TaskIdle {
		t.Fatalf("task state after rejected resume = %#v, want idle", state)
	}
	if pending, err := client.HasPending(context.Background(), saved.ID); err != nil || !pending {
		t.Fatalf("checkpoint after rejected resume: pending=%v err=%v; want preserved", pending, err)
	}
	state, found, err := client.runtime.PendingStateFor(context.Background(), saved.ID)
	if err != nil || !found || state.TaskSettingsVersion != 0 {
		t.Fatalf("old checkpoint after rejection: found=%v state=%#v err=%v", found, state, err)
	}
}

func TestRewindSessionPreservesWholeTurnsAndRejectsPausedTasks(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	ctx := context.Background()
	history := json.RawMessage(`[
		{"type":"message","role":"user","content":"one"},
		{"type":"message","role":"assistant","content":"answer one"},
		{"type":"message","role":"user","content":"two"},
		{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{}"},
		{"type":"function_call_output","call_id":"call-1","output":"result"},
		{"type":"message","role":"assistant","content":"answer two"},
		{"type":"message","role":"user","content":"three"},
		{"type":"message","role":"assistant","content":"answer three"}
	]`)
	saved, err := client.CreateSession(ctx, history)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := client.RewindSession(ctx, saved.ID, 1)
	if err != nil || removed != 1 {
		t.Fatalf("RewindSession removed=%d, error=%v", removed, err)
	}
	updated, err := client.GetSession(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(updated.History, &items); err != nil {
		t.Fatalf("decode rewound history %s: %v", updated.History, err)
	}
	if len(items) != 6 || items[3]["type"] != "function_call" || items[4]["type"] != "function_call_output" {
		t.Fatalf("rewound history split a user turn or tool pair: %s", updated.History)
	}
	removed, err = client.RewindSession(ctx, saved.ID, 10)
	if err != nil || removed != 2 {
		t.Fatalf("oversized rewind removed=%d, error=%v; want all two remaining turns", removed, err)
	}
	updated, err = client.GetSession(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(updated.History)) != "[]" {
		t.Fatalf("rewind to start left history %s", updated.History)
	}
	if _, err := client.RewindSession(ctx, saved.ID, 0); err == nil {
		t.Fatal("zero rewind should be rejected")
	}

	checkpointID := client.runtime.checkpointID(saved.ID)
	createBareTestCheckpoint(t, client, checkpointID)
	if _, err := client.RewindSession(ctx, saved.ID, 1); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("rewind with pending task error = %v", err)
	}
}

func TestDeleteSessionCascadesPendingTeamCheckpoints(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	saved, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	parentID := client.runtime.checkpointID(saved.ID)
	createBareTestCheckpoint(t, client, parentID)
	teamRun, _, err := client.teamRuns.GetOrCreate(parentID, "call-1", []agentgraph.TeamAssignment{{
		ID: "worker", Model: "primary", Task: "inspect",
	}}, client.models.Profiles())
	if err != nil {
		t.Fatal(err)
	}
	memberCheckpointID := teamRun.ID + ":worker"
	createBareTestCheckpoint(t, client, memberCheckpointID)

	client.mu.Lock()
	client.active = TaskState{Status: TaskRunning, SessionID: saved.ID}
	client.mu.Unlock()
	if err := client.DeleteSession(context.Background(), saved.ID); err == nil {
		t.Fatal("DeleteSession deleted a currently running session")
	}
	client.mu.Lock()
	client.active = TaskState{Status: TaskIdle}
	client.mu.Unlock()

	if err := client.DeleteSession(context.Background(), saved.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := client.GetSession(context.Background(), saved.ID); err == nil {
		t.Fatal("deleted session still exists")
	}
	for _, id := range []string{parentID, memberCheckpointID} {
		if _, err := client.checkpoints.Load(context.Background(), id); !errors.Is(err, checkpoint.ErrNotFound) {
			t.Fatalf("checkpoint %q after delete: %v", id, err)
		}
	}
	if _, err := client.teamRuns.Member(teamRun.ID, "worker"); err == nil {
		t.Fatal("team run remains after deleting its session")
	}
	if err := client.DeleteSession(context.Background(), saved.ID); err == nil {
		t.Fatal("repeat DeleteSession should report the missing session")
	}
}

func createBareTestCheckpoint(t *testing.T, client *Core, runID string) {
	t.Helper()
	if err := client.checkpoints.Create(context.Background(), graph.Checkpoint[agentgraph.State]{
		FormatVersion: graph.CheckpointFormatVersion,
		RunID:         runID,
		MachineID:     "lune-agent-v1",
		Revision:      1,
	}); err != nil {
		t.Fatalf("create checkpoint %q: %v", runID, err)
	}
}
