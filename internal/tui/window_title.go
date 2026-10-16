package tui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

const (
	defaultWindowTitle    = "lune"
	windowTitleTickPeriod = 100 * time.Millisecond
)

var titleSpinner = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type terminalTaskState uint8

const (
	terminalIdle terminalTaskState = iota
	terminalRunning
	terminalWaiting
)

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *synchronizedWriter) Fd() uintptr {
	if file, ok := w.w.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return 0
}

type windowTitleTickMsg struct {
	sessionID string
	token     uint64
}

type sessionWindowTitleMsg struct {
	sessionID string
	revision  uint64
	title     string
	err       error
}

func (m *tuiModel) setSessionWindowTitle(saved core.Session) {
	m.windowTitle = terminalTitleForSession(saved)
	m.windowTitleRevision++
}

func (m *tuiModel) computeWindowTitle() string {
	title := safeWindowTitle(m.taskTitle)
	if title == "" {
		title = safeWindowTitle(latestUserTaskTitleSummary(m.entries))
	}
	if title == "" {
		title = safeWindowTitle(m.windowTitle)
	}
	if title == "" {
		title = defaultWindowTitle
	}
	title = fitTerminalText(title, 32)

	if m.hasPendingUserQuestion() {
		question := m.pendingQuestionSummary()
		if question == "" {
			question = "等待回答"
		}
		return safeWindowTitle("⚠ 待回答: " + fitTerminalText(question, 36) + " | " + title)
	}
	if m.hasPendingApproval() {
		action := "操作待审批"
		if request := m.pendingApprovalRequest(); request != nil {
			action = approvalTitleSummary(*request, m.workspace)
		}
		if action == "" || strings.Contains(action, "(preparing)") {
			action = "操作待审批"
		}
		return safeWindowTitle("⚠ 待审批: " + fitTerminalText(action, 36) + " | " + title)
	}
	if m.running {
		frame := m.windowTitleFrame % len(titleSpinner)
		return safeWindowTitle(titleSpinner[frame] + " " + fitTerminalText(m.currentWindowTitleAction(), 32) + " · " + title)
	}
	if m.taskCompleted {
		return safeWindowTitle("✔ 已完成 · " + title + " | luneAgent")
	}
	return title + " | luneAgent"
}

func (m *tuiModel) currentWindowTitleAction() string {
	action := m.currentLiveAction()
	if action == "Thinking" || action == "Working" {
		return "Thinking..."
	}
	return action
}

func (m *tuiModel) terminalState() terminalTaskState {
	if m.hasPendingUserQuestion() || m.hasPendingApproval() {
		return terminalWaiting
	}
	if m.running {
		return terminalRunning
	}
	return terminalIdle
}

func (m *tuiModel) hasPendingApproval() bool {
	return m.pendingApproval != nil || (m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingApproval)
}

func approvalTitleSummary(request core.ApprovalRequest, workspace string) string {
	if request.Tool == "command" {
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(request.Arguments, &args)
		if command := strings.Join(strings.Fields(safeToolText(args.Command)), " "); command != "" {
			return command
		}
	}
	return toolApprovalAction(request.Tool, request.Arguments, workspace)
}

func (m *tuiModel) terminalProgressBar() *tea.ProgressBar {
	switch m.terminalState() {
	case terminalRunning:
		return &tea.ProgressBar{State: tea.ProgressBarIndeterminate}
	case terminalWaiting:
		return &tea.ProgressBar{State: tea.ProgressBarWarning, Value: 100}
	default:
		return &tea.ProgressBar{State: tea.ProgressBarNone}
	}
}

func (m *tuiModel) pendingQuestionSummary() string {
	if m.question != nil {
		return cleanInlineText(m.question.request.Question)
	}
	if m.pendingRequest != nil && m.pendingRequest.UserQuestion != nil {
		return cleanInlineText(m.pendingRequest.UserQuestion.Question)
	}
	return ""
}

func (m *tuiModel) pendingApprovalRequest() *core.ApprovalRequest {
	if m.pendingApproval != nil {
		return &m.pendingApproval.request
	}
	if m.pendingRequest != nil && m.pendingRequest.Approval != nil {
		return m.pendingRequest.Approval
	}
	return nil
}

func (m *tuiModel) setTaskTitle(summary string) {
	summary = strings.ReplaceAll(strings.ReplaceAll(summary, "\r\n", "\n"), "\r", "\n")
	summary = strings.TrimSpace(strings.SplitN(summary, "\n", 2)[0])
	if summary = strings.Join(strings.Fields(cleanInlineText(summary)), " "); summary != "" {
		m.taskTitle = fitTerminalText(summary, 32)
		m.taskCompleted = false
	}
}

func latestUserTaskTitleSummary(entries []tuiEntry) string {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].role != "you" {
			continue
		}
		text := strings.ReplaceAll(strings.ReplaceAll(entries[index].text, "\r\n", "\n"), "\r", "\n")
		text = strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
		return strings.Join(strings.Fields(text), " ")
	}
	return ""
}

func (m *tuiModel) terminalBell() tea.Cmd {
	if m.terminalOutput == nil {
		return nil
	}
	output := m.terminalOutput
	return func() tea.Msg {
		_, _ = io.WriteString(output, "\a")
		return nil
	}
}

func (m *tuiModel) hasPendingUserQuestion() bool {
	return m.question != nil || (m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingUserQuestion)
}

func (m *tuiModel) startWindowTitleAnimation() tea.Cmd {
	m.windowTitleAnimationToken++
	m.windowTitleFrame = 0
	if !m.running {
		return nil
	}
	return nextWindowTitleTick(m.sessionID, m.windowTitleAnimationToken)
}

func nextWindowTitleTick(sessionID string, token uint64) tea.Cmd {
	return tea.Tick(windowTitleTickPeriod, func(time.Time) tea.Msg {
		return windowTitleTickMsg{sessionID: sessionID, token: token}
	})
}

func safeWindowTitle(title string) string {
	title = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, title)
	return strings.TrimSpace(title)
}

func (m *tuiModel) loadSessionWindowTitle() tea.Cmd {
	if m.client == nil || m.sessionID == "" {
		return nil
	}
	client := m.client
	sessionID := m.sessionID
	revision := m.windowTitleRevision
	return func() tea.Msg {
		saved, err := client.GetSession(context.Background(), sessionID)
		if err != nil {
			return sessionWindowTitleMsg{sessionID: sessionID, revision: revision, err: err}
		}
		return sessionWindowTitleMsg{sessionID: sessionID, revision: revision, title: terminalTitleForSession(saved)}
	}
}

func terminalTitleForSession(saved core.Session) string {
	if strings.TrimSpace(saved.Title) == "" && len(rawHistoryItems(saved.History)) == 0 {
		return defaultWindowTitle
	}
	return sessionDisplayTitle(saved)
}
