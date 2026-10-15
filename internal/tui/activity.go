package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) updateContextCompaction(event core.Event) {
	if event.Compaction == nil {
		return
	}
	details := event.Compaction
	switch details.Stage {
	case "started":
		if details.Mode == "server" {
			m.status = "Provider is compacting the model context…"
		} else {
			m.status = "Compacting context: clearing older tool output and summarizing…"
		}
	case "completed":
		if details.AfterTokens > 0 {
			m.status = fmt.Sprintf("Context compacted: %s → %s estimated tokens.", compactTokens(details.BeforeTokens), compactTokens(details.AfterTokens))
		} else {
			m.status = "Provider compacted the context."
		}
	case "fallback":
		m.status = "Server compaction unavailable; using local compaction."
	case "skipped":
		if strings.Contains(strings.ToLower(event.Err), "contextwindow is unset") {
			m.status = "Auto-compaction needs a context window. Set it with /context window <tokens>."
		}
	case "failed":
		m.status = "Context compaction failed: " + event.Err
	}
}

func (m *tuiModel) updateModelUsage(usage core.Usage, agentID, profile, provider, model string, contextWindow, estimatedInputTokens int64) {
	available := usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.OutputTokensDetails.ReasoningTokens != 0 ||
		usage.TotalTokens != 0 || usage.InputTokensDetails.CachedTokens != 0
	requestNumber := m.sessionUsage.requests + 1
	estimatedInput := int64(0)
	if usage.InputTokens <= 0 && estimatedInputTokens > 0 {
		estimatedInput = estimatedInputTokens
	}
	record := tokenUsageRecord{
		request: requestNumber, usage: usage, available: available,
		agentID: agentID, profile: profile, provider: provider, model: model,
		contextWindow:        contextWindow,
		estimatedInputTokens: estimatedInput,
	}
	if agentID == "" || agentID == "lead" {
		m.contextWindow = contextWindow
		if usage.InputTokens > 0 {
			m.contextInput = usage.InputTokens
			m.contextEstimated = false
		} else if record.estimatedInputTokens > 0 {
			m.contextInput = record.estimatedInputTokens
			m.contextEstimated = true
		} else {
			m.contextInput = 0
			m.contextEstimated = false
		}
	}
	m.taskUsage.add(usage, available)
	m.sessionUsage.add(usage, available)
	m.taskUsage.estimatedInput += record.estimatedInputTokens
	m.sessionUsage.estimatedInput += record.estimatedInputTokens
	m.usageRequests = append(m.usageRequests, record)
	if len(m.usageRequests) > 200 {
		m.usageRequests = append([]tokenUsageRecord(nil), m.usageRequests[len(m.usageRequests)-200:]...)
	}
	if m.agentUsage == nil {
		m.agentUsage = make(map[string]agentUsageSummary)
	}
	summary := m.agentUsage[agentID]
	summary.requests++
	if available {
		summary.measured++
		summary.input += usage.InputTokens
		summary.output += usage.OutputTokens
		summary.reasoning += usage.OutputTokensDetails.ReasoningTokens
		summary.total += usage.TotalTokens
		summary.cached += usage.InputTokensDetails.CachedTokens
	}
	m.agentUsage[agentID] = summary
}

func (totals *tokenUsageTotals) add(usage core.Usage, available bool) {
	totals.requests++
	if !available {
		return
	}
	totals.measured++
	totals.input += usage.InputTokens
	totals.output += usage.OutputTokens
	totals.reasoning += usage.OutputTokensDetails.ReasoningTokens
	totals.total += usage.TotalTokens
	totals.cached += usage.InputTokensDetails.CachedTokens
}

func (record tokenUsageRecord) String() string {
	label := fmt.Sprintf("#%d", record.request)
	var attribution []string
	if record.agentID != "" {
		attribution = append(attribution, record.agentID)
	}
	if record.profile != "" && record.profile != record.agentID {
		attribution = append(attribution, "profile="+record.profile)
	}
	model := record.model
	if record.provider != "" {
		if model == "" {
			model = record.provider
		} else {
			model = record.provider + "/" + model
		}
	}
	if model != "" {
		attribution = append(attribution, model)
	}
	if len(attribution) > 0 {
		label += " · " + strings.Join(attribution, " · ")
	}
	if !record.available {
		label += " · usage unavailable"
		if record.estimatedInputTokens > 0 {
			label += " · input ~" + compactTokens(record.estimatedInputTokens)
		}
		return label
	}
	inputTokens := compactTokens(record.usage.InputTokens)
	if record.usage.InputTokens <= 0 && record.estimatedInputTokens > 0 {
		inputTokens = "~" + compactTokens(record.estimatedInputTokens)
	}
	return fmt.Sprintf("%s · in %s · out %s · reason %s · total %s · cached %s (%s)",
		label,
		inputTokens,
		compactTokens(record.usage.OutputTokens),
		compactTokens(record.usage.OutputTokensDetails.ReasoningTokens),
		compactTokens(record.usage.TotalTokens),
		compactTokens(record.usage.InputTokensDetails.CachedTokens),
		cacheHitRate(record.usage.InputTokens, record.usage.InputTokensDetails.CachedTokens),
	)
}

