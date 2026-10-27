package tui

import (
	"fmt"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) markRunningSubagentsAndToolsDone() {
	m.settleAllSubagentStreams()
	for i := range m.entries {
		changed := false
		if m.entries[i].Kind == KindTool && m.entries[i].ToolRunning {
			m.entries[i].ToolRunning = false
			changed = true
		}
		if m.entries[i].Kind == KindSubagent && m.entries[i].SubagentRunning {
			m.entries[i].SubagentRunning = false
			changed = true
		}
		if changed {
			m.view.invalidateEntry(i)
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
}

func (m *tuiModel) settleSubagentStream(agentID string) {
	agentID = strings.TrimSpace(agentID)
	state := m.run.finishSubagentStream(agentID)
	if state == nil {
		return
	}
	idx := state.entryIndex
	if !m.validSubagentEntryIndex(idx, agentID) {
		idx = m.findSubagentEntryIndex(agentID)
	}
	if idx >= 0 {
		m.entries[idx].Text = state.text.String()
		if preview := latestSubagentPreview(m.entries[idx].Text); preview != "" {
			m.entries[idx].SubagentPreview = preview
		}
	}
	m.view.invalidateLive()
}

func (m *tuiModel) settleAllSubagentStreams() {
	if len(m.run.subagentStreams) == 0 {
		return
	}
	for agentID, state := range m.run.subagentStreams {
		i := state.entryIndex
		if !m.validSubagentEntryIndex(i, agentID) {
			i = m.findSubagentEntryIndex(agentID)
		}
		if i >= 0 {
			entry := &m.entries[i]
			entry.Text = state.text.String()
			if preview := latestSubagentPreview(entry.Text); preview != "" {
				entry.SubagentPreview = preview
			}
			if entry.SubagentRunning {
				entry.SubagentRunning = false
				if entry.SubagentDuration == 0 && !entry.Timestamp.IsZero() {
					entry.SubagentDuration = time.Since(entry.Timestamp)
				}
			}
			m.view.invalidateEntry(i)
		}
		delete(m.run.subagentStreams, agentID)
	}
	m.run.subagentStreamOrder = nil
	m.view.invalidateLive()
}

func (m *tuiModel) findSubagentEntryIndex(agentID string) int {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			return i
		}
	}
	return -1
}

func (m *tuiModel) validSubagentEntryIndex(index int, agentID string) bool {
	return index >= 0 && index < len(m.entries) && m.entries[index].Kind == KindSubagent && m.entries[index].AgentID == agentID
}

func latestSubagentPreview(text string) string {
	for end := len(text); end > 0; {
		start := strings.LastIndex(text[:end], "\n") + 1
		line := strings.TrimSpace(text[start:end])
		if line != "" && !strings.HasPrefix(line, "```") {
			return line
		}
		if start == 0 {
			break
		}
		end = start - 1
	}
	return ""
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
			m.view.invalidateEntry(i)
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
	m.view.invalidateContent()
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
			if !m.entries[i].SubagentRunning {
				// Restart the live elapsed clock when a finished subagent wakes up again.
				m.entries[i].Timestamp = time.Now()
			}
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
			m.view.invalidateEntry(i)
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
	m.view.invalidateContent()
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
			m.view.invalidateEntry(i)
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
	state := m.run.subagentStreams[agentID]
	idx := -1
	if state != nil && m.validSubagentEntryIndex(state.entryIndex, agentID) {
		idx = state.entryIndex
	} else {
		idx = m.findSubagentEntryIndex(agentID)
	}
	created := idx < 0
	if created {
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
	wasRunning := entry.SubagentRunning
	entry.SubagentRunning = true
	if event.AgentName != "" {
		entry.AgentName = event.AgentName
	}
	if event.AgentRole != "" {
		entry.AgentRole = event.AgentRole
	}
	state = m.run.appendSubagentDelta(agentID, displayName, entry.Text, delta)
	state.entryIndex = idx
	entry.Text = state.text.String()
	if created {
		m.view.invalidateContent()
	} else if !wasRunning {
		m.view.invalidateEntry(idx)
	}
	m.view.invalidateLive()
}
