package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func normalizeAgentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "lead" {
		return "lead"
	}
	return id
}

func (m *tuiModel) ensureAgentMetrics(agentID string) *agentRuntimeMetrics {
	if m.agentMetrics == nil {
		m.agentMetrics = make(map[string]*agentRuntimeMetrics)
	}
	id := normalizeAgentID(agentID)
	stats := m.agentMetrics[id]
	if stats == nil {
		name, role := id, "worker"
		status := "Idle"
		if id == "lead" {
			name, role, status = "Main agent", "main", "Idle"
		}
		stats = &agentRuntimeMetrics{ID: id, Name: name, Role: role, Status: status}
		m.agentMetrics[id] = stats
	}
	return stats
}

func (m *tuiModel) modelProfileContextWindow(profileID string) int64 {
	if m.client == nil || strings.TrimSpace(profileID) == "" {
		return 0
	}
	for _, profile := range m.client.ListModelProfiles() {
		if strings.EqualFold(profile.Profile, strings.TrimSpace(profileID)) {
			return profile.ContextWindow
		}
	}
	return 0
}

func (m *tuiModel) resolvedAgentContextWindow(profileID string, reported int64) int64 {
	if reported > 0 {
		return reported
	}
	if strings.TrimSpace(profileID) == "" {
		profileID = m.currentModelProfile
	}
	if window := m.modelProfileContextWindow(profileID); window > 0 {
		return window
	}
	if m.usage.contextWindow > 0 {
		return m.usage.contextWindow
	}
	if m.client != nil {
		return m.client.ProjectContextDefaults().ContextWindow
	}
	return 0
}

func (m *tuiModel) applyAgentModelContext(stats *agentRuntimeMetrics, profileID string, reported int64) {
	if profileID != "" && !strings.EqualFold(stats.ModelProfile, profileID) {
		stats.ModelProfile = profileID
		stats.ContextWindow = 0
	}
	if reported > 0 {
		stats.ContextWindow = reported
	}
	if stats.ContextWindow <= 0 {
		stats.ContextWindow = m.resolvedAgentContextWindow(stats.ModelProfile, 0)
	}
}

func (m *tuiModel) seedAgentMetrics(saved core.Session) {
	m.agentMetrics = make(map[string]*agentRuntimeMetrics, len(saved.AgentUsage)+1)
	for agentID, totals := range saved.AgentUsage {
		stats := m.ensureAgentMetrics(agentID)
		stats.InputTokens = totals.Input
		stats.EstimatedInputTokens = totals.EstimatedInput
		stats.OutputTokens = totals.Output
		stats.CachedTokens = totals.Cached
		stats.ReasoningTokens = totals.Reasoning
	}
	if len(saved.AgentUsage) == 0 && (saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0) {
		stats := m.ensureAgentMetrics("lead")
		stats.InputTokens = saved.SessionUsage.Input
		stats.OutputTokens = saved.SessionUsage.Output
		stats.CachedTokens = saved.SessionUsage.Cached
		stats.ReasoningTokens = saved.SessionUsage.Reasoning
	}
	for _, record := range saved.UsageRequests {
		stats := m.ensureAgentMetrics(record.AgentID)
		m.applyAgentModelContext(stats, record.ModelProfile, record.ContextWindow)
		if record.Provider != "" {
			stats.Provider = record.Provider
		}
		if record.Model != "" {
			stats.Model = record.Model
		}
		if record.Usage.InputTokens > 0 || record.EstimatedInputTokens > 0 {
			stats.ContextInputTokens = record.Usage.InputTokens
			if stats.ContextInputTokens <= 0 {
				stats.ContextInputTokens = record.EstimatedInputTokens
			}
		}
	}
	for i := range m.entries {
		entry := &m.entries[i]
		if entry.Kind != KindSubagent {
			continue
		}
		stats := m.ensureAgentMetrics(entry.AgentID)
		if entry.AgentName != "" {
			stats.Name = entry.AgentName
		}
		if entry.AgentRole != "" {
			stats.Role = entry.AgentRole
		}
		stats.Task = entry.AgentTask
		stats.ToolCount = entry.SubagentTools
		stats.Activity = entry.SubagentPreview
		if entry.SubagentRunning {
			stats.Status = "Running"
		} else if strings.TrimSpace(entry.Text) != "" || entry.SubagentPreview == "Completed" {
			stats.Status = "Completed"
		}
	}
	main := m.ensureAgentMetrics("lead")
	main.Name, main.Role = "Main agent", "main"
	main.ModelProfile = m.currentModelProfile
	main.ContextWindow = m.usage.contextWindow
	if main.ContextWindow <= 0 {
		main.ContextWindow = m.modelProfileContextWindow(m.currentModelProfile)
	}
	if m.running {
		main.Status = "Running"
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser {
			main.Task = compactAgentTask(m.entries[i].Text)
			break
		}
	}
}

