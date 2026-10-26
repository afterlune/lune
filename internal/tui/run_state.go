package tui

import (
	"context"
	"time"

	core "github.com/afterlune/luneCore"
)

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
