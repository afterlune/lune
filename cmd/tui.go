package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
)

type tuiEntry struct {
	role string
	text string
	err  bool
}

type tuiActivity struct {
	callID   string
	name     string
	status   string
	duration time.Duration
}

type toolActivityMsg struct{ event tool.ToolEvent }

type taskFinishedMsg struct {
	result taskResult
}

type taskResult struct {
	response *model.Response
	notice   string
	err      error
}

type tuiModel struct {
	client   responder
	runtime  resumableResponder
	input    textarea.Model
	chat     viewport.Model
	activity viewport.Model

	entries            []tuiEntry
	activities         []tuiActivity
	commandSuggestions []string
	suggestionIndex    int
	taskUsage          tokenUsageTotals
	sessionUsage       tokenUsageTotals
	usageRequests      []tokenUsageRecord
	width              int
	height             int
	pending            bool
	running            bool
	cancel             context.CancelFunc
	cancelled          bool
	status             string
}

type compactionResponder interface {
	Compact(context.Context) (int, error)
}

type modelUsageMsg struct{ usage model.ResponseUsage }

type tokenUsageRecord struct {
	request   int
	usage     model.ResponseUsage
	available bool
}

type tokenUsageTotals struct {
	requests  int
	measured  int
	input     int64
	output    int64
	reasoning int64
	total     int64
	cached    int64
}

var tuiCommands = []string{"/compact", "/discard", "/exit", "/resume"}

func useTUI(stdin, stdout *os.File) bool {
	return isTerminal(stdin) && isTerminal(stdout)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runTUI(client responder, registry *tool.Registry) error {
	runtime, _ := client.(resumableResponder)
	pending := false
	if runtime != nil {
		var err error
		pending, err = runtime.HasPending()
		if err != nil {
			return fmt.Errorf("inspect interrupted task: %w", err)
		}
	}
	program := tea.NewProgram(newTUIModel(client, runtime, pending), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	if observer, ok := client.(interface {
		SetUsageObserver(func(model.ResponseUsage))
	}); ok {
		observer.SetUsageObserver(func(usage model.ResponseUsage) {
			program.Send(modelUsageMsg{usage: usage})
		})
		defer observer.SetUsageObserver(nil)
	}
	if registry != nil {
		registry.SetToolEventObserver(func(event tool.ToolEvent) {
			program.Send(toolActivityMsg{event: event})
		})
		defer registry.SetToolEventObserver(nil)
	}
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func newTUIModel(client responder, runtime resumableResponder, pending bool) *tuiModel {
	input := textarea.New()
	input.Placeholder = "Ask anything, or paste ResponseInput JSON..."
	input.Prompt = "┃ "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 2
	input.MaxHeight = 6
	input.MaxContentHeight = 10000
	input.SetVirtualCursor(false)
	input.Focus()
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "newline"))

	model := &tuiModel{
		client:   client,
		runtime:  runtime,
		input:    input,
		chat:     viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		activity: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		width:    100,
		height:   30,
		pending:  pending,
		status:   "Ready",
	}
	model.chat.SoftWrap = true
	model.activity.SoftWrap = true
	if pending {
		model.status = "An interrupted task is pending; use /resume or /discard."
		model.entries = append(model.entries, tuiEntry{role: "notice", text: model.status, err: true})
	}
	model.updateLayout()
	model.refreshPanels()
	return model
}

func (m *tuiModel) Init() tea.Cmd { return textarea.Blink }

func (m *tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case toolActivityMsg:
		m.updateToolActivity(message.event)
		m.refreshPanels()
		return m, nil
	case modelUsageMsg:
		m.updateModelUsage(message.usage)
		m.refreshPanels()
		return m, nil
	case taskFinishedMsg:
		m.finishTask(message.result)
		m.refreshPanels()
		return m, nil
	case tea.KeyPressMsg:
		switch message.String() {
		case "enter":
			if m.running {
				return m, nil
			}
			return m.submit()
		case "esc":
			m.interrupt()
			return m, nil
		case "ctrl+c":
			if m.running {
				m.interrupt()
				return m, nil
			}
			return m, tea.Quit
		case "pgup":
			m.chat.HalfPageUp()
			return m, nil
		case "pgdown":
			m.chat.HalfPageDown()
			return m, nil
		case "ctrl+up":
			m.activity.HalfPageUp()
			return m, nil
		case "ctrl+down":
			m.activity.HalfPageDown()
			return m, nil
		}
		if m.running {
			return m, nil
		}
		if len(m.commandSuggestions) > 0 {
			switch message.String() {
			case "up":
				if m.suggestionIndex > 0 {
					m.suggestionIndex--
				}
				return m, nil
			case "down":
				if m.suggestionIndex+1 < len(m.commandSuggestions) {
					m.suggestionIndex++
				}
				return m, nil
			case "tab":
				m.input.SetValue(m.commandSuggestions[m.suggestionIndex])
				m.updateCommandSuggestions()
				m.updateLayout()
				return m, nil
			}
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	default:
		if m.running {
			return m, nil
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	}
}

func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
	switch line {
	case "":
		m.status = "Enter a message or command."
		return m, nil
	case "/exit":
		return m, tea.Quit
	case "/resume":
		return m.resume()
	case "/discard":
		return m.discard()
	case "/compact":
		return m.compact()
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Unknown command."
		m.addEntry("error", fmt.Sprintf("Unknown command %q. Available commands: %s", line, strings.Join(tuiCommands, " ")), true)
		return m, nil
	}

	if pending, err := m.hasPending(); err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Could not inspect interrupted task."
		return m, nil
	} else if pending {
		m.pending = true
		m.status = "An interrupted task is pending; use /resume or /discard."
		m.addEntry("notice", m.status, true)
		return m, nil
	}

	input, err := parseInputLine(raw)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Input was not accepted."
		return m, nil
	}
	m.addEntry("you", raw, false)
	m.resetInput()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.Respond(ctx, input)
		return taskResult{response: response, err: err}
	}, true)
}

