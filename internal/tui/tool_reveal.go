package tui

import (
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	toolRevealFrameInterval = time.Second / 60
	toolRevealRowInterval   = time.Second / 30
	toolRevealMaxDelay      = 120 * time.Millisecond
)

type toolRevealItem struct {
	index int
	lines []string
	shown int
}

type toolRevealStep struct {
	item     *toolRevealItem
	row      int
	deadline time.Time
}

// toolRoundReveal holds a projection of the trailing live round. The entries
// remain authoritative; only newly added rendered rows wait for presentation.
type toolRoundReveal struct {
	start        int
	header       string
	items        []*toolRevealItem
	queue        []toolRevealStep
	nextAt       time.Time
	prefixLines  []string
	prefixRanges []entryLineRange
	offset       int
}

func (r *toolRoundReveal) sync(block string, ranges []entryLineRange, entries []TranscriptEntry, animate bool, now time.Time) {
	lines := strings.Split(block, "\n")
	r.header = lines[0]
	old := make(map[int]*toolRevealItem, len(r.items))
	for _, item := range r.items {
		old[item.index] = item
	}
	items := make([]*toolRevealItem, 0, len(ranges)-1)
	firstTool := true
	for _, rr := range ranges {
		if rr.IsRoundHeader || rr.IsRoundFooter {
			continue
		}
		item := old[rr.EntryIndex]
		kind := entries[rr.EntryIndex-r.start].Kind
		previous := 0
		if item == nil {
			item = &toolRevealItem{index: rr.EntryIndex}
			// The first call title appears immediately, even if its result is
			// already available in the same event batch.
			if firstTool && kind == KindTool {
				item.shown = 1
			}
		} else {
			previous = len(item.lines)
		}
		item.lines = lines[rr.StartLine : rr.EndLine+1]
		item.shown = min(item.shown, len(item.lines))
		if kind == KindTool {
			firstTool = false
		}
		if !animate || kind != KindTool {
			item.shown = len(item.lines)
		} else {
			for row := max(previous, item.shown); row < len(item.lines); row++ {
				r.queue = append(r.queue, toolRevealStep{item: item, row: row, deadline: now.Add(toolRevealMaxDelay)})
			}
		}
		items = append(items, item)
	}
	r.items = items
	r.prune()
	sort.Slice(r.queue, func(i, j int) bool {
		if r.queue[i].item.index != r.queue[j].item.index {
			return r.queue[i].item.index < r.queue[j].item.index
		}
		return r.queue[i].row < r.queue[j].row
	})
	if !animate {
		r.flush()
		return
	}
	if deadline := r.earliestDeadline(); !deadline.IsZero() && !now.Before(deadline) {
		r.advance(now)
	}
	r.scheduleNext(now)
}

func (r *toolRoundReveal) prune() {
	active := make(map[*toolRevealItem]bool, len(r.items))
	for _, item := range r.items {
		active[item] = true
	}
	kept := r.queue[:0]
	for _, step := range r.queue {
		if active[step.item] && step.row >= step.item.shown && step.row < len(step.item.lines) {
			kept = append(kept, step)
		}
	}
	r.queue = kept
}

func (r *toolRoundReveal) scheduleNext(now time.Time) {
	deadline := r.earliestDeadline()
	if deadline.IsZero() {
		r.nextAt = time.Time{}
		return
	}
	delay := min(toolRevealRowInterval, max(time.Duration(0), deadline.Sub(now))/time.Duration(len(r.queue)))
	at := now.Add(delay)
	if r.nextAt.IsZero() || at.Before(r.nextAt) {
		r.nextAt = at
	}
}

func (r *toolRoundReveal) advance(now time.Time) bool {
	r.prune()
	deadline := r.earliestDeadline()
	if deadline.IsZero() || now.Before(r.nextAt) {
		return false
	}
	slots := max(1, int((max(time.Duration(0), deadline.Sub(now))+toolRevealRowInterval-1)/toolRevealRowInterval))
	count := max(1, (len(r.queue)+slots-1)/slots)
	// If a frame was delayed, catch up through the latest expired row in
	// transcript order. Rows queued more recently after that point keep their
	// own pacing window instead of being flushed by an older deadline.
	expiredPrefix := 0
	for i, step := range r.queue {
		if !now.Before(step.deadline) {
			expiredPrefix = i + 1
		}
	}
	if expiredPrefix > 0 {
		count = min(count, expiredPrefix)
	}
	for len(r.queue) > 0 && count > 0 {
		step := r.queue[0]
		r.queue = r.queue[1:]
		step.item.shown = max(step.item.shown, step.row+1)
		count--
	}
	r.nextAt = time.Time{}
	r.scheduleNext(now)
	return true
}

func (r *toolRoundReveal) earliestDeadline() time.Time {
	var earliest time.Time
	for _, step := range r.queue {
		if earliest.IsZero() || step.deadline.Before(earliest) {
			earliest = step.deadline
		}
	}
	return earliest
}

