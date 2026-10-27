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
	sessionID  string
	generation uint64
	events     <-chan core.Event
	err        error
}

type runtimeEventMsg struct {
	sessionID  string
	generation uint64
	event      core.Event
	batch      []core.Event
	events     <-chan core.Event
}

// runtimeReconnectMsg re-opens the event stream after Core reported a failure or
// the subscription channel closed early.
type runtimeReconnectMsg struct {
	sessionID  string
	generation uint64
}

// runtimeRestoreSettledMsg is retained for explicit restore-settlement messages.
type runtimeRestoreSettledMsg struct {
	generation uint64
}

type turnSubmittedMsg struct {
	sessionID         string
	bindingGeneration uint64
	submission        core.TurnSubmission
	err               error
}

type turnFinishedMsg struct {
	sessionID         string
	bindingGeneration uint64
	executionID       string
	generation        uint64
	result            core.TurnResult
	err               error
}

type tickMsg time.Time

const (
	// maxRuntimeStreamReconnects bounds the automatic reconnects of one stream so
	// a dead subscription cannot spin.
	maxRuntimeStreamReconnects = 3
	// runtimeStreamReconnectDelay is the first backoff step; it doubles per attempt.
	runtimeStreamReconnectDelay = 250 * time.Millisecond
)

func (m *tuiModel) markStreamDirty() {
	// Bubble Tea's renderer already coalesces model updates at its frame rate.
	// Keeping a second timer here adds latency without reducing terminal writes.
	m.view.invalidateLive()
}

// openRuntimeEventStream subscribes to Core's runtime events. The first
// subscription also asks for the replay-complete marker so a session restore can
// settle, while a reconnect resumes after the last processed cursor instead of
// replaying the whole journal. Tests replace it to simulate a failing stream.
var openRuntimeEventStream = func(ctx context.Context, runtime *core.SessionRuntime, cursor int64) (<-chan core.Event, error) {
	if cursor > 0 {
		return runtime.SubscribeEvents(ctx, cursor)
	}
	return runtime.SubscribeEventsWithReplayComplete(ctx, cursor)
}

func (m *tuiModel) subscribeRuntimeEvents() tea.Cmd {
	streamCmd := m.startRuntimeEventSubscription(false)
	return tea.Batch(streamCmd, m.waitForExecution(m.run.runningExecution))
}

func (m *tuiModel) startRuntimeEventSubscription(subscribeNow bool) tea.Cmd {
	if m.runtime == nil {
		return nil
	}
	if m.run.runtimeCancel != nil {
		m.run.runtimeCancel()
	}
	parent := m.boundContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.run.runtimeCancel = cancel
	m.run.subscriptionGeneration++
	generation := m.run.subscriptionGeneration
	runtime := m.runtime
	sessionID := m.sessionID
	cursor := m.run.lastEventCursor
	streamCmd := func() tea.Msg {
		rawEvents, err := openRuntimeEventStream(ctx, runtime, cursor)
		if err != nil {
			cancel()
			return runtimeStreamReadyMsg{sessionID: sessionID, generation: generation, err: err}
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
		return runtimeStreamReadyMsg{sessionID: sessionID, generation: generation, events: buffered}
	}
	if !subscribeNow {
		return streamCmd
	}
	// The first turn in a lazy session submits immediately alongside the other
	// Bubble Tea commands. Open the live subscription now so that submission cannot
	// race past the subscription command and drop live-only model deltas.
	ready := streamCmd()
	if msg, ok := ready.(runtimeStreamReadyMsg); ok && msg.err != nil {
		m.run.runtimeCancel = nil
	}
	return func() tea.Msg { return ready }
}

// reconnectRuntimeEventStream re-opens the event stream after Core reported a
// failure or the subscription channel closed early. Reconnects are bounded and
// back off, so a permanently dead stream surfaces one actionable message instead
// of retrying forever.
func (m *tuiModel) reconnectRuntimeEventStream() tea.Cmd {
	if m.runtime == nil {
		return nil
	}
	if m.run.streamReconnects >= maxRuntimeStreamReconnects {
		m.addSystemEntry("The runtime event stream is lost; run `/resume <id>` to reattach to this session.")
		return nil
	}
	delay := runtimeStreamReconnectDelay << m.run.streamReconnects
	m.run.streamReconnects++
	sessionID := m.sessionID
	generation := m.run.subscriptionGeneration
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return runtimeReconnectMsg{sessionID: sessionID, generation: generation}
	})
}