func (m *tuiModel) resume() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	pending, err := m.runtime.HasPending()
	if err != nil {
		m.status = "Could not inspect interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if !pending {
		m.pending = false
		m.status = "No interrupted task to resume."
		m.addEntry("notice", m.status, false)
		m.resetInput()
		return m, nil
	}
	m.pending = true
	m.addEntry("notice", "Recovery may repeat a model request or tool call.", true)
	m.resetInput()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.runtime.Resume(ctx)
		return taskResult{response: response, err: err}
	}, false)
}

func (m *tuiModel) compact() (tea.Model, tea.Cmd) {
	runtime, ok := m.client.(compactionResponder)
	if !ok {
		m.status = "Conversation compaction is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.status = "Could not inspect interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	} else if pending {
		m.pending = true
		m.status = "An interrupted task is pending; use /resume or /discard."
		m.addEntry("notice", m.status, true)
		return m, nil
	}
	m.resetInput()
	return m.startTask(func(ctx context.Context) taskResult {
		compactedTurns, err := runtime.Compact(ctx)
		if err != nil {
			return taskResult{err: err}
		}
		if compactedTurns == 0 {
			return taskResult{notice: "No older conversation to compact; the latest four user turns are already retained."}
		}
		return taskResult{notice: fmt.Sprintf("Compacted %d older user turn(s); kept the latest four.", compactedTurns)}
	}, true)
}

