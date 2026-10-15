package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func TestTUITokenUsageShowsRequestDetailsAndTaskSessionTotals(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.Update(modelUsageMsg{usage: core.Usage{
		InputTokens:         100,
		InputTokensDetails:  core.TokenInputDetails{CachedTokens: 25},
		OutputTokens:        40,
		TotalTokens:         140,
		OutputTokensDetails: core.TokenOutputDetails{ReasoningTokens: 12},
	}})
	ui.Update(modelUsageMsg{usage: core.Usage{
		InputTokens:        50,
		InputTokensDetails: core.TokenInputDetails{CachedTokens: 5},
		OutputTokens:       10,
		TotalTokens:        60,
	}})
	if ui.taskUsage.requests != 2 || ui.taskUsage.input != 150 || ui.taskUsage.output != 50 || ui.taskUsage.reasoning != 12 || ui.taskUsage.total != 200 || ui.taskUsage.cached != 30 {
		t.Fatalf("task usage = %#v", ui.taskUsage)
	}
	view := ui.View().Content
	for _, expected := range []string{"in 150", "out 50", "reason 12", "20.0%"} {
		if !strings.Contains(view, expected) {
			t.Errorf("compact footer missing %q: %q", expected, view)
		}
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"#1 · in 100 · out 40 · reason 12 · total 140 · cached 25",
		"#2 · in 50 · out 10 · reason 0 · total 60 · cached 5",
		"Session usage by agent (all 2 requests)",
		"Unattributed · 2 request(s) · 2 metered · in 150 · out 50 · reason 12 · total 200 · cache 30 (20.0%)",
		"Task · in 150 · out 50 · reason 12 · total 200 · cache 30",
		"Session · in 150 · out 50 · reason 12 · total 200 · cache 30",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details missing %q: %q", expected, details)
		}
	}
	if ui.activity.TotalLineCount() <= ui.activity.Height() {
		t.Fatalf("activity viewport lines=%d height=%d; expected scrollable usage details", ui.activity.TotalLineCount(), ui.activity.Height())
	}

	ui.Update(modelUsageMsg{})
	if !strings.Contains(ui.activityDetailsContent(), "usage unavailable") {
		t.Fatalf("zero usage should be marked unavailable: %q", ui.activityDetailsContent())
	}
	if ui.sessionUsage.requests != 3 || ui.sessionUsage.measured != 2 {
		t.Fatalf("session usage = %#v, want three requests and two metered", ui.sessionUsage)
	}

	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	ui.input.SetValue("next task")
	updated, command := ui.submit()
	if command == nil {
		t.Fatal("next task did not start")
	}
	finished := updated.(*tuiModel)
	if finished.taskUsage.requests != 0 || finished.sessionUsage.requests != 3 || len(finished.usageRequests) != 3 {
		t.Fatalf("task usage=%#v session usage=%#v request details=%d after new task", finished.taskUsage, finished.sessionUsage, len(finished.usageRequests))
	}
	runTUICommands(finished, command)
}

func TestTUIUsageDetailsPreserveAgentAndModelAttribution(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	usage := core.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventUsage, SessionID: ui.sessionID, AgentID: "worker-1",
		ModelProfile: "review", Provider: "deepseek", Model: "deepseek-worker", Usage: usage,
	}})
	current := ui.usageRequests[0].String()
	for _, expected := range []string{"worker-1", "profile=review", "deepseek/deepseek-worker", "in 100", "out 20"} {
		if !strings.Contains(current, expected) {
			t.Fatalf("current usage row missing %q: %q", expected, current)
		}
	}

	ui.restoreSessionState(core.Session{UsageRequests: []core.UsageRecord{
		{
			Request: 7, Usage: usage, Available: true, AgentID: "worker-2", ModelProfile: "text",
			Provider: "anthropic", Model: "claude-worker",
		},
		{Request: 8, Usage: usage, Available: true, AgentID: "lead", ModelProfile: "lead", ContextWindow: 32000},
	}})
	restored := ui.usageRequests[0].String()
	for _, expected := range []string{"#7", "worker-2", "profile=text", "anthropic/claude-worker"} {
		if !strings.Contains(restored, expected) {
			t.Fatalf("restored usage row missing %q: %q", expected, restored)
		}
	}
	if ui.contextWindow != 32000 || ui.contextInput != usage.InputTokens {
		t.Fatalf("restored context usage = input %d / window %d", ui.contextInput, ui.contextWindow)
	}
	if details := ui.activityDetailsContent(); !strings.Contains(details, "Context · ctx 100/32.0k (0%)") {
		t.Fatalf("restored details missing context ratio: %q", details)
	}
}

