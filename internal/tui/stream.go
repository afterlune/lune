package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
)

const modelStreamRefreshInterval = 32 * time.Millisecond

type modelStreamFlushMsg struct{ token uint64 }

type proposedPlanTagStreamFilter struct {
	pending string
}

func (f *proposedPlanTagStreamFilter) write(text string) string {
	const openTag, closeTag = "<proposed_plan>", "</proposed_plan>"
	f.pending += text
	var visible strings.Builder
	for len(f.pending) > 0 {
		switch {
		case strings.HasPrefix(f.pending, openTag):
			f.pending = f.pending[len(openTag):]
		case strings.HasPrefix(f.pending, closeTag):
			f.pending = f.pending[len(closeTag):]
		case strings.HasPrefix(openTag, f.pending), strings.HasPrefix(closeTag, f.pending):
			return visible.String()
		default:
			visible.WriteByte(f.pending[0])
			f.pending = f.pending[1:]
		}
	}
	return visible.String()
}

func (f *proposedPlanTagStreamFilter) flush() string {
	visible := f.pending
	f.pending = ""
	return visible
}

func (m *tuiModel) startModelStream(event core.Event) {
	if event.SessionID != m.sessionID || event.TaskID == "" {
		return
	}
	if m.modelStreamTaskID == event.TaskID {
		return
	}
	m.resetModelStream()
	m.taskStartedAt = time.Now()
	m.modelStreamTaskID = event.TaskID
	m.modelStreamStartedAt = time.Now()
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
}

func (m *tuiModel) queueModelStreamEvent(event core.Event) tea.Cmd {
	if event.SessionID != m.sessionID || event.TaskID == "" || event.TaskID != m.modelStreamTaskID ||
		m.modelStreamClosed || event.AgentID != "lead" || event.Stream == nil {
		return nil
	}
	switch event.Stream.Type {
	case "text_delta":
		if event.Stream.Delta == "" {
			return nil
		}
	case "completed", "failed":
	default:
		return nil
	}
	m.modelStreamPending = append(m.modelStreamPending, event)
	if m.modelStreamTick {
		return nil
	}
	m.modelStreamTick = true
	m.modelStreamToken++
	token := m.modelStreamToken
	return tea.Tick(modelStreamRefreshInterval, func(time.Time) tea.Msg {
		return modelStreamFlushMsg{token: token}
	})
}

func (m *tuiModel) flushModelStreamEvents() bool {
	events := m.modelStreamPending
	m.modelStreamPending = nil
	m.modelStreamTick = false
	m.modelStreamToken++
	var itemKey string
	var delta strings.Builder
	changed := false
	flushDelta := func() {
		if delta.Len() == 0 {
			return
		}
		changed = m.appendModelStreamText(itemKey, delta.String()) || changed
		delta.Reset()
	}
	for _, event := range events {
		if event.TaskID != m.modelStreamTaskID || event.Stream == nil {
			continue
		}
		if event.Stream.RequestID != "" && event.Stream.RequestID != m.modelStreamRequestID {
			flushDelta()
			m.closeModelStreamResponse(false, true)
			m.modelStreamRequestID = event.Stream.RequestID
		}
		switch event.Stream.Type {
		case "text_delta":
			if event.Stream.Delta != "" && !m.modelStreamFirstTokenRecorded && !m.modelStreamStartedAt.IsZero() {
				m.modelStreamFirstTokenLatency = time.Since(m.modelStreamStartedAt)
				m.modelStreamFirstTokenRecorded = true
			}
			key := modelStreamItemKey(event.Stream)
			if delta.Len() > 0 && key != itemKey {
				flushDelta()
			}
			itemKey = key
			delta.WriteString(event.Stream.Delta)
		case "completed":
			flushDelta()
			m.closeModelStreamResponse(false, true)
		case "failed":
			flushDelta()
			changed = m.closeModelStreamResponse(true, true) || changed
		}
	}
	flushDelta()
	return changed
}

func modelStreamItemKey(event *core.ModelStreamEvent) string {
	if event.ItemID != "" {
		return "item:" + event.ItemID
	}
	if event.OutputIndex != nil {
		return "output:" + strconv.Itoa(*event.OutputIndex)
	}
	return "text"
}

func (m *tuiModel) appendModelStreamText(itemKey, text string) bool {
	if text == "" {
		return false
	}
	if m.modelStreamTagFilters == nil {
		m.modelStreamTagFilters = make(map[string]*proposedPlanTagStreamFilter)
	}
	filter := m.modelStreamTagFilters[itemKey]
	if filter == nil {
		filter = &proposedPlanTagStreamFilter{}
		m.modelStreamTagFilters[itemKey] = filter
		m.modelStreamTagFilterOrder = append(m.modelStreamTagFilterOrder, itemKey)
	}
	if m.modelStreamRawPending == nil {
		m.modelStreamRawPending = make(map[string]string)
	}
	m.modelStreamRawPending[itemKey] += text
	visible := filter.write(text)
	if visible == "" {
		return false
	}
	m.appendVisibleModelStreamText(itemKey, visible)
	return true
}

