package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	publictool "github.com/afterlune/luneCore/tool"
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
	m.run.running = true
	m.usage.seededSessionUsage = true
	m.usage.lastTokensPerSec = 42
	m.agentMetrics["lead"] = &agentRuntimeMetrics{ID: "lead", Name: "Main agent", Status: "Running"}
	m.agentMetrics["lead/worker"] = &agentRuntimeMetrics{ID: "lead/worker", Name: "worker", Role: "worker", Status: "Running", ContextWindow: 128000}
	m.processRuntimeEventNoRefresh(core.Event{
		Kind: core.EventUsage, AgentID: "lead/worker", UsagePurpose: core.UsagePurposeTeam,
		ContextWindow: 128000,
		Usage:         core.Usage{InputTokens: 900, InputTokensDetails: core.TokenInputDetails{CachedTokens: 300}, OutputTokens: 100, TotalTokens: 1000},
	})
	if m.usage.lastTokensPerSec != 42 {
		t.Fatalf("worker usage replaced main speed: got %v", m.usage.lastTokensPerSec)
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
	if m.modals.agents.open {
		t.Fatal("agents panel remained open after escape")
	}
	for _, command := range builtinCommands {
		if command.Name == "/subagents" {
			t.Fatal("/subagents still duplicates /agents")
		}
	}
}

func TestAgentContextWindowUsesProfileAndInheritedProjectCapacity(t *testing.T) {
	project := core.DefaultProjectConfig()
	project.Models = publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{
			"host": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "test"},
		},
		Profiles: map[string]publicmodel.ModelProfile{
			"lead":      {Provider: "host", Model: "lead-model"},
			"inherited": {Provider: "host", Model: "worker-model"},
			"small":     {Provider: "host", Model: "small-model", ContextWindow: 128000},
		},
		DefaultProfile: "lead",
	}
	project.Runtime.ContextWindow = 1_000_000
	workspace := t.TempDir()
	client, err := core.New(core.Config{Workspace: workspace, ProjectConfig: &project, Memory: core.MemoryConfig{Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})

	m := newTUIModel(client, nil, "")
	m.currentModelProfile = "lead"
	m.updateAgentEvent(core.Event{AgentID: "lead/inherited", ModelProfile: "inherited"}, true)
	if got := m.agentMetric("lead/inherited").ContextWindow; got != 1_000_000 {
		t.Fatalf("inherited worker context window = %d, want project capacity 1000000", got)
	}
	m.updateAgentEvent(core.Event{AgentID: "lead/profile", ModelProfile: "small"}, true)
	if got := m.agentMetric("lead/profile").ContextWindow; got != 128000 {
		t.Fatalf("profile context window = %d, want 128000", got)
	}
	m.updateAgentEvent(core.Event{AgentID: "lead/reported", ModelProfile: "inherited", ContextWindow: 256000}, true)
	if got := m.agentMetric("lead/reported").ContextWindow; got != 256000 {
		t.Fatalf("reported context window = %d, want 256000", got)
	}

	m.updateAgentEvent(core.Event{AgentID: "lead/reprofiled", ModelProfile: "small"}, true)
	m.updateAgentEvent(core.Event{AgentID: "lead/reprofiled", ModelProfile: "inherited"}, true)
	if got := m.agentMetric("lead/reprofiled").ContextWindow; got != 1_000_000 {
		t.Fatalf("context window after profile change = %d, want inherited project capacity 1000000", got)
	}
}

func TestRemovedSlashAliasesAreUnknown(t *testing.T) {
	removed := []string{"/reasoning", "/delete", "/quit", "/process"}
	for _, alias := range removed {
		for _, item := range builtinCommands {
			if item.Name == alias {
				t.Fatalf("removed alias %q remains in command help/autocomplete", alias)
			}
		}
	}
	for _, canonical := range []string{"/effort", "/drop", "/exit", "/ps"} {
		found := false
		for _, item := range builtinCommands {
			found = found || item.Name == canonical
		}
		if !found {
			t.Fatalf("canonical command %q is missing", canonical)
		}
	}

	m := newTUIModel(nil, nil, "session")
	for _, alias := range removed {
		before := len(m.entries)
		m.handleSlashCommand(alias)
		if len(m.entries) <= before || !strings.Contains(m.entries[len(m.entries)-1].Text, "Unknown command") {
			t.Errorf("alias %q did not produce an unknown-command response", alias)
		}
	}
}