func TestTUIContextUsageFooterUsesOnlyKnownLimits(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 80, height: 24}, {width: 120, height: 30}} {
		ui := newTUIModel(&fakeResponder{}, false)
		ui.preferences.Theme = themeDark
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		ui.contextWindow = 64000
		ui.contextInput = 12300
		footer := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), "")
		if !strings.Contains(footer, "ctx 12.3k/64.0k (19%)") {
			t.Fatalf("%dx%d footer missing configured context ratio: %q", size.width, size.height, footer)
		}
		if got := lipgloss.Width(ui.renderFooter(size.width)); got > size.width {
			t.Fatalf("%dx%d context footer width = %d", size.width, size.height, got)
		}
		ui.contextWindow = 0
		if got := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), ""); !strings.Contains(got, "ctx 12.3k/unknown") {
			t.Fatalf("%dx%d footer should label unknown window: %q", size.width, size.height, got)
		}
		ui.contextInput = 0
		if got := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), ""); strings.Contains(got, "ctx ") {
			t.Fatalf("%dx%d footer inferred context usage without input/window data: %q", size.width, size.height, got)
		}
	}
}

func TestTUIContextUsageLabelsMissingServerInputAsEstimate(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-estimate"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventUsage, SessionID: ui.sessionID, AgentID: "lead", ContextWindow: 64000,
		EstimatedInputTokens: 1200, Usage: core.Usage{OutputTokens: 6, TotalTokens: 6},
	}})
	if ui.contextInput != 1200 || !ui.contextEstimated || ui.taskUsage.input != 0 || ui.taskUsage.estimatedInput != 1200 {
		t.Fatalf("context input=%d estimated=%v task totals=%#v", ui.contextInput, ui.contextEstimated, ui.taskUsage)
	}
	footer := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(120), "")
	if !strings.Contains(footer, "ctx ~1.2k/64.0k (2%)") {
		t.Fatalf("footer does not identify estimated input: %q", footer)
	}
	details := ui.activityDetailsContent()
	for _, expected := range []string{"Context · ctx ~1.2k/64.0k (2%) · estimate; image contents excluded", "in ~1.2k", "est input ~1.2k"} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details %q missing %q", details, expected)
		}
	}
}

func TestTUIUsageDetailsSummarizeRecentRequestsByAgent(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.usageRequests = []tokenUsageRecord{
		{request: 1, agentID: "lead", available: true, usage: core.Usage{
			InputTokens: 100, OutputTokens: 20, TotalTokens: 120, InputTokensDetails: core.TokenInputDetails{CachedTokens: 20},
		}},
		{request: 2, agentID: "research", available: true, usage: core.Usage{
			InputTokens: 50, OutputTokens: 10, TotalTokens: 60, InputTokensDetails: core.TokenInputDetails{CachedTokens: 5},
		}},
		{request: 3, agentID: "research", available: false},
		{request: 4, available: false},
	}
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"Recent usage by agent (4 requests)",
		"Unattributed · 1 request(s) · 0 metered · usage unavailable",
		"lead · 1 request(s) · 1 metered · in 100 · out 20 · reason 0 · total 120 · cache 20 (20.0%)",
		"research · 2 request(s) · 1 metered · in 50 · out 10 · reason 0 · total 60 · cache 5 (10.0%)",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details missing %q: %q", expected, details)
		}
	}
}

func TestTUIUsageDetailsUsePersistedSessionAgentTotals(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.restoreSessionState(core.Session{
		UsageRequests:      []core.UsageRecord{{Request: 206, AgentID: "lead", Available: true, Usage: core.Usage{InputTokens: 1, TotalTokens: 1}}},
		AgentUsageComplete: true,
		AgentUsage: map[string]core.UsageTotals{
			"lead":     {Requests: 205, Measured: 204, Input: 32000, EstimatedInput: 1200, Output: 8000, Reasoning: 1000, Total: 40000, Cached: 6000},
			"research": {Requests: 42, Measured: 42, Input: 9000, Output: 500, Total: 9500, Cached: 0},
		},
		SessionUsage: core.UsageTotals{Requests: 247},
	})
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"Session usage by agent (all 247 requests)",
		"lead · 205 request(s) · 204 metered · est input ~1.2k · in 32.0k · out 8.0k · reason 1.0k · total 40.0k · cache 6.0k (18.8%)",
		"research · 42 request(s) · 42 metered · in 9.0k · out 500 · reason 0 · total 9.5k · cache 0 (0.0%)",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("session usage details missing %q: %q", expected, details)
		}
	}

	ui.agentUsageComplete = false
	if details := ui.usageByAgentDetails(); !strings.Contains(details, "older requests may be missing") {
		t.Fatalf("incomplete legacy totals lack a coverage note: %q", details)
	}
	ui.agentUsage = nil
	ui.usageRequests = nil
	ui.sessionUsage.requests = 5
	if details := ui.usageByAgentDetails(); !strings.Contains(details, "No retained requests are available") {
		t.Fatalf("legacy totals without retained details lack an explanation: %q", details)
	}
}
