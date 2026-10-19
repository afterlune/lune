package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

type runtimeStreamReadyMsg struct {
	sessionID string
	events    <-chan core.Event
	err       error
}

type runtimeEventMsg struct {
	sessionID string
	event     core.Event
	batch     []core.Event
	events    <-chan core.Event
}

type turnSubmittedMsg struct {
	sessionID  string
	submission core.TurnSubmission
	err        error
}

type turnFinishedMsg struct {
	sessionID   string
	executionID string
	generation  uint64
	result      core.TurnResult
	err         error
}

type tickMsg time.Time

func (m *tuiModel) subscribeRuntimeEvents() tea.Cmd {
	if m.runtime == nil {
		return nil
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runtimeCancel = cancel
	runtime := m.runtime
	sessionID := m.sessionID
	streamCmd := func() tea.Msg {
		rawEvents, err := runtime.SubscribeEvents(ctx, 0)
		if err != nil {
			return runtimeStreamReadyMsg{sessionID: sessionID, err: err}
		}
		// Relay events through a large 2048-buffer channel so luneCore's 64-element
		// non-blocking live channel never overflows while Bubble Tea renders a frame.
		buffered := make(chan core.Event, 2048)
		go func() {
			defer close(buffered)
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-rawEvents:
					if !ok {
						return
					}
					select {
					case buffered <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return runtimeStreamReadyMsg{sessionID: sessionID, events: buffered}
	}
	return tea.Batch(streamCmd, m.waitForExecution(m.runningExecution))
}

func (m *tuiModel) waitForExecution(executionID string) tea.Cmd {
	if !m.running {
		return nil
	}
	return m.awaitExecutionResult(executionID)
}

func (m *tuiModel) awaitExecutionResult(executionID string) tea.Cmd {
	if m.runtime == nil || executionID == "" || m.questionInFlight(executionID) {
		return nil
	}
	if _, waiting := m.awaitingTurns[executionID]; waiting {
		return nil
	}
	m.waitGeneration++
	m.awaitingTurns[executionID] = m.waitGeneration
	return waitForTurn(m.runtime, m.sessionID, executionID, m.waitGeneration)
}

func nextRuntimeEvent(sessionID string, events <-chan core.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return runtimeEventMsg{sessionID: sessionID, events: nil}
		}
		// Coalesce queued stream events so Bubble Tea renders once per batch
		// instead of re-rendering the full Markdown transcript on every token.
		var batch []core.Event
		if event.Kind == core.EventModelStream {
			for len(batch) < 128 {
				select {
				case nextEv, ok := <-events:
					if !ok {
						return runtimeEventMsg{sessionID: sessionID, event: event, batch: batch, events: events}
					}
					batch = append(batch, nextEv)
					if nextEv.Kind != core.EventModelStream {
						return runtimeEventMsg{sessionID: sessionID, event: event, batch: batch, events: events}
					}
				default:
					return runtimeEventMsg{sessionID: sessionID, event: event, batch: batch, events: events}
				}
			}
		}
		return runtimeEventMsg{sessionID: sessionID, event: event, batch: batch, events: events}
	}
}

func waitForTurn(runtime *core.SessionRuntime, sessionID, executionID string, generation uint64) tea.Cmd {
	if runtime == nil || executionID == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := runtime.WaitTurn(context.Background(), executionID)
		return turnFinishedMsg{sessionID: sessionID, executionID: executionID, generation: generation, result: res, err: err}
	}
}

