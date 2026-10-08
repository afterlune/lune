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
	response *model.Response
	err      error
}

type tuiModel struct {
	client   responder
	runtime  resumableResponder
	input    textarea.Model
	chat     viewport.Model
	activity viewport.Model

	entries    []tuiEntry
	activities []tuiActivity
	width      int
	height     int
	pending    bool
	running    bool
	cancel     context.CancelFunc
	cancelled  bool
	status     string
}

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
	case taskFinishedMsg:
		m.finishTask(message)
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
		}
		if m.running {
			return m, nil
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.updateLayout()
		return m, command
	default:
		if m.running {
			return m, nil
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
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
	m.input.Reset()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) (*model.Response, error) {
		return m.client.Respond(ctx, input)
	})
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
		m.input.Reset()
		return m, nil
	}
	m.pending = true
	m.addEntry("notice", "Recovery may repeat a model request or tool call.", true)
	m.input.Reset()
	m.cancelled = false
	return m.startTask(m.runtime.Resume)
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
	m.input.Reset()
	return m, nil
}

func (m *tuiModel) hasPending() (bool, error) {
	if m.runtime == nil {
		return false, nil
	}
	pending, err := m.runtime.HasPending()
	m.pending = pending
	return pending, err
}

func (m *tuiModel) startTask(operation func(context.Context) (*model.Response, error)) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.cancelled = false
	m.status = "Working… Press Esc to interrupt."
	return m, func() tea.Msg {
		response, err := operation(ctx)
		return taskFinishedMsg{response: response, err: err}
	}
}

func (m *tuiModel) interrupt() {
	if m.running && m.cancel != nil && !m.cancelled {
		m.cancelled = true
		m.status = "Cancelling task…"
		m.cancel()
	}
}

func (m *tuiModel) finishTask(message taskFinishedMsg) {
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	if message.response != nil {
		text, err := responseDisplayText(message.response)
		if err != nil {
			m.addEntry("error", err.Error(), true)
		} else if text != "" {
			m.addEntry("assistant", text, false)
		}
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
	} else if message.err != nil {
		m.status = "Task failed."
		m.addEntry("error", message.err.Error(), true)
	} else {
		m.status = "Completed."
	}
	m.cancelled = false
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
	if activities.Len() == 0 {
		activities.WriteString("Tool executions will appear here.")
	}
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

	bodyHeight := max(6, m.height-m.input.Height()-10)
	if m.width >= 100 {
		activityPanelWidth := min(42, max(30, m.width/3))
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

func (m *tuiModel) View() tea.View {
	width := max(40, m.width)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(" Lune Agent")
	body := m.renderBody(width)
	inputLabel := lipgloss.NewStyle().Bold(true).Render(" Input · Enter sends · Ctrl+O newline")
	inputPanel := lipgloss.NewStyle().Width(max(1, width-2)).Border(lipgloss.RoundedBorder()).Render(m.input.View())
	status := m.status
	if m.running {
		status = "Working… Press Esc to interrupt."
	}
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(status + "\n/resume  /discard  /exit  ·  PgUp/PgDn scroll")
	content := lipgloss.JoinVertical(lipgloss.Left, header, body, inputLabel, inputPanel, footer)
	view := tea.NewView(content)
	view.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.X++
		cursor.Y += lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(inputLabel) + 3
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderBody(width int) string {
	if width >= 100 {
		activityWidth := min(42, max(30, width/3))
		chatWidth := max(20, width-activityWidth-1)
		panelHeight := max(3, m.height-m.input.Height()-10)
		chatPanel := renderTUIPanel("Conversation", m.chat.View(), chatWidth, panelHeight)
		activityPanel := renderTUIPanel("Tool activity", m.activity.View(), activityWidth, panelHeight)
		return lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, activityPanel)
	}
	bodyHeight := max(6, m.height-m.input.Height()-10)
	activityHeight := min(8, max(4, bodyHeight/3))
	chatHeight := max(3, bodyHeight-activityHeight-1)
	chatPanel := renderTUIPanel("Conversation", m.chat.View(), width, chatHeight)
	activityPanel := renderTUIPanel("Tool activity", m.activity.View(), width, activityHeight)
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