func TestMultiAgentStreamIsolation(t *testing.T) {
	m := newTUIModel(nil, nil, "multi-agent-sess")
	m.run.running = true

	// 1. Subagent starts and streams reasoning + text
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentStarted,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		AgentRole: "TUI Auditor",
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventModelStream,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Subagent secret thought",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventModelStream,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Subagent report line 1\nSubagent report line 2",
		},
	})

	// Lead buffers must remain untouched by subagent events
	if m.run.currentReasoningText != "" {
		t.Fatalf("expected lead currentReasoningText to be empty, got %q", m.run.currentReasoningText)
	}
	if m.run.currentStreamText != "" {
		t.Fatalf("expected lead currentStreamText to be empty, got %q", m.run.currentStreamText)
	}

	// 2. Background memory stream is also ignored by lead stream
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Purpose: string(core.UsagePurposeCompaction),
			Type:    "text_delta",
			Delta:   "internal memory json",
		},
	})
	if m.run.currentStreamText != "" {
		t.Fatalf("expected memory purpose delta to be ignored, got %q", m.run.currentStreamText)
	}

	// 3. Lead agent streams its own reasoning and text
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Lead thought part 1. ",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Lead thought part 2.",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Final lead response.",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentFinished,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
	})
	m.finalizeTurnOutput("", nil, "exec-1")

	var subagentEntries, reasoningEntries, assistantEntries []TranscriptEntry
	for _, e := range m.entries {
		switch e.Kind {
		case KindSubagent:
			subagentEntries = append(subagentEntries, e)
		case KindReasoning:
			reasoningEntries = append(reasoningEntries, e)
		case KindAssistant:
			assistantEntries = append(assistantEntries, e)
		}
	}

	if len(subagentEntries) != 1 {
		t.Fatalf("expected 1 subagent entry, got %d", len(subagentEntries))
	}
	if subagentEntries[0].SubagentRunning {
		t.Errorf("expected subagent entry to be marked finished")
	}
	if subagentEntries[0].SubagentPreview != "Subagent report line 2" {
		t.Errorf("unexpected subagent preview: %q", subagentEntries[0].SubagentPreview)
	}
	if len(reasoningEntries) != 1 || reasoningEntries[0].Text != "Lead thought part 1. Lead thought part 2." {
		t.Errorf("expected 1 merged lead reasoning entry, got %+v", reasoningEntries)
	}
	if len(assistantEntries) != 1 || assistantEntries[0].Text != "Final lead response." {
		t.Errorf("expected 1 clean lead assistant entry, got %+v", assistantEntries)
	}
}

func TestExecutionTimelineAlignment(t *testing.T) {
	// 1. File change card: "• Added script_helper.py (+3 -0)" with line-numbered diff preview and "+ Show details"
	addedEntry := TranscriptEntry{
		Kind:     KindTool,
		ToolName: "write_file",
		ToolChanges: []publictool.FileChange{
			{
				Path:  "script_helper.py",
				Added: 3,
				Lines: []publictool.DiffLine{
					{Kind: "add", NewLine: 1, Text: "from pathlib import Path"},
					{Kind: "add", NewLine: 2, Text: "import re"},
					{Kind: "add", NewLine: 3, Text: "p=Path('internal/tui/view.go')"},
				},
			},
		},
	}
	renderedAdded := ansi.Strip(renderEntry(addedEntry, 100))
	if !strings.Contains(renderedAdded, "• Added script_helper.py (+3 -0)") {
		t.Fatalf("expected Added file header, got:\n%s", renderedAdded)
	}
	if !strings.Contains(renderedAdded, "1 +from pathlib import Path") || !strings.Contains(renderedAdded, "+ Show details") {
		t.Fatalf("expected inline diff preview and + Show details, got:\n%s", renderedAdded)
	}

	// 2. Command tail preview: shows the LAST 3 lines of output (e.g. FAIL summary / traceback)
	exitOne := 1
	failedEntry := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "go test ./internal/tui",
		ToolOutput:   "line 1 setup\nline 2 compile\nline 3 check\ninternal\\tui\\view.go:83:6: too many errors\nFAIL\tgithub.com/afterlune/lune/internal/tui [build failed]\nFAIL",
		ToolExitCode: &exitOne,
	}
	renderedFailed := ansi.Strip(renderEntry(failedEntry, 100))
	if !strings.Contains(renderedFailed, "Failed (exit 1)") {
		t.Fatalf("expected Failed (exit 1), got:\n%s", renderedFailed)
	}
	if strings.Contains(renderedFailed, "line 1 setup") {
		t.Fatalf("expected collapsed preview to show tail lines rather than line 1, got:\n%s", renderedFailed)
	}
	if !strings.Contains(renderedFailed, "[build failed]") || !strings.Contains(renderedFailed, "+ 3 lines (click to expand") {
		t.Fatalf("expected tail error lines and expand hint, got:\n%s", renderedFailed)
	}

	// 3. Borderless horizontal-rule Markdown table
	mdTable := "| 领域 | 功能 |\n| --- | --- |\n| 会话 | 多会话并行、历史搜索 |\n| 对话 | 流式回复、消息排队 |"
	renderedTable := ansi.Strip(renderMarkdown(mdTable, 80))
	if strings.Contains(renderedTable, "╭") || strings.Contains(renderedTable, "│") {
		t.Fatalf("expected borderless horizontal-rule table without vertical box borders, got:\n%s", renderedTable)
	}
	if !strings.Contains(renderedTable, "领域") || !strings.Contains(renderedTable, "────") || !strings.Contains(renderedTable, "多会话并行") {
		t.Fatalf("expected table headers, horizontal dividers, and cell text, got:\n%s", renderedTable)
	}
}