// matchesSubscription reports whether a stream message belongs to the live
// subscription. A zero generation marks a message built without a subscription
// (tests that inject events directly) and is accepted.
func (m *tuiModel) matchesSubscription(generation uint64) bool {
	return generation == 0 || generation == m.run.subscriptionGeneration
}

// advanceEventCursor remembers the last processed Core event so a reconnect can
// resume after it instead of replaying the whole journal into the transcript.
func (m *tuiModel) advanceEventCursor(event core.Event) {
	if event.Cursor > m.run.lastEventCursor {
		m.run.lastEventCursor = event.Cursor
	}
}

func (m *tuiModel) waitForExecution(executionID string) tea.Cmd {
	if !m.run.running {
		return nil
	}
	return m.awaitExecutionResult(executionID)
}

func (m *tuiModel) awaitExecutionResult(executionID string) tea.Cmd {
	if m.runtime == nil || executionID == "" || m.questionInFlight(executionID) {
		return nil
	}
	if _, waiting := m.run.awaitingTurns[executionID]; waiting {
		return nil
	}
	m.run.waitGeneration++
	m.run.awaitingTurns[executionID] = m.run.waitGeneration
	ctx := m.boundContext
	if ctx == nil {
		ctx = context.Background()
	}
	return waitForTurn(ctx, m.runtime, m.sessionID, m.bindingGeneration, executionID, m.run.waitGeneration)
}

// nextRuntimeEvent delivers the next Core runtime event with the subscription
// identity so a message from a replaced subscription can be ignored.
func nextRuntimeEvent(sessionID string, generation uint64, events <-chan core.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return runtimeEventMsg{sessionID: sessionID, generation: generation, events: nil}
		}
		// Coalesce queued stream events into bounded batches. Every delta is still
		// processed in order, while the renderer presents the latest state per frame.
		var batch []core.Event
		if event.Kind == core.EventModelStream {
			for len(batch) < 128 {
				select {
				case nextEv, ok := <-events:
					if !ok {
						return runtimeEventMsg{sessionID: sessionID, generation: generation, event: event, batch: batch, events: events}
					}
					batch = append(batch, nextEv)
					if nextEv.Kind != core.EventModelStream {
						return runtimeEventMsg{sessionID: sessionID, generation: generation, event: event, batch: batch, events: events}
					}
				default:
					return runtimeEventMsg{sessionID: sessionID, generation: generation, event: event, batch: batch, events: events}
				}
			}
		}
		return runtimeEventMsg{sessionID: sessionID, generation: generation, event: event, batch: batch, events: events}
	}
}

func waitForTurn(ctx context.Context, runtime *core.SessionRuntime, sessionID string, bindingGeneration uint64, executionID string, generation uint64) tea.Cmd {
	if runtime == nil || executionID == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := runtime.WaitTurn(ctx, executionID)
		return turnFinishedMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, executionID: executionID, generation: generation, result: res, err: err}
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
	if m.run.compactInFlight || (m.usage.compacting && !m.run.running) {
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

	m.input.history = append(m.input.history, text)
	m.input.historyIndex = len(m.input.history)
	m.input.historyDraft = ""
	m.modals.shortcuts = false
	m.input.autocompleteOpen = false

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
	bindingGeneration := m.bindingGeneration
	ctx := m.boundContext
	if ctx == nil {
		ctx = context.Background()
	}

	if m.run.running || m.run.hasInterruptedExecution() {
		// Queue follow-up turns while work is active or Core is cleaning up an
		// explicitly interrupted execution.
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
			sub, err := runtime.SubmitTurn(ctx, req, core.QueueInput)
			return turnSubmittedMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, submission: sub, err: err}
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
	m.run.running = true
	m.run.thinking = false
	m.run.taskActivity = ""
	m.run.taskStartedAt = time.Now()
	m.run.turnStreamStartTime = time.Now()
	m.run.streamFirstTokenTime = time.Time{}
	m.run.streamGenerationTokens = 0
	m.run.resetStream()
	m.run.resetReasoning()
	m.view.invalidateLive()
	m.run.reasoningStartTime = time.Time{}
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
	main.StartedAt = m.run.taskStartedAt
	main.Duration = 0
	main.ContextWindow = m.usage.contextWindow
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
			sub, err := runtime.SubmitTurn(ctx, req, core.StartIfIdle)
			if err != nil && strings.Contains(err.Error(), "session task is paused") {
				_, _ = runtime.DiscardPaused(ctx)
				sub, err = runtime.SubmitTurn(ctx, req, core.StartIfIdle)
			}
			return turnSubmittedMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, submission: sub, err: err}
		},
	)
}
