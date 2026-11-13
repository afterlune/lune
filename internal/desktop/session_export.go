package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	core "github.com/afterlune/luneCore"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type ExportResultView struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	Canceled bool   `json:"canceled"`
}

func (s *DesktopService) ExportSession(sessionID, format string) (ExportResultView, error) {
	normalized, err := normalizeExportFormat(format)
	if err != nil {
		return ExportResultView{}, err
	}
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return ExportResultView{}, err
	}
	ctx := context.Background()
	var saved core.Session
	var history []ExecutionActivityView
	var app *application.App
	var workspace string
	func() {
		defer release()
		if sessionContext.core == nil || sessionContext.sessionID == "" {
			err = errors.New("没有打开会话")
			return
		}
		saved, err = sessionContext.core.GetSession(ctx, sessionContext.sessionID)
		if err != nil {
			return
		}
		s.flushSessionReasoning(sessionContext.sessionID)
		s.mu.Lock()
		store := s.activityStore
		app = s.app
		s.mu.Unlock()
		workspace = sessionContext.workspace
		history, err = loadExecutionHistory(store, sessionContext.sessionID)
	}()
	if err != nil {
		return ExportResultView{}, err
	}
	messages := transcriptMessages(saved.History)
	usage := buildSessionUsageView(sessionContext.core, saved)
	payload, err := renderSessionExport(normalized, saved, workspace, messages, history, usage)
	if err != nil {
		return ExportResultView{}, err
	}
	if app == nil {
		return ExportResultView{}, errors.New("桌面文件选择器尚未就绪")
	}
	target, err := app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:                "导出会话",
		ButtonText:           "导出",
		Directory:            workspace,
		Filename:             defaultExportFilename(saved, normalized),
		CanCreateDirectories: true,
		Filters:              []application.FileFilter{exportFileFilter(normalized)},
	}).PromptForSingleSelection()
	if err != nil {
		return ExportResultView{}, err
	}
	if target == "" {
		return ExportResultView{Format: normalized, Canceled: true}, nil
	}
	if filepath.Ext(target) == "" {
		target += "." + normalized
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ExportResultView{}, fmt.Errorf("创建导出目录: %w", err)
	}
	if err := os.WriteFile(target, payload, 0o644); err != nil {
		return ExportResultView{}, fmt.Errorf("写入导出文件: %w", err)
	}
	return ExportResultView{Path: target, Format: normalized}, nil
}

func normalizeExportFormat(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "md", "markdown":
		return "md", nil
	case "html":
		return "html", nil
	case "jsonl":
		return "jsonl", nil
	default:
		return "", fmt.Errorf("不支持的导出格式: %s", raw)
	}
}

func exportFileFilter(format string) application.FileFilter {
	switch format {
	case "html":
		return application.FileFilter{DisplayName: "HTML 文档 (*.html)", Pattern: "*.html"}
	case "jsonl":
		return application.FileFilter{DisplayName: "JSON Lines (*.jsonl)", Pattern: "*.jsonl"}
	default:
		return application.FileFilter{DisplayName: "Markdown (*.md)", Pattern: "*.md"}
	}
}

func defaultExportFilename(saved core.Session, format string) string {
	base := strings.TrimSpace(saved.Title)
	if base == "" {
		base = saved.ID
	}
	if base == "" {
		base = "lune-session"
	}
	var cleaned strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cleaned.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ':
			if !lastDash && cleaned.Len() > 0 {
				cleaned.WriteByte('-')
				lastDash = true
			}
		}
		if cleaned.Len() >= 64 {
			break
		}
	}
	name := strings.Trim(cleaned.String(), "-")
	if name == "" {
		name = "lune-session"
	}
	return name + "." + format
}

func renderSessionExport(format string, saved core.Session, workspace string, messages []ConversationMessage, history []ExecutionActivityView, usage SessionUsageView) ([]byte, error) {
	normalized, err := normalizeExportFormat(format)
	if err != nil {
		return nil, err
	}
	switch normalized {
	case "html":
		return renderHTMLExport(saved, workspace, messages, history, usage), nil
	case "jsonl":
		return renderJSONLExport(saved, workspace, messages, history, usage)
	default:
		return renderMarkdownExport(saved, workspace, messages, history, usage), nil
	}
}

func activityByUserMessage(history []ExecutionActivityView) map[int][]ExecutionActivityView {
	grouped := make(map[int][]ExecutionActivityView)
	for _, run := range history {
		if run.UserMessageIndex >= 0 {
			grouped[run.UserMessageIndex] = append(grouped[run.UserMessageIndex], run)
		}
	}
	return grouped
}

