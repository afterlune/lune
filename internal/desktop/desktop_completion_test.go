package desktop

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	publictool "github.com/afterlune/luneCore/tool"
	_ "modernc.org/sqlite"
)

func TestPendingRequestViewIncludesApprovalAndModelFailureMetadata(t *testing.T) {
	retryAt := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	approvalView := pendingRequestView(core.PendingRequest{
		Kind: core.PendingApproval,
		Approval: &core.ApprovalRequest{
			ID:               "appr-1",
			Tool:             "run_command",
			Arguments:        json.RawMessage(`{"command":"npm test"}`),
			AutoReviewReason: "Auto-review flagged external test runner",
			Fingerprint:      "fp-run-command-npm-test",
		},
	})
	if approvalView.AutoReviewReason != "Auto-review flagged external test runner" || approvalView.Fingerprint != "fp-run-command-npm-test" {
		t.Fatalf("approvalView = %+v", approvalView)
	}

	recoveryView := pendingRequestView(core.PendingRequest{
		Kind: core.PendingModelRecoveryRequest,
		ModelRecovery: &core.PendingModelRecovery{
			RunID:        "run-1",
			InvocationID: "inv-1",
			CallID:       "call-1",
			ExecutionID:  "exec-1",
			Failure: core.ModelFailure{
				Category:       publicmodel.ErrorRateLimit,
				StatusCode:     429,
				Code:           "rate_limit_exceeded",
				RequestID:      "req-xyz",
				Attempts:       3,
				StreamStarted:  true,
				RetryNotBefore: retryAt,
			},
		},
	})
	if recoveryView.ModelFailure == nil {
		t.Fatal("expected ModelFailure metadata")
	}
	if recoveryView.ModelFailure.Category != "rate_limit" || recoveryView.ModelFailure.StatusCode != 429 ||
		recoveryView.ModelFailure.Code != "rate_limit_exceeded" || recoveryView.ModelFailure.RequestID != "req-xyz" ||
		recoveryView.ModelFailure.Attempts != 3 || !recoveryView.ModelFailure.StreamStarted ||
		recoveryView.ModelFailure.RetryNotBefore != "2026-11-13T12:00:00Z" {
		t.Fatalf("recoveryView.ModelFailure = %+v", recoveryView.ModelFailure)
	}
}

func TestParseApprovalDecisionAcceptsThreeWayValues(t *testing.T) {
	cases := map[string]publictool.ApprovalDecision{
		"allow_once":    publictool.ApprovalAllowOnce,
		"once":          publictool.ApprovalAllowOnce,
		"allow_session": publictool.ApprovalAllowSession,
		"session":       publictool.ApprovalAllowSession,
		"deny":          publictool.ApprovalDeny,
		"":              publictool.ApprovalDeny,
	}
	for input, want := range cases {
		got, err := parseApprovalDecision(input)
		if err != nil || got != want {
			t.Fatalf("parseApprovalDecision(%q) = (%q, %v), want %q", input, got, err, want)
		}
	}
	if _, err := parseApprovalDecision("always"); err == nil {
		t.Fatal("expected error for unsupported approval decision")
	}
}

func TestToAgentEventMapsUsageAndCompaction(t *testing.T) {
	usageEvt := toAgentEvent(core.Event{
		Kind:                 core.EventUsage,
		SessionID:            "sess-1",
		ExecutionID:          "exec-1",
		AgentID:              "agent",
		UsagePurpose:         "agent",
		ContextWindow:        128000,
		EstimatedInputTokens: 4100,
		Usage: core.Usage{
			InputTokens:         4000,
			OutputTokens:        320,
			TotalTokens:         4320,
			InputTokensDetails:  core.TokenInputDetails{CachedTokens: 1200, CacheCreationTokens: 100},
			OutputTokensDetails: core.TokenOutputDetails{ReasoningTokens: 80},
		},
	})
	if usageEvt.Usage == nil || usageEvt.Usage.InputTokens != 4000 || usageEvt.Usage.EstimatedInputTokens != 4100 ||
		usageEvt.Usage.ReasoningTokens != 80 || usageEvt.Usage.CachedTokens != 1200 || usageEvt.Usage.ContextWindow != 128000 {
		t.Fatalf("usageEvt.Usage = %+v", usageEvt.Usage)
	}

	compactionEvt := toAgentEvent(core.Event{
		Kind:      core.EventContextCompaction,
		SessionID: "sess-1",
		Compaction: &core.ContextCompactionDetails{
			Stage:        "completed",
			Mode:         "local",
			Reason:       "manual",
			Strategy:     "summary",
			BeforeTokens: 90000,
			AfterTokens:  18000,
			Threshold:    80000,
			Turns:        6,
		},
	})
	if compactionEvt.Compaction == nil || compactionEvt.Compaction.Stage != "completed" ||
		compactionEvt.Compaction.BeforeTokens != 90000 || compactionEvt.Compaction.AfterTokens != 18000 {
		t.Fatalf("compactionEvt.Compaction = %+v", compactionEvt.Compaction)
	}
}

func TestBuildSessionUsageViewAppliesOverridesAndLastMeasuredRequest(t *testing.T) {
	cw := int64(200000)
	threshold := 0.75
	saved := core.Session{
		Settings: core.SessionSettings{
			ContextWindow:        &cw,
			AutoCompactThreshold: &threshold,
		},
		SessionUsage: core.UsageTotals{
			Requests:  3,
			Measured:  3,
			Input:     12000,
			Output:    1500,
			Reasoning: 400,
			Total:     13500,
			Cached:    3000,
		},
		UsageRequests: []core.UsageRecord{
			{Purpose: "agent", EstimatedInputTokens: 4200, Usage: core.Usage{InputTokens: 4100, OutputTokens: 500, TotalTokens: 4600}},
			{Purpose: "compaction", EstimatedInputTokens: 900, Usage: core.Usage{InputTokens: 880, OutputTokens: 120, TotalTokens: 1000}},
		},
	}
	view := buildSessionUsageView(nil, saved)
	if view.ContextWindow != 200000 || view.AutoCompactThreshold != 0.75 || view.LastInputTokens != 4100 || view.Total != 13500 {
		t.Fatalf("buildSessionUsageView = %+v", view)
	}
}

