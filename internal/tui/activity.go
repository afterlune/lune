package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
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
	if (event.Name == "command_status" || event.Name == "command_stop") && event.Details == nil {
		return
	}
	key := m.toolActivityKey(event)
	groupID := toolActivityGroupID(event, key, m.runningExecutionID, len(m.activities))
	index := -1
	for i := range m.activities {
		if key != "" && m.activities[i].callID == key {
			index = i
			break
		}
	}
	if index >= 0 && strings.HasPrefix(groupID, "call:") && m.activities[index].groupID != "" {
		groupID = m.activities[index].groupID
	}
	if index < 0 {
		index = len(m.activities)
		m.activities = append(m.activities, tuiActivity{callID: key, groupID: groupID, status: "running"})
	}
	activity := &m.activities[index]
	activity.cachedRender = ""
	if groupID != "" {
		activity.groupID = groupID
	}
	if len(event.Arguments) > 0 && string(event.Arguments) != "null" || activity.summary == "" {
		activity.summary = humanizedToolSummary(event.Name, event.Arguments, m.workspace)
	}
	if event.Details != nil {
		keepCurrent := false
		if activity.details != nil && activity.details.Command != nil && event.Details.Command != nil {
			previous, next := activity.details.Command, event.Details.Command
			// A start receipt can arrive after the final process notification.
			keepCurrent = !previous.Command.Status.Active() && next.Command.Status.Active() || next.EndCursor < previous.EndCursor
		}
		if !keepCurrent {
			activity.details = event.Details
		}
	}
	if !event.Started {
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
	if activity.details != nil && activity.details.Command != nil {
		c := activity.details.Command.Command
		switch c.Status {
		case publictool.CommandRunning, publictool.CommandStopping:
			activity.status = "running"
		case publictool.CommandSucceeded:
			activity.status = "done"
		case publictool.CommandFailed:
			activity.status = "failed"
		default:
			activity.status = string(c.Status)
		}
	}
	entryIndex := -1
	for i := range m.entries {
		if key != "" && m.entries[i].role == "tool" && m.entries[i].activityID == key {
			entryIndex = i
			break
		}
	}
	text := toolActivityTitle(*activity, m.workspace)
	m.clearTranscriptSelection()
	if entryIndex < 0 {
		m.entries = append(m.entries, tuiEntry{role: "tool", text: text, err: activity.status == "failed", activityID: key, activityGroupID: groupID})
	} else {
		m.entries[entryIndex].text = text
		m.entries[entryIndex].err = activity.status == "failed"
		m.entries[entryIndex].activityID = key
		m.entries[entryIndex].cachedRender = ""
	}
}

func (m *tuiModel) toolActivityKey(event core.ToolCall) string {
	if event.Details != nil && event.Details.Command != nil {
		command := event.Details.Command.Command
		if command.InvocationID != "" {
			event.InvocationID = command.InvocationID
		}
		if command.ExecutionID != "" {
			event.ExecutionID = command.ExecutionID
		}
	}
	key := activityKey(event)
	if alias := m.toolActivityAliases[key]; alias != "" {
		return alias
	}
	for _, id := range []string{event.ID, event.InvocationID} {
		if id == "" {
			continue
		}
		aliasKey := id
		if event.AgentID != "" {
			aliasKey = event.AgentID + ":" + aliasKey
		}
		if event.ExecutionID != "" {
			aliasKey = event.ExecutionID + ":" + id
			if event.AgentID != "" {
				aliasKey = event.ExecutionID + ":" + event.AgentID + ":" + id
			}
		}
		if alias := m.toolActivityAliases[aliasKey]; alias != "" {
			m.toolActivityAliases[key] = alias
			return alias
		}
	}
	return key
}

func toolActivityGroupID(event core.ToolCall, key, currentExecutionID string, activityCount int) string {
	executionID := event.ExecutionID
	if event.Details != nil && event.Details.Command != nil && event.Details.Command.Command.ExecutionID != "" {
		executionID = event.Details.Command.Command.ExecutionID
	}
	if executionID == "" {
		executionID = currentExecutionID
	}
	if executionID != "" {
		return "execution:" + executionID
	}
	if key != "" {
		return "call:" + key
	}
	return fmt.Sprintf("activity:%d", activityCount)
}