func (r *toolRoundReveal) flush() {
	for _, item := range r.items {
		item.shown = len(item.lines)
	}
	r.queue = nil
	r.nextAt = time.Time{}
}

func (r *toolRoundReveal) project() ([]string, []entryLineRange) {
	lines := []string{r.header}
	ranges := []entryLineRange{{EntryIndex: r.start, IsRoundHeader: true}}
	for _, item := range r.items {
		if item.shown == 0 {
			continue
		}
		start := len(lines)
		lines = append(lines, item.lines[:item.shown]...)
		ranges = append(ranges, entryLineRange{EntryIndex: item.index, StartLine: start, EndLine: len(lines) - 1})
	}
	return lines, ranges
}

type toolRevealFrameMsg struct {
	generation        uint64
	bindingGeneration uint64
	at                time.Time
}

func (m *tuiModel) canRevealTools() bool {
	return m.run.running && !m.run.restoringSession && !m.scrolledUp && !m.input.selecting && !m.input.composerSelecting && !m.hasSelection() && m.activeCardKind() == cardNone && m.modals.active() == modalNone
}

func (m *tuiModel) nextToolRevealFrame() tea.Cmd {
	if !m.canRevealTools() {
		m.finishToolReveal()
		return nil
	}
	r := m.view.toolReveal
	if r == nil || len(r.queue) == 0 || m.view.toolRevealPending {
		return nil
	}
	m.view.toolRevealPending = true
	generation, binding := m.view.toolRevealGeneration, m.bindingGeneration
	delay := min(toolRevealFrameInterval, max(time.Duration(0), r.earliestDeadline().Sub(time.Now())))
	return tea.Tick(delay, func(at time.Time) tea.Msg {
		return toolRevealFrameMsg{generation: generation, bindingGeneration: binding, at: at}
	})
}

// publishToolReveal reuses the formatted history prefix and child rows. While
// following the bottom, viewport measurement is restricted to the visible window;
// input handling restores the complete document before hit testing or scrolling.
func (m *tuiModel) publishToolReveal(windowed bool) {
	r := m.view.toolReveal
	if r == nil {
		return
	}
	rows, ranges := r.project()
	lines := m.view.transcriptLines[:0]
	lines = append(lines, r.prefixLines...)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, rows...)
	m.view.transcriptLines = lines
	m.view.entryLineRanges = append(m.view.entryLineRanges[:0], r.prefixRanges...)
	for _, rr := range ranges {
		rr.StartLine += r.offset
		rr.EndLine += r.offset
		m.view.entryLineRanges = append(m.view.entryLineRanges, rr)
	}
	m.view.toolRevealWindowed = windowed
	m.view.toolRevealWindowStart = 0
	visible := lines
	if windowed && !m.viewport.SoftWrap {
		m.view.toolRevealWindowStart = max(0, len(lines)-m.viewport.Height())
		visible = lines[m.view.toolRevealWindowStart:]
	}
	m.viewport.SetContentLines(visible)
	if !m.scrolledUp {
		m.viewport.GotoBottom()
	}
}

func (m *tuiModel) finishToolReveal() {
	m.view.animateTools = false
	r := m.view.toolReveal
	if r == nil {
		return
	}
	if len(r.queue) == 0 && !m.view.toolRevealWindowed {
		return
	}
	r.flush()
	m.view.toolRevealGeneration++
	m.view.toolRevealPending = false
	m.publishToolReveal(false)
}

// Keep the row under the pointer (or the top row for scrolling) anchored while
// restoring the full viewport. New rows may have been inserted above that row.
func (m *tuiModel) finishToolRevealForInput(screenRow int) {
	r := m.view.toolReveal
	if r == nil || len(r.queue) == 0 {
		m.finishToolReveal()
		return
	}
	screenRow = clamp(screenRow, 0, max(0, m.viewport.Height()-1))
	docRow := m.viewport.YOffset() + screenRow
	if m.view.toolRevealWindowed && !m.viewport.SoftWrap {
		docRow += m.view.toolRevealWindowStart
	}
	var anchor entryLineRange
	found := false
	for _, rr := range m.view.entryLineRanges {
		if docRow >= rr.StartLine && docRow <= rr.EndLine {
			anchor, found = rr, true
			break
		}
		if rr.StartLine > docRow {
			anchor, found = rr, true
			break
		}
	}
	m.finishToolReveal()
	if !found {
		return
	}
	for _, rr := range m.view.entryLineRanges {
		if rr.EntryIndex == anchor.EntryIndex && rr.IsRoundHeader == anchor.IsRoundHeader && rr.IsRoundFooter == anchor.IsRoundFooter {
			row := rr.StartLine + min(docRow-anchor.StartLine, rr.EndLine-rr.StartLine)
			m.viewport.SetYOffset(row - screenRow)
			m.scrolledUp = !m.viewport.AtBottom()
			if m.scrolledUp {
				m.view.invalidateLive()
			}
			return
		}
	}
}
