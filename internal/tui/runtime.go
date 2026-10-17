package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type runtimeStreamReadyMsg struct {
	sessionID string
	events    <-chan core.Event
	err       error
}

type runtimeEventMsg struct {
	sessionID string
	event     core.Event
	events    <-chan core.Event
}

type turnSubmittedMsg struct {
	sessionID  string
	submission core.TurnSubmission
	err        error
}

type turnFinishedMsg struct {
	sessionID string
	result    core.TurnResult
	err       error
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
	return func() tea.Msg {
		events, err := runtime.SubscribeEvents(ctx, 0)
		return runtimeStreamReadyMsg{sessionID: sessionID, events: events, err: err}
	}
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
		return runtimeEventMsg{sessionID: sessionID, event: event, events: events}
	}
}

func waitForTurn(runtime *core.SessionRuntime, sessionID, executionID string) tea.Cmd {
	if runtime == nil || executionID == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := runtime.WaitTurn(context.Background(), executionID)
		return turnFinishedMsg{sessionID: sessionID, result: res, err: err}
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

	// Add user entry to transcript and prompt history
	m.entries = append(m.entries, TranscriptEntry{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Kind:      KindUser,
		Text:      text,
		Timestamp: time.Now(),
	})
	m.promptHistory = append(m.promptHistory, text)
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""

	m.running = true
	m.thinking = false
	m.taskStartedAt = time.Now()
	m.currentStreamText = ""
	m.streamActive = true
	m.scrolledUp = false
	m.activeQuestion = nil
	m.activeApproval = nil
	m.activePlanReview = nil
	m.shortcutsOpen = false
	m.autocompleteOpen = false
	m.updateLayout()
	m.refreshViewportContent()
	m.viewport.GotoBottom()

	input, err := core.ParseTaskInput(text)
	if err != nil {
		m.addSystemEntry("Failed to parse task input: " + err.Error())
		m.running = false
		m.updateLayout()
		return m, nil
	}
	req := core.TurnRequest{
		Input:    input,
		PlanMode: m.planMode,
	}

	runtime := m.runtime
	sessionID := m.sessionID
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

// handleRuntimeEvent processes incoming runtime events from Core.
func (m *tuiModel) handleRuntimeEvent(event core.Event) (tea.Model, tea.Cmd) {
	switch event.Kind {
	case core.EventModelStream:
		if event.Stream != nil {
			switch event.Stream.Type {
			case "reasoning_delta":
				m.thinking = true
				m.refreshViewportContent()
			case "text_delta", "":
				if event.Stream.Delta != "" {
					m.thinking = false
					m.currentStreamText += event.Stream.Delta
					m.refreshViewportContent()
				}
			}
		}

	case core.EventToolStarted:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		m.thinking = false
		cmdText := event.Tool.Name
		outText := ""
		if event.Tool.Details != nil {
			outText = event.Tool.Details.Output
			if event.Tool.Details.Command != nil {
				cmdText = event.Tool.Details.Command.Command.Command
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
		}
		entry := TranscriptEntry{
			ID:          event.Tool.ID,
			Kind:        KindTool,
			ToolCallID:  event.Tool.ID,
			ToolName:    event.Tool.Name,
			ToolArgs:    string(event.Tool.Arguments),
			Timestamp:   time.Now(),
			ToolCommand: cmdText,
			ToolOutput:  outText,
		}
		m.entries = append(m.entries, entry)
		m.refreshViewportContent()

	case core.EventToolDetails:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		if event.Tool.Details != nil {
			cmdText := event.Tool.Name
			outText := event.Tool.Details.Output
			if event.Tool.Details.Command != nil {
				cmdText = event.Tool.Details.Command.Command.Command
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
			found := false
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && (m.entries[i].ToolCallID == event.CommandID || m.entries[i].ToolName == event.Tool.Name) {
					m.entries[i].ToolCommand = cmdText
					m.entries[i].ToolOutput = outText
					m.entries[i].ToolExitCode = event.CommandExitCode
					found = true
					break
				}
			}
			if !found {
				m.entries = append(m.entries, TranscriptEntry{
					ID:           event.CommandID,
					Kind:         KindTool,
					ToolCommand:  cmdText,
					ToolOutput:   outText,
					ToolExitCode: event.CommandExitCode,
					Timestamp:    time.Now(),
				})
			}
			m.refreshViewportContent()
		}

	case core.EventToolFinished:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindTool && m.entries[i].ToolCallID == event.Tool.ID {
				m.entries[i].ToolStatus = event.CommandStatus
				m.entries[i].ToolExitCode = event.CommandExitCode
				break
			}
		}
		m.refreshViewportContent()

	case core.EventUserQuestionRequired:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.refreshViewportContent()
		m.updateLayout()

	case core.EventApprovalRequired:
		m.thinking = false
		if event.Approval != nil && m.permissionMode == core.PermissionFullAccess {
			_ = m.runtime.ResolveApproval(event.Approval.ID, true)
		} else {
			m.checkPendingState(context.Background())
			m.refreshViewportContent()
			m.updateLayout()
		}

	case core.EventApprovalCancelled:
		if m.activeApproval != nil && m.activeApproval.Request.ID == event.ApprovalID {
			m.activeApproval = nil
			m.updateLayout()
		}

	case core.EventTaskFinished:
		if m.runningExecution != "" && event.ExecutionID != "" && event.ExecutionID != m.runningExecution {
			return m, nil
		}
		m.thinking = false
		if m.currentStreamText != "" {
			m.entries = append(m.entries, TranscriptEntry{
				ID:        fmt.Sprintf("asst-%d", time.Now().UnixNano()),
				Kind:      KindAssistant,
				Text:      m.currentStreamText,
				Timestamp: time.Now(),
			})
			m.currentStreamText = ""
		}
		m.running = false
		if event.Status == core.TaskPaused {
			m.checkPendingState(context.Background())
		}
		m.refreshViewportContent()
		m.updateLayout()

	case core.EventTaskDiscarded:
		m.thinking = false
		m.running = false
		m.currentStreamText = ""
		m.refreshViewportContent()
		m.updateLayout()
	}

	return m, nil
}