func TestSubagentTrackingAndCommands(t *testing.T) {
	m := &tuiModel{
		run: runState{running: true, taskStartedAt: time.Now().Add(-10 * time.Second)},
	}

	// 1. Subagent tool event routing: subagent tools do not mix into lead agent tools
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentStarted,
		AgentID:   "researcher-1",
		AgentName: "code_researcher",
		AgentRole: "Codebase Analyst",
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventToolStarted,
		AgentID:   "researcher-1",
		AgentName: "code_researcher",
		Tool: core.ToolCall{
			ID:        "call-1",
			Name:      "read_file",
			Arguments: []byte(`{"path":"internal/tui/theme.go"}`),
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventToolFinished,
		AgentID: "researcher-1",
		Tool: core.ToolCall{
			ID:       "call-1",
			Name:     "read_file",
			Duration: 250 * time.Millisecond,
		},
	})

	// Check that no KindTool entries were added to lead entries
	for _, e := range m.entries {
		if e.Kind == KindTool {
			t.Fatalf("expected no KindTool in lead entries from subagent event, found: %+v", e)
		}
	}

	// Check subagent entry updated properly
	subs := m.getSubagentEntries()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subagent entry, got %d", len(subs))
	}
	if subs[0].SubagentTools != 1 || subs[0].SubagentLastTool != "read_file" {
		t.Fatalf("unexpected subagent tools: count=%d, last=%s", subs[0].SubagentTools, subs[0].SubagentLastTool)
	}

	// 2. Subagent summary in Working indicator
	summary := m.subagentsStatusSummary()
	if !strings.Contains(summary, "1 subagent running") {
		t.Fatalf("expected '1 subagent running', got %q", summary)
	}
	workingBlock := renderWorking(10*time.Second, false, summary)
	if !strings.Contains(workingBlock, "1 subagent running") {
		t.Fatalf("expected workingBlock to contain subagent summary, got:\n%s", workingBlock)
	}

	// 3. Subagent badge in footer
	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "1 active") || !strings.Contains(footer, "/agents") {
		t.Fatalf("expected footer to show active subagent count and /agents hint, got:\n%s", footer)
	}

	// 4. /agents opens the interactive list with the main agent and worker.
	m.recordAgentUsage(core.Event{AgentID: "researcher-1", ContextWindow: 64000, Usage: core.Usage{
		InputTokens: 1000, InputTokensDetails: core.TokenInputDetails{CachedTokens: 400}, OutputTokens: 200, TotalTokens: 1200,
	}})
	_, _ = m.handleAgentsCommand([]string{"/agents"})
	if !m.modals.agents.open {
		t.Fatal("/agents did not open the interactive panel")
	}
	panel := ansi.Strip(m.renderAgentsPanel(120))
	if !strings.Contains(panel, "Main agent") || !strings.Contains(panel, "code_researcher") || !strings.Contains(panel, "cache 40%") || !strings.Contains(panel, "total 1,200") {
		t.Fatalf("agents list missing live agent statistics:\n%s", panel)
	}

	// 5. Keyboard navigation opens the selected worker detail.
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	detail := ansi.Strip(m.renderAgentsPanel(120))
	if !m.modals.agents.detail || !strings.Contains(detail, "Agent details · code_researcher") || !strings.Contains(strings.ToLower(detail), "tools: 1") {
		t.Fatalf("expected interactive agent details, got:\n%s", detail)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.modals.agents.open {
		t.Fatal("escape did not close the agents panel")
	}
}

