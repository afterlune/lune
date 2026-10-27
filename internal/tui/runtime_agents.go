package tui

import (
	"fmt"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) markRunningSubagentsAndToolsDone() {
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
	idx := -1
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindSubagent && m.entries[i].AgentID == agentID {
			idx = i
			break
		}
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
	entry.SubagentRunning = true
	if event.AgentName != "" {
		entry.AgentName = event.AgentName
	}
	if event.AgentRole != "" {
		entry.AgentRole = event.AgentRole
	}
	entry.Text += delta
	// Extract the latest non-empty line from entry.Text as a clean single-line status preview
	lines := strings.Split(strings.TrimSpace(entry.Text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "```") {
			entry.SubagentPreview = line
			break
		}
	}
	if created {
		m.view.invalidateContent()
	} else {
		m.view.invalidateEntry(idx)
	}
}