func tickClock() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// submitTurn initiates a model turn for user input.
func (m *tuiModel) submitTurn(rawInput string) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(rawInput)
	if text == "" {
		return m, nil
	}

	m.promptHistory = append(m.promptHistory, text)
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
	m.shortcutsOpen = false
	m.autocompleteOpen = false

	if strings.TrimSpace(m.sessionTitle) == "" || m.sessionTitle == "<untitled>" {
		if autoTitle := deriveSessionTitleText(text); autoTitle != "" {
			m.sessionTitle = autoTitle
			m.syncTerminalTitle()
			if m.client != nil && m.sessionID != "" {
				client := m.client
				sid := m.sessionID
				go func() {
					_ = client.SetSessionTitle(context.Background(), sid, autoTitle)
				}()
			}
		}
	}

	attachments, augmentedText := ExpandFileMentions(text, m.workspace)
	var attachedLabels []string
	for _, att := range attachments {
		attachedLabels = append(attachedLabels, att.Label)
	}

	input, err := core.ParseTaskInput(augmentedText)
	if err != nil {
		m.addSystemEntry("Failed to parse task input: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	req := core.TurnRequest{
		Input:    input,
		PlanMode: m.planMode,
	}

	runtime := m.runtime
	sessionID := m.sessionID

	if m.running {
		// Queue follow-up turn while active task runs
		m.entries = append(m.entries, TranscriptEntry{
			ID:            fmt.Sprintf("queued-%d", time.Now().UnixNano()),
			Kind:          KindQueued,
			Text:          text,
			AttachedFiles: attachedLabels,
			Timestamp:     time.Now(),
		})
		m.refreshViewportContent()
		m.updateLayout()
		m.viewport.GotoBottom()

		return m, func() tea.Msg {
			ctx := context.Background()
			sub, err := runtime.SubmitTurn(ctx, req, core.QueueInput)
			return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
		}
	}

	// Normal submission when idle
	m.entries = append(m.entries, TranscriptEntry{
		ID:            fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Kind:          KindUser,
		Text:          text,
		AttachedFiles: attachedLabels,
		Timestamp:     time.Now(),
	})
	m.running = true
	m.thinking = false
	m.taskStartedAt = time.Now()
	m.turnStreamStartTime = time.Now()
	m.streamFirstTokenTime = time.Time{}
	m.streamGenerationTokens = 0
	m.currentStreamText = ""
	m.currentReasoningText = ""
	m.reasoningStartTime = time.Time{}
	m.scrolledUp = false
	m.activeQuestion = nil
	m.activeApproval = nil
	m.activePlanReview = nil
	m.effortPicker = nil
	m.activeRecovery = nil
	m.activeToolRecovery = nil
	main := m.ensureAgentMetrics("lead")
	main.Task = compactAgentTask(text)
	main.Status = "Running"
	main.Activity = "Starting"
	main.StartedAt = m.taskStartedAt
	main.Duration = 0
	main.ContextWindow = m.contextWindow
	main.ModelProfile = m.currentModelProfile
	main.StreamStartedAt = time.Time{}
	main.StreamGenerationTokens = 0
	main.TokensPerSecond = 0
	m.updateLayout()
	m.refreshViewportContent()
	m.viewport.GotoBottom()

	return m, tea.Batch(
		tickClock(),
		func() tea.Msg {
			ctx := context.Background()
			sub, err := runtime.SubmitTurn(ctx, req, core.StartIfIdle)
			if err != nil && strings.Contains(err.Error(), "session task is paused") {
				_, _ = runtime.DiscardPaused(ctx)
				sub, err = runtime.SubmitTurn(ctx, req, core.StartIfIdle)
			}
			return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
		},
	)
}

