package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ExportFormat defines the output file format for session transcript export.
type ExportFormat int

// exportResultMsg carries the outcome of an export written by a command.
type exportResultMsg struct {
	text string
	err  error
}

// exportDirectory is where an export without an explicit path lands. Keeping it
// under .luneagent/ keeps the repository clean and out of @ mentions and reviews.
const exportDirectory = ".luneagent/exports"

func determineExportFormatAndPath(raw string, sessionID string, workspace string) (ExportFormat, string) {
	raw = strings.TrimSpace(raw)
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "current"
	}
	timestamp := time.Now().Format("20060102-150405")
	defaultPath := func(extension string) string {
		return filepath.Join(exportDirectory, fmt.Sprintf("export-%s-%s%s", shortID, timestamp, extension))
	}

	format := ExportFormatMarkdown
	path := ""

	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ExportFormatMarkdown, filepath.Join(workspace, defaultPath(".md"))
	}

	first := strings.ToLower(parts[0])
	switch first {
	case "html", "htm":
		format = ExportFormatHTML
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = defaultPath(".html")
		}
	case "jsonl", "json":
		format = ExportFormatJSONL
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = defaultPath(".jsonl")
		}
	case "md", "markdown":
		format = ExportFormatMarkdown
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = defaultPath(".md")
		}
	default:
		path = raw
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".html", ".htm":
			format = ExportFormatHTML
		case ".jsonl", ".json":
			format = ExportFormatJSONL
		default:
			format = ExportFormatMarkdown
		}
	}

	if !filepath.IsAbs(path) && workspace != "" {
		path = filepath.Join(workspace, path)
	}
	return format, path
}

// exportSnapshot is the immutable slice of session state the exporters read. It
// is built on the update goroutine so rendering and the file write can run in a
// command.
type exportSnapshot struct {
	sessionID string
	workspace string
	title     string
	modelName string
	entries   []TranscriptEntry
	usage     sessionUsage
}

// snapshotForExport copies the state /export renders.
func (m *tuiModel) snapshotForExport() *exportSnapshot {
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
	}
	title := m.sessionTitle
	if title == "" {
		title = "<untitled session>"
	}
	return &exportSnapshot{
		sessionID: m.sessionID,
		workspace: m.workspace,
		title:     title,
		modelName: modelName,
		entries:   append([]TranscriptEntry(nil), m.entries...),
		usage:     m.usage,
	}
}

func (m *exportSnapshot) exportTranscript(targetPath string) (string, error) {
	title := m.title
	modelName := m.modelName
	userTurns := 0
	assistantCount := 0
	toolCount := 0
	for _, entry := range m.entries {
		switch entry.Kind {
		case KindUser:
			userTurns++
		case KindAssistant:
			assistantCount++
		case KindTool:
			toolCount++
		}
	}

	format, finalPath := determineExportFormatAndPath(targetPath, m.sessionID, m.workspace)

	switch format {
	case ExportFormatHTML:
		return m.exportHTML(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	case ExportFormatJSONL:
		return m.exportJSONL(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	default:
		return m.exportMarkdown(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	}
}

func (m *exportSnapshot) exportMarkdown(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session Transcript: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Session ID:** `%s`\n", m.sessionID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", m.workspace))
	sb.WriteString(fmt.Sprintf("- **Model:** `%s`\n", modelName))
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s\n", time.Now().Format(time.RFC3339)))
	if m.usage.totalInputTokens > 0 || m.usage.totalOutputTokens > 0 {
		sb.WriteString(fmt.Sprintf("- **Token Usage:** %s in (cached: %s) · %s out (reasoning: %s)\n",
			formatTokens(m.usage.totalInputTokens), formatTokens(m.usage.cachedTokens),
			formatTokens(m.usage.totalOutputTokens), formatTokens(m.usage.reasoningTokens)))
	}
	sb.WriteString(fmt.Sprintf("- **Statistics:** %d user turn(s) · %d assistant message(s) · %d tool execution(s)\n",
		userTurns, assistantCount, toolCount))
	sb.WriteString("\n---\n\n")

	entryCount := 0
	for _, entry := range m.entries {
		switch entry.Kind {
		case KindUser:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 👤 User (%s)\n\n%s\n\n", entry.Timestamp.Format("15:04:05"), entry.Text))
		case KindAssistant:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 🤖 Assistant (%s)\n\n%s\n\n", entry.Timestamp.Format("15:04:05"), entry.Text))
		case KindTool:
			entryCount++
			status := "Completed"
			if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
				status = fmt.Sprintf("Failed (exit code %d)", *entry.ToolExitCode)
			}
			toolName := entry.ToolName
			if toolName == "" {
				toolName = "tool"
			}
			sb.WriteString(fmt.Sprintf("<details>\n<summary>Tool: <code>%s</code> (%s)</summary>\n\n", toolName, status))
			if entry.ToolCommand != "" {
				sb.WriteString(fmt.Sprintf("**Command:**\n```sh\n%s\n```\n\n", entry.ToolCommand))
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				sb.WriteString(fmt.Sprintf("**Arguments:**\n```json\n%s\n```\n\n", entry.ToolArgs))
			}
			if entry.ToolOutput != "" {
				sb.WriteString(fmt.Sprintf("**Output:**\n```\n%s\n```\n\n", strings.TrimSpace(entry.ToolOutput)))
			}
			sb.WriteString("</details>\n\n")
		case KindPlan:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 📋 Plan\n\n%s\n\n", entry.Text))
		case KindSystem:
			sb.WriteString(fmt.Sprintf("> *System: %s*\n\n", entry.Text))
		}
	}

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d turns, %d tools).", targetPath, formatTokens(int64(len(content))), userTurns, toolCount), nil
}