func (m *tuiModel) discard() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	discarded, err := m.runtime.Discard(context.Background())
	if err != nil {
		m.status = "Could not discard interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.pending = false
	m.status = "No interrupted task to discard."
	if discarded {
		m.status = "Interrupted task discarded."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) resetInput() {
	m.input.Reset()
	m.updateCommandSuggestions()
	m.updateLayout()
}

func (m *tuiModel) hasPending() (bool, error) {
	if m.runtime == nil {
		return false, nil
	}
	pending, err := m.runtime.HasPending()
	m.pending = pending
	return pending, err
}

func (m *tuiModel) startTask(operation func(context.Context) taskResult, resetUsage bool) (tea.Model, tea.Cmd) {
	if resetUsage {
		m.taskUsage = tokenUsageTotals{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.cancelled = false
	m.status = "Working… Press Esc to interrupt."
	return m, func() tea.Msg {
		return taskFinishedMsg{result: operation(ctx)}
	}
}

func (m *tuiModel) interrupt() {
	if m.running && m.cancel != nil && !m.cancelled {
		m.cancelled = true
		m.status = "Cancelling task…"
		m.cancel()
	}
}

func (m *tuiModel) finishTask(result taskResult) {
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	if result.response != nil {
		text, err := responseDisplayText(result.response)
		if err != nil {
			m.addEntry("error", err.Error(), true)
		} else if text != "" {
			m.addEntry("assistant", text, false)
		}
	}
	if result.notice != "" {
		m.addEntry("notice", result.notice, false)
	}

	pending, pendingErr := m.hasPending()
	if pendingErr != nil {
		m.addEntry("error", "Could not inspect interrupted task: "+pendingErr.Error(), true)
	}
	if m.cancelled {
		if pending {
			m.status = "Task interrupted. Use /resume or /discard."
		} else {
			m.status = "Task cancelled."
		}
	} else if result.err != nil {
		m.status = "Task failed."
		m.addEntry("error", result.err.Error(), true)
	} else {
		m.status = "Completed."
	}
	m.cancelled = false
}

func (m *tuiModel) updateCommandSuggestions() {
	m.commandSuggestions = nil
	m.suggestionIndex = 0
	raw := m.input.Value()
	if strings.ContainsAny(raw, "\r\n") {
		return
	}
	line := strings.TrimSpace(raw)
	if !strings.HasPrefix(line, "/") {
		return
	}
	for _, command := range tuiCommands {
		if strings.HasPrefix(command, line) {
			m.commandSuggestions = append(m.commandSuggestions, command)
		}
	}
}

func (m *tuiModel) updateModelUsage(usage model.ResponseUsage) {
	available := usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.OutputTokensDetails.ReasoningTokens != 0 ||
		usage.TotalTokens != 0 || usage.InputTokensDetails.CachedTokens != 0
	requestNumber := m.sessionUsage.requests + 1
	record := tokenUsageRecord{request: requestNumber, usage: usage, available: available}
	m.taskUsage.add(usage, available)
	m.sessionUsage.add(usage, available)
	m.usageRequests = append(m.usageRequests, record)
	if len(m.usageRequests) > 200 {
		m.usageRequests = append([]tokenUsageRecord(nil), m.usageRequests[len(m.usageRequests)-200:]...)
	}
}

func (totals *tokenUsageTotals) add(usage model.ResponseUsage, available bool) {
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
	if !record.available {
		return fmt.Sprintf("#%d usage unavailable", record.request)
	}
	return fmt.Sprintf("#%d %d/%d/%d/%d/%d · hit=%s",
		record.request,
		record.usage.InputTokens,
		record.usage.OutputTokens,
		record.usage.OutputTokensDetails.ReasoningTokens,
		record.usage.TotalTokens,
		record.usage.InputTokensDetails.CachedTokens,
		cacheHitRate(record.usage.InputTokens, record.usage.InputTokensDetails.CachedTokens),
	)
}

func (totals tokenUsageTotals) String(label string) string {
	if totals.requests == 0 {
		return fmt.Sprintf("%s: no model requests", label)
	}
	if totals.measured == 0 {
		return fmt.Sprintf("%s: usage unavailable (%d request(s))", label, totals.requests)
	}
	return fmt.Sprintf("%s %d/%d/%d/%d/%d · hit %s",
		label,
		totals.input,
		totals.output,
		totals.reasoning,
		totals.total,
		totals.cached,
		cacheHitRate(totals.input, totals.cached),
	)
}

func cacheHitRate(inputTokens, cachedTokens int64) string {
	if inputTokens <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(cachedTokens)*100/float64(inputTokens))
}

func (m *tuiModel) updateToolActivity(event tool.ToolEvent) {
	index := -1
	for i := len(m.activities) - 1; i >= 0; i-- {
		if event.CallID != "" && m.activities[i].callID == event.CallID {
			index = i
			break
		}
	}
	if event.Started {
		activity := tuiActivity{callID: event.CallID, name: event.Name, status: "running"}
		if index >= 0 {
			m.activities[index] = activity
		} else {
			m.activities = append(m.activities, activity)
		}
	} else {
		if index < 0 {
			m.activities = append(m.activities, tuiActivity{callID: event.CallID, name: event.Name})
			index = len(m.activities) - 1
		}
		activity := &m.activities[index]
		activity.name = event.Name
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
	if len(m.activities) > 200 {
		m.activities = append([]tuiActivity(nil), m.activities[len(m.activities)-200:]...)
	}
}

func (m *tuiModel) addEntry(role, text string, isError bool) {
	m.entries = append(m.entries, tuiEntry{role: role, text: text, err: isError})
	m.refreshPanels()
}

func (m *tuiModel) refreshPanels() {
	var messages strings.Builder
	for i, entry := range m.entries {
		if i > 0 {
			messages.WriteString("\n\n")
		}
		style := lipgloss.NewStyle().Bold(true)
		if entry.err {
			style = style.Foreground(lipgloss.Color("9")).Bold(true)
		}
		messages.WriteString(style.Render(strings.ToUpper(entry.role)))
		messages.WriteByte('\n')
		messages.WriteString(entry.text)
	}
	if messages.Len() == 0 {
		messages.WriteString("Start a task. Use /resume or /discard for an interrupted task.")
	}
	m.chat.SetContent(messages.String())
	m.chat.GotoBottom()

	var activities strings.Builder
	activities.WriteString("Tool activity\n")
	if len(m.activities) == 0 {
		activities.WriteString("Tool executions will appear here.")
	}
	for i, activity := range m.activities {
		if i > 0 {
			activities.WriteByte('\n')
		}
		line := fmt.Sprintf("%-16s  %-9s", activity.name, activity.status)
		if activity.status != "running" && activity.duration > 0 {
			line += "  " + activity.duration.Round(time.Millisecond).String()
		}
		activities.WriteString(line)
	}
	activities.WriteString("\n\nModel requests: in/out/reason/total/cache")
	activities.WriteString("\nCache hit = cached/input")
	if len(m.usageRequests) == 0 {
		activities.WriteString("\nNo model requests yet.")
	} else {
		for _, record := range m.usageRequests {
			activities.WriteByte('\n')
			activities.WriteString(record.String())
		}
	}
	activities.WriteByte('\n')
	activities.WriteString(m.taskUsage.String("Task"))
	activities.WriteByte('\n')
	activities.WriteString(m.sessionUsage.String("Session"))
	activities.WriteString(fmt.Sprintf("\nMetered requests: task %d/%d · session %d/%d", m.taskUsage.measured, m.taskUsage.requests, m.sessionUsage.measured, m.sessionUsage.requests))
	m.activity.SetContent(activities.String())
	m.activity.GotoBottom()
}

func (m *tuiModel) updateLayout() {
	if m.width <= 0 {
		m.width = 80
	}
	if m.height <= 0 {
		m.height = 24
	}
	innerWidth := max(1, m.width-2)
	m.input.SetWidth(innerWidth)

	bodyHeight := max(6, m.height-m.input.Height()-10-m.commandSuggestionHeight())
	if m.width >= 100 {
		activityPanelWidth := min(44, max(40, m.width/3))
		chatPanelWidth := max(20, m.width-activityPanelWidth-1)
		panelHeight := bodyHeight
		m.chat.SetWidth(max(1, chatPanelWidth-2))
		m.activity.SetWidth(max(1, activityPanelWidth-2))
		m.chat.SetHeight(max(1, panelHeight-3))
		m.activity.SetHeight(max(1, panelHeight-3))
		return
	}

	activityPanelHeight := min(8, max(4, bodyHeight/3))
	chatPanelHeight := max(3, bodyHeight-activityPanelHeight-1)
	innerWidth = max(1, m.width-2)
	m.chat.SetWidth(innerWidth)
	m.activity.SetWidth(innerWidth)
	m.chat.SetHeight(max(1, chatPanelHeight-3))
	m.activity.SetHeight(max(1, activityPanelHeight-3))
}

func (m *tuiModel) commandSuggestionHeight() int {
	if len(m.commandSuggestions) == 0 {
		return 0
	}
	return len(m.commandSuggestions) + 1
}

func (m *tuiModel) View() tea.View {
	width := max(40, m.width)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(" Lune Agent")
	body := m.renderBody(width)
	inputLabel := lipgloss.NewStyle().Bold(true).Render(" Input · Enter sends · Ctrl+O newline · Tab completes command")
	inputPanel := lipgloss.NewStyle().Width(max(1, width-2)).Border(lipgloss.RoundedBorder()).Render(m.input.View())
	suggestions := m.renderCommandSuggestions()
	status := m.status
	if m.running {
		status = "Working… Press Esc to interrupt."
	}
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(status + "\n/compact  /resume  /discard  /exit  ·  PgUp/PgDn chat · Ctrl+↑/↓ activity")
	parts := []string{header, body, inputLabel, inputPanel}
	if suggestions != "" {
		parts = append(parts, suggestions)
	}
	parts = append(parts, footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.X++
		cursor.Y += lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(inputLabel) + 3
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderCommandSuggestions() string {
	if len(m.commandSuggestions) == 0 {
		return ""
	}
	lines := make([]string, 0, len(m.commandSuggestions)+1)
	for index, command := range m.commandSuggestions {
		line := "  " + command
		if index == m.suggestionIndex {
			line = "› " + command
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  ↑/↓ select · Tab complete · Enter run")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderBody(width int) string {
	if width >= 100 {
		activityWidth := min(44, max(40, width/3))
		chatWidth := max(20, width-activityWidth-1)
		panelHeight := max(3, m.height-m.input.Height()-10-m.commandSuggestionHeight())
		chatPanel := renderTUIPanel("Conversation", m.chat.View(), chatWidth, panelHeight)
		activityPanel := renderTUIPanel("Activity & usage", m.activity.View(), activityWidth, panelHeight)
		return lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, activityPanel)
	}
	bodyHeight := max(6, m.height-m.input.Height()-10-m.commandSuggestionHeight())
	activityHeight := min(8, max(4, bodyHeight/3))
	chatHeight := max(3, bodyHeight-activityHeight-1)
	chatPanel := renderTUIPanel("Conversation", m.chat.View(), width, chatHeight)
	activityPanel := renderTUIPanel("Activity & usage", m.activity.View(), width, activityHeight)
	return lipgloss.JoinVertical(lipgloss.Left, chatPanel, activityPanel)
}

func renderTUIPanel(title, content string, width, height int) string {
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	body := lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Bold(true).Render(title), content)
	return lipgloss.NewStyle().Width(innerWidth).Height(innerHeight).Border(lipgloss.RoundedBorder()).Render(body)
}

func responseDisplayText(response *model.Response) (string, error) {
	var output strings.Builder
	if err := printResponse(&output, response); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}
