package tui

import (
	"context"
	"strings"
	"time"

	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	core "github.com/afterlune/luneCore"
)

const liveMarkdownTailLines = 32

// runState owns everything about the task that is currently running (or was
// running before a pause): the run flags, the Core task snapshot, waiting
// bookkeeping, the queue, the streaming buffers and the restore freeze.
//
// Interaction cards (question, approval, plan review, recovery, paused
// checkpoint) stay on the model: they are shown in the transcript, not owned by
// the run.
type runState struct {
	goal             *core.Goal
	running          bool
	taskStartedAt    time.Time
	runningExecution string

	// runtimeCancel cancels the local event subscription when a session is left.
	runtimeCancel context.CancelFunc
	// subscriptionGeneration identifies the live event subscription so a closed
	// stream from a replaced subscription cannot trigger a reconnect.
	subscriptionGeneration uint64
	// lastEventCursor is the highest Core event cursor processed for this
	// session; a reconnect resumes after it instead of replaying from scratch.
	lastEventCursor int64
	// streamReconnects counts the failed reconnects of the current stream.
	streamReconnects int

	awaitingTurns       map[string]uint64
	waitGeneration      uint64
	taskSnapshot        core.TaskState
	questionSubmissions map[questionWaitKey]questionSubmissionState
	terminalEvents      map[string]core.Event
	// autoApprovalFailures keeps approval IDs whose silent full-access
	// auto-approval failed, so the card is shown instead of retrying forever.
	autoApprovalFailures map[string]struct{}

	queuedTurns              []QueuedItem
	cancelingQueuedExecution string
	discardInFlight          bool

	// restoringSession freezes footer metrics and transcript rebuilds while Core
	// replays durable events for a freshly loaded session.
	restoringSession  bool
	restoreGeneration uint64
	restoreStartedAt  time.Time

	// Streaming buffers and generation diagnostics.
	currentStreamText      string
	currentReasoningText   string
	streamBuilder          strings.Builder
	reasoningBuilder       strings.Builder
	assistantTail          *tuiMarkdown.LiveTail
	reasoningTail          *tuiMarkdown.LiveTail
	planBeforeTail         *tuiMarkdown.LiveTail
	planTail               *tuiMarkdown.LiveTail
	planAfterTail          *tuiMarkdown.LiveTail
	planProbeTail          string
	planOpenEnd            int
	planCloseStart         int
	planCloseEnd           int
	planFound              bool
	planComplete           bool
	lastStreamRenderAt     time.Time
	streamRenderPending    bool
	streamRenderGeneration uint64
	taskActivity           string
	reasoningStartTime     time.Time
	reasoningLiveCollapsed bool
	thinking               bool
	turnStreamStartTime    time.Time
	streamFirstTokenTime   time.Time
	streamGenerationTokens int64
}

// newRunState returns the empty run state used by a fresh model.
func newRunState() runState {
	return runState{
		awaitingTurns:          map[string]uint64{},
		terminalEvents:         map[string]core.Event{},
		autoApprovalFailures:   map[string]struct{}{},
		reasoningLiveCollapsed: true,
		assistantTail:          tuiMarkdown.NewLiveTail(liveMarkdownTailLines),
		reasoningTail:          tuiMarkdown.NewLiveTail(liveMarkdownTailLines),
		planBeforeTail:         tuiMarkdown.NewLiveTail(liveMarkdownTailLines),
		planTail:               tuiMarkdown.NewLiveTail(liveMarkdownTailLines),
		planAfterTail:          tuiMarkdown.NewLiveTail(liveMarkdownTailLines),
	}
}

func (r *runState) appendStreamDelta(delta string) {
	if delta == "" {
		return
	}
	r.streamBuilder.WriteString(delta)
	r.currentStreamText = r.streamBuilder.String()

	if r.planFound {
		if r.planComplete {
			r.planAfterTail.Append(delta)
			return
		}
		r.planTail.Append(delta)
		window := r.planProbeTail + delta
		previousWindowLength := len(r.planProbeTail)
		r.planProbeTail = planProbeSuffix(window)
		if !tuiMarkdown.HasProposedPlanTagAfter(window, previousWindowLength) {
			return
		}
		ranges := tuiMarkdown.ProposedPlanTagRanges(r.currentStreamText)
		if close := firstPlanCloseAfter(ranges, r.planOpenEnd); close != nil {
			r.planCloseStart, r.planCloseEnd = close.Start, close.End
			r.planComplete = true
			r.planTail.Reset()
			r.planTail.Append(strings.TrimSpace(r.currentStreamText[r.planOpenEnd:r.planCloseStart]))
			r.planAfterTail.Reset()
			r.planAfterTail.Append(r.currentStreamText[r.planCloseEnd:])
		}
		return
	}

	r.assistantTail.Append(delta)
	window := r.planProbeTail + delta
	previousWindowLength := len(r.planProbeTail)
	r.planProbeTail = planProbeSuffix(window)
	if !tuiMarkdown.HasProposedPlanTagAfter(window, previousWindowLength) {
		return
	}
	ranges := tuiMarkdown.ProposedPlanTagRanges(r.currentStreamText)
	var open *tuiMarkdown.PlanTagRange
	for index := range ranges {
		if ranges[index].Open {
			open = &ranges[index]
			break
		}
	}
	if open == nil {
		return
	}
	split := tuiMarkdown.SplitProposedPlanStream(r.currentStreamText)
	if !split.Found {
		return
	}
	r.planFound = true
	r.planOpenEnd = open.End
	r.planBeforeTail.Reset()
	r.planBeforeTail.Append(split.Before)
	r.assistantTail.Reset()
	r.planTail.Reset()
	close := firstPlanCloseAfter(ranges, open.End)
	if close == nil {
		r.planTail.Append(strings.TrimSpace(r.currentStreamText[open.End:]))
		return
	}
	r.planComplete = true
	r.planCloseStart, r.planCloseEnd = close.Start, close.End
	r.planTail.Append(strings.TrimSpace(r.currentStreamText[open.End:close.Start]))
	r.planAfterTail.Reset()
	r.planAfterTail.Append(split.After)
}