func (m *exportSnapshot) exportJSONL(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var lines []string

	meta := map[string]interface{}{
		"type":        "metadata",
		"session_id":  m.sessionID,
		"title":       title,
		"workspace":   m.workspace,
		"model":       modelName,
		"exported_at": time.Now().Format(time.RFC3339),
		"token_usage": map[string]int64{
			"input_tokens":     m.usage.totalInputTokens,
			"cached_tokens":    m.usage.cachedTokens,
			"output_tokens":    m.usage.totalOutputTokens,
			"reasoning_tokens": m.usage.reasoningTokens,
		},
		"statistics": map[string]int{
			"user_turns":         userTurns,
			"assistant_messages": assistantCount,
			"tool_calls":         toolCount,
		},
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	lines = append(lines, string(metaBytes))

	for _, entry := range m.entries {
		rec := map[string]interface{}{
			"timestamp": entry.Timestamp.Format(time.RFC3339),
		}
		switch entry.Kind {
		case KindUser:
			rec["type"] = "message"
			rec["role"] = "user"
			rec["content"] = entry.Text
		case KindAssistant:
			rec["type"] = "message"
			rec["role"] = "assistant"
			rec["content"] = entry.Text
		case KindTool:
			rec["type"] = "tool_call"
			rec["role"] = "tool"
			rec["tool_name"] = entry.ToolName
			if entry.ToolCommand != "" {
				rec["command"] = entry.ToolCommand
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				var parsedArgs interface{}
				if err := json.Unmarshal([]byte(entry.ToolArgs), &parsedArgs); err == nil {
					rec["arguments"] = parsedArgs
				} else {
					rec["arguments"] = entry.ToolArgs
				}
			}
			rec["output"] = entry.ToolOutput
			if entry.ToolExitCode != nil {
				rec["exit_code"] = *entry.ToolExitCode
			}
		case KindPlan:
			rec["type"] = "message"
			rec["role"] = "plan"
			rec["content"] = entry.Text
		case KindSystem:
			rec["type"] = "message"
			rec["role"] = "system"
			rec["content"] = entry.Text
		}
		b, err := json.Marshal(rec)
		if err == nil {
			lines = append(lines, string(b))
		}
	}

	content := []byte(strings.Join(lines, "\n") + "\n")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d records).", targetPath, formatTokens(int64(len(content))), len(lines)), nil
}

