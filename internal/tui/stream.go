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

type streamToolCall struct {
	activityID  string
	taskID      string
	requestID   string
	id          string
	itemID      string
	agentID     string
	executionID string
	outputIndex *int
	name        string
	arguments   strings.Builder
	preparing   bool
	finished    bool
	hidden      bool
}

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
		m.modelStreamClosed || event.Stream == nil {
		return nil
	}
	if event.Stream.Purpose == "memory" {
		return nil
	}
	switch event.Stream.Type {
	case "text_delta":
		if event.AgentID != "lead" || event.Stream.Delta == "" {
			return nil
		}
	case "tool_started":
	case "tool_arguments_delta":
		if event.Stream.Delta == "" {
			return nil
		}
	case "completed", "failed":
		if event.AgentID != "lead" {
			return nil
		}
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
		if event.AgentID == "lead" && event.Stream.RequestID != "" && event.Stream.RequestID != m.modelStreamRequestID {
			flushDelta()
			m.closeModelStreamResponse(false, true)
			m.finishUnidentifiedStreamToolCalls(m.modelStreamRequestID)
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
		case "tool_started":
			flushDelta()
			m.updateStreamToolCall(event)
			changed = true
		case "tool_arguments_delta":
			flushDelta()
			m.updateStreamToolArguments(event)
			changed = true
		case "completed":
			flushDelta()
			m.closeModelStreamResponse(false, true)
			m.finishUnidentifiedStreamToolCalls(event.Stream.RequestID)
		case "failed":
			flushDelta()
			changed = m.closeModelStreamResponse(true, true) || changed
			m.finishUnidentifiedStreamToolCalls(event.Stream.RequestID)
		}
	}
	flushDelta()
	return changed
}

func (m *tuiModel) updateStreamToolCall(event core.Event) {
	stream := event.Stream
	if stream == nil {
		return
	}
	call := m.findOrCreateStreamToolCall(event)
	if stream.ToolName != "" {
		call.name = stream.ToolName
	}
	m.updateStreamToolCallIdentity(call, event)
	if stream.ToolArguments != "" && (call.arguments.Len() == 0 || len(stream.ToolArguments) > call.arguments.Len()) {
		call.arguments.Reset()
		call.arguments.WriteString(stream.ToolArguments)
	}
	call.hidden = call.hidden || hiddenModelToolCall(call.name)
	m.refreshStreamToolActivity(call)
}

func (m *tuiModel) updateStreamToolArguments(event core.Event) {
	stream := event.Stream
	if stream == nil {
		return
	}
	call := m.findOrCreateStreamToolCall(event)
	m.updateStreamToolCallIdentity(call, event)
	if stream.ToolName != "" {
		call.name = stream.ToolName
	}
	call.arguments.WriteString(stream.Delta)
	call.hidden = call.hidden || hiddenModelToolCall(call.name)
	m.refreshStreamToolActivity(call)
}

func (m *tuiModel) refreshStreamToolActivity(call *streamToolCall) {
	if call == nil {
		return
	}
	m.bindStreamToolActivityAliases(call)
	if call.hidden {
		m.removeStreamToolActivity(call)
		return
	}
	if call.name == "" || call.activityID == "" {
		call.preparing = true
		return
	}
	args := []byte(call.arguments.String())
	summary := humanizedToolSummary(call.name, args, m.workspace)
	if toolSummaryPreparing(summary) {
		call.preparing = true
		return
	}
	call.preparing = false
	m.updateToolActivity(core.ToolCall{AgentID: call.agentID, ID: call.activityID, Name: call.name, Arguments: args, Started: true})
}

func (m *tuiModel) hasPreparingToolCall() bool {
	seen := make(map[*streamToolCall]bool)
	for _, call := range m.streamToolCalls {
		if call == nil || seen[call] {
			continue
		}
		seen[call] = true
		if call.preparing && !call.finished && !call.hidden {
			return true
		}
	}
	return false
}

func (m *tuiModel) finishUnidentifiedStreamToolCalls(requestID string) {
	seen := make(map[*streamToolCall]bool)
	for _, call := range m.streamToolCalls {
		if call == nil || seen[call] || call.name != "" || call.finished {
			continue
		}
		seen[call] = true
		if requestID != "" && call.requestID != requestID {
			continue
		}
		call.preparing = false
		call.finished = true
	}
}

func (m *tuiModel) findOrCreateStreamToolCall(event core.Event) *streamToolCall {
	if m.streamToolCalls == nil {
		m.streamToolCalls = make(map[string]*streamToolCall)
	}
	aliases := streamToolAliases(event)
	var call *streamToolCall
	for _, alias := range aliases {
		candidate := m.streamToolCalls[alias]
		if candidate == nil {
			continue
		}
		if call == nil {
			call = candidate
		} else if candidate != call {
			call = m.mergeStreamToolCalls(call, candidate)
		}
	}
	if call == nil {
		call = m.streamToolCalls[streamPendingAlias(event)]
	}
	if call == nil {
		call = &streamToolCall{taskID: event.TaskID, agentID: event.AgentID}
	}
	m.updateStreamToolCallIdentity(call, event)
	for _, alias := range aliases {
		m.streamToolCalls[alias] = call
	}
	return call
}