func renderMarkdownExport(saved core.Session, workspace string, messages []ConversationMessage, history []ExecutionActivityView, usage SessionUsageView) []byte {
	var b strings.Builder
	title := strings.TrimSpace(saved.Title)
	if title == "" {
		title = "未命名会话"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- 会话 ID: `%s`\n", saved.ID)
	if workspace != "" {
		fmt.Fprintf(&b, "- 工作区: `%s`\n", workspace)
	}
	if !saved.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "- 更新时间: %s\n", saved.UpdatedAt.Local().Format(time.RFC3339))
	}
	if saved.Settings.ModelProfile != "" {
		fmt.Fprintf(&b, "- 模型配置: `%s`\n", saved.Settings.ModelProfile)
	}
	if saved.Settings.ReasoningEffort != "" {
		fmt.Fprintf(&b, "- 思考强度: `%s`\n", saved.Settings.ReasoningEffort)
	}
	fmt.Fprintf(&b, "- 累计用量: 输入 %d / 输出 %d / 推理 %d / 总计 %d tokens\n\n", usage.Input, usage.Output, usage.Reasoning, usage.Total)

	grouped := activityByUserMessage(history)
	for index, message := range messages {
		roleLabel := "助手"
		if message.Role == "user" {
			roleLabel = "用户"
		}
		fmt.Fprintf(&b, "## %s\n\n", roleLabel)
		if text := strings.TrimSpace(message.Text); text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
		if len(message.Images) > 0 {
			fmt.Fprintf(&b, "*附带 %d 张图片*\n\n", len(message.Images))
		}
		if message.Role == "user" {
			for _, run := range grouped[index] {
				if reasoning := strings.TrimSpace(run.ReasoningText); reasoning != "" {
					b.WriteString("<details>\n<summary>思考过程</summary>\n\n")
					b.WriteString(reasoning)
					b.WriteString("\n\n</details>\n\n")
				}
				if len(run.Tools) > 0 {
					b.WriteString("<details>\n")
					fmt.Fprintf(&b, "<summary>执行工具（%d 项）</summary>\n\n", len(run.Tools))
					for _, tool := range run.Tools {
						status := "完成"
						switch strings.ToLower(tool.Status) {
						case "failed", "error":
							status = "失败"
						case "canceled", "cancelled":
							status = "已取消"
						}
						fmt.Fprintf(&b, "- `%s`（%s）\n", tool.Name, status)
					}
					b.WriteString("\n</details>\n\n")
				}
			}
		}
	}
	return []byte(b.String())
}

func renderHTMLExport(saved core.Session, workspace string, messages []ConversationMessage, history []ExecutionActivityView, usage SessionUsageView) []byte {
	var b strings.Builder
	title := strings.TrimSpace(saved.Title)
	if title == "" {
		title = "未命名会话"
	}
	b.WriteString("<!doctype html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	b.WriteString("<style>body{font-family:system-ui,-apple-system,sans-serif;max-width:860px;margin:40px auto;padding:0 20px;line-height:1.6;color:#1f2328;background:#fff}header{border-bottom:1px solid #d0d7de;padding-bottom:16px;margin-bottom:24px}.meta{color:#57606a;font-size:13px}.msg{margin:20px 0;padding:16px;border:1px solid #e5e7eb;border-radius:12px}.msg.user{background:#f6f8fa}.role{font-size:12px;font-weight:700;color:#57606a;margin-bottom:8px;text-transform:uppercase}.content{white-space:pre-wrap;word-break:break-word}details{margin-top:12px;padding:10px 12px;border-radius:8px;background:#f8fafc;border:1px solid #e2e8f0}summary{cursor:pointer;font-weight:600;font-size:13px}</style>\n")
	b.WriteString("</head>\n<body>\n<header>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(title))
	fmt.Fprintf(&b, "<div class=\"meta\">会话: <code>%s</code> · 工作区: <code>%s</code> · 用量: %d tokens</div>\n", html.EscapeString(saved.ID), html.EscapeString(workspace), usage.Total)
	b.WriteString("</header>\n<main>\n")
	grouped := activityByUserMessage(history)
	for index, message := range messages {
		cls := "msg assistant"
		role := "助手"
		if message.Role == "user" {
			cls = "msg user"
			role = "用户"
		}
		fmt.Fprintf(&b, "<article class=\"%s\">\n<div class=\"role\">%s</div>\n", cls, role)
		if text := strings.TrimSpace(message.Text); text != "" {
			fmt.Fprintf(&b, "<div class=\"content\">%s</div>\n", html.EscapeString(text))
		}
		if message.Role == "user" {
			for _, run := range grouped[index] {
				if reasoning := strings.TrimSpace(run.ReasoningText); reasoning != "" {
					fmt.Fprintf(&b, "<details><summary>思考过程</summary><div class=\"content\">%s</div></details>\n", html.EscapeString(reasoning))
				}
				if len(run.Tools) > 0 {
					fmt.Fprintf(&b, "<details><summary>执行工具（%d 项）</summary><ul>", len(run.Tools))
					for _, tool := range run.Tools {
						fmt.Fprintf(&b, "<li><code>%s</code></li>", html.EscapeString(tool.Name))
					}
					b.WriteString("</ul></details>\n")
				}
			}
		}
		b.WriteString("</article>\n")
	}
	b.WriteString("</main>\n</body>\n</html>\n")
	return []byte(b.String())
}

func renderJSONLExport(saved core.Session, workspace string, messages []ConversationMessage, history []ExecutionActivityView, usage SessionUsageView) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{
		"type":      "session",
		"id":        saved.ID,
		"title":     saved.Title,
		"workspace": workspace,
		"updatedAt": saved.UpdatedAt,
		"settings":  saved.Settings,
		"usage":     usage,
	}); err != nil {
		return nil, err
	}
	for index, msg := range messages {
		if err := enc.Encode(map[string]any{
			"type":   "message",
			"index":  index,
			"role":   msg.Role,
			"text":   msg.Text,
			"images": msg.Images,
		}); err != nil {
			return nil, err
		}
	}
	for _, run := range history {
		if err := enc.Encode(map[string]any{
			"type":             "activity",
			"executionId":      run.ExecutionID,
			"userMessageIndex": run.UserMessageIndex,
			"status":           run.Status,
			"reasoningText":    run.ReasoningText,
			"startedAt":        run.StartedAt,
			"finishedAt":       run.FinishedAt,
			"tools":            run.Tools,
		}); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
