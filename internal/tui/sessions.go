package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) openSessionPicker() (tea.Model, tea.Cmd) {
	if m.running || len(m.queued) > 0 {
		m.status = "Interrupt the active turn and clear queued work before switching sessions."
		return m, nil
	}
	m.showArchived = false
	if err := m.reloadSessionList(m.sessionID); err != nil {
		m.status = "Could not load sessions."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(m.sessionList) == 0 {
		m.sessionPicker = true
		m.status = "No active sessions; press " + m.keyLabel("session_toggle_archived") + " to show archived sessions."
		m.resetInput()
		m.updateLayout()
		return m, nil
	}
	m.sessionPicker = true
	m.status = "Choose a session · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " restore · " + m.keyLabel("session_archive") + " archive · " + m.keyLabel("session_rename") + " rename · " + m.keyLabel("session_delete") + " delete · " + m.keyLabel("session_toggle_archived") + " archived."
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) reloadSessionList(preferredID string) error {
	sessions, err := m.client.ListSessions(context.Background(), 30, m.showArchived)
	if err != nil {
		return err
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	m.sessionList = sessions
	if len(sessions) == 0 {
		m.sessionIndex = 0
		return nil
	}
	m.sessionIndex = min(max(m.sessionIndex, 0), len(sessions)-1)
	for index, saved := range sessions {
		if saved.ID == preferredID {
			m.sessionIndex = index
			return nil
		}
	}
	return nil
}

func (m *tuiModel) selectSession() (tea.Model, tea.Cmd) {
	if m.renamingSession {
		return m.saveSessionRename()
	}
	if len(m.sessionList) == 0 {
		m.status = "No session to restore; press Tab to show archived sessions."
		return m, nil
	}
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.sessionPicker = false
		return m, nil
	}
	if m.running || len(m.queued) > 0 {
		m.status = "Interrupt the active turn and clear queued work before switching sessions."
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
		m.sessionPicker = false
		m.status = "Could not open the selected session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(saved)
	m.planReview = false
	m.sessionPicker = false
	m.status = "Session restored."
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	commands := []tea.Cmd{m.subscribeRuntime()}
	if m.running && m.runningExecutionID != "" {
		commands = append(commands, waitForTurn(m.runtime, m.runningExecutionID))
	}
	if m.running {
		commands = append(commands, m.startWindowTitleAnimation())
	}
	return m, tea.Batch(commands...)
}

func (m *tuiModel) beginSessionRename() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		return m, nil
	}
	m.renamingSession = true
	m.input.Placeholder = "Session title · empty clears"
	m.setInputValue(m.sessionList[m.sessionIndex].Title)
	m.status = "Edit title · Enter save · empty clears · Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) saveSessionRename() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.renamingSession = false
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	title := m.input.Value()
	if err := m.client.SetSessionTitle(context.Background(), saved.ID, title); err != nil {
		m.status = "Session title could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	saved.Title = title
	if saved.ID == m.sessionID {
		m.setSessionWindowTitle(saved)
	}
	m.renamingSession = false
	m.input.Placeholder = m.composerPlaceholder()
	m.resetInput()
	if err := m.reloadSessionList(saved.ID); err != nil {
		m.status = "Title saved, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.status = "Session title saved."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) toggleSelectedArchive() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	archived := !saved.Archived
	if err := m.client.SetSessionArchived(context.Background(), saved.ID, archived); err != nil {
		m.status = "Session archive state could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.reloadSessionList(saved.ID); err != nil {
		m.status = "Archive state saved, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if archived {
		m.status = "Session archived."
	} else {
		m.status = "Session unarchived."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) deleteSelectedSession() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.deleteConfirm = false
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	if err := m.client.DeleteSession(context.Background(), saved.ID); err != nil {
		m.status = "Session could not be deleted."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.deleteConfirm = false
	if saved.ID == m.sessionID {
		m.sessionPicker = false
		updated, command := m.newSession()
		if finished, ok := updated.(*tuiModel); ok {
			if finished.sessionID == saved.ID {
				finished.sessionID = ""
				finished.showArchived = false
				finished.sessionPicker = true
				if err := finished.reloadSessionList(""); err != nil {
					finished.addEntry("error", err.Error(), true)
				}
				finished.status = "Session deleted, but a new session could not be created."
				finished.updateLayout()
			} else {
				finished.status = "Current session deleted; started a new session."
			}
		}
		return updated, command
	}
	if err := m.reloadSessionList(m.sessionID); err != nil {
		m.status = "Session deleted, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(m.sessionList) == 0 {
		m.sessionPicker = false
		m.status = "Session deleted."
	} else {
		m.status = "Session deleted."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) newSession() (tea.Model, tea.Cmd) {
	saved, err := m.client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		m.status = "Could not create a session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
		m.status = "Session was created, but its runtime could not be opened."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.setSessionWindowTitle(saved)
	m.question = nil
	m.questionOpen = false
	m.pendingRequest = nil
	m.finishedTasks = nil
	m.sessionSettings = saved.Settings
	m.clearQueue()
	m.entries = nil
	m.activities = nil
	m.activityExpanded = make(map[string]bool)
	m.clearTranscriptSelection()
	m.unreadEntries = 0
	m.searchOpen = false
	m.searchQuery = ""
	m.searchMatches = nil
	m.searchExpandedState = nil
	m.usageRequests = nil
	m.agentUsage = make(map[string]agentUsageSummary)
	m.agentUsageComplete = saved.AgentUsageComplete
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	m.contextWindow = 0
	if saved.Settings.ContextWindow != nil {
		m.contextWindow = *saved.Settings.ContextWindow
	}
	m.contextInput = 0
	m.contextEstimated = false
	m.taskUsage = tokenUsageTotals{}
	m.sessionUsage = tokenUsageTotals{}
	m.planReview = false
	m.pending = false
	m.status = "New session."
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, m.subscribeRuntime()
}

func (m *tuiModel) forkSession() (tea.Model, tea.Cmd) {
	branch, err := m.client.ForkSession(context.Background(), m.sessionID)
	if err != nil {
		m.status = "Could not fork this session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.bindSessionRuntime(context.Background(), branch.ID); err != nil {
		m.status = "Session was forked, but its runtime could not be opened."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(branch)
	m.clearQueue()
	m.planReview = false
	m.pending = false
	m.status = "Forked session history into a new session."
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, m.subscribeRuntime()
}

func (m *tuiModel) rewindSession(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /rewind <n> to remove the latest n user turns."
		return m, nil
	}
	turns, err := strconv.Atoi(fields[1])
	if err != nil || turns <= 0 {
		m.status = "Rewind count must be a positive integer."
		return m, nil
	}
	removed, err := m.client.RewindSession(context.Background(), m.sessionID, turns)
	if err != nil {
		m.status = "Could not rewind this session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	saved, err := m.client.GetSession(context.Background(), m.sessionID)
	if err != nil {
		m.status = "History was rewound, but the session could not be reloaded."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(saved)
	m.status = fmt.Sprintf("Rewound %d user turn(s).", removed)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) restoreSessionState(saved core.Session) {
	m.setSessionWindowTitle(saved)
	m.question = nil
	m.questionOpen = false
	m.pendingRequest = nil
	m.finishedTasks = nil
	m.sessionSettings = saved.Settings
	m.entries = transcriptEntries(saved.History, m.workspace)
	m.activities = nil
	m.activityExpanded = make(map[string]bool)
	m.searchOpen = false
	m.searchQuery = ""
	m.searchMatches = nil
	m.searchExpandedState = nil
	m.clearTranscriptSelection()
	m.activityFocus = false
	m.unreadEntries = 0
	m.teamActivities = nil
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	m.contextWindow = 0
	m.contextInput = 0
	m.contextEstimated = false
	if saved.Settings.ContextWindow != nil {
		m.contextWindow = *saved.Settings.ContextWindow
	}
	m.usageRequests = make([]tokenUsageRecord, 0, len(saved.UsageRequests))
	for _, record := range saved.UsageRequests {
		m.usageRequests = append(m.usageRequests, tokenUsageRecord{
			request: record.Request, usage: record.Usage, available: record.Available,
			agentID: record.AgentID, profile: record.ModelProfile, provider: record.Provider, model: record.Model,
			contextWindow: record.ContextWindow, estimatedInputTokens: record.EstimatedInputTokens,
		})
	}
	m.agentUsage = make(map[string]agentUsageSummary, len(saved.AgentUsage))
	for agentID, totals := range saved.AgentUsage {
		m.agentUsage[agentID] = usageSummaryFromTotals(totals)
	}
	m.agentUsageComplete = saved.AgentUsageComplete
	for index := len(m.usageRequests) - 1; index >= 0; index-- {
		record := m.usageRequests[index]
		if record.agentID != "" && record.agentID != "lead" {
			continue
		}
		if record.contextWindow > 0 {
			m.contextWindow = record.contextWindow
			if record.usage.InputTokens > 0 {
				m.contextInput = record.usage.InputTokens
				m.contextEstimated = false
			} else if record.estimatedInputTokens > 0 {
				m.contextInput = record.estimatedInputTokens
				m.contextEstimated = true
			} else {
				m.contextInput = 0
				m.contextEstimated = false
			}
		} else if saved.Settings.ContextWindow == nil {
			m.contextWindow = 0
			m.contextInput = 0
			m.contextEstimated = false
		}
		break
	}
	m.taskUsage = loadUsageTotals(saved.TaskUsage)
	m.sessionUsage = loadUsageTotals(saved.SessionUsage)
}

func loadUsageTotals(totals core.UsageTotals) tokenUsageTotals {
	return tokenUsageTotals{requests: totals.Requests, measured: totals.Measured, input: totals.Input, estimatedInput: totals.EstimatedInput,
		output: totals.Output, reasoning: totals.Reasoning, total: totals.Total, cached: totals.Cached}
}

func transcriptFromHistory(history json.RawMessage, workspace ...string) []transcriptEntry {
	items := rawHistoryItems(history)
	var entries []transcriptEntry
	turn := 0
	for _, item := range items {
		var value struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &value) != nil {
			continue
		}
		switch value.Type {
		case "message":
			role := "notice"
			if value.Role == "user" {
				turn++
				role = "you"
			} else if value.Role == "assistant" {
				role = "assistant"
			}
			if text := messageContentText(value.Content); text != "" {
				entries = append(entries, transcriptEntry{Role: role, Text: text})
			}
		case "function_call":
			arguments := value.Arguments
			if len(arguments) > 0 && arguments[0] == '"' {
				var text string
				_ = json.Unmarshal(arguments, &text)
				arguments = json.RawMessage(text)
			}
			entries = append(entries, transcriptEntry{
				Role: "tool", Text: humanizedToolSummary(value.Name, arguments, workspace...) + " · done",
				ActivityGroupID: "history-turn:" + strconv.Itoa(turn),
			})
		}
	}
	return entries
}

func transcriptEntries(history json.RawMessage, workspace ...string) []tuiEntry {
	saved := transcriptFromHistory(history, workspace...)
	entries := make([]tuiEntry, 0, len(saved))
	for _, entry := range saved {
		entries = append(entries, tuiEntry{role: entry.Role, text: entry.Text, activityGroupID: entry.ActivityGroupID})
	}
	return entries
}

func rawHistoryItems(history json.RawMessage) []json.RawMessage {
	if len(history) == 0 || string(history) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(history, &text) == nil {
		encoded, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": text})
		return []json.RawMessage{encoded}
	}
	var items []json.RawMessage
	_ = json.Unmarshal(history, &items)
	return items
}

func sessionPreview(history json.RawMessage) string {
	for _, entry := range transcriptFromHistory(history) {
		if entry.Role != "you" {
			continue
		}
		preview := strings.Join(strings.Fields(entry.Text), " ")
		if len([]rune(preview)) > 44 {
			preview = string([]rune(preview)[:41]) + "..."
		}
		if preview != "" {
			return preview
		}
	}
	return "empty session"
}

func messageContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return "[multimodal content]"
	}
	var out strings.Builder
	images := 0
	for _, raw := range parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &part)
		switch part.Type {
		case "input_text", "output_text":
			out.WriteString(part.Text)
		case "input_image":
			images++
			if strings.Count(out.String(), "[Image #") < images {
				out.WriteString("[image]")
			}
		case "reasoning_text":
		default:
			out.WriteString("[multimodal content]")
		}
	}
	return out.String()
}

func inputTranscriptText(input core.TaskInput, fallback string) string {
	if len(input.Raw) == 0 {
		return fallback
	}
	var text string
	if json.Unmarshal(input.Raw, &text) == nil {
		return text
	}
	var items []json.RawMessage
	if json.Unmarshal(input.Raw, &items) != nil {
		return "[structured input]"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		var value struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(item, &value)
		if value.Type == "message" {
			if content := messageContentText(value.Content); content != "" {
				parts = append(parts, content)
			} else {
				parts = append(parts, "[multimodal content]")
			}
		} else if value.Type == "function_call" {
			parts = append(parts, value.Name+"(tool call)")
		} else {
			parts = append(parts, "[structured input item]")
		}
	}
	if len(parts) == 0 {
		return "[structured input]"
	}
	return strings.Join(parts, "\n")
}

func sessionDisplayTitle(saved core.Session) string {
	if title := strings.TrimSpace(saved.Title); title != "" {
		return title
	}
	return sessionPreview(saved.History)
}