func TestFooterSubagentsAlwaysVisible(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "agents 0 active · disabled") {
		t.Fatalf("expected footer to show disabled when idle, got:\n%s", footer)
	}
}

func TestFooterSubagentCountReportsActiveAgents(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.entries = []TranscriptEntry{
		{Kind: KindSubagent, AgentID: "a", AgentName: "researcher", SubagentRunning: false},
		{Kind: KindSubagent, AgentID: "b", AgentName: "reviewer", SubagentRunning: true},
	}
	m.width = 140
	footer := ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "agents 1 active · disabled") {
		t.Fatalf("expected footer to retain the active count with delegation disabled, got:\n%s", footer)
	}
	if strings.Contains(footer, "2 active") {
		t.Fatalf("completed agents must not be counted as active, got:\n%s", footer)
	}
}

func TestFooterKeepsSubagentCountVisibleWhenCrowded(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.currentModel = strings.Repeat("LongModelName", 5)
	m.currentModelProfile = m.currentModel
	m.reasoningEffort = "high"
	m.workspace = strings.Repeat("long/workspace/path/", 5)
	m.sessionTitle = strings.Repeat("long session title ", 5)
	m.entries = []TranscriptEntry{{Kind: KindSubagent, AgentID: "active", SubagentRunning: true}}

	footer := ansi.Strip(renderFooter(m, 80))
	if !strings.Contains(footer, "agents 1 · off") {
		t.Fatalf("crowded footer should preserve the compact active/disabled state, got:\n%s", footer)
	}
	firstLine := strings.Split(footer, "\n")[0]
	if width := ansi.StringWidth(firstLine); width > 80 {
		t.Fatalf("footer first line wrapped past terminal width: %d > 80: %q", width, firstLine)
	}
}

func TestSummarizeToolRoundRecognizesAskUserAndSubagents(t *testing.T) {
	entries := []TranscriptEntry{
		{Kind: KindTool, ToolName: "list_dir"},
		{Kind: KindTool, ToolName: "read_file"},
		{Kind: KindTool, ToolName: "grep"},
		{Kind: KindTool, ToolName: "ask_user"},
		{Kind: KindTool, ToolName: "delegate_team"},
	}
	summary := summarizeToolRound(entries)
	if !strings.Contains(summary, "Explored 1 directory, 1 file, 1 search") {
		t.Fatalf("expected file exploration in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "asked 1 question") {
		t.Fatalf("expected 'asked 1 question' in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "ran 1 subagent") {
		t.Fatalf("expected 'ran 1 subagent' in summary, got: %q", summary)
	}
}

func TestSubagentUsageDoesNotReplaceLeadContextMetrics(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.run.running = true
	m.usage.contextWindow = 1000000

	m.handleRuntimeEvent(core.Event{
		Kind: core.EventUsage, AgentID: "lead", UsagePurpose: core.UsagePurposeMain, ContextWindow: 1000000,
		Usage: core.Usage{InputTokens: 42000, OutputTokens: 200},
	})
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventUsage, AgentID: "lead/researcher", UsagePurpose: core.UsagePurposeTeam,
		EstimatedInputTokens: 180000, Usage: core.Usage{InputTokens: 180000, OutputTokens: 1000},
	})
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventUsage, AgentID: "memory", UsagePurpose: core.UsagePurposeCompaction,
		EstimatedInputTokens: 9000, Usage: core.Usage{InputTokens: 9000},
	})

	if m.usage.lastInputTokens != 42000 || m.usage.estimatedInputTokens != 0 || m.usage.contextWindow != 1000000 {
		t.Fatalf("subagent usage changed lead context metrics: input=%d estimated=%d context=%d", m.usage.lastInputTokens, m.usage.estimatedInputTokens, m.usage.contextWindow)
	}
	if m.usage.totalInputTokens != 231000 || m.usage.totalOutputTokens != 1200 {
		t.Fatalf("session totals should include all agent usage: input=%d output=%d", m.usage.totalInputTokens, m.usage.totalOutputTokens)
	}

	m.handleRuntimeEvent(core.Event{
		Kind: core.EventUsage, AgentID: "lead", UsagePurpose: core.UsagePurposeMain, ContextWindow: 1000000,
		EstimatedInputTokens: 55000,
	})
	if m.usage.lastInputTokens != 0 || m.usage.estimatedInputTokens != 55000 {
		t.Fatalf("latest lead estimate should replace prior measured input: input=%d estimated=%d", m.usage.lastInputTokens, m.usage.estimatedInputTokens)
	}
}
