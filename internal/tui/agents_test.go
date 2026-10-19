package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestAgentMetricsTrackPerAgentUsageAndStreamSpeed(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	m.updateAgentEvent(core.Event{
		AgentID: "lead/researcher", AgentName: "researcher", AgentRole: "worker",
		AgentTask: "Inspect the provider boundary", ContextWindow: 64000, ModelProfile: "review", Model: "wire-model",
	}, true)
	m.recordAgentUsage(core.Event{
		AgentID: "lead/researcher", ContextWindow: 64000, ModelProfile: "review",
		Usage: core.Usage{InputTokens: 1000, InputTokensDetails: core.TokenInputDetails{CachedTokens: 250}, OutputTokens: 80, TotalTokens: 1080},
	})
	stats := m.agentMetric("lead/researcher")
	stats.StreamStartedAt = time.Now().Add(-time.Second)
	stats.StreamGenerationTokens = 10
	m.recordAgentStream(core.Event{
		AgentID: "lead/researcher", ModelProfile: "review",
		Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: "more output"},
	})

	if stats.ContextWindow != 64000 || stats.ContextInputTokens != 1000 || stats.TokensPerSecond <= 0 {
		t.Fatalf("agent context/speed = %#v", stats)
	}
	if stats.InputTokens != 1000 || stats.OutputTokens != 80 || stats.CachedTokens != 250 || stats.ToolCount != 0 {
		t.Fatalf("agent usage = %#v", stats)
	}
	if !strings.Contains(agentCacheLabel(stats), "25.0% hit") {
		t.Fatalf("cache hit label = %q", agentCacheLabel(stats))
	}
	if !strings.Contains(agentContextLabel(stats), "64,000") || !strings.Contains(agentSpeedLabel(stats), "tok/s") {
		t.Fatalf("context/speed labels = %q / %q", agentContextLabel(stats), agentSpeedLabel(stats))
	}
}

func TestWorkerUsageDoesNotOverwriteMainSpeedAndAgentsPanelIsInteractive(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	m.running = true
	m.seededSessionUsage = true
	m.lastTokensPerSec = 42
	m.agentMetrics["lead"] = &agentRuntimeMetrics{ID: "lead", Name: "Main agent", Status: "Running"}
	m.agentMetrics["lead/worker"] = &agentRuntimeMetrics{ID: "lead/worker", Name: "worker", Role: "worker", Status: "Running", ContextWindow: 128000}
	m.processRuntimeEventNoRefresh(core.Event{
		Kind: core.EventUsage, AgentID: "lead/worker", UsagePurpose: core.UsagePurposeTeam,
		ContextWindow: 128000,
		Usage:         core.Usage{InputTokens: 900, InputTokensDetails: core.TokenInputDetails{CachedTokens: 300}, OutputTokens: 100, TotalTokens: 1000},
	})
	if m.lastTokensPerSec != 42 {
		t.Fatalf("worker usage replaced main speed: got %v", m.lastTokensPerSec)
	}
	if m.agentMetric("lead/worker").InputTokens != 900 {
		t.Fatalf("worker usage was not recorded: %#v", m.agentMetric("lead/worker"))
	}

	if !m.openAgentsPanel("") {
		t.Fatal("failed to open agents panel")
	}
	list := ansi.Strip(m.renderAgentsPanel(120))
	if !strings.Contains(list, "Main agent") || !strings.Contains(list, "worker") || !strings.Contains(list, "cache 33%") {
		t.Fatalf("agents list omitted agent metrics:\n%s", list)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	detail := ansi.Strip(m.renderAgentsPanel(120))
	if !strings.Contains(detail, "Agent details · worker") || !strings.Contains(detail, "128,000") {
		t.Fatalf("agent details omitted context:\n%s", detail)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.agentsPanelOpen {
		t.Fatal("agents panel remained open after escape")
	}
	for _, command := range builtinCommands {
		if command.Command == "/subagents" {
			t.Fatal("/subagents still duplicates /agents")
		}
	}
}