func (m *tuiModel) appendVisibleModelStreamText(itemKey, text string) {
	if text == "" {
		return
	}
	m.clearTranscriptSelection()
	if m.modelStreamItems == nil {
		m.modelStreamItems = make(map[string]int)
	}
	index, exists := m.modelStreamItems[itemKey]
	if !exists {
		index = len(m.entries)
		m.entries = append(m.entries, tuiEntry{role: "assistant"})
		m.modelStreamItems[itemKey] = index
		m.modelStreamCurrent = append(m.modelStreamCurrent, index)
	}
	m.entries[index].text += text
	if m.modelStreamRawEntries == nil {
		m.modelStreamRawEntries = make(map[int]string)
	}
	if m.modelStreamRawPending != nil {
		m.modelStreamRawEntries[index] += m.modelStreamRawPending[itemKey]
		m.modelStreamRawPending[itemKey] = ""
	}
}

func (m *tuiModel) flushModelStreamTagFilters() {
	for _, itemKey := range m.modelStreamTagFilterOrder {
		if filter := m.modelStreamTagFilters[itemKey]; filter != nil {
			m.appendVisibleModelStreamText(itemKey, filter.flush())
		}
	}
	m.modelStreamTagFilters = nil
	m.modelStreamTagFilterOrder = nil
	m.modelStreamRawPending = nil
}

func (m *tuiModel) closeModelStreamResponse(incomplete, replaceLast bool) bool {
	m.flushModelStreamTagFilters()
	changed := false
	if incomplete {
		for _, index := range m.modelStreamCurrent {
			if index >= 0 && index < len(m.entries) && !m.entries[index].incomplete {
				m.entries[index].incomplete = true
				changed = true
			}
		}
	}
	if replaceLast && len(m.modelStreamCurrent) > 0 {
		m.modelStreamLast = append(m.modelStreamLast[:0], m.modelStreamCurrent...)
	}
	m.modelStreamCurrent = nil
	m.modelStreamItems = nil
	return changed
}

func (m *tuiModel) finishModelStreamEvent(event core.Event) {
	if event.SessionID != m.sessionID || event.TaskID != m.modelStreamTaskID || m.modelStreamClosed {
		return
	}
	m.flushModelStreamEvents()
	m.closeModelStreamResponse(event.Status != core.TaskCompleted, true)
	m.modelStreamClosed = true
}

func (m *tuiModel) finishModelStream(finalText string, taskSucceeded bool) bool {
	m.flushModelStreamEvents()
	m.flushModelStreamTagFilters()
	if len(m.modelStreamCurrent) > 0 {
		m.closeModelStreamResponse(!taskSucceeded, true)
	}
	if strings.TrimSpace(finalText) == "" {
		for _, index := range m.modelStreamLast {
			if index >= 0 && index < len(m.entries) {
				if raw := m.modelStreamRawEntries[index]; raw != "" {
					m.entries[index].text = raw
				}
			}
		}
	}
	reconciled := m.reconcileModelStreamFinal(finalText)
	m.resetModelStream()
	return reconciled
}

func (m *tuiModel) reconcileModelStreamFinal(finalText string) bool {
	indices := m.modelStreamLast
	if finalText == "" || len(indices) == 0 {
		return false
	}
	first := indices[0]
	if first < 0 || first >= len(m.entries) {
		return false
	}
	previous := first
	for _, index := range indices[1:] {
		if index <= previous || index >= len(m.entries) {
			return false
		}
		previous = index
	}
	incomplete := false
	for _, index := range indices {
		incomplete = incomplete || m.entries[index].incomplete
	}
	m.clearTranscriptSelection()
	m.entries[first].text = finalText
	m.entries[first].incomplete = incomplete
	if len(indices) > 1 {
		for position := len(indices) - 1; position > 0; position-- {
			index := indices[position]
			m.entries = append(m.entries[:index], m.entries[index+1:]...)
		}
	}
	return true
}

func (m *tuiModel) resetModelStream() {
	m.modelStreamToken++
	m.modelStreamTaskID = ""
	m.modelStreamRequestID = ""
	m.modelStreamStartedAt = time.Time{}
	m.modelStreamClosed = false
	m.modelStreamPending = nil
	m.modelStreamTick = false
	m.modelStreamItems = nil
	m.modelStreamTagFilters = nil
	m.modelStreamTagFilterOrder = nil
	m.modelStreamRawPending = nil
	m.modelStreamRawEntries = nil
	m.modelStreamCurrent = nil
	m.modelStreamLast = nil
}