func firstPlanCloseAfter(tags []tuiMarkdown.PlanTagRange, position int) *tuiMarkdown.PlanTagRange {
	for index := range tags {
		if !tags[index].Open && tags[index].Start >= position {
			return &tags[index]
		}
	}
	return nil
}

func planProbeSuffix(text string) string {
	const maxPlanTagLength = 32
	if len(text) > maxPlanTagLength {
		text = text[len(text)-maxPlanTagLength:]
	}
	return text
}

func (r *runState) resetStream() {
	r.streamBuilder.Reset()
	r.currentStreamText = ""
	r.assistantTail.Reset()
	r.planBeforeTail.Reset()
	r.planTail.Reset()
	r.planAfterTail.Reset()
	r.planProbeTail = ""
	r.planOpenEnd = 0
	r.planCloseStart = 0
	r.planCloseEnd = 0
	r.planFound = false
	r.planComplete = false
	r.lastStreamRenderAt = time.Time{}
	r.streamRenderPending = false
	r.streamRenderGeneration++
}

func (r *runState) appendReasoningDelta(delta string) {
	if delta == "" {
		return
	}
	r.reasoningBuilder.WriteString(delta)
	r.currentReasoningText = r.reasoningBuilder.String()
	r.reasoningTail.Append(delta)
}

func (r *runState) resetReasoning() {
	r.reasoningBuilder.Reset()
	r.currentReasoningText = ""
	r.reasoningTail.Reset()
}

func (r *runState) ensureLiveTails() {
	if r.currentStreamText != "" && r.streamBuilder.Len() == 0 {
		text := r.currentStreamText
		r.resetStream()
		r.appendStreamDelta(text)
	}
	if r.currentReasoningText != "" && r.reasoningBuilder.Len() == 0 {
		text := r.currentReasoningText
		r.resetReasoning()
		r.appendReasoningDelta(text)
	}
}

// busy reports whether a task is running or waiting in the queue.
func (r *runState) busy() bool {
	return r.running || len(r.queuedTurns) > 0
}

// restoreFrom adopts Core's task snapshot. The paused/failed cards stay with the
// caller; this only decides whether a task counts as running.
func (r *runState) restoreFrom(task core.TaskState) {
	r.taskSnapshot = task
	r.running = false
	r.runningExecution = ""
	switch task.Status {
	case core.TaskRunning, core.TaskQueued:
		r.running = true
		r.runningExecution = task.ExecutionID
		r.taskStartedAt = time.Now()
	case core.TaskPaused:
		r.runningExecution = task.ExecutionID
	}
}

// beginRestore freezes footer metrics and transcript rebuilds for a session load.
func (r *runState) beginRestore() {
	r.restoringSession = true
	r.restoreGeneration++
	r.restoreStartedAt = time.Now()
}

// finishRestore clears the freeze; it reports whether a restore was in flight.
func (r *runState) finishRestore() bool {
	if !r.restoringSession {
		return false
	}
	r.restoringSession = false
	return true
}

// reset clears every per-session run value before another session is bound. The
// live-reasoning display preference survives: it belongs to the user, not the
// session.
func (r *runState) reset() {
	live := r.reasoningLiveCollapsed
	subscriptionGeneration := r.subscriptionGeneration
	waitGeneration := r.waitGeneration
	restoreGeneration := r.restoreGeneration
	*r = newRunState()
	r.reasoningLiveCollapsed = live
	// Keep asynchronous identities unique across session resets. Resetting these
	// counters could let delayed results from A→B→A switches look current.
	r.subscriptionGeneration = subscriptionGeneration
	r.waitGeneration = waitGeneration
	r.restoreGeneration = restoreGeneration
}