func humanizedToolSummary(name string, arguments json.RawMessage, workspace ...string) string {
	var args struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Target   string `json:"target"`
		Pattern  string `json:"pattern"`
		URL      string `json:"url"`
		Command  string `json:"command"`
	}
	_ = json.Unmarshal(arguments, &args)
	if args.Path == "" {
		args.Path = firstNonEmpty(partialJSONField(string(arguments), "path"), partialJSONField(string(arguments), "file_path"), partialJSONField(string(arguments), "target"))
	}
	if args.Pattern == "" {
		args.Pattern = partialJSONField(string(arguments), "pattern")
	}
	workspaceRoot := ""
	if len(workspace) > 0 {
		workspaceRoot = workspace[0]
	}
	path := displayToolPath(firstNonEmpty(args.Path, args.FilePath, args.Target), workspaceRoot)
	summary := "Tool request"
	switch name {
	case "read_file":
		summary = summaryWithPath("Read", path)
	case "write_file":
		summary = summaryWithPath("Wrote", path)
	case "edit_file":
		summary = summaryWithPath("Edited", path)
	case "list_dir":
		summary = summaryWithPath("Listed", path)
	case "glob":
		summary = summaryWithPath("Found", displayToolPath(args.Pattern, workspaceRoot))
	case "grep":
		if path != "" {
			summary = "Searched " + path
			if pattern := cleanInlineText(args.Pattern); pattern != "" {
				summary += " for " + pattern
			}
		} else {
			summary = summaryWithPath("Searched", displayToolPath(args.Pattern, workspaceRoot))
		}
	case "web_search":
		summary = "Searched the web"
	case "web_fetch":
		if parsed, err := url.Parse(args.URL); err == nil && parsed.Hostname() != "" {
			summary = "Fetched " + parsed.Hostname()
		} else {
			summary = "Fetched (preparing)"
		}
	case "command":
		if strings.TrimSpace(args.Command) == "" {
			summary = "Run (preparing)"
		} else {
			summary = "Run " + safeToolText(args.Command)
		}
	case "html":
		summary = "Processed web content"
	default:
		summary = "Tool request (preparing)"
	}
	return summary
}

func partialJSONField(raw, name string) string {
	needle := `"` + name + `"`
	index := strings.Index(raw, needle)
	if index < 0 {
		return ""
	}
	index += len(needle)
	colon := strings.IndexByte(raw[index:], ':')
	if colon < 0 {
		return ""
	}
	index += colon + 1
	for index < len(raw) && (raw[index] == ' ' || raw[index] == '\t' || raw[index] == '\r' || raw[index] == '\n') {
		index++
	}
	if index >= len(raw) || raw[index] != '"' {
		return ""
	}
	index++
	var value strings.Builder
	escaped := false
	for index < len(raw) {
		char := raw[index]
		if char == '"' && !escaped {
			break
		}
		value.WriteByte(char)
		if char == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
		index++
	}
	quoted := `"` + value.String() + `"`
	decoded, err := strconv.Unquote(quoted)
	if err == nil {
		return decoded
	}
	return strings.ReplaceAll(strings.ReplaceAll(value.String(), `\"`, `"`), `\\`, `\`)
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

func displayToolPath(value, workspace string) string {
	value = strings.TrimSpace(value)
	if !filepath.IsAbs(value) {
		return safeDisplayPath(value)
	}
	clean := filepath.Clean(value)
	if workspace != "" {
		if relative, err := filepath.Rel(workspace, clean); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if display := safeDisplayPath(relative); display != "" {
				return display
			}
		}
	}
	return filepath.ToSlash(clean)
}

func summaryWithPath(action, path string) string {
	if path == "" {
		return action + " (preparing)"
	}
	return action + " " + path
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
	m.clearTranscriptSelection()
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
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Render(icon) + " " + m.displayToolActivityTitle(activity)
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

func (m *tuiModel) displayToolActivityTitle(activity tuiActivity) string {
	title := toolActivityTitle(activity, m.workspace)
	if m.lightLayout() {
		return strings.TrimSuffix(title, " · done")
	}
	return title
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
	for _, prefix := range []string{"Read ", "Wrote ", "Edited ", "Listed ", "Searched "} {
		if strings.HasPrefix(summary, prefix) {
			path := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
			if filepath.IsAbs(path) {
				return path
			}
		}
	}
	return ""
}