func (totals tokenUsageTotals) String(label string) string {
	if totals.requests == 0 {
		return fmt.Sprintf("%s: no model requests", label)
	}
	if totals.measured == 0 {
		if totals.estimatedInput > 0 {
			return fmt.Sprintf("%s: usage unavailable (%d request(s)); input estimate ~%s", label, totals.requests, compactTokens(totals.estimatedInput))
		}
		return fmt.Sprintf("%s: usage unavailable (%d request(s))", label, totals.requests)
	}
	result := fmt.Sprintf("%s · in %s · out %s · reason %s · total %s · cache %s (%s)",
		label,
		compactTokens(totals.input),
		compactTokens(totals.output),
		compactTokens(totals.reasoning),
		compactTokens(totals.total),
		compactTokens(totals.cached),
		cacheHitRate(totals.input, totals.cached),
	)
	if totals.estimatedInput > 0 {
		result += " · est input ~" + compactTokens(totals.estimatedInput)
	}
	return result
}

func compactTokens(tokens int64) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprint(tokens)
}

func activityBadge(status string) string {
	switch status {
	case "running":
		return "◉"
	case "done":
		return "✓"
	case "failed":
		return "×"
	case "cancelled":
		return "–"
	default:
		return "·"
	}
}

func cacheHitRate(inputTokens, cachedTokens int64) string {
	if inputTokens <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(cachedTokens)*100/float64(inputTokens))
}

func (m *tuiModel) updateToolActivity(event core.ToolCall) {
	summary := humanizedToolSummary(event.Name, event.Arguments, m.workspace)
	index := -1
	for i := len(m.activities) - 1; i >= 0; i-- {
		if event.ID != "" && m.activities[i].callID == event.ID {
			index = i
			break
		}
	}
	if event.Started {
		activity := tuiActivity{callID: event.ID, summary: summary, status: "running"}
		if index >= 0 {
			m.activities[index] = activity
		} else {
			m.activities = append(m.activities, activity)
			index = len(m.activities) - 1
		}
	} else {
		if index < 0 {
			m.activities = append(m.activities, tuiActivity{callID: event.ID, summary: summary})
			index = len(m.activities) - 1
		}
		activity := &m.activities[index]
		if summary != "" {
			activity.summary = summary
		}
		activity.duration = event.Duration
		switch {
		case event.Canceled:
			activity.status = "cancelled"
		case event.Failed:
			activity.status = "failed"
		default:
			activity.status = "done"
		}
	}

	activity := m.activities[index]
	entryText := activity.summary + " · " + activity.status
	if activity.status != "running" && activity.duration > 0 {
		entryText += " · " + activity.duration.Round(time.Millisecond).String()
	}
	entryIndex := -1
	if event.ID != "" {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].activityID == event.ID {
				entryIndex = i
				break
			}
		}
	}
	if activity.status != "running" && entryIndex < 0 {
		m.entries = append(m.entries, tuiEntry{
			role: "tool", text: entryText, err: activity.status == "failed", activityID: event.ID,
		})
	}
	if len(m.activities) > 200 {
		m.activities = append([]tuiActivity(nil), m.activities[len(m.activities)-200:]...)
	}
}

func humanizedToolSummary(name string, arguments json.RawMessage, workspace ...string) string {
	var args struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	_ = json.Unmarshal(arguments, &args)
	path := safeDisplayPath(args.Path)
	summary := "Working on a tool request"
	switch name {
	case "read_file":
		summary = summaryWithPath("Reading", path)
	case "write_file":
		summary = summaryWithPath("Writing", path)
	case "edit_file":
		summary = summaryWithPath("Editing", path)
	case "list_dir":
		summary = summaryWithPath("Listing", path)
	case "glob":
		summary = "Finding project files"
	case "grep":
		if path != "" {
			summary = "Searching " + path
		} else {
			summary = "Searching project files"
		}
	case "web_search":
		summary = "Searching the web"
	case "web_fetch":
		if parsed, err := url.Parse(args.URL); err == nil && parsed.Hostname() != "" {
			summary = "Fetching " + parsed.Hostname()
		} else {
			summary = "Fetching a web page"
		}
	case "command":
		summary = "Running a shell command"
	case "html":
		summary = "Processing web content"
	}
	if len(workspace) > 0 {
		summary = absoluteToolSummary(summary, workspace[0])
	}
	return summary
}

func safeDisplayPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(clean)
}

func summaryWithPath(action, path string) string {
	if path == "" {
		return action + " a workspace file"
	}
	return action + " " + path
}

func absoluteToolSummary(summary, workspace string) string {
	if workspace == "" {
		return summary
	}
	for _, prefix := range []string{"Reading ", "Writing ", "Editing ", "Listing ", "Searching "} {
		if !strings.HasPrefix(summary, prefix) {
			continue
		}
		relative := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
		if relative == "" || relative == "a workspace file" || relative == "project files" || filepath.IsAbs(relative) {
			return summary
		}
		absolute := filepath.Clean(filepath.Join(workspace, filepath.FromSlash(relative)))
		inside, err := filepath.Rel(workspace, absolute)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return summary
		}
		return prefix + filepath.ToSlash(absolute)
	}
	return summary
}

func cleanInlineText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\\r\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func (m *tuiModel) addEntry(role, text string, isError bool) {
	if role == "notice" && len(m.entries) > 0 {
		last := m.entries[len(m.entries)-1]
		if last.role == "notice" && last.text == text {
			return
		}
	}
	m.entries = append(m.entries, tuiEntry{role: role, text: text, err: isError})
	m.refreshPanels()
}

func (m *tuiModel) activityDetailsContent() string {
	colors := m.themeColors()
	var activities strings.Builder
	if len(m.activities) == 0 {
		activities.WriteString("No tool activity yet.")
	}
	for i, activity := range m.activities {
		if i > 0 {
			activities.WriteByte('\n')
		}
		icon := activityBadge(activity.status)
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Render(icon) + " " + activity.summary + " · " + activity.status
		if activity.status != "running" && activity.duration > 0 {
			line += "  " + activity.duration.Round(time.Millisecond).String()
		}
		activities.WriteString(line)
	}
	activities.WriteString("\n\nLead agent · " + stateName(m.visualState()))
	if m.modelStreamFirstTokenRecorded {
		activities.WriteString("\nFirst token · " + m.modelStreamFirstTokenLatency.Round(time.Millisecond).String())
	}
	if teamDetails := m.teamActivityDetails(colors); teamDetails != "" {
		activities.WriteString("\n\n" + teamDetails)
	}
	if contextLabel := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); contextLabel != "" {
		activities.WriteString("\n\nContext · " + contextLabel)
		if m.contextEstimated {
			activities.WriteString(" · estimate; image contents excluded")
		}
	}
	paths := m.recentWorkspacePaths(5)
	if len(paths) > 0 {
		activities.WriteString("\nRecent workspace paths")
		for _, path := range paths {
			activities.WriteString("\n  " + path)
		}
	}
	activities.WriteString("\n\nModel usage · input / output / reasoning / total / cached")
	if len(m.usageRequests) == 0 {
		activities.WriteString("\nNo model requests yet.")
	} else {
		for _, record := range m.usageRequests[max(0, len(m.usageRequests)-6):] {
			activities.WriteByte('\n')
			activities.WriteString(record.String())
		}
	}
	if byAgent := m.usageByAgentDetails(); byAgent != "" {
		activities.WriteString("\n\n")
		activities.WriteString(byAgent)
	}
	activities.WriteByte('\n')
	activities.WriteString(m.taskUsage.String("Task"))
	activities.WriteByte('\n')
	activities.WriteString(m.sessionUsage.String("Session"))
	activities.WriteString(fmt.Sprintf("\nMetered requests: task %d/%d · session %d/%d", m.taskUsage.measured, m.taskUsage.requests, m.sessionUsage.measured, m.sessionUsage.requests))
	if m.client != nil {
		activities.WriteString("\nPermissions: " + permissionModeLabel(m.currentPermissionMode()))
	}
	return activities.String()
}

func activityColor(status string, colors themeColors) string {
	switch status {
	case "running":
		return colors.Accent
	case "done":
		return colors.Success
	case "failed", "cancelled":
		return colors.Error
	default:
		return colors.Muted
	}
}

func (m *tuiModel) recentWorkspacePaths(limit int) []string {
	seen := make(map[string]struct{})
	var paths []string
	for index := len(m.activities) - 1; index >= 0 && len(paths) < limit; index-- {
		path := activityPath(m.activities[index].summary)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func activityPath(summary string) string {
	for _, prefix := range []string{"Reading ", "Writing ", "Editing ", "Listing ", "Searching "} {
		if strings.HasPrefix(summary, prefix) {
			path := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
			if filepath.IsAbs(path) {
				return path
			}
		}
	}
	return ""
}
