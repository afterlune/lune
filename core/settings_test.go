package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/session"
)

func writeSettingsConfig(t *testing.T, workspace, endpoint string) {
	t.Helper()
	config := model.Config{
		Providers: map[string]model.ProviderConfig{
			"provider-a": {Protocol: model.ProtocolResponses, BaseURL: endpoint, APIKey: "key-a"},
			"provider-b": {Protocol: model.ProtocolResponses, BaseURL: endpoint, APIKey: "key-b"},
		},
		Models: map[string]model.ModelProfile{
			"primary": {Provider: "provider-a", Model: "primary-wire-model"},
			"review":  {Provider: "provider-b", Model: "review-wire-model"},
		},
		DefaultModel: "primary",
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, model.ConfigFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func openSettingsCore(t *testing.T, workspace, stateDir string) *Core {
	t.Helper()
	client, err := New(Config{Workspace: workspace, StateDir: stateDir})
	if err != nil {
		t.Fatalf("New Core: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})
	return client
}

func TestSessionModelSettingsPersistAndDefault(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)

	session, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.Settings.ModelProfile != "primary" || session.Settings.ReasoningEffort != "" {
		t.Fatalf("new session settings = %#v", session.Settings)
	}
	settings := SessionSettings{ModelProfile: "review", ReasoningEffort: model.ReasoningEffortHigh}
	if err := client.SetSessionSettings(context.Background(), session.ID, settings); err != nil {
		t.Fatalf("SetSessionSettings: %v", err)
	}
	if err := client.SetSessionSettings(context.Background(), session.ID, SessionSettings{ModelProfile: "missing", ReasoningEffort: "high"}); err == nil {
		t.Fatal("unknown profile should be rejected")
	}
	if err := client.SetSessionSettings(context.Background(), session.ID, SessionSettings{ModelProfile: "primary", ReasoningEffort: "ultra"}); err == nil {
		t.Fatal("unknown effort should be rejected")
	}
	profiles := client.ListModelProfiles()
	if len(profiles) != 2 || profiles[0].Profile != "primary" || profiles[1].Profile != "review" {
		t.Fatalf("model profiles = %#v, want sorted profiles", profiles)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close Core before reopen: %v", err)
	}

	reopened := openSettingsCore(t, workspace, stateDir)
	restored, err := reopened.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if restored.Settings != settings {
		t.Fatalf("restored settings = %#v, want %#v", restored.Settings, settings)
	}
	newSession, err := reopened.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatalf("CreateSession after reopen: %v", err)
	}
	if newSession.Settings.ModelProfile != "primary" || newSession.Settings.ReasoningEffort != "" {
		t.Fatalf("new session defaults = %#v", newSession.Settings)
	}
}

func TestLegacySessionWithoutSettingsGetsDefaultProfile(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	store, err := session.Open(filepath.Join(stateDir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), session.Snapshot{
		ID: "legacy-session", Workspace: workspace, Data: json.RawMessage(`{"history":null}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	client := openSettingsCore(t, workspace, stateDir)
	restored, err := client.GetSession(context.Background(), "legacy-session")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if restored.Settings.ModelProfile != "primary" || restored.Settings.ReasoningEffort != "" {
		t.Fatalf("legacy session settings = %#v", restored.Settings)
	}
}

func TestPausedTaskResumeUsesCheckpointModelAndEffort(t *testing.T) {
	observed := make(chan model.ModelRequest, 2)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		observed <- request
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		writeCompletedResponseSSE(w, request.Model)
	}))
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	saved, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	initial := SessionSettings{ModelProfile: "primary", ReasoningEffort: model.ReasoningEffortLow}
	if err := client.SetSessionSettings(context.Background(), saved.ID, initial); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	input := mustSettingsTaskInput(t, "pause me")
	go func() {
		_, runErr := client.RunTask(ctx, TaskRequest{SessionID: saved.ID, Input: input})
		firstDone <- runErr
	}()
	select {
	case first := <-observed:
		if first.Model != "primary-wire-model" || first.Reasoning == nil || first.Reasoning.Effort != model.ReasoningEffortLow {
			cancel()
			t.Fatalf("initial request = %#v", first)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("initial request was not observed")
	}
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted task error = %v, want context canceled", err)
	}
	if pending, err := client.HasPending(context.Background(), saved.ID); err != nil || !pending {
		t.Fatalf("pending after interruption = %v, error = %v", pending, err)
	}
	if err := client.SetSessionSettings(context.Background(), saved.ID, SessionSettings{
		ModelProfile: "review", ReasoningEffort: model.ReasoningEffortHigh,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResumeTask(context.Background(), saved.ID); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	select {
	case resumed := <-observed:
		if resumed.Model != "primary-wire-model" || resumed.Reasoning == nil || resumed.Reasoning.Effort != model.ReasoningEffortLow {
			t.Fatalf("resumed request = %#v; want pinned original settings", resumed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resumed request was not observed")
	}
	current, err := client.GetSession(context.Background(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Settings.ModelProfile != "review" || current.Settings.ReasoningEffort != model.ReasoningEffortHigh {
		t.Fatalf("session settings after resume = %#v", current.Settings)
	}
}

func TestDefaultReasoningEffortIsOmittedFromModelRequest(t *testing.T) {
	observed := make(chan model.ModelRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		observed <- request
		writeCompletedResponseSSE(w, request.Model)
	}))
	defer server.Close()
	workspace, stateDir := t.TempDir(), t.TempDir()
	writeSettingsConfig(t, workspace, server.URL)
	client := openSettingsCore(t, workspace, stateDir)
	saved, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	input := mustSettingsTaskInput(t, "use the provider default")
	if _, err := client.RunTask(context.Background(), TaskRequest{SessionID: saved.ID, Input: input}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	request := <-observed
	if request.Model != "primary-wire-model" || request.Reasoning != nil {
		t.Fatalf("default request = %#v, want configured model and no reasoning override", request)
	}
}

func mustSettingsTaskInput(t *testing.T, text string) TaskInput {
	t.Helper()
	input, err := NewTaskInput(text)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func writeCompletedResponseSSE(w http.ResponseWriter, modelName string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"done\",\"object\":\"response\",\"status\":\"completed\",\"model\":%q,\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n", modelName)
}