func (m *tuiModel) flushReasoningEntry() {
	if m.currentReasoningText == "" {
		return
	}
	trimmed := strings.TrimSpace(m.currentReasoningText)
	dur := time.Since(m.reasoningStartTime)
	m.currentReasoningText = ""
	m.reasoningStartTime = time.Time{}
	if trimmed == "" {
		return
	}
	// Merge into the trailing reasoning entry if no assistant/tool entry intervened,
	// avoiding fragmented micro-duration "Thought for 8ms" blocks.
	if len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindReasoning {
		last := &m.entries[len(m.entries)-1]
		if last.Text != "" && !strings.HasSuffix(last.Text, "\n") {
			last.Text += "\n"
		}
		last.Text += trimmed
		last.ReasoningDuration += dur
		m.invalidateEntryCache()
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		Kind:              KindReasoning,
		Text:              trimmed,
		ReasoningDuration: dur,
		ReasoningExpanded: false,
		Timestamp:         time.Now(),
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) flushStreamTextEntry() {
	if m.currentStreamText == "" {
		return
	}
	text := stripProposedPlan(m.currentStreamText)
	m.currentStreamText = ""
	if strings.TrimSpace(text) == "" {
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID:          fmt.Sprintf("asst-%d", time.Now().UnixNano()),
		ExecutionID: m.runningExecution,
		Kind:        KindAssistant,
		Text:        text,
		Timestamp:   time.Now(),
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) finalizeTurnOutput(proposedPlan string, fallbackResponse json.RawMessage, executionID string) {
	m.flushReasoningEntry()
	m.markRunningSubagentsAndToolsDone()

	text := m.currentStreamText
	m.currentStreamText = ""

	if len(fallbackResponse) > 0 {
		if responseText := extractResponseText(fallbackResponse); strings.TrimSpace(responseText) != "" {
			text = responseText
			if executionID != "" {
				stableID := assistantOutputID(executionID)
				for i := len(m.entries) - 1; i >= 0; i-- {
					if m.entries[i].Kind == KindUser {
						break
					}
					if m.entries[i].Kind == KindAssistant && m.entries[i].ExecutionID == executionID && m.entries[i].ID != stableID {
						m.entries = append(m.entries[:i], m.entries[i+1:]...)
					}
				}
			}
		}
	}

	// The proposed plan is authoritative only when the Core returns one complete
	// <proposed_plan>...</proposed_plan> block. Never infer a plan from a dangling
	// or literal tag in the assistant text: that used to turn ordinary replies
	// (including greetings that merely mention the tag) into empty plan reviews.
	proposedPlan = cleanProposedPlanTags(proposedPlan)
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind != KindAssistant || !strings.Contains(strings.ToLower(m.entries[i].Text), "proposed_plan") {
			continue
		}
		m.entries[i].Text = stripProposedPlan(m.entries[i].Text)
		if strings.TrimSpace(m.entries[i].Text) == "" {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
		}
		m.invalidateEntryCache()
	}

	text = stripProposedPlan(text)

	var turnDur time.Duration
	assistantIndex := m.assistantEntryIndex(executionID)
	if assistantIndex >= 0 {
		turnDur = m.entries[assistantIndex].TurnDuration
	} else if !m.taskStartedAt.IsZero() {
		turnDur = time.Since(m.taskStartedAt)
	}

	if strings.TrimSpace(text) != "" {
		if assistantIndex >= 0 {
			m.entries[assistantIndex].Text = text
		} else {
			id := fmt.Sprintf("asst-%d", time.Now().UnixNano())
			if executionID != "" {
				id = assistantOutputID(executionID)
			}
			m.entries = append(m.entries, TranscriptEntry{
				ID: id, ExecutionID: executionID, Kind: KindAssistant, Text: text,
				TurnDuration: turnDur, Timestamp: time.Now(),
			})
		}
		m.invalidateEntryCache()
	} else if turnDur >= time.Second && len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindAssistant && m.entries[len(m.entries)-1].TurnDuration == 0 {
		m.entries[len(m.entries)-1].TurnDuration = turnDur
		m.invalidateEntryCache()
	}

	if strings.TrimSpace(proposedPlan) != "" {
		hasPlanEntry := len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindPlan && m.entries[len(m.entries)-1].Text == proposedPlan
		if !hasPlanEntry {
			m.entries = append(m.entries, TranscriptEntry{
				ID:        fmt.Sprintf("plan-%d", time.Now().UnixNano()),
				Kind:      KindPlan,
				Text:      proposedPlan,
				Timestamp: time.Now(),
			})
			m.invalidateEntryCache()
		}
		m.activePlanReview = &ActivePlanReview{
			Text:        proposedPlan,
			ExecutionID: executionID,
			Selected:    0,
		}
	}
}

func assistantOutputID(executionID string) string { return "asst-execution-" + executionID }

func (m *tuiModel) assistantEntryIndex(executionID string) int {
	if executionID == "" {
		return -1
	}
	id := assistantOutputID(executionID)
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].ID == id && m.entries[i].Kind == KindAssistant {
			return i
		}
	}
	return -1
}

func (m *tuiModel) markRunningSubagentsAndToolsDone() {
	changed := false
	for i := range m.entries {
		if m.entries[i].Kind == KindTool && m.entries[i].ToolRunning {
			m.entries[i].ToolRunning = false
			changed = true
		}
		if m.entries[i].Kind == KindSubagent && m.entries[i].SubagentRunning {
			m.entries[i].SubagentRunning = false
			changed = true
		}
	}
	for _, stats := range m.agentMetrics {
		if stats.ID != "lead" && stats.Status == "Running" {
			stats.Status = "Completed"
			if !stats.StartedAt.IsZero() {
				stats.Duration = time.Since(stats.StartedAt)
			}
			if stats.Activity == "" || stats.Activity == "Starting" {
				stats.Activity = "Completed"
			}
		}
	}
	if changed {
		m.invalidateEntryCache()
	}
}

func (m *tuiModel) upsertSubagentEntry(agentID, agentName, agentRole string, running bool, duration time.Duration) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || agentID == "lead" {
		return
	}
	displayName := strings.TrimSpace(agentName)
	if displayName == "" {
		displayName = agentID
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			m.entries[i].AgentName = displayName
			if agentRole != "" {
				m.entries[i].AgentRole = agentRole
			}
			m.entries[i].SubagentRunning = running
			if duration > 0 {
				m.entries[i].SubagentDuration = duration
			} else if !running && m.entries[i].SubagentDuration == 0 && !m.entries[i].Timestamp.IsZero() {
				m.entries[i].SubagentDuration = time.Since(m.entries[i].Timestamp)
			}
			if !running && m.entries[i].SubagentPreview == "" {
				m.entries[i].SubagentPreview = "Completed"
			}
			m.invalidateEntryCache()
			return
		}
	}
	preview := "Working..."
	if !running {
		preview = "Completed"
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID:               "subagent-" + agentID,
		Kind:             KindSubagent,
		AgentID:          agentID,
		AgentName:        displayName,
		AgentRole:        agentRole,
		SubagentRunning:  running,
		SubagentPreview:  preview,
		SubagentDuration: duration,
		Timestamp:        time.Now(),
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) activeSubagentsCount() int {
	count := 0
	for i := range m.entries {
		if m.entries[i].Kind == KindSubagent && m.entries[i].SubagentRunning {
			count++
		}
	}
	return count
}

