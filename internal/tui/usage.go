package tui

import (
	"math"
	"time"

	core "github.com/afterlune/luneCore"
)

// sessionUsage owns every token counter and context-window number the footer,
// /status, /context, the exports and the agents panel read. Session switches,
// live usage events and compaction all write through this type, so a session
// never ends up with a half-updated footer.
type sessionUsage struct {
	// Cumulative usage of the whole session, including subagents.
	totalInputTokens   int64
	totalOutputTokens  int64
	cachedTokens       int64
	reasoningTokens    int64
	seededSessionUsage bool

	// Numbers of the latest main-agent request plus the window it ran against.
	contextWindow        int64
	lastInputTokens      int64
	lastCachedTokens     int64
	lastRequestID        string
	lastRequestAttempt   int
	lastRequestChanges   []string
	estimatedInputTokens int64

	// Compaction and live generation markers.
	compacting          bool
	compactionStartedAt time.Time
	lastTokensPerSec    float64
}

// reset clears per-session accounting before another session is bound.
func (u *sessionUsage) reset() {
	*u = sessionUsage{}
}

// applyTotals installs the persisted session counters and marks them seeded so
// live events keep accumulating from the stored baseline.
func (u *sessionUsage) applyTotals(usage core.UsageTotals) {
	u.totalInputTokens = usage.Input
	u.totalOutputTokens = usage.Output
	u.cachedTokens = usage.Cached
	u.reasoningTokens = usage.Reasoning
	u.seededSessionUsage = usage.Input > 0 || usage.Output > 0 || usage.Cached > 0 || usage.Reasoning > 0
}

// clearLastRequest drops the measured numbers of the previous main-agent request.
func (u *sessionUsage) clearLastRequest() {
	u.lastInputTokens, u.lastCachedTokens = 0, 0
	u.lastRequestID, u.lastRequestAttempt, u.lastRequestChanges = "", 0, nil
	u.estimatedInputTokens = 0
}

// applyLeadRequest records one authoritative main-agent request. A zero window
// keeps the current budget, mirroring Core's optional ContextWindow field.
func (u *sessionUsage) applyLeadRequest(input, cached, estimated, window int64, requestID string, attempt int, changes []string) {
	if window > 0 {
		u.contextWindow = window
	}
	u.lastInputTokens = input
	u.lastCachedTokens = cached
	u.lastRequestID, u.lastRequestAttempt = requestID, attempt
	u.lastRequestChanges = append([]string(nil), changes...)
	u.estimatedInputTokens = estimated
}

// addEventUsage accumulates one live usage event into the session totals.
func (u *sessionUsage) addEventUsage(usage core.Usage) {
	u.totalInputTokens += usage.InputTokens
	u.totalOutputTokens += usage.OutputTokens
	u.cachedTokens += usage.InputTokensDetails.CachedTokens
	u.reasoningTokens += usage.OutputTokensDetails.ReasoningTokens
}

// beginCompaction marks a compaction pass as running so the footer can say so.
func (u *sessionUsage) beginCompaction() {
	u.compacting = true
	u.compactionStartedAt = time.Now()
}

// finishCompaction ends the pass. A reported after-token estimate replaces the
// measured input of the previous request; zero means Core reported no count.
func (u *sessionUsage) finishCompaction(afterTokens int64) {
	u.compacting = false
	if afterTokens > 0 {
		u.estimatedInputTokens = afterTokens
		u.lastInputTokens, u.lastCachedTokens = 0, 0
	}
}

// cachePercent is the weighted cache rate of every request in this session.
func (u *sessionUsage) cachePercent() float64 {
	if u.totalInputTokens <= 0 {
		return 0
	}
	return math.Min(100, float64(u.cachedTokens)/float64(u.totalInputTokens)*100)
}

// totalTokens counts everything the session has billed so far.
func (u *sessionUsage) totalTokens() int64 {
	return u.totalInputTokens + u.totalOutputTokens
}

// usedContext is the current occupancy of the context window: the measured input
// of the latest request, or its local estimate when nothing was measured yet.
func (u *sessionUsage) usedContext() int64 {
	if u.lastInputTokens > 0 {
		return u.lastInputTokens
	}
	return u.estimatedInputTokens
}