func (m *tuiModel) updateStreamToolCallIdentity(call *streamToolCall, event core.Event) {
	stream := event.Stream
	if stream == nil {
		return
	}
	if call.taskID == "" {
		call.taskID = event.TaskID
	}
	if call.requestID == "" {
		call.requestID = stream.RequestID
	}
	if call.agentID == "" {
		call.agentID = event.AgentID
	}
	if call.executionID == "" {
		call.executionID = event.ExecutionID
	}
	if stream.ToolCallID != "" {
		call.id = stream.ToolCallID
	}
	if stream.ItemID != "" {
		call.itemID = stream.ItemID
	}
	if stream.OutputIndex != nil {
		index := *stream.OutputIndex
		call.outputIndex = &index
	}
	if call.activityID == "" {
		identity := ""
		switch {
		case call.id != "":
			identity = "call:" + call.id
		case call.itemID != "":
			identity = "item:" + call.itemID
		case call.outputIndex != nil:
			identity = "output:" + strconv.Itoa(*call.outputIndex)
		}
		if identity != "" {
			call.activityID = strings.Join([]string{"stream", call.taskID, call.requestID, call.agentID, identity}, ":")
		}
	}
	for _, alias := range streamToolAliases(event) {
		m.streamToolCalls[alias] = call
	}
}

func streamToolAliases(event core.Event) []string {
	if event.Stream == nil {
		return nil
	}
	stream := event.Stream
	scope := []string{event.TaskID, stream.RequestID, event.AgentID}
	aliases := make([]string, 0, 4)
	add := func(kind, value string) {
		if value == "" {
			return
		}
		aliases = append(aliases, strings.Join(append(append([]string(nil), scope...), kind, value), "\x00"))
	}
	add("call", stream.ToolCallID)
	add("item", stream.ItemID)
	if stream.OutputIndex != nil {
		add("output", strconv.Itoa(*stream.OutputIndex))
	}
	if len(aliases) == 0 {
		aliases = append(aliases, strings.Join(append(append([]string(nil), scope...), "pending"), "\x00"))
	}
	return aliases
}

func streamPendingAlias(event core.Event) string {
	if event.Stream == nil {
		return ""
	}
	return strings.Join([]string{event.TaskID, event.Stream.RequestID, event.AgentID, "pending"}, "\x00")
}

func (m *tuiModel) mergeStreamToolCalls(target, source *streamToolCall) *streamToolCall {
	if target == nil {
		return source
	}
	if source == nil || source == target {
		return target
	}
	if target.activityID == "" && source.activityID != "" {
		target, source = source, target
	}
	if target.name == "" {
		target.name = source.name
	}
	if target.id == "" {
		target.id = source.id
	}
	if target.itemID == "" {
		target.itemID = source.itemID
	}
	if target.outputIndex == nil {
		target.outputIndex = source.outputIndex
	}
	if target.executionID == "" {
		target.executionID = source.executionID
	}
	if target.arguments.Len() == 0 || strings.HasPrefix(source.arguments.String(), target.arguments.String()) {
		target.arguments.Reset()
		target.arguments.WriteString(source.arguments.String())
	} else if source.arguments.Len() > 0 && !strings.HasPrefix(target.arguments.String(), source.arguments.String()) {
		target.arguments.WriteString(source.arguments.String())
	}
	target.preparing = target.preparing || source.preparing
	target.finished = target.finished || source.finished
	target.hidden = target.hidden || source.hidden
	if target.activityID != "" && source.activityID != "" && target.activityID != source.activityID {
		m.rekeyToolActivity(source.activityID, target.activityID)
	}
	for alias, call := range m.streamToolCalls {
		if call == source {
			m.streamToolCalls[alias] = target
		}
	}
	return target
}

func (m *tuiModel) bindStreamToolActivityAliases(call *streamToolCall) {
	if call.activityID == "" {
		return
	}
	if m.toolActivityAliases == nil {
		m.toolActivityAliases = make(map[string]string)
	}
	m.bindToolActivityAlias(core.ToolCall{AgentID: call.agentID, ID: call.activityID}, call.activityID)
	for _, id := range []string{call.id, call.itemID} {
		if id == "" {
			continue
		}
		m.bindToolActivityAlias(core.ToolCall{AgentID: call.agentID, ExecutionID: call.executionID, ID: id}, call.activityID)
	}
}

func (m *tuiModel) removeStreamToolActivity(call *streamToolCall) {
	if call == nil || call.activityID == "" {
		return
	}
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.entries[index].role == "tool" && (m.entries[index].activityID == call.activityID || m.toolActivityAliases[m.entries[index].activityID] == call.activityID) {
			m.entries = append(m.entries[:index], m.entries[index+1:]...)
		}
	}
	for index := len(m.activities) - 1; index >= 0; index-- {
		if m.activities[index].callID == call.activityID {
			m.activities = append(m.activities[:index], m.activities[index+1:]...)
		}
	}
}

func hiddenModelToolCall(name string) bool {
	switch name {
	case "select_tool_category", "ask_user", "delegate_team", "propose_workspace_memories":
		return true
	default:
		return false
	}
}

func toolSummaryPreparing(summary string) bool {
	return summary == "Tool request" || strings.Contains(summary, "(preparing)")
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
	m.streamToolCalls = nil
}