func (m *tuiModel) updateAgentEvent(event core.Event, started bool) {
	if normalizeAgentID(event.AgentID) == "lead" {
		return
	}
	stats := m.ensureAgentMetrics(event.AgentID)
	if event.AgentName != "" {
		stats.Name = event.AgentName
	}
	if event.AgentRole != "" {
		stats.Role = event.AgentRole
	}
	if event.AgentTask != "" {
		stats.Task = compactAgentTask(event.AgentTask)
	}
	m.applyAgentModelContext(stats, event.ModelProfile, event.ContextWindow)
	if event.Provider != "" {
		stats.Provider = event.Provider
	}
	if event.Model != "" {
		stats.Model = event.Model
	}
	if started {
		stats.Status = "Running"
		stats.Activity = "Starting"
		stats.StartedAt = time.Now()
		stats.StreamStartedAt = time.Time{}
		stats.StreamGenerationTokens = 0
		stats.TokensPerSecond = 0
		stats.Duration = 0
	} else {
		stats.Status = string(event.Status)
		if stats.Status == "" || stats.Status == string(core.TaskCompleted) {
			stats.Status = "Completed"
		} else if stats.Status == string(core.TaskFailed) {
			stats.Activity = "Failed: " + cleanToolInline(event.Err)
		} else if stats.Status == string(core.TaskCancelled) {
			stats.Activity = "Cancelled"
		}
		if event.Err != "" && stats.Status != "Completed" {
			stats.Activity = "Failed: " + cleanToolInline(event.Err)
		}
		if !stats.StartedAt.IsZero() {
			stats.Duration = time.Since(stats.StartedAt)
		}
	}
}

func compactAgentTask(task string) string {
	task = strings.Join(strings.Fields(task), " ")
	return truncateString(task, 220)
}

func (m *tuiModel) recordAgentUsage(event core.Event) {
	stats := m.ensureAgentMetrics(event.AgentID)
	stats.InputTokens += event.Usage.InputTokens
	stats.OutputTokens += event.Usage.OutputTokens
	stats.CachedTokens += event.Usage.InputTokensDetails.CachedTokens
	stats.ReasoningTokens += event.Usage.OutputTokensDetails.ReasoningTokens
	stats.EstimatedInputTokens = event.EstimatedInputTokens
	stats.ContextInputTokens = event.Usage.InputTokens
	if stats.ContextInputTokens <= 0 {
		stats.ContextInputTokens = event.EstimatedInputTokens
	}
	m.applyAgentModelContext(stats, event.ModelProfile, event.ContextWindow)
	if event.Provider != "" {
		stats.Provider = event.Provider
	}
	if event.Model != "" {
		stats.Model = event.Model
	}
}

func (m *tuiModel) recordAgentStream(event core.Event) {
	if event.Stream == nil {
		return
	}
	stats := m.ensureAgentMetrics(event.AgentID)
	if event.AgentName != "" && stats.ID != "lead" {
		stats.Name = event.AgentName
	}
	m.applyAgentModelContext(stats, event.ModelProfile, 0)
	if event.Provider != "" {
		stats.Provider = event.Provider
	}
	if event.Model != "" {
		stats.Model = event.Model
	}
	if event.Stream.Type == "reasoning_delta" {
		stats.Activity = "Thinking"
	} else if event.Stream.Type == "text_delta" || event.Stream.Type == "" {
		stats.Activity = "Generating response"
	}
	if event.Stream.Delta != "" {
		if stats.StreamStartedAt.IsZero() {
			stats.StreamStartedAt = time.Now()
			stats.StreamGenerationTokens = 0
		}
		stats.StreamGenerationTokens += estimateDeltaTokens(event.Stream.Delta)
		elapsed := time.Since(stats.StreamStartedAt).Seconds()
		if elapsed >= 0.05 {
			stats.TokensPerSecond = float64(stats.StreamGenerationTokens) / elapsed
		}
	}
}