func TestActivityStoreMigratesAndPersistsMainAgentReasoningOnly(t *testing.T) {
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "desktop-activities.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`
		CREATE TABLE execution_activity_runs (
			session_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			user_message_index INTEGER NOT NULL DEFAULT -1,
			status TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			PRIMARY KEY(session_id, execution_id)
		);
	`); err != nil {
		t.Fatal(err)
	}
	_ = legacyDB.Close()

	store, err := OpenActivityStore(stateDir)
	if err != nil {
		t.Fatalf("OpenActivityStore migration failed: %v", err)
	}
	defer store.Close()

	service := &DesktopService{activityStore: store}
	ctx := context.Background()
	if err := store.UpsertExecution(ctx, "sess-1", "exec-1", nil, "running"); err != nil {
		t.Fatal(err)
	}
	service.persistActivityEvent(core.Event{
		Kind:        core.EventModelStream,
		SessionID:   "sess-1",
		ExecutionID: "exec-1",
		Stream:      &core.ModelStreamEvent{Type: "reasoning_delta", Purpose: "agent", Delta: "First thought. "},
	})
	service.persistActivityEvent(core.Event{
		Kind:        core.EventModelStream,
		SessionID:   "sess-1",
		ExecutionID: "exec-1",
		Stream:      &core.ModelStreamEvent{Type: "reasoning_delta", Purpose: "subagent", Delta: "Ignored helper thought."},
	})
	service.persistActivityEvent(core.Event{
		Kind:        core.EventModelStream,
		SessionID:   "sess-1",
		ExecutionID: "exec-1",
		Stream:      &core.ModelStreamEvent{Type: "text_delta", Purpose: "agent", Delta: "Answer"},
	})
	service.persistActivityEvent(core.Event{
		Kind:        core.EventTaskFinished,
		SessionID:   "sess-1",
		ExecutionID: "exec-1",
		Status:      core.TaskCompleted,
	})

	runs, err := store.ListSession(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ReasoningText != "First thought. " {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestSetSessionEffortAndForkAndExportRenderers(t *testing.T) {
	service, _, sessionID := newDesktopServiceForTest(t, immediateDesktopProvider{})

	if err := service.SetSessionEffort(sessionID, "invalid-effort"); err == nil {
		t.Fatal("expected error for invalid reasoning effort")
	}
	if err := service.SetSessionEffort(sessionID, "HIGH"); err != nil {
		t.Fatalf("SetSessionEffort(HIGH) failed: %v", err)
	}
	refreshed, err := service.RefreshSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Session.Settings.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", refreshed.Session.Settings.ReasoningEffort)
	}
	if err := service.SetSessionEffort(sessionID, "default"); err != nil {
		t.Fatalf("SetSessionEffort(default) failed: %v", err)
	}
	refreshed, err = service.RefreshSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Session.Settings.ReasoningEffort != "" {
		t.Fatalf("ReasoningEffort after default = %q, want empty", refreshed.Session.Settings.ReasoningEffort)
	}

	forked, err := service.ForkSession(sessionID)
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}
	if forked.Session.ID == "" || forked.Session.ID == sessionID {
		t.Fatalf("forked.Session.ID = %q (source %q)", forked.Session.ID, sessionID)
	}

	saved := core.Session{
		ID:        "sess-exp",
		Title:     "Desktop Export Demo",
		UpdatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		Settings:  core.SessionSettings{ModelProfile: "primary", ReasoningEffort: "high"},
	}
	messages := []ConversationMessage{
		{Role: "user", Text: "Explain compaction"},
		{Role: "assistant", Text: "Compaction summarizes older turns."},
	}
	history := []ExecutionActivityView{
		{
			ExecutionID:      "exec-1",
			UserMessageIndex: 0,
			Status:           "completed",
			ReasoningText:    "Checked compaction flow",
			Tools:            []ToolActivityView{{ID: "t1", InvocationID: "t1", Name: "read_file", Status: "completed"}},
		},
	}
	usage := SessionUsageView{Input: 1000, Output: 200, Reasoning: 50, Total: 1200}

	mdBytes, err := renderSessionExport("md", saved, `D:\repo`, messages, history, usage)
	if err != nil || !strings.Contains(string(mdBytes), "# Desktop Export Demo") || !strings.Contains(string(mdBytes), "Checked compaction flow") {
		t.Fatalf("markdown export = (%q, %v)", string(mdBytes), err)
	}
	htmlBytes, err := renderSessionExport("html", saved, `D:\repo`, messages, history, usage)
	if err != nil || !strings.Contains(string(htmlBytes), "<title>Desktop Export Demo</title>") || !strings.Contains(string(htmlBytes), "Checked compaction flow") {
		t.Fatalf("html export = (%q, %v)", string(htmlBytes), err)
	}
	jsonlBytes, err := renderSessionExport("jsonl", saved, `D:\repo`, messages, history, usage)
	if err != nil || !strings.Contains(string(jsonlBytes), `"type":"session"`) || !strings.Contains(string(jsonlBytes), `"reasoningText":"Checked compaction flow"`) {
		t.Fatalf("jsonl export = (%q, %v)", string(jsonlBytes), err)
	}
}
