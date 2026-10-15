package tui

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

var reasoningEffortOptions = []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"}

func (m *tuiModel) showGoal() (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(m.sessionSettings.Goal)
	if goal == "" {
		m.addEntry("notice", "No task goal is set. Use /goal <text> to set one.", false)
	} else {
		m.addEntry("notice", "Task goal:\n"+goal, false)
	}
	m.status = "Goal applies to future tasks in this session."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showContextSettings() (tea.Model, tea.Cmd) {
	defaults := m.projectContextDefaults()
	window, windowSource := defaults.ContextWindow, "project default"
	reserve, reserveSource := defaults.ReserveOutputTokens, "project default"
	if m.sessionSettings.ContextWindow != nil {
		window, windowSource = *m.sessionSettings.ContextWindow, "session override"
	}
	if m.sessionSettings.ReserveOutputTokens != nil {
		reserve, reserveSource = *m.sessionSettings.ReserveOutputTokens, "session override"
	}
	windowLabel := "unknown"
	if window > 0 {
		windowLabel = compactTokens(window)
	} else if windowSource == "session override" {
		windowLabel = "disabled"
	}
	reserveLabel := "none"
	if reserve > 0 {
		reserveLabel = compactTokens(reserve)
	}
	message := fmt.Sprintf("Context window: %s (%s)\nOutput reserve: %s (%s)", windowLabel, windowSource, reserveLabel, reserveSource)
	message += "\nUse /context <tokens>, /context window <tokens|reset>, /context reserve <tokens|reset>, or /context reset."
	message += "\nWhen automatic compaction is enabled, the context window sets its trigger budget."
	message += "\nThe output reserve leaves room for the model's response when compaction is triggered."
	m.addEntry("notice", message, false)
	m.status = "Context settings are session overrides; project defaults remain unchanged."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) projectContextDefaults() core.ContextDefaults {
	if m.client == nil {
		return core.ContextDefaults{}
	}
	return m.client.ProjectContextDefaults()
}

func (m *tuiModel) setContextSettingsFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	settings := m.sessionSettings
	message := ""
	switch {
	case len(fields) == 2 && fields[1] == "reset":
		settings.ContextWindow = nil
		settings.ReserveOutputTokens = nil
		message = "Context window and output reserve overrides cleared."
	case len(fields) == 3 && fields[1] == "window" && fields[2] == "reset":
		settings.ContextWindow = nil
		message = "Context window override cleared."
	case len(fields) == 3 && fields[1] == "reserve" && fields[2] == "reset":
		settings.ReserveOutputTokens = nil
		message = "Output reserve override cleared."
	case len(fields) == 2:
		value, ok := parseContextTokenCount(fields[1])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ContextWindow = &value
		if value == 0 {
			message = "Context window cleared. Automatic compaction will wait for a positive window."
		} else {
			message = "Context window saved. Automatic compaction can use it on future tasks."
		}
	case len(fields) == 3 && fields[1] == "window":
		value, ok := parseContextTokenCount(fields[2])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ContextWindow = &value
		if value == 0 {
			message = "Context window cleared. Automatic compaction will wait for a positive window."
		} else {
			message = "Context window saved. Automatic compaction can use it on future tasks."
		}
	case len(fields) == 3 && fields[1] == "reserve":
		value, ok := parseContextTokenCount(fields[2])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ReserveOutputTokens = &value
		message = "Output reserve saved for future tasks. It reduces the input budget available before compaction."
	default:
		return m.contextCommandUsage()
	}
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Context settings could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	defaults := m.projectContextDefaults()
	m.contextWindow = defaults.ContextWindow
	if settings.ContextWindow != nil {
		m.contextWindow = *settings.ContextWindow
	}
	if m.pending {
		message += " The paused task keeps its original context settings."
	}
	m.status = message
	m.addEntry("notice", message, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showAutoCompactSettings() (tea.Model, tea.Cmd) {
	defaults := m.projectContextDefaults()
	enabled, enabledSource := defaults.AutoCompact, "project default"
	threshold, thresholdSource := defaults.AutoCompactThreshold, "project default"
	if m.sessionSettings.AutoCompact != nil {
		enabled, enabledSource = *m.sessionSettings.AutoCompact, "session override"
	}
	if m.sessionSettings.AutoCompactThreshold != nil {
		threshold, thresholdSource = *m.sessionSettings.AutoCompactThreshold, "session override"
	}
	if threshold == 0 {
		threshold = core.DefaultAutoCompactThreshold
		thresholdSource = "default when enabled"
	}
	state := "off"
	if enabled {
		state = "on"
	}
	message := fmt.Sprintf("Automatic compaction: %s (%s)\nThreshold: %.0f%% (%s)", state, enabledSource, threshold*100, thresholdSource)
	message += "\nAutomatic compaction runs before model requests when the configured threshold is reached."
	window := defaults.ContextWindow
	if m.sessionSettings.ContextWindow != nil {
		window = *m.sessionSettings.ContextWindow
	}
	if enabled && window <= 0 {
		message += "\nSet a context window with /context window <tokens> to activate automatic compaction."
	}
	message += "\nUse /autocompact on|off, /autocompact threshold <0..1>, or /autocompact reset."
	m.addEntry("notice", message, false)
	m.status = "Auto-compaction settings apply to future tasks."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) setAutoCompactFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	settings := m.sessionSettings
	message := ""
	switch {
	case len(fields) == 2 && fields[1] == "reset":
		settings.AutoCompact = nil
		settings.AutoCompactThreshold = nil
		message = "Auto-compaction overrides cleared."
	case len(fields) == 2 && (fields[1] == "on" || fields[1] == "off"):
		enabled := fields[1] == "on"
		settings.AutoCompact = &enabled
		message = fmt.Sprintf("Auto-compaction setting saved as %s.", fields[1])
	case len(fields) == 3 && fields[1] == "threshold" && fields[2] == "reset":
		settings.AutoCompactThreshold = nil
		message = "Auto-compaction threshold override cleared."
	case len(fields) == 3 && fields[1] == "threshold":
		threshold, err := strconv.ParseFloat(fields[2], 64)
		if err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 || threshold > 1 {
			m.status = "Threshold must be greater than 0 and at most 1."
			m.addEntry("error", m.status, true)
			return m, nil
		}
		settings.AutoCompactThreshold = &threshold
		message = fmt.Sprintf("Auto-compaction threshold saved as %.0f%%.", threshold*100)
	default:
		m.status = "Use /autocompact on|off, /autocompact threshold <0..1>, or /autocompact reset."
		return m, nil
	}
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Auto-compaction settings could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	message += " This setting applies to future tasks."
	if m.pending {
		message += " The paused task keeps its original settings."
	}
	m.status = message
	m.addEntry("notice", message, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) contextCommandUsage() (tea.Model, tea.Cmd) {
	m.status = "Use /context <tokens>, /context window <tokens|reset>, /context reserve <tokens|reset>, or /context reset."
	return m, nil
}

func parseContextTokenCount(value string) (int64, bool) {
	tokens, err := strconv.ParseInt(value, 10, 64)
	return tokens, err == nil && tokens >= 0
}

func (m *tuiModel) setGoalFromCommand(line string) (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(line[len("/goal"):])
	clear := strings.EqualFold(goal, "clear")
	if clear {
		goal = ""
	}
	settings := m.sessionSettings
	settings.Goal = goal
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Task goal could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	switch {
	case clear:
		m.status = "Task goal cleared."
	case goal == "":
		m.status = "Use /goal <text> to set a task goal, or /goal clear to remove it."
	default:
		m.status = "Task goal saved for future tasks."
	}
	if m.pending {
		m.status += " The paused task keeps its original goal."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showMessageDetails(line string) (tea.Model, tea.Cmd) {
	var messages []tuiEntry
	for _, entry := range m.entries {
		if entry.role == "you" {
			messages = append(messages, entry)
		}
	}
	fields := strings.Fields(line)
	if len(fields) == 1 {
		if len(messages) == 0 {
			m.status = "No user messages to show."
			m.addEntry("notice", m.status, false)
		} else {
			lines := []string{"User messages · use /details <n> to show one in the transcript:"}
			for index, entry := range messages {
				preview := strings.Join(strings.Fields(entry.text), " ")
				if runes := []rune(preview); len(runes) > 72 {
					preview = string(runes[:69]) + "…"
				}
				lines = append(lines, fmt.Sprintf("%d · %s", index+1, preview))
			}
			m.addEntry("notice", strings.Join(lines, "\n"), false)
			m.status = "Choose a message with /details <n>."
		}
		m.resetInput()
		return m, nil
	}
	if len(fields) != 2 {
		m.status = "Use /details <n> to show a user message in the transcript."
		return m, nil
	}
	index, err := strconv.Atoi(fields[1])
	if err != nil || index < 1 || index > len(messages) {
		m.status = "Unknown message number. Use /details to list user messages."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.addEntry("notice", fmt.Sprintf("User message %d:\n%s", index, messages[index-1].text), false)
	m.status = fmt.Sprintf("Displayed user message %d in the transcript.", index)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) openModelPicker() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Model selection is unavailable."
		return m, nil
	}
	options := m.client.ListModelProfiles()
	if len(options) == 0 {
		m.status = "No model profiles are configured."
		return m, nil
	}
	m.modelOptions = options
	m.modelIndex = 0
	for index, option := range options {
		if option.Profile == m.sessionSettings.ModelProfile {
			m.modelIndex = index
			break
		}
	}
	m.modelPicker = true
	m.resetInput()
	m.status = "Choose a model profile; " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select, " + m.keyLabel("send") + " apply, Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) openEffortPicker() (tea.Model, tea.Cmd) {
	m.effortIndex = 0
	for index, value := range reasoningEffortOptions {
		if value == m.sessionSettings.ReasoningEffort {
			m.effortIndex = index
			break
		}
	}
	m.effortPicker = true
	m.resetInput()
	m.status = "Choose reasoning effort; " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select, " + m.keyLabel("send") + " apply, Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) selectModelProfile() (tea.Model, tea.Cmd) {
	if m.modelIndex < 0 || m.modelIndex >= len(m.modelOptions) {
		m.modelPicker = false
		return m, nil
	}
	settings := m.sessionSettings
	settings.ModelProfile = m.modelOptions[m.modelIndex].Profile
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Model profile could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.modelPicker = false
	m.status = "Model profile set to " + settings.ModelProfile + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original model."
	}
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) selectReasoningEffort() (tea.Model, tea.Cmd) {
	if m.effortIndex < 0 || m.effortIndex >= len(reasoningEffortOptions) {
		m.effortPicker = false
		return m, nil
	}
	settings := m.sessionSettings
	settings.ReasoningEffort = reasoningEffortOptions[m.effortIndex]
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Reasoning effort could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.effortPicker = false
	m.status = "Reasoning effort set to " + reasoningEffortLabel(settings.ReasoningEffort) + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original effort."
	}
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) setModelFromCommand(line string) (tea.Model, tea.Cmd) {
	profile := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "/model"), "\t"))
	profile = strings.TrimSpace(strings.TrimPrefix(profile, " "))
	for _, option := range m.client.ListModelProfiles() {
		if option.Profile != profile {
			continue
		}
		settings := m.sessionSettings
		settings.ModelProfile = profile
		if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
			m.status = "Model profile could not be saved."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
		m.sessionSettings = settings
		m.status = "Model profile set to " + profile + "; it applies to future tasks."
		if m.pending {
			m.status += " /continue keeps the paused task's original model."
		}
		m.resetInput()
		return m, nil
	}
	m.status = "Unknown model profile. Use /model to list profiles."
	m.addEntry("error", m.status, true)
	return m, nil
}

