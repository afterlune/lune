package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
	"github.com/charmbracelet/colorprofile"
)

func TestTUITeamActivityGroupsAndUpdatesMemberState(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, event := range []core.Event{
		{Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-1", AgentID: "research", AgentName: "Research", AgentRole: "worker", ModelProfile: "review", Provider: "deepseek", Model: "deepseek-worker", Status: core.TaskRunning},
		{Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-1", AgentID: "writer", AgentName: "Writer", AgentRole: "worker", ModelProfile: "text", Provider: "anthropic", Model: "claude-writer", Status: core.TaskRunning},
	} {
		ui.Update(coreEventMsg{event: event})
	}
	details := tuiScreenshotANSI.ReplaceAllString(ui.activityDetailsContent(), "")
	for _, expected := range []string{"Team 1", "Research · running", "profile=review", "deepseek/deepseek-worker", "Writer · running", "anthropic/claude-writer"} {
		if !strings.Contains(details, expected) {
			t.Fatalf("team details missing %q: %q", expected, details)
		}
	}
	if strings.Contains(details, "Team 2") {
		t.Fatalf("members from one team run were split into multiple groups: %q", details)
	}

	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentFinished, SessionID: ui.sessionID, TeamRunID: "team-run-1",
		AgentID: "research", AgentName: "Research", AgentRole: "worker", Status: core.TaskCompleted,
	}})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentStarted, SessionID: "other-session", TeamRunID: "team-run-2",
		AgentID: "ignored", AgentName: "Ignored", AgentRole: "worker", Status: core.TaskRunning,
	}})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-2",
		AgentID: "second", AgentName: "Second team", AgentRole: "worker", Status: core.TaskRunning,
	}})
	details = tuiScreenshotANSI.ReplaceAllString(ui.activityDetailsContent(), "")
	if !strings.Contains(details, "Research · done") || !strings.Contains(details, "Team 2") || !strings.Contains(details, "Second team · running") || strings.Contains(details, "Ignored") {
		t.Fatalf("team status update/session filtering failed: %q", details)
	}
}

func TestTUISessionWorkspaceSVGsMatchDeterministicFixtures(t *testing.T) {
	scenarios := []string{"empty", "short", "long", "activity-top", "activity-bottom", "usage-session-top", "usage-session-bottom", "usage-estimated", "narrow", "streaming", "interrupted", "resumed"}
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, scenario := range scenarios {
			theme, scenario := theme, scenario
			name := fmt.Sprintf("%s-%s.svg", theme, scenario)
			t.Run(name, func(t *testing.T) {
				ui := newSessionWorkspaceFixture(theme, scenario)
				got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
				assertTUIScreenshotSVG(t, name, got)
			})
		}
	}
}

func newSessionWorkspaceFixture(theme themeName, scenario string) *tuiModel {
	width, height := 120, 30
	if scenario == "narrow" {
		width, height = 80, 24
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = theme
	ui.colorProfile = colorprofile.TrueColor
	ui.sessionID = "session-current"
	ui.workspace = "D:/work/project"
	ui.sessionList = workspaceFixtureSessions()
	ui.sessionIndex = 0
	applyTextareaTheme(&ui.input, theme)
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})

	switch scenario {
	case "short":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "The merge order is clear and predictable."},
		}
		ui.activities = workspaceFixtureActivities(5)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "long":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "I reviewed defaults and project settings across the config flow.\nThe environment overrides are applied after file values.\nProvider fields and optional settings are validated before use.\nThe longer response fills the chat viewport without moving the session rail.\nActivity and Usage open in the bottom drawer."},
		}
		ui.activities = workspaceFixtureActivities(5)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "activity-top", "activity-bottom", "usage-session-top", "usage-session-bottom", "usage-estimated":
		ui.entries = []tuiEntry{{role: "you", text: "Review the recent activity."}, {role: "assistant", text: "Tool events stay in order in the work log."}}
		ui.activities = workspaceFixtureActivities(28)
		ui.teamActivities = workspaceFixtureTeamActivities()
		ui.sessionUsage = measuredWorkspaceUsage()
		ui.usageRequests = workspaceFixtureUsageRequests()
		if scenario == "usage-session-top" || scenario == "usage-session-bottom" {
			ui.sessionUsage = tokenUsageTotals{
				requests: 247, measured: 246, input: 41000, output: 8500, reasoning: 1000, total: 49500, cached: 6000,
			}
			ui.agentUsage = map[string]agentUsageSummary{
				"lead":     {requests: 205, measured: 204, input: 32000, output: 8000, reasoning: 1000, total: 40000, cached: 6000},
				"research": {requests: 42, measured: 42, input: 9000, output: 500, total: 9500},
			}
			ui.agentUsageComplete = true
		}
		ui.contextWindow = 64000
		ui.contextInput = 840
		if scenario == "usage-estimated" {
			ui.contextInput = 12300
			ui.contextEstimated = true
			ui.usageRequests = []tokenUsageRecord{{
				request: 5, usage: core.Usage{OutputTokens: 600, TotalTokens: 600}, available: true,
				agentID: "lead", profile: "lead", provider: "deepseek", model: "deepseek-main",
				contextWindow: 64000, estimatedInputTokens: 12300,
			}}
			ui.agentUsage = map[string]agentUsageSummary{
				"lead": {requests: 5, measured: 5, estimatedInput: 12300, output: 2400, total: 48000},
			}
			ui.sessionUsage = tokenUsageTotals{requests: 5, measured: 5, estimatedInput: 12300, output: 2400, total: 48000}
			ui.taskUsage = tokenUsageTotals{requests: 1, measured: 1, estimatedInput: 12300, output: 600, total: 12000}
		}
		ui.detailsOpen = true
	case "narrow":
		ui.entries = []tuiEntry{
			{role: "you", text: "Review the current task."},
			{role: "assistant", text: "The session rail becomes a compact header switcher."},
		}
		ui.activities = workspaceFixtureActivities(8)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "streaming", "interrupted", "resumed":
		ui.running = true
		ui.status = "Working… Press Esc to interrupt."
		ui.entries = []tuiEntry{{role: "you", text: "Explain the active provider stream."}}
		ui.Update(coreEventMsg{event: core.Event{
			Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-stream", Status: core.TaskRunning,
		}})
		ui.Update(coreEventMsg{event: core.Event{
			Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-stream", AgentID: "lead",
			Stream: &core.ModelStreamEvent{Type: "text_delta", ItemID: "response-stream", Delta: "The response appears as text deltas arrive, keeping the workspace layout steady."},
		}})
		ui.flushModelStreamEvents()
		if scenario == "interrupted" {
			ui.finishModelStream("", false)
			ui.running = false
			ui.status = "Task paused. Use /continue to resume or type a new message to branch."
		} else if scenario == "resumed" {
			ui.finishModelStream("", false)
			ui.running = true
			ui.status = "Working… Press Esc to interrupt."
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-resumed", Status: core.TaskRunning,
			}})
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-resumed", AgentID: "lead",
				Stream: &core.ModelStreamEvent{Type: "text_delta", ItemID: "response-resumed", Delta: "The full answer appears after resuming the interrupted turn."},
			}})
			ui.flushModelStreamEvents()
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-resumed", AgentID: "lead",
				Stream: &core.ModelStreamEvent{Type: "completed", ItemID: "response-resumed"},
			}})
			ui.flushModelStreamEvents()
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventTaskFinished, SessionID: ui.sessionID, TaskID: "task-resumed", Status: core.TaskCompleted,
			}})
			ui.finishModelStream("The full answer appears after resuming the interrupted turn.", true)
			ui.running = false
			ui.status = "Completed."
		}
	case "empty":
	default:
		panic("unknown session workspace fixture: " + scenario)
	}

	if ui.modelStreamFirstTokenRecorded {
		ui.modelStreamFirstTokenLatency = 240 * time.Millisecond
	}
	ui.updateLayout()
	ui.refreshPanels()
	switch scenario {
	case "activity-top":
		ui.activity.GotoTop()
	case "activity-bottom", "usage-session-bottom":
		ui.activity.GotoBottom()
	case "usage-session-top":
		ui.activity.GotoTop()
	case "usage-estimated":
		ui.activity.GotoBottom()
	}
	return ui
}

