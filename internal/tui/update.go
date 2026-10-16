package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/atotto/clipboard"
)

type planImplementedMsg struct {
	impl core.PlanImplementation
	err  error
}

type clearCopyFeedbackMsg struct {
	at time.Time
}

// Update handles all incoming Bubble Tea messages and key events.
func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()
		return m, nil

	case tickMsg:
		if m.running {
			m.refreshViewportContent()
			return m, tickClock()
		}

	case runtimeStreamReadyMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.err != nil {
			m.addSystemEntry("Failed to connect to runtime event stream: " + msg.err.Error())
			return m, nil
		}
		return m, nextRuntimeEvent(m.sessionID, msg.events)

	case runtimeEventMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.events == nil {
			return m, nil
		}
		var cmd tea.Cmd
		var mod tea.Model
		mod, cmd = m.handleRuntimeEvent(msg.event)
		m = mod.(*tuiModel)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, nextRuntimeEvent(m.sessionID, msg.events))
		return m, tea.Batch(cmds...)

	case turnSubmittedMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.err != nil {
			m.running = false
			m.addSystemEntry("Failed to submit task: " + msg.err.Error())
			m.updateLayout()
			return m, nil
		}
		m.runningExecution = msg.submission.ExecutionID
		cmds = append(cmds, waitForTurn(m.runtime, m.sessionID, msg.submission.ExecutionID))
		return m, tea.Batch(cmds...)

	case turnFinishedMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		m.running = false
		m.thinking = false
		m.streamActive = false
		if msg.err != nil {
			m.addSystemEntry("Task failed: " + msg.err.Error())
		} else {
			if m.currentStreamText != "" {
				m.entries = append(m.entries, TranscriptEntry{
					Kind: KindAssistant,
					Text: m.currentStreamText,
				})
				m.currentStreamText = ""
			}
			if msg.result.ProposedPlan != nil && msg.result.ProposedPlan.Text != "" {
				m.entries = append(m.entries, TranscriptEntry{
					Kind: KindPlan,
					Text: msg.result.ProposedPlan.Text,
				})
				m.activePlanReview = &ActivePlanReview{
					Text:        msg.result.ProposedPlan.Text,
					ExecutionID: msg.result.ExecutionID,
					Selected:    0,
				}
			}
		}
		m.refreshViewportContent()
		m.updateLayout()
		return m, nil

	case compactDoneMsg:
		if msg.err != nil {
			m.addSystemEntry("Compaction failed: " + msg.err.Error())
		} else {
			m.addSystemEntry("Compacted older conversation history.")
		}
		return m, nil

	case sessionForkedMsg:
		if msg.err != nil {
			m.addSystemEntry("Fork failed: " + msg.err.Error())
			return m, nil
		}
		if err := m.bindSessionRuntime(context.Background(), msg.session.ID); err != nil {
			m.addSystemEntry("Failed to bind forked session runtime: " + err.Error())
			return m, nil
		}
		m.sessionTitle = msg.session.Title
		m.entries = nil
		m.activeQuestion = nil
		m.activeApproval = nil
		m.activePlanReview = nil
		m.running = false
		m.currentStreamText = ""
		m.refreshViewportContent()
		m.updateLayout()
		m.addSystemEntry("Forked into new session: " + msg.session.ID)
		return m, m.subscribeRuntimeEvents()

	case sessionCreatedMsg:
		if msg.err != nil {
			m.addSystemEntry("New session failed: " + msg.err.Error())
			return m, nil
		}
		if err := m.bindSessionRuntime(context.Background(), msg.session.ID); err != nil {
			m.addSystemEntry("Failed to bind new session runtime: " + err.Error())
			return m, nil
		}
		m.sessionTitle = msg.session.Title
		m.entries = nil
		m.activeQuestion = nil
		m.activeApproval = nil
		m.activePlanReview = nil
		m.running = false
		m.currentStreamText = ""
		m.refreshViewportContent()
		m.updateLayout()
		m.addSystemEntry("Started fresh session.")
		return m, m.subscribeRuntimeEvents()

	case planImplementedMsg:
		if msg.err != nil {
			m.addSystemEntry("Plan implementation failed: " + msg.err.Error())
			return m, nil
		}
		var planCmds []tea.Cmd
		if msg.impl.SessionID != "" && msg.impl.SessionID != m.sessionID {
			if err := m.bindSessionRuntime(context.Background(), msg.impl.SessionID); err != nil {
				m.addSystemEntry("Failed to bind new plan session: " + err.Error())
				return m, nil
			}
			m.entries = nil
			m.refreshViewportContent()
			m.updateLayout()
			planCmds = append(planCmds, m.subscribeRuntimeEvents())
		}
		m.running = true
		m.taskStartedAt = time.Now()
		m.runningExecution = msg.impl.Submission.ExecutionID
		planCmds = append(planCmds, tickClock(), waitForTurn(m.runtime, m.sessionID, msg.impl.Submission.ExecutionID))
		return m, tea.Batch(planCmds...)

	case taskDiscardedMsg:
		if msg.err != nil {
			m.addSystemEntry("Discard failed: " + msg.err.Error())
		} else {
			m.running = false
			m.addSystemEntry("Discarded paused task.")
		}
		return m, nil

	case clearCopyFeedbackMsg:
		if !time.Now().Before(msg.at) {
			m.copyFeedback = ""
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case tea.MouseWheelMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		m.scrolledUp = !m.viewport.AtBottom()
		return m, vpCmd

	case tea.MouseClickMsg:
		headerHeight := 3
		vpEnd := headerHeight + m.viewport.Height()
		if msg.Button == tea.MouseLeft {
			if msg.Y >= headerHeight && msg.Y < vpEnd {
				m.isSelecting = true
				m.selectStartX = msg.X
				m.selectStartY = msg.Y
				m.selectEndX = msg.X
				m.selectEndY = msg.Y
				m.selectedText = ""
				return m, nil
			}
			if m.scrolledUp {
				m.viewport.GotoBottom()
				m.scrolledUp = false
				m.isSelecting = false
				m.selectedText = ""
				m.selectStartX = 0
				m.selectEndX = 0
				m.selectStartY = 0
				m.selectEndY = 0
				return m, nil
			}
			m.isSelecting = false
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
		}

	case tea.MouseMotionMsg:
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			return m, nil
		}

	case tea.MouseReleaseMsg:
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			text := m.extractSelectedText()
			m.isSelecting = false
			if strings.TrimSpace(text) != "" {
				m.selectedText = text
				_ = clipboard.WriteAll(text)
				count := utf8.RuneCountInString(text)
				m.copyFeedback = fmt.Sprintf("Copied %d chars to host clipboard", count)
				m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
				expiresAt := m.copyFeedbackExpiresAt
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearCopyFeedbackMsg{at: expiresAt}
				})
			}
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
			return m, nil
		}
	}

	// Default composer update
	var compCmd tea.Cmd
	m.composer, compCmd = m.composer.Update(msg)
	if compCmd != nil {
		cmds = append(cmds, compCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()

	// 1. Interrupt / Quit / Esc
	switch keyStr {
	case "ctrl+c":
		if m.selectedText != "" {
			_ = clipboard.WriteAll(m.selectedText)
			count := utf8.RuneCountInString(m.selectedText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars to host clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		if m.running && m.runtime != nil && m.runningExecution != "" {
			m.runtime.Interrupt(m.runningExecution)
			m.running = false
			m.addSystemEntry("Interrupted.")
			m.refreshViewportContent()
			return m, nil
		}
		return m, tea.Quit

	case "esc":
		if m.selectedText != "" || m.hasSelection() {
			m.isSelecting = false
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
			return m, nil
		}
		if m.autocompleteOpen {
			m.autocompleteOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.activeQuestion != nil {
			m.activeQuestion = nil
			m.updateLayout()
			return m, nil
		}
		if m.activeApproval != nil {
			m.activeApproval.Selected = 1
			return m.resolveActiveApproval()
		}
		if m.activePlanReview != nil {
			m.activePlanReview = nil
			m.updateLayout()
			return m, nil
		}
		if m.scrolledUp {
			m.scrolledUp = false
			m.viewport.GotoBottom()
			return m, nil
		}
		if m.running && m.runtime != nil && m.runningExecution != "" {
			m.runtime.Interrupt(m.runningExecution)
			m.running = false
			m.addSystemEntry("Interrupted.")
			m.refreshViewportContent()
			return m, nil
		}

	case "shift+tab":
		if !m.planMode {
			// Cycle permission mode: read-only -> workspace -> ask -> full-access -> read-only
			nextMode := core.PermissionWorkspace
			switch m.permissionMode {
			case core.PermissionReadOnly:
				nextMode = core.PermissionWorkspace
			case core.PermissionWorkspace:
				nextMode = core.PermissionAsk
			case core.PermissionAsk:
				nextMode = core.PermissionFullAccess
			case core.PermissionFullAccess:
				nextMode = core.PermissionReadOnly
			}
			_ = m.client.SetPermissionMode(nextMode)
			m.permissionMode = nextMode
			m.updateLayout()
		}
		return m, nil

	case "ctrl+t":
		// Toggle tool expand for latest tool entry
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindTool {
				m.entries[i].ToolExpanded = !m.entries[i].ToolExpanded
				m.refreshViewportContent()
				break
			}
		}
		return m, nil
	}

	// 2. Navigation in Modal / Autocomplete
	if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		switch keyStr {
		case "up":
			if m.autocompleteIndex > 0 {
				m.autocompleteIndex--
			} else {
				m.autocompleteIndex = len(m.autocompleteItems) - 1
			}
			return m, nil
		case "down":
			if m.autocompleteIndex < len(m.autocompleteItems)-1 {
				m.autocompleteIndex++
			} else {
				m.autocompleteIndex = 0
			}
			return m, nil
		case "tab", "enter":
			selected := m.autocompleteItems[m.autocompleteIndex].Command
			m.composer.SetValue(selected + " ")
			m.autocompleteOpen = false
			m.updateLayout()
			return m, nil
		}
	}

	if m.activeApproval != nil {
		switch keyStr {
		case "up", "k":
			if m.activeApproval.Selected > 0 {
				m.activeApproval.Selected--
				m.updateLayout()
			}
			return m, nil
		case "down", "j":
			if m.activeApproval.Selected < 1 {
				m.activeApproval.Selected++
				m.updateLayout()
			}
			return m, nil
		case "enter":
			return m.resolveActiveApproval()
		}
		return m, nil
	}

	if m.activeQuestion != nil && len(m.activeQuestion.Request.Questions) > 0 {
		maxOpt := len(m.activeQuestion.Request.Questions[0].Options) // including "None of the above"
		switch keyStr {
		case "up":
			if m.activeQuestion.Selected > 0 {
				m.activeQuestion.Selected--
			} else {
				m.activeQuestion.Selected = maxOpt
			}
			m.updateLayout()
			return m, nil
		case "down":
			if m.activeQuestion.Selected < maxOpt {
				m.activeQuestion.Selected++
			} else {
				m.activeQuestion.Selected = 0
			}
			m.updateLayout()
			return m, nil
		case "tab":
			m.activeQuestion.NotesOpen = !m.activeQuestion.NotesOpen
			return m, nil
		case "enter":
			return m.resolveActiveQuestion()
		}
	}

	if m.activePlanReview != nil {
		switch keyStr {
		case "up":
			if m.activePlanReview.Selected > 0 {
				m.activePlanReview.Selected--
			}
			m.updateLayout()
			return m, nil
		case "down":
			if m.activePlanReview.Selected < 2 {
				m.activePlanReview.Selected++
			}
			m.updateLayout()
			return m, nil
		case "enter":
			return m.resolveActivePlanReview()
		}
	}

	// 3. Viewport Scrolling
	switch keyStr {
	case "pgup":
		m.viewport.HalfPageUp()
		m.scrolledUp = !m.viewport.AtBottom()
		return m, nil
	case "pgdown":
		m.viewport.HalfPageDown()
		m.scrolledUp = !m.viewport.AtBottom()
		return m, nil
	case "alt+end":
		m.viewport.GotoBottom()
		m.scrolledUp = false
		return m, nil
	}

	// 4. Shift+Enter / Alt+Enter to insert newline
	key := msg.Key()
	isShiftEnter := (key.Code == tea.KeyEnter && (key.Mod&tea.ModShift != 0 || key.Mod&tea.ModAlt != 0 || key.Mod&tea.ModCtrl != 0)) ||
		keyStr == "shift+enter" || keyStr == "alt+enter" || keyStr == "ctrl+enter"
	if isShiftEnter {
		m.composer.InsertRune('\n')
		m.updateLayout()
		return m, nil
	}

	// 5. Enter to submit Composer
	if keyStr == "enter" {
		val := strings.TrimSpace(m.composer.Value())
		if val == "" {
			return m, nil
		}
		m.composer.Reset()
		m.autocompleteOpen = false
		m.updateLayout()

		if strings.HasPrefix(val, "/") {
			return m.handleSlashCommand(val)
		}
		return m.submitTurn(val)
	}

	// 5. Update composer text & autocomplete triggers
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	val := m.composer.Value()
	if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		items := filterCommands(val)
		if len(items) > 0 {
			m.autocompleteOpen = true
			m.autocompleteItems = items
			m.autocompleteIndex = 0
		} else {
			m.autocompleteOpen = false
		}
	} else {
		m.autocompleteOpen = false
	}
	m.updateLayout()

	return m, cmd
}

func (m *tuiModel) resolveActiveQuestion() (tea.Model, tea.Cmd) {
	q := m.activeQuestion
	if q == nil || len(q.Request.Questions) == 0 {
		return m, nil
	}

	qItem := q.Request.Questions[0]
	optCount := len(qItem.Options)
	var answerText string
	if q.Selected < optCount {
		opt := qItem.Options[q.Selected]
		answerText = opt.Label
	} else {
		answerText = "None of the above"
		if q.Custom != "" {
			answerText = q.Custom
		}
	}

	m.entries = append(m.entries, TranscriptEntry{
		Kind:         KindQuestionAnswered,
		QuestionText: qItem.Question,
		AnswerText:   answerText,
	})

	qAnswer := core.UserQuestionAnswer{
		ExecutionID:  q.Request.ExecutionID,
		RunID:        q.Request.RunID,
		InvocationID: q.Request.InvocationID,
		CallID:       q.Request.CallID,
		Answers: []core.UserQuestionResponse{
			{
				QuestionID: qItem.ID,
				Answer:     answerText,
			},
		},
	}

	m.activeQuestion = nil
	m.refreshViewportContent()
	m.updateLayout()

	runtime := m.runtime
	return m, func() tea.Msg {
		_ = runtime.ResolveUserQuestion(context.Background(), qAnswer)
		return nil
	}
}

func (m *tuiModel) resolveActivePlanReview() (tea.Model, tea.Cmd) {
	review := m.activePlanReview
	if review == nil {
		return m, nil
	}

	var mode core.PlanImplementationMode
	switch review.Selected {
	case 0:
		mode = core.PlanImplementSameContext
	case 1:
		mode = core.PlanImplementNewContext
	default:
		m.activePlanReview = nil
		m.updateLayout()
		return m, nil
	}

	m.activePlanReview = nil
	m.planMode = false
	_ = m.client.SetPermissionMode(m.savedMode)
	m.permissionMode = m.savedMode
	m.updateLayout()

	runtime := m.runtime
	return m, func() tea.Msg {
		impl, err := runtime.ImplementPlan(context.Background(), review.ExecutionID, mode)
		return planImplementedMsg{impl: impl, err: err}
	}
}

func (m *tuiModel) resolveActiveApproval() (tea.Model, tea.Cmd) {
	appr := m.activeApproval
	if appr == nil {
		return m, nil
	}
	allowed := appr.Selected == 0
	apprID := appr.Request.ID
	toolName := appr.Request.Tool
	m.activeApproval = nil
	m.updateLayout()

	decision := "Denied"
	if allowed {
		decision = "Allowed"
	}
	m.addSystemEntry(fmt.Sprintf("%s tool execution for `%s`.", decision, toolName))

	runtime := m.runtime
	return m, func() tea.Msg {
		_ = runtime.ResolveApproval(apprID, allowed)
		return nil
	}
}
