package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/filemention"
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
	batch     []core.Event
	events    <-chan core.Event
}

// runtimeRestoreSettledMsg is retained for explicit restore-settlement messages.
type runtimeRestoreSettledMsg struct {
	generation uint64
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
	streamCmd := m.startRuntimeEventSubscription(false)
	return tea.Batch(streamCmd, m.waitForExecution(m.runningExecution))
}

func (m *tuiModel) startRuntimeEventSubscription(subscribeNow bool) tea.Cmd {
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
		rawEvents, err := runtime.SubscribeEventsWithReplayComplete(ctx, 0)
		if err != nil {
			cancel()
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
	if !subscribeNow {
		return streamCmd
	}
	// The first turn in a lazy session submits immediately alongside the other
	// Bubble Tea commands. Open the live subscription now so that submission cannot
	// race past the subscription command and drop live-only model deltas.
	ready := streamCmd()
	if msg, ok := ready.(runtimeStreamReadyMsg); ok && msg.err != nil {
		m.runtimeCancel = nil
	}
	return func() tea.Msg { return ready }
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
	return m.submitTurnWithOptions(rawInput, "", m.planMode, true)
}

func (m *tuiModel) submitTurnWithOptions(rawInput string, permissionMode core.PermissionMode, planMode, expandMentions bool) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(rawInput)
	if text == "" {
		return m, nil
	}
	if m.compacting {
		m.addSystemEntry("Compaction is in progress; wait for it to finish.")
		return m, nil
	}

	creatingSession := m.sessionID == ""
	if err := m.ensureSession(context.Background()); err != nil {
		m.addSystemEntry("Failed to create session: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	// A new turn is live work; stop deferring the restored transcript so its usage
	// events are counted immediately.
	m.finishSessionRestore()

	m.promptHistory = append(m.promptHistory, text)
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
	m.shortcutsOpen = false
	m.autocompleteOpen = false

	if strings.TrimSpace(m.sessionTitle) == "" || m.sessionTitle == "<untitled>" || isTrivialGreeting(m.sessionTitle) {
		if autoTitle := deriveSessionTitleText(text); autoTitle != "" {
			if !isTrivialGreeting(text) || strings.TrimSpace(m.sessionTitle) == "" || m.sessionTitle == "<untitled>" {
				m.sessionTitle = autoTitle
				if m.client != nil && m.sessionID != "" {
					client := m.client
					sid := m.sessionID
					go func() {
						_ = client.SetSessionTitle(context.Background(), sid, autoTitle)
					}()
				}
			}
		}
	}

	attachments := []filemention.Attachment(nil)
	augmentedText := text
	if expandMentions {
		attachments, augmentedText = filemention.Expand(text, m.workspace)
	}
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
		Input:          input,
		PlanMode:       planMode,
		PermissionMode: permissionMode,
	}
	var runtimeEventsCmd tea.Cmd
	if creatingSession {
		runtimeEventsCmd = m.startRuntimeEventSubscription(true)
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

		submitCmd := func() tea.Msg {
			ctx := context.Background()
			sub, err := runtime.SubmitTurn(ctx, req, core.QueueInput)
			return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
		}
		return m, tea.Batch(runtimeEventsCmd, submitCmd)
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
	m.taskActivity = ""
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
		runtimeEventsCmd,
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