func (m *tuiModel) recordAgentToolStarted(agentID, toolName string) {
	stats := m.ensureAgentMetrics(agentID)
	stats.ToolCount++
	stats.Activity = "Running " + toolName
	if stats.ID != "lead" {
		stats.Status = "Running"
	}
	stats.StreamStartedAt = time.Time{}
	stats.StreamGenerationTokens = 0
}

func (m *tuiModel) recordAgentToolFinished(agentID, toolName string, errText string) {
	stats := m.ensureAgentMetrics(agentID)
	if errText != "" {
		stats.Activity = "Failed " + toolName + ": " + cleanToolInline(errText)
	} else {
		stats.Activity = "Finished " + toolName
	}
}

func (m *tuiModel) agentMetricIDs() []string {
	ids := []string{"lead"}
	seen := map[string]bool{"lead": true}
	for _, entry := range m.entries {
		if entry.Kind != KindSubagent {
			continue
		}
		id := normalizeAgentID(entry.AgentID)
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	var extra []string
	for id := range m.agentMetrics {
		id = normalizeAgentID(id)
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	return append(ids, extra...)
}

func (m *tuiModel) agentMetric(id string) *agentRuntimeMetrics {
	stats := m.ensureAgentMetrics(id)
	if stats.ID == "lead" {
		stats.Name, stats.Role = "Main agent", "main"
		if m.currentModelProfile != "" {
			stats.ModelProfile = m.currentModelProfile
		}
		if m.currentModel != "" {
			stats.Model = m.currentModel
		}
		if m.usage.contextWindow > 0 {
			stats.ContextWindow = m.usage.contextWindow
		} else if stats.ContextWindow <= 0 {
			stats.ContextWindow = m.modelProfileContextWindow(m.currentModelProfile)
		}
		if stats.ModelProfile == "" {
			stats.ModelProfile = m.currentModelProfile
		}
		if m.running {
			stats.Status = "Running"
		}
	}
	return stats
}

func (m *tuiModel) openAgentsPanel(target string) bool {
	ids := m.agentMetricIDs()
	m.modals.open()
	m.modals.agents.open = true
	m.modals.agents.detail = false
	m.modals.agents.detailID = ""
	m.modals.agents.selected = 0
	if target != "" {
		for i, id := range ids {
			stats := m.agentMetric(id)
			if strings.EqualFold(target, id) || strings.EqualFold(target, stats.Name) {
				m.modals.agents.selected = i
				m.modals.agents.detail = true
				m.modals.agents.detailID = id
				return true
			}
		}
		if number, err := strconv.Atoi(target); err == nil {
			if number >= 1 && number <= len(ids) {
				m.modals.agents.selected = number - 1
				m.modals.agents.detail = true
				m.modals.agents.detailID = ids[number-1]
				return true
			}
		}
		m.modals.agents.open = false
		return false
	}
	return true
}

func (m *tuiModel) closeAgentsPanel() {
	m.modals.agents.open = false
	m.modals.agents.detail = false
	m.modals.agents.detailID = ""
	m.updateLayout()
}

func (m *tuiModel) renderAgentsPanel(width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	ids := m.agentMetricIDs()
	var lines []string
	if m.modals.agents.detail {
		stats := m.agentMetric(m.modals.agents.detailID)
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render("Agent details · "+stats.Name))
		lines = append(lines, styleTextMuted().Render("ID: "+stats.ID+" · Role: "+displayDash(stats.Role)+" · Status: "+displayDash(stats.Status)))
		model := stats.ModelProfile
		if stats.Model != "" && stats.Model != model {
			model += " (" + stats.Model + ")"
		}
		lines = append(lines, "Model: "+displayDash(model))
		lines = append(lines, "Task: "+displayDash(sanitizeAndTruncateLine(stats.Task, max(12, width-12))))
		lines = append(lines, "Activity: "+displayDash(sanitizeAndTruncateLine(stats.Activity, max(12, width-16))))
		lines = append(lines, "Context: "+agentContextLabel(stats))
		lines = append(lines, "Cache: "+agentCacheLabel(stats)+" · Speed: "+agentSpeedLabel(stats))
		lines = append(lines, fmt.Sprintf("Tokens: in %s · out %s · total %s",
			formatTokens(stats.InputTokens), formatTokens(stats.OutputTokens), formatTokens(stats.InputTokens+stats.OutputTokens)))
		lines = append(lines, fmt.Sprintf("Tools: %d · Duration: %s", stats.ToolCount, agentDurationLabel(stats)))
		if entry := m.agentTranscript(stats.ID); entry != nil && strings.TrimSpace(entry.Text) != "" {
			lines = append(lines, "Output: "+sanitizeAndTruncateLine(strings.Join(strings.Fields(entry.Text), " "), max(12, width-14)))
		}
		lines = append(lines, "")
		lines = append(lines, styleTextMuted().Render("backspace/← list · esc close"))
	} else {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render(fmt.Sprintf("Agents · %d", len(ids))))
		lines = append(lines, styleTextMuted().Render("Select an agent to inspect its live context and usage."))
		lines = append(lines, "")
		maxVisible := 6
		start := 0
		if m.modals.agents.selected >= maxVisible {
			start = m.modals.agents.selected - maxVisible + 1
		}
		end := min(len(ids), start+maxVisible)
		availWidth := max(20, width-4)
		for i := start; i < end; i++ {
			stats := m.agentMetric(ids[i])
			name := stats.Name
			if name == "" {
				name = stats.ID
			}
			status := strings.ToLower(stats.Status)
			activity := sanitizeAndTruncateLine(stats.Activity, max(12, availWidth-24-lipgloss.Width(name)))
			row := fmt.Sprintf("%s%s · %s · %s", map[bool]string{true: "› ", false: "  "}[i == m.modals.agents.selected], name, status, activity)
			row = sanitizeAndTruncateLine(row, availWidth)
			statsLine := fmt.Sprintf("   ctx %s · cache %s · %s · total %s",
				agentContextLabel(stats), agentCachePercentLabel(stats), agentSpeedLabel(stats), formatTokens(stats.InputTokens+stats.OutputTokens))
			statsLine = sanitizeAndTruncateLine(statsLine, availWidth)
			if i == m.modals.agents.selected {
				lines = append(lines, styleSelectedRow().Render(row))
				lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Render(statsLine))
			} else {
				lines = append(lines, row, styleTextMuted().Render(statsLine))
			}
		}
		if len(ids) == 0 {
			lines = append(lines, styleTextMuted().Render("No agents in this session."))
		}
		lines = append(lines, "")
		lines = append(lines, styleTextMuted().Render("↑/↓ navigate · enter details · esc close"))
	}
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func (m *tuiModel) agentTranscript(id string) *TranscriptEntry {
	id = normalizeAgentID(id)
	for i := range m.entries {
		if m.entries[i].Kind == KindSubagent && normalizeAgentID(m.entries[i].AgentID) == id {
			return &m.entries[i]
		}
	}
	return nil
}

func displayDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func agentContextLabel(stats *agentRuntimeMetrics) string {
	used := stats.ContextInputTokens
	if used <= 0 {
		used = stats.EstimatedInputTokens
	}
	usedText := "unknown"
	if used > 0 {
		usedText = formatTokens(used)
	}
	if stats.ContextWindow <= 0 {
		return usedText + "/unknown"
	}
	if used <= 0 {
		return "unknown/" + formatTokens(stats.ContextWindow)
	}
	pct := float64(used) / float64(stats.ContextWindow) * 100
	return fmt.Sprintf("%s/%s (%.0f%%)", formatTokens(used), formatTokens(stats.ContextWindow), pct)
}

func agentCachePercentLabel(stats *agentRuntimeMetrics) string {
	if stats.InputTokens <= 0 {
		return "—"
	}
	pct := float64(stats.CachedTokens) / float64(stats.InputTokens) * 100
	return fmt.Sprintf("%.0f%%", pct)
}

func agentCacheLabel(stats *agentRuntimeMetrics) string {
	if stats.InputTokens <= 0 {
		return "unknown"
	}
	pct := float64(stats.CachedTokens) / float64(stats.InputTokens) * 100
	return fmt.Sprintf("%s/%s (%.1f%% hit)", formatTokens(stats.CachedTokens), formatTokens(stats.InputTokens), pct)
}

func agentSpeedLabel(stats *agentRuntimeMetrics) string {
	if stats.TokensPerSecond <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f tok/s", stats.TokensPerSecond)
}

func agentDurationLabel(stats *agentRuntimeMetrics) string {
	duration := stats.Duration
	if stats.Status == "Running" && !stats.StartedAt.IsZero() {
		duration = time.Since(stats.StartedAt)
	}
	if duration <= 0 {
		return "—"
	}
	return formatDuration(duration)
}