func (m *tuiModel) totalSubagentsCount() int {
	count := 0
	for i := range m.entries {
		if m.entries[i].Kind == KindSubagent {
			count++
		}
	}
	return count
}

func (m *tuiModel) getSubagentEntries() []TranscriptEntry {
	var subs []TranscriptEntry
	for _, e := range m.entries {
		if e.Kind == KindSubagent {
			subs = append(subs, e)
		}
	}
	return subs
}

func (m *tuiModel) subagentsStatusSummary() string {
	active := m.activeSubagentsCount()
	total := m.totalSubagentsCount()
	if total == 0 {
		return ""
	}
	if active > 0 {
		if active == 1 {
			return "1 subagent running"
		}
		return fmt.Sprintf("%d subagents running", active)
	}
	if total == 1 {
		return "1 subagent"
	}
	return fmt.Sprintf("%d subagents", total)
}

func (m *tuiModel) recordSubagentToolStarted(agentID, agentName, agentRole, toolName, toolArgs string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || agentID == "lead" {
		return
	}
	displayName := strings.TrimSpace(agentName)
	if displayName == "" {
		displayName = agentID
	}
	preview := fmt.Sprintf("Running %s...", toolName)
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			m.entries[i].SubagentRunning = true
			m.entries[i].SubagentTools++
			m.entries[i].SubagentLastTool = toolName
			m.entries[i].SubagentPreview = preview
			if displayName != "" && displayName != agentID {
				m.entries[i].AgentName = displayName
			}
			if agentRole != "" {
				m.entries[i].AgentRole = agentRole
			}
			m.transcriptDirty = true
			m.invalidateEntryCache()
			return
		}
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID:               "subagent-" + agentID,
		Kind:             KindSubagent,
		AgentID:          agentID,
		AgentName:        displayName,
		AgentRole:        agentRole,
		SubagentRunning:  true,
		SubagentTools:    1,
		SubagentLastTool: toolName,
		SubagentPreview:  preview,
		Timestamp:        time.Now(),
	})
	m.transcriptDirty = true
	m.invalidateEntryCache()
}

func (m *tuiModel) recordSubagentToolFinished(agentID, toolName string, dur time.Duration) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || agentID == "lead" {
		return
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			if dur > 0 {
				m.entries[i].SubagentDuration += dur
			}
			m.entries[i].SubagentPreview = fmt.Sprintf("Finished %s", toolName)
			m.transcriptDirty = true
			m.invalidateEntryCache()
			return
		}
	}
}

func (m *tuiModel) upsertSubagentStream(event core.Event) {
	agentID := strings.TrimSpace(event.AgentID)
	if agentID == "" || agentID == "lead" || event.Stream == nil {
		return
	}
	delta := event.Stream.Delta
	if delta == "" {
		return
	}
	displayName := strings.TrimSpace(event.AgentName)
	if displayName == "" {
		displayName = agentID
	}
	idx := -1
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.entries = append(m.entries, TranscriptEntry{
			ID:              "subagent-" + agentID,
			Kind:            KindSubagent,
			AgentID:         agentID,
			AgentName:       displayName,
			AgentRole:       event.AgentRole,
			SubagentRunning: true,
			Timestamp:       time.Now(),
		})
		idx = len(m.entries) - 1
	}
	entry := &m.entries[idx]
	entry.SubagentRunning = true
	if event.AgentName != "" {
		entry.AgentName = event.AgentName
	}
	if event.AgentRole != "" {
		entry.AgentRole = event.AgentRole
	}
	entry.Text += delta
	// Extract the latest non-empty line from entry.Text as a clean single-line status preview
	lines := strings.Split(strings.TrimSpace(entry.Text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "```") {
			entry.SubagentPreview = line
			break
		}
	}
	m.invalidateEntryCache()
}

// handleRuntimeEvent processes incoming runtime events from Core.
func (m *tuiModel) handleRuntimeEvent(event core.Event) (tea.Model, tea.Cmd) {
	mod, cmd := m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)
	if m.transcriptDirty || m.cachedDirty {
		m.refreshViewportContent()
	}
	return m, cmd
}