func (m *tuiModel) setEffortFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /effort <default|none|minimal|low|medium|high|xhigh|max>."
		return m, nil
	}
	effort := strings.ToLower(fields[1])
	if effort == "default" {
		effort = ""
	}
	valid := false
	for _, option := range reasoningEffortOptions {
		if option == effort {
			valid = true
			break
		}
	}
	if !valid {
		m.status = "Unknown reasoning effort. Use /effort to list values."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	settings := m.sessionSettings
	settings.ReasoningEffort = effort
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Reasoning effort could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.status = "Reasoning effort set to " + reasoningEffortLabel(effort) + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original effort."
	}
	m.resetInput()
	return m, nil
}

func reasoningEffortLabel(value string) string {
	if value == "" {
		return "Default"
	}
	return value
}

func (m *tuiModel) setThemeFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /theme auto, /theme dark, or /theme light."
		return m, nil
	}
	theme := themeName(strings.ToLower(fields[1]))
	if theme != themeAuto && theme != themeDark && theme != themeLight {
		m.status = "Theme must be auto, dark, or light."
		return m, nil
	}
	m.preferences.Theme = theme
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status = "Theme changed for this session; preference could not be saved."
	} else {
		m.status = "Theme set to " + string(theme) + "."
	}
	if theme == themeAuto {
		m.terminalBackgroundReceived = false
		m.terminalForegroundReceived = false
		m.terminalColorQueryPending = true
	} else {
		m.terminalColorQueryPending = false
	}
	m.applyResolvedTheme()
	m.resetInput()
	if theme == themeAuto {
		return m, m.requestTerminalColorCommands()
	}
	return m, nil
}

func (m *tuiModel) themePreferenceLabel() string {
	if m.preferences.Theme == themeAuto {
		return "auto (" + string(m.resolvedTheme()) + ")"
	}
	return string(m.preferences.Theme)
}

func (m *tuiModel) requestTerminalColors() tea.Cmd {
	m.terminalBackgroundReceived = false
	m.terminalForegroundReceived = false
	m.terminalColorQueryPending = true
	return m.requestTerminalColorCommands()
}

func (m *tuiModel) requestTerminalColorCommands() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		tea.RequestForegroundColor,
		tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return terminalColorTimeoutMsg{} }),
	)
}

func (m *tuiModel) finishTerminalColorQueryIfReady() {
	if m.terminalColorQueryPending && m.terminalBackgroundReceived && m.terminalForegroundReceived {
		m.terminalColorQueryPending = false
	}
}