func (m *exportSnapshot) exportHTML(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	sb.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	sb.WriteString(fmt.Sprintf("<title>%s - Lune Transcript</title>\n", html.EscapeString(title)))
	sb.WriteString(`<style>
:root {
  --bg: #0d1117;
  --card-bg: #161b22;
  --border: #30363d;
  --text-primary: #e6edf3;
  --text-muted: #8b949e;
  --accent: #58a6ff;
  --user-bg: #1a2233;
  --tool-bg: #121820;
  --success: #3fb950;
  --error: #f85149;
  --code-bg: #0b0e14;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #ffffff;
    --card-bg: #f6f8fa;
    --border: #d0d7de;
    --text-primary: #1f2328;
    --text-muted: #656d76;
    --accent: #0969da;
    --user-bg: #eef3fc;
    --tool-bg: #f3f5f8;
    --success: #1a7f37;
    --error: #cf222e;
    --code-bg: #e8ecf1;
  }
}
* { box-sizing: border-box; }
body {
  background-color: var(--bg);
  color: var(--text-primary);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  line-height: 1.6;
  margin: 0;
  padding: 32px 16px;
}
.container { max-width: 900px; margin: 0 auto; }
.header-card {
  background: var(--card-bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 24px;
  margin-bottom: 24px;
}
.header-card h1 { margin: 0 0 16px 0; font-size: 1.5rem; color: var(--text-primary); }
.meta-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px 20px;
  font-size: 0.875rem;
  color: var(--text-muted);
}
.meta-item strong { color: var(--text-primary); }
.message {
  margin-bottom: 20px;
  border-radius: 8px;
  padding: 16px 20px;
  border: 1px solid var(--border);
}
.message-user {
  background: var(--user-bg);
  border-color: var(--accent);
}
.message-assistant {
  background: var(--card-bg);
}
.message-plan {
  background: var(--card-bg);
  border-left: 4px solid var(--accent);
}
.message-system {
  background: transparent;
  border-left: 3px solid var(--text-muted);
  font-style: italic;
  color: var(--text-muted);
  padding: 8px 16px;
  margin-bottom: 16px;
}
.message-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
  font-size: 0.85rem;
  font-weight: 600;
}
.role-user { color: var(--accent); }
.role-assistant { color: var(--success); }
.timestamp { color: var(--text-muted); font-weight: normal; font-size: 0.8rem; }
.content { white-space: pre-wrap; word-break: break-word; }
details.tool-card {
  background: var(--tool-bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  margin-bottom: 16px;
  padding: 10px 14px;
}
details.tool-card summary {
  cursor: pointer;
  font-weight: 600;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.875rem;
}
.badge {
  display: inline-block;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 0.75rem;
  font-weight: 600;
  margin-left: 8px;
}
.badge-success { background: var(--success); color: #ffffff; }
.badge-error { background: var(--error); color: #ffffff; }
pre {
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  overflow-x: auto;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.85rem;
  margin: 8px 0;
}
</style>
</head>
<body>
<div class="container">
`)

	sb.WriteString("<div class=\"header-card\">\n")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(title)))
	sb.WriteString("<div class=\"meta-grid\">\n")
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Session ID:</strong> <code>%s</code></div>\n", html.EscapeString(m.sessionID)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Model:</strong> %s</div>\n", html.EscapeString(modelName)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Workspace:</strong> <code>%s</code></div>\n", html.EscapeString(m.workspace)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Exported:</strong> %s</div>\n", time.Now().Format("2006-01-02 15:04:05")))
	if m.usage.totalInputTokens > 0 || m.usage.totalOutputTokens > 0 {
		sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Token Usage:</strong> %s in · %s out</div>\n",
			formatTokens(m.usage.totalInputTokens), formatTokens(m.usage.totalOutputTokens)))
	}
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Turns / Tools:</strong> %d turns · %d tools</div>\n",
		userTurns, toolCount))
	sb.WriteString("</div>\n</div>\n\n")

	for _, entry := range m.entries {
		ts := entry.Timestamp.Format("15:04:05")
		switch entry.Kind {
		case KindUser:
			sb.WriteString("<div class=\"message message-user\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span class=\"role-user\">👤 User</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindAssistant:
			sb.WriteString("<div class=\"message message-assistant\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span class=\"role-assistant\">🤖 Assistant</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindTool:
			statusClass := "badge-success"
			statusText := "Completed"
			if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
				statusClass = "badge-error"
				statusText = fmt.Sprintf("Failed (%d)", *entry.ToolExitCode)
			}
			toolName := entry.ToolName
			if toolName == "" {
				toolName = "tool"
			}
			sb.WriteString("<details class=\"tool-card\">\n")
			sb.WriteString(fmt.Sprintf("<summary>🔧 %s <span class=\"badge %s\">%s</span> <span class=\"timestamp\">%s</span></summary>\n",
				html.EscapeString(toolName), statusClass, statusText, ts))
			if entry.ToolCommand != "" {
				sb.WriteString(fmt.Sprintf("<p><strong>Command:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(entry.ToolCommand)))
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				sb.WriteString(fmt.Sprintf("<p><strong>Arguments:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(entry.ToolArgs)))
			}
			if entry.ToolOutput != "" {
				sb.WriteString(fmt.Sprintf("<p><strong>Output:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(strings.TrimSpace(entry.ToolOutput))))
			}
			sb.WriteString("</details>\n")
		case KindPlan:
			sb.WriteString("<div class=\"message message-plan\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span>📋 Plan</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindSystem:
			sb.WriteString(fmt.Sprintf("<div class=\"message-system\">ℹ️ %s</div>\n", html.EscapeString(entry.Text)))
		}
	}

	sb.WriteString("</div>\n</body>\n</html>\n")

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d turns, %d tools).",
		targetPath, formatTokens(int64(len(content))), userTurns, toolCount), nil
}

// handleExportCommand starts the transcript export. Rendering and the file write
// run in a command so a large transcript cannot stall the interface.
func (m *tuiModel) handleExportCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Transcript export is unavailable while a task is running or queued.")
		return m, nil
	}
	targetPath := ""
	if len(parts) > 1 {
		targetPath = strings.TrimSpace(strings.Join(parts[1:], " "))
	}
	snapshot := m.snapshotForExport()
	return m, func() tea.Msg {
		text, err := snapshot.exportTranscript(targetPath)
		return exportResultMsg{text: text, err: err}
	}
}

// handleExportResult reports the export outcome written by the export command.
func (m *tuiModel) handleExportResult(msg exportResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addSystemEntry("Export failed: " + msg.err.Error())
	} else {
		m.addSystemEntry(msg.text)
	}
	m.updateLayout()
	return m, nil
}