func (m *tuiModel) processRuntimeEventNoRefresh(event core.Event) (tea.Model, tea.Cmd) {
	switch event.Kind {
	case core.EventTaskStarted:
		execID := event.ExecutionID
		if execID == "" {
			return m, nil
		}
		queued := len(m.queuedTurns) > 0 && m.queuedTurns[0].ExecutionID == execID
		if queued {
			m.queuedTurns = m.queuedTurns[1:]
			for i := range m.entries {
				if m.entries[i].Kind == KindQueued && (m.entries[i].ToolCallID == execID || m.entries[i].ToolCallID == "") {
					m.entries[i].Kind = KindUser
					m.entries[i].ToolCallID = execID
					m.invalidateEntryCache()
					break
				}
			}
			m.currentStreamText = ""
			m.currentReasoningText = ""
		}
		if !queued && ((!m.running && m.runningExecution != execID) || (m.runningExecution != "" && m.runningExecution != execID)) {
			return m, nil
		}
		if m.runningExecution == execID && m.taskSnapshot.TaskID == event.TaskID && m.running {
			return m, m.waitForExecution(execID)
		}
		limits := core.ExecutionOptions{}
		if m.client != nil {
			limits = m.client.ExecutionDefaults()
		}
		nextTask := core.TaskState{TaskID: event.TaskID, ExecutionID: execID, Status: core.TaskRunning, Execution: limits}
		if m.runtime != nil {
			if state, err := m.runtime.State(context.Background()); err == nil && state.Task.ExecutionID == execID {
				nextTask = state.Task
				if state.Task.TaskID != event.TaskID || state.Task.Status != core.TaskRunning {
					m.restoreRuntimeSnapshot(state)
					m.checkPendingState(context.Background())
					switch state.Task.Status {
					case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
						m.runningExecution = execID
						return m, m.awaitExecutionResult(execID)
					}
					return m, m.waitForExecution(m.runningExecution)
				}
			}
		}
		if m.taskSnapshot.TaskID != "" && m.taskSnapshot.TaskID != event.TaskID {
			delete(m.awaitingTurns, execID)
			delete(m.terminalEvents, execID)
		}
		m.taskSnapshot = nextTask
		m.running = true
		m.runningExecution = execID
		m.taskStartedAt = time.Now()
		main := m.ensureAgentMetrics("lead")
		main.Status = "Running"
		main.Activity = "Starting"
		main.StartedAt = m.taskStartedAt
		main.Duration = 0
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindUser {
				main.Task = compactAgentTask(m.entries[i].Text)
				break
			}
		}
		if event.ContextWindow > 0 {
			main.ContextWindow = event.ContextWindow
			m.contextWindow = event.ContextWindow
		}
		if event.ModelProfile != "" {
			main.ModelProfile = event.ModelProfile
			m.currentModelProfile = event.ModelProfile
		}
		if event.Model != "" {
			main.Model = event.Model
		}
		m.thinking = false
		m.transcriptDirty = true
		m.updateLayout()
		return m, m.waitForExecution(execID)

	case core.EventModelStream:
		if event.Stream == nil {
			return m, nil
		}
		// Ignore internal background stream purposes (e.g. memory extraction, compaction summary).
		purpose := strings.TrimSpace(event.Stream.Purpose)
		if purpose != "" && purpose != string(core.UsagePurposeMain) && purpose != string(core.UsagePurposeTeam) {
			return m, nil
		}
		m.recordAgentStream(event)
		// Isolate concurrent subagent streams from the lead assistant stream!
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			m.upsertSubagentStream(event)
			return m, nil
		}

		switch event.Stream.Type {
		case "reasoning_delta":
			if m.reasoningStartTime.IsZero() {
				m.reasoningStartTime = time.Now()
			}
			if m.streamFirstTokenTime.IsZero() {
				m.streamFirstTokenTime = time.Now()
				m.streamGenerationTokens = 0
			}
			m.thinking = true
			m.currentReasoningText += event.Stream.Delta
			if event.Stream.Delta != "" {
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.streamGenerationTokens += toks
				elapsed := time.Since(m.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(m.streamGenerationTokens) / elapsed
				}
			}
			m.transcriptDirty = true

		case "text_delta", "":
			if m.currentReasoningText != "" {
				m.flushReasoningEntry()
			}
			if m.streamFirstTokenTime.IsZero() {
				m.streamFirstTokenTime = time.Now()
				m.streamGenerationTokens = 0
			}
			if event.Stream.Delta != "" {
				m.thinking = false
				m.currentStreamText += event.Stream.Delta
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.streamGenerationTokens += toks
				elapsed := time.Since(m.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(m.streamGenerationTokens) / elapsed
				}
				m.transcriptDirty = true
			}
		}

	case core.EventAgentStarted:
		m.updateAgentEvent(event, true)
		m.upsertSubagentEntry(event.AgentID, event.AgentName, event.AgentRole, true, 0)

	case core.EventAgentFinished:
		m.updateAgentEvent(event, false)
		m.upsertSubagentEntry(event.AgentID, event.AgentName, event.AgentRole, false, 0)

	case core.EventToolStarted:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			m.recordAgentToolStarted(agentID, event.Tool.Name)
			m.recordSubagentToolStarted(agentID, event.AgentName, event.AgentRole, event.Tool.Name, string(event.Tool.Arguments))
			return m, nil
		}
		m.streamFirstTokenTime = time.Time{}
		m.streamGenerationTokens = 0
		m.recordAgentToolStarted("lead", event.Tool.Name)
		m.flushReasoningEntry()
		m.flushStreamTextEntry()
		m.thinking = false

		rawArgs := string(event.Tool.Arguments)
		cmdText := formatToolSummary(event.Tool.Name, rawArgs, m.workspace)
		outText := ""
		var changes []publictool.FileChange
		if event.Tool.Details != nil {
			outText = event.Tool.Details.Output
			changes = event.Tool.Details.Changes
			if event.Tool.Details.Command != nil && strings.TrimSpace(event.Tool.Details.Command.Command.Command) != "" {
				cmdText = event.Tool.Details.Command.Command.Command
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
		}
		callID := event.Tool.ID
		// Deduplicate if this tool call is already present in m.entries (e.g., from session history replay).
		if callID != "" {
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && m.entries[i].ToolCallID == callID {
					m.entries[i].ToolName = event.Tool.Name
					if rawArgs != "" {
						m.entries[i].ToolArgs = rawArgs
					}
					if cmdText != "" {
						m.entries[i].ToolCommand = cmdText
					}
					if outText != "" {
						m.entries[i].ToolOutput = outText
					}
					if len(changes) > 0 {
						m.entries[i].ToolChanges = changes
					}
					if m.running {
						m.entries[i].ToolRunning = true
					}
					m.invalidateEntryCache()
					return m, nil
				}
			}
		}
		entry := TranscriptEntry{
			ID:          callID,
			Kind:        KindTool,
			ToolCallID:  callID,
			ToolName:    event.Tool.Name,
			ToolArgs:    rawArgs,
			Timestamp:   time.Now(),
			ToolCommand: cmdText,
			ToolRunning: m.running,
			ToolChanges: changes,
			ToolOutput:  outText,
		}
		m.entries = append(m.entries, entry)
		m.invalidateEntryCache()

	case core.EventToolDetails:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			return m, nil
		}
		if event.Tool.Details != nil {
			outText := event.Tool.Details.Output
			cmdOverride := ""
			if event.Tool.Details.Command != nil {
				cmdOverride = strings.TrimSpace(event.Tool.Details.Command.Command.Command)
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
			targetID := event.Tool.ID
			if targetID == "" {
				targetID = event.CommandID
			}
			found := false
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && ((targetID != "" && m.entries[i].ToolCallID == targetID) || (targetID == "" && m.entries[i].ToolName == event.Tool.Name)) {
					if cmdOverride != "" {
						m.entries[i].ToolCommand = cmdOverride
					}
					if outText != "" {
						m.entries[i].ToolOutput = outText
					}
					if len(event.Tool.Details.Changes) > 0 {
						m.entries[i].ToolChanges = event.Tool.Details.Changes
					}
					if event.CommandExitCode != nil {
						m.entries[i].ToolExitCode = event.CommandExitCode
					}
					m.invalidateEntryCache()
					found = true
					break
				}
			}
			if !found {
				cmdText := cmdOverride
				if cmdText == "" {
					cmdText = formatToolSummary(event.Tool.Name, string(event.Tool.Arguments), m.workspace)
				}
				m.entries = append(m.entries, TranscriptEntry{
					ID:           targetID,
					Kind:         KindTool,
					ToolCallID:   targetID,
					ToolName:     event.Tool.Name,
					ToolCommand:  cmdText,
					ToolOutput:   outText,
					ToolChanges:  event.Tool.Details.Changes,
					ToolExitCode: event.CommandExitCode,
					ToolRunning:  m.running,
					Timestamp:    time.Now(),
				})
				m.invalidateEntryCache()
			}
		}

	case core.EventToolFinished:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			errText := event.Err
			if errText == "" && (event.Tool.Failed || event.Tool.Canceled || event.CommandStatus == core.CommandFailed || event.CommandStatus == core.CommandTimedOut) {
				errText = "tool failed"
			}
			m.recordAgentToolFinished(agentID, event.Tool.Name, errText)
			m.recordSubagentToolFinished(agentID, event.Tool.Name, event.Tool.Duration)
			return m, nil
		}
		isFailed := event.Tool.Failed ||
			event.Tool.Canceled ||
			event.CommandStatus == core.CommandFailed ||
			event.CommandStatus == core.CommandTimedOut ||
			(event.CommandExitCode != nil && *event.CommandExitCode != 0)
		errText := event.Err
		if errText == "" && isFailed {
			errText = "tool failed"
		}
		m.recordAgentToolFinished("lead", event.Tool.Name, errText)
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindTool && (m.entries[i].ToolCallID == event.Tool.ID || (event.Tool.ID == "" && m.entries[i].ToolName == event.Tool.Name)) {
				m.entries[i].ToolRunning = false
				m.entries[i].ToolFailed = isFailed
				m.entries[i].ToolStatus = event.CommandStatus
				if event.CommandExitCode != nil {
					m.entries[i].ToolExitCode = event.CommandExitCode
				}
				if event.Tool.Duration > 0 {
					m.entries[i].ToolDuration = event.Tool.Duration
				}
				if event.Tool.Details != nil {
					if event.Tool.Details.Output != "" {
						m.entries[i].ToolOutput = event.Tool.Details.Output
					} else if event.Tool.Details.Command != nil && event.Tool.Details.Command.Output != "" {
						m.entries[i].ToolOutput = event.Tool.Details.Command.Output
					}
					if len(event.Tool.Details.Changes) > 0 {
						m.entries[i].ToolChanges = event.Tool.Details.Changes
					}
				}
				m.invalidateEntryCache()
				break
			}
		}

	case core.EventContextCompaction:
		if event.Compaction != nil && m.running {
			switch event.Compaction.Stage {
			case "completed":
				if event.Compaction.BeforeTokens > 0 && event.Compaction.AfterTokens > 0 {
					m.addSystemEntry(fmt.Sprintf("Context compacted: %d → %d tokens.", event.Compaction.BeforeTokens, event.Compaction.AfterTokens))
				}
			case "failed":
				if event.Compaction.Err != "" {
					m.addSystemEntry("Context compaction failed: " + event.Compaction.Err)
				}
			}
		}

	case core.EventUserQuestionRequired:
		if m.staleTaskAttempt(event.ExecutionID, event.TaskID) {
			return m, nil
		}
		if event.UserQuestion != nil {
			request := *event.UserQuestion
			if request.ExecutionID == "" {
				request.ExecutionID = event.ExecutionID
			}
			if m.questionSubmissions[m.questionKey(request)] != 0 {
				return m, nil
			}
		}
		m.checkPendingState(context.Background())
		if m.activeQuestion != nil && !m.activeQuestion.Submitting {
			m.thinking = false
			m.running = false
		}
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventUserInteractionRequired:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventApprovalRequired:
		m.thinking = false
		if event.Approval != nil && m.permissionMode == core.PermissionFullAccess {
			_ = m.runtime.ResolveApproval(event.Approval.ID, true)
		} else {
			m.checkPendingState(context.Background())
			m.transcriptDirty = true
			m.updateLayout()
		}

	case core.EventApprovalCancelled:
		if m.activeApproval != nil && m.activeApproval.Request.ID == event.ApprovalID {
			m.activeApproval = nil
			m.updateLayout()
		}

	case core.EventUsage:
		if m.running || !m.seededSessionUsage {
			m.totalInputTokens += event.Usage.InputTokens
			m.totalOutputTokens += event.Usage.OutputTokens
			m.cachedTokens += event.Usage.InputTokensDetails.CachedTokens
			m.reasoningTokens += event.Usage.OutputTokensDetails.ReasoningTokens
			if strings.TrimSpace(event.AgentID) != "" || isLeadMainUsage(event.AgentID, event.UsagePurpose) {
				m.recordAgentUsage(event)
			}
			if isLeadMainUsage(event.AgentID, event.UsagePurpose) && event.Usage.OutputTokens > 0 {
				var elapsed float64
				if !m.streamFirstTokenTime.IsZero() {
					elapsed = time.Since(m.streamFirstTokenTime).Seconds()
				} else if !m.turnStreamStartTime.IsZero() {
					elapsed = time.Since(m.turnStreamStartTime).Seconds()
				}
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(event.Usage.OutputTokens) / elapsed
				}
				m.streamFirstTokenTime = time.Time{}
			}
		}
		if isLeadMainUsage(event.AgentID, event.UsagePurpose) {
			if event.ContextWindow > 0 {
				m.contextWindow = event.ContextWindow
			}
			m.lastInputTokens = event.Usage.InputTokens
			m.estimatedInputTokens = event.EstimatedInputTokens
		}

	case core.EventModelRecoveryRequired:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()
		if m.activeRecovery != nil && !m.activeRecovery.RetryNotBefore.IsZero() {
			return m, tickClock()
		}

	case core.EventToolRecoveryRequired, core.EventExecutionBlocked:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventRuntimeStreamFailed:
		m.thinking = false
		m.running = false
		if event.Err != "" {
			m.addSystemEntry("Runtime stream error: " + event.Err)
		}
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventModelRecoveryResumed:
		m.activeRecovery = nil
		m.pausedCheckpoint = nil
		m.running = true
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventTaskFinished:
		m.finishTurnEvent(event)
		return m, m.waitForExecution(event.ExecutionID)

	case core.EventTaskDiscarded:
		m.thinking = false
		m.running = false
		main := m.ensureAgentMetrics("lead")
		main.Status = "Cancelled"
		main.Activity = "Cancelled"
		if !main.StartedAt.IsZero() {
			main.Duration = time.Since(main.StartedAt)
		}
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.currentStreamText = ""
		m.currentReasoningText = ""
		m.markRunningSubagentsAndToolsDone()
		m.transcriptDirty = true
		m.updateLayout()
	}

	return m, nil
}