func workspaceFixtureSessions() []core.Session {
	return []core.Session{
		{ID: "session-current", Title: "Provider configuration"},
		{ID: "session-search", Title: "Add search index"},
		{ID: "session-tests", Title: "Fix CI failure"},
		{ID: "session-docs", Title: "Polish documentation"},
	}
}

func workspaceFixtureActivities(count int) []tuiActivity {
	activities := make([]tuiActivity, count)
	for index := range activities {
		status := "done"
		if index == count-1 {
			status = "running"
		}
		activities[index] = tuiActivity{
			callID:  fmt.Sprintf("call-%02d", index+1),
			summary: fmt.Sprintf("Reading internal/config/section-%02d/provider-settings.go", index+1),
			status:  status,
		}
	}
	return activities
}

func workspaceFixtureTeamActivities() []tuiTeamActivity {
	return []tuiTeamActivity{
		{teamRunID: "fixture-team-1", agentID: "research", name: "Research", profile: "review", provider: "deepseek", model: "deepseek-worker", status: "done"},
		{teamRunID: "fixture-team-1", agentID: "reviewer", name: "Reviewer", profile: "review", provider: "anthropic", model: "claude-review", status: "running"},
	}
}

func measuredWorkspaceUsage() tokenUsageTotals {
	return tokenUsageTotals{requests: 4, measured: 4, input: 1200, output: 300, reasoning: 600, total: 2100, cached: 400}
}

func workspaceFixtureUsageRequests() []tokenUsageRecord {
	return []tokenUsageRecord{
		{
			request: 3, available: true, agentID: "lead", profile: "lead", provider: "deepseek", model: "deepseek-main",
			usage: core.Usage{InputTokens: 840, OutputTokens: 210, TotalTokens: 1050, InputTokensDetails: core.TokenInputDetails{CachedTokens: 240}},
		},
		{
			request: 4, available: true, agentID: "research", profile: "review", provider: "anthropic", model: "claude-review",
			usage: core.Usage{InputTokens: 360, OutputTokens: 90, TotalTokens: 450, InputTokensDetails: core.TokenInputDetails{CachedTokens: 160}},
		},
	}
}

func plainTUIView(ui *tuiModel) string {
	var history []string
	for _, entry := range ui.entries {
		if strings.TrimSpace(entry.text) == "" {
			continue
		}
		prefix := ""
		if entry.incomplete {
			prefix = "Partial · "
		} else if entry.role == "you" {
			prefix = "You · "
		}
		history = append(history, prefix+plainConversationText(entry.text))
	}
	return tuiScreenshotANSI.ReplaceAllString(ui.View().Content, "") + "\n" + strings.Join(history, "\n")
}

func jsonNull() json.RawMessage { return json.RawMessage("null") }