func isLeadMainUsage(agentID string, purpose core.UsagePurpose) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID != "" && agentID != "lead" {
		return false
	}
	return purpose == "" || purpose == core.UsagePurposeMain
}

func latestLeadUsageRecord(records []core.UsageRecord) (core.UsageRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if isLeadMainUsage(record.AgentID, record.Purpose) {
			return record, true
		}
	}
	return core.UsageRecord{}, false
}

// formatToolSummary parses raw JSON tool arguments into a concise human-readable command summary.
func formatToolSummary(toolName, rawArgs, workspace string) string {
	name := strings.TrimSpace(toolName)
	argsText := strings.TrimSpace(rawArgs)
	if argsText == "" || argsText == "{}" || argsText == "null" {
		return name
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsText), &args); err != nil {
		return cleanToolInline(name + " " + argsText)
	}

	strField := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	intField := func(keys ...string) (int, bool) {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				switch n := v.(type) {
				case float64:
					return int(n), true
				case int:
					return n, true
				}
			}
		}
		return 0, false
	}

	relPath := func(p string) string {
		if p == "" {
			return ""
		}
		if workspace != "" && filepath.IsAbs(p) {
			if rel, err := filepath.Rel(workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.ToSlash(p)
	}

	switch strings.ToLower(name) {
	case "command", "run_command":
		if cmd := strField("command", "cmd"); cmd != "" {
			return cleanToolInline(cmd)
		}
	case "read_file":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			start, okStart := intField("start_line", "offset", "startLine")
			end, okEnd := intField("end_line", "endLine")
			limit, okLimit := intField("limit")
			if okStart && okEnd && end >= start && start > 0 {
				return fmt.Sprintf("Read %s (L%d-%d)", p, start, end)
			}
			if okStart && okLimit && limit > 0 && start > 0 {
				return fmt.Sprintf("Read %s (L%d-%d)", p, start, start+limit-1)
			}
			return "Read " + p
		}
	case "write_file":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			return "Write " + p
		}
	case "edit_file", "replace_file_content":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			return "Edit " + p
		}
	case "list_dir":
		p := relPath(strField("path", "dir", "directory"))
		if p == "" {
			p = "."
		}
		return "List " + p
	case "glob", "find_files":
		pat := strField("pattern", "glob", "query")
		p := relPath(strField("path", "dir"))
		if pat != "" && p != "" && p != "." {
			return fmt.Sprintf("Find %s in %s", pat, p)
		}
		if pat != "" {
			return "Find " + pat
		}
	case "grep", "search_files":
		pat := strField("pattern", "query", "regex")
		p := relPath(strField("path", "dir", "include"))
		if pat != "" && p != "" && p != "." {
			return fmt.Sprintf("Search %q in %s", pat, p)
		}
		if pat != "" {
			return fmt.Sprintf("Search %q", pat)
		}
	case "web_search":
		if q := strField("query", "q"); q != "" {
			return fmt.Sprintf("Web search %q", q)
		}
	case "web_fetch":
		if rawURL := strField("url"); rawURL != "" {
			if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
				return "Fetch " + u.Host + u.Path
			}
			return "Fetch " + rawURL
		}
	case "delegate_team":
		if tasks, ok := args["tasks"].([]any); ok && len(tasks) > 0 {
			return fmt.Sprintf("Delegate %d subagent(s)", len(tasks))
		}
		return "Delegate subagent team"
	}

	return cleanToolInline(name + " " + argsText)
}

func cleanToolInline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}

// estimateDeltaTokens estimates token count from an incremental stream delta.
func estimateDeltaTokens(delta string) int64 {
	if delta == "" {
		return 0
	}
	var count float64
	for _, r := range delta {
		if r >= 0x4e00 && r <= 0x9fff || r >= 0x3400 && r <= 0x4dbf || r >= 0x20000 && r <= 0x2a6df {
			// CJK characters in modern LLMs average 1.3 to 1.5 tokens
			count += 1.4
		} else if r > 127 {
			count += 1.0
		} else {
			count += 0.25
		}
	}
	toks := int64(math.Ceil(count))
	if toks < 1 {
		toks = 1
	}
	return toks
}
