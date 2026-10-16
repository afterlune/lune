package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/afterlune/luneCore"
)

type mcpCommandResultMsg struct {
	operation string
	name      string
	err       error
}

type mcpOpenURLResultMsg struct {
	interactionID string
	err           error
}

type mcpInteractionField struct {
	name        string
	title       string
	description string
	typeName    string
	format      string
	required    bool
	enum        []string
	enumNames   []string
	defaultRaw  json.RawMessage
	value       string
	minLength   *int
	maxLength   *int
	minimum     *float64
	maximum     *float64
}

type mcpInteractionState struct {
	request core.UserInteractionRequest
	mode    string
	message string
	target  string
	fields  []mcpInteractionField
	index   int
	input   textinput.Model
	err     string
	opening bool
}

func (m *tuiModel) handleMCPCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) == 1 {
		m.addEntry("notice", "Use /mcp list, /mcp enable <name>, /mcp disable <name>, or /mcp auth <name>.", false)
		m.status = "MCP server management."
		m.resetInput()
		return m, nil
	}
	if m.client == nil {
		m.status = "MCP server management is unavailable."
		m.resetInput()
		return m, nil
	}
	if len(fields) == 2 && fields[1] == "list" {
		m.showMCPServers(m.client.MCPServers())
		m.resetInput()
		return m, nil
	}
	if len(fields) != 3 || (fields[1] != "enable" && fields[1] != "disable" && fields[1] != "auth") {
		m.addEntry("error", "Usage: /mcp list | /mcp enable <name> | /mcp disable <name> | /mcp auth <name>", true)
		m.status = "MCP command was not accepted."
		m.resetInput()
		return m, nil
	}
	if m.running {
		m.status = "Wait for the current task to finish before changing MCP servers."
		m.addEntry("error", m.status, true)
		m.resetInput()
		return m, nil
	}
	if m.mcpCommandPending {
		m.status = "An MCP operation is already in progress."
		m.resetInput()
		return m, nil
	}
	operation, name := fields[1], fields[2]
	if operation == "auth" && m.mcpAuthCancel != nil {
		m.status = "MCP authorization is already in progress."
		m.resetInput()
		return m, nil
	}
	m.mcpCommandPending = true
	m.status = mcpOperationStatus(operation, name)
	m.resetInput()
	var authContext context.Context
	if operation == "auth" {
		authContext, m.mcpAuthCancel = context.WithCancel(context.Background())
		m.addEntry("notice", "Authorization opened in your browser. Finish sign-in, then return to Lune.", false)
	}
	client := m.client
	return m, func() tea.Msg {
		var err error
		switch operation {
		case "enable":
			err = client.EnableMCPServer(name)
		case "disable":
			err = client.DisableMCPServer(name)
		case "auth":
			err = client.AuthenticateMCPServer(authContext, name)
		}
		return mcpCommandResultMsg{operation: operation, name: name, err: err}
	}
}

func mcpOperationStatus(operation, name string) string {
	switch operation {
	case "enable":
		return "Connecting MCP server " + cleanInlineText(name) + "…"
	case "disable":
		return "Stopping MCP server " + cleanInlineText(name) + "…"
	case "auth":
		return "Waiting for MCP authorization for " + cleanInlineText(name) + "…"
	default:
		return "MCP operation in progress…"
	}
}

func (m *tuiModel) finishMCPCommand(message mcpCommandResultMsg) {
	m.mcpCommandPending = false
	if message.operation == "auth" && m.mcpAuthCancel != nil {
		m.mcpAuthCancel()
		m.mcpAuthCancel = nil
	}
	if message.err != nil {
		if message.operation == "auth" && errors.Is(message.err, context.Canceled) {
			m.status = "MCP authorization cancelled: " + cleanInlineText(message.name) + "."
			m.addEntry("notice", m.status, false)
			return
		}
		m.status = "MCP " + message.operation + " failed."
		m.addEntry("error", m.status+" "+cleanInlineText(message.err.Error()), true)
		return
	}
	switch message.operation {
	case "enable":
		m.status = "MCP server enabled: " + cleanInlineText(message.name) + "."
		m.addEntry("notice", m.status, false)
	case "disable":
		m.status = "MCP server disabled: " + cleanInlineText(message.name) + "."
		m.addEntry("notice", m.status, false)
	case "auth":
		m.status = "MCP authorization completed: " + cleanInlineText(message.name) + "."
		m.addEntry("notice", "OAuth credentials stored in the operating system keyring.", false)
	}
}

func (m *tuiModel) showMCPServers(servers []core.MCPServerStatus) {
	if len(servers) == 0 {
		m.addEntry("notice", "No MCP servers are configured. Add servers under mcpServers in config.json.", false)
		m.status = "No MCP servers configured."
		return
	}
	var lines []string
	lines = append(lines, "MCP servers")
	for _, server := range servers {
		enabled, connected := "disabled", "disconnected"
		if server.Enabled {
			enabled = "enabled"
		}
		if server.Connected {
			connected = "connected"
		}
		count := "—"
		if server.Connected {
			count = strconv.Itoa(server.ToolCount)
		}
		lines = append(lines, fmt.Sprintf("  %-18s %-16s %s · %s · %s tools", cleanInlineText(server.Name), server.Transport, enabled, connected, count))
		if server.Endpoint != "" {
			lines = append(lines, "    "+cleanInlineText(server.Endpoint))
		}
		if server.Command != "" {
			lines = append(lines, "    command: "+cleanInlineText(server.Command))
		}
		if server.LastError != "" {
			lines = append(lines, "    status: "+cleanInlineText(server.LastError))
		}
	}
	lines = append(lines, "Use /mcp enable <name>, /mcp disable <name>, or /mcp auth <name>.")
	m.addEntry("notice", strings.Join(lines, "\n"), false)
	m.status = "MCP server list updated."
}

func (m *tuiModel) beginMCPInteraction(request core.UserInteractionRequest) tea.Cmd {
	if m.client == nil {
		return nil
	}
	if m.pendingMCPInteraction != nil {
		m.addEntry("error", "Another MCP request is already awaiting input; the new request was declined.", true)
		m.rejectMCPInteraction(request.ID)
		return nil
	}
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Mode            string          `json:"mode"`
			Message         string          `json:"message"`
			URL             string          `json:"url"`
			RequestedSchema json.RawMessage `json:"requestedSchema"`
		} `json:"params"`
	}
	if err := json.Unmarshal(request.Request, &envelope); err != nil || envelope.Method != "elicitation/create" {
		m.status = "MCP requested an unsupported user interaction."
		m.addEntry("error", m.status, true)
		m.rejectMCPInteraction(request.ID)
		return nil
	}
	state := &mcpInteractionState{
		request: request, mode: envelope.Params.Mode,
		message: cleanInlineText(envelope.Params.Message), target: envelope.Params.URL,
	}
	switch state.mode {
	case "form":
		fields, err := parseMCPForm(envelope.Params.RequestedSchema)
		if err != nil {
			m.status = "Unsupported MCP form request."
			m.addEntry("error", m.status+" "+cleanInlineText(err.Error()), true)
			m.rejectMCPInteraction(request.ID)
			return nil
		}
		state.fields = fields
	case "url":
		if _, err := parseMCPExternalURL(state.target); err != nil {
			m.status = "Invalid MCP authorization URL."
			m.addEntry("error", m.status+" "+cleanInlineText(err.Error()), true)
			m.rejectMCPInteraction(request.ID)
			return nil
		}
	default:
		m.status = "Unsupported MCP elicitation mode."
		m.addEntry("error", m.status, true)
		m.rejectMCPInteraction(request.ID)
		return nil
	}
	state.input = newMCPTextInput(m.resolvedTheme())
	if len(state.fields) > 0 {
		setMCPFieldPlaceholder(&state.input, state.fields[0])
		state.input.SetValue(state.fields[0].value)
	}
	state.input.SetWidth(m.mcpInteractionInputWidth())
	m.pendingMCPInteraction = state
	m.input.Blur()
	m.status = "MCP server needs your input: " + cleanInlineText(request.ServerName)
	m.updateLayout()
	if state.mode == "form" {
		return state.input.Focus()
	}
	return nil
}

func newMCPTextInput(theme themeName) textinput.Model {
	input := textinput.New()
	input.Prompt = "  "
	input.CharLimit = 0
	input.SetVirtualCursor(false)
	input.SetStyles(textinput.DefaultStyles(theme != themeLight))
	return input
}

func setMCPFieldPlaceholder(input *textinput.Model, field mcpInteractionField) {
	switch {
	case len(field.enum) > 0:
		input.Placeholder = "Type one of the listed choices"
	case field.typeName == "boolean":
		input.Placeholder = "true or false"
	case field.typeName == "integer":
		input.Placeholder = "Whole number"
	case field.typeName == "number":
		input.Placeholder = "Number"
	default:
		input.Placeholder = "Enter a value"
	}
}

func (m *tuiModel) applyMCPInteractionTheme(theme themeName) {
	if m.pendingMCPInteraction == nil {
		return
	}
	m.pendingMCPInteraction.input.SetStyles(textinput.DefaultStyles(theme != themeLight))
}

func (m *tuiModel) mcpInteractionInputWidth() int {
	return max(12, m.width-6)
}

func (m *tuiModel) updateMCPInteractionWidth(width int) {
	if m.pendingMCPInteraction != nil {
		m.pendingMCPInteraction.input.SetWidth(max(12, width-6))
	}
}

func (m *tuiModel) activeCursor() *tea.Cursor {
	if m.questionOpen {
		if m.question != nil && m.question.notes {
			return m.question.input.Cursor()
		}
		return nil
	}
	if m.pendingMCPInteraction != nil {
		if m.pendingMCPInteraction.mode == "form" && len(m.pendingMCPInteraction.fields) > 0 {
			return m.pendingMCPInteraction.input.Cursor()
		}
		return nil
	}
	return m.input.Cursor()
}

func (m *tuiModel) handleMCPInteractionKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.pendingMCPInteraction
	if state == nil {
		return m, nil
	}
	if state.opening {
		return m, nil
	}
	if state.mode == "url" {
		switch {
		case m.matchesKey("mcp_url_open", message):
			state.opening = true
			state.err = ""
			m.status = "Opening the confirmed MCP page…"
			client := m.client
			serverName, target, id := state.request.ServerName, state.target, state.request.ID
			return m, func() tea.Msg {
				return mcpOpenURLResultMsg{interactionID: id, err: client.OpenMCPURL(serverName, target)}
			}
		case m.matchesKey("mcp_url_decline", message):
			m.resolveMCPInteractionResponse(state.request.ID, "decline", nil)
		case message.String() == "esc":
			m.resolveMCPInteractionResponse(state.request.ID, "cancel", nil)
		}
		m.updateLayout()
		return m, nil
	}
	switch {
	case m.matchesKey("mcp_field_next", message):
		m.moveMCPFormField(1)
		return m, nil
	case m.matchesKey("mcp_field_previous", message):
		m.moveMCPFormField(-1)
		return m, nil
	case m.matchesKey("send", message):
		m.acceptMCPForm()
		return m, nil
	case m.matchesKey("mcp_form_decline", message):
		m.resolveMCPInteractionResponse(state.request.ID, "decline", nil)
		return m, nil
	case message.String() == "esc":
		m.resolveMCPInteractionResponse(state.request.ID, "cancel", nil)
		return m, nil
	default:
		var command tea.Cmd
		state.input, command = state.input.Update(message)
		m.updateLayout()
		m.refreshPanels()
		return m, command
	}
}

func (m *tuiModel) updateMCPInteractionInput(message tea.Msg) tea.Cmd {
	if m.pendingMCPInteraction == nil || m.pendingMCPInteraction.mode != "form" {
		return nil
	}
	var command tea.Cmd
	m.pendingMCPInteraction.input, command = m.pendingMCPInteraction.input.Update(message)
	m.pendingMCPInteraction.err = ""
	m.updateLayout()
	m.refreshPanels()
	return command
}

func (m *tuiModel) moveMCPFormField(direction int) {
	state := m.pendingMCPInteraction
	if state == nil || len(state.fields) == 0 {
		return
	}
	state.fields[state.index].value = state.input.Value()
	state.index = (state.index + direction + len(state.fields)) % len(state.fields)
	state.err = ""
	state.input.Reset()
	setMCPFieldPlaceholder(&state.input, state.fields[state.index])
	state.input.SetValue(state.fields[state.index].value)
	state.input.CursorEnd()
	m.updateLayout()
}

func (m *tuiModel) acceptMCPForm() {
	state := m.pendingMCPInteraction
	if state == nil {
		return
	}
	if len(state.fields) > 0 {
		state.fields[state.index].value = state.input.Value()
	}
	content := make(map[string]any)
	for _, field := range state.fields {
		value, include, err := field.responseValue()
		if err != nil {
			state.err = field.title + ": " + err.Error()
			m.status = "Check the MCP form fields."
			m.updateLayout()
			return
		}
		if include {
			content[field.name] = value
		}
	}
	m.resolveMCPInteractionResponse(state.request.ID, "accept", content)
}

func (field mcpInteractionField) responseValue() (any, bool, error) {
	value := strings.TrimSpace(field.value)
	if value == "" {
		if field.required {
			return nil, false, errors.New("this field is required")
		}
		return nil, false, nil
	}
	switch field.typeName {
	case "string":
		if field.minLength != nil && utf8.RuneCountInString(value) < *field.minLength {
			return nil, false, fmt.Errorf("must contain at least %d characters", *field.minLength)
		}
		if field.maxLength != nil && utf8.RuneCountInString(value) > *field.maxLength {
			return nil, false, fmt.Errorf("must contain at most %d characters", *field.maxLength)
		}
		if len(field.enum) > 0 && !containsMCPChoice(field.enum, value) {
			return nil, false, errors.New("choose one of the listed values")
		}
		if err := validateMCPStringFormat(field.format, value); err != nil {
			return nil, false, err
		}
		return value, true, nil
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, false, errors.New("enter true or false")
		}
		return parsed, true, nil
	case "integer":
		integer, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return nil, false, errors.New("enter a whole number")
		}
		number := new(big.Float).SetInt(integer)
		if field.minimum != nil && number.Cmp(new(big.Float).SetFloat64(*field.minimum)) < 0 ||
			field.maximum != nil && number.Cmp(new(big.Float).SetFloat64(*field.maximum)) > 0 {
			return nil, false, errors.New("number is outside the requested range")
		}
		return json.Number(integer.String()), true, nil
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, false, errors.New("enter a finite number")
		}
		if field.minimum != nil && number < *field.minimum || field.maximum != nil && number > *field.maximum {
			return nil, false, errors.New("number is outside the requested range")
		}
		return json.Number(strconv.FormatFloat(number, 'f', -1, 64)), true, nil
	default:
		return nil, false, errors.New("unsupported field type")
	}
}

func validateMCPStringFormat(format, value string) error {
	switch format {
	case "email":
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value {
			return errors.New("enter a valid email address")
		}
	case "uri":
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" {
			return errors.New("enter an absolute URI")
		}
	case "date":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return errors.New("enter a date in YYYY-MM-DD format")
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return errors.New("enter a date-time in RFC3339 format")
		}
	}
	return nil
}

func (m *tuiModel) resolveMCPInteractionResponse(id, action string, content map[string]any) {
	state := m.pendingMCPInteraction
	if state == nil || state.request.ID != id {
		return
	}
	if err := m.sendMCPInteractionResponse(id, action, content); err != nil {
		state.err = "Could not send the response: " + cleanInlineText(err.Error())
		m.status = "Could not resolve MCP interaction."
		m.updateLayout()
		return
	}
	message := "MCP request accepted."
	if action == "decline" {
		message = "MCP request declined."
	} else if action == "cancel" {
		message = "MCP request cancelled."
	}
	m.finishMCPInteraction(message, true)
}

func (m *tuiModel) rejectMCPInteraction(id string) {
	if err := m.sendMCPInteractionResponse(id, "decline", nil); err != nil {
		m.addEntry("error", "Could not decline the MCP request: "+cleanInlineText(err.Error()), true)
	}
}

func (m *tuiModel) sendMCPInteractionResponse(id, action string, content map[string]any) error {
	if m.runtime == nil {
		return errors.New("MCP client is unavailable")
	}
	response := map[string]any{"action": action}
	if action == "accept" && m.pendingMCPInteraction != nil && m.pendingMCPInteraction.request.ID == id && m.pendingMCPInteraction.mode == "form" {
		response["content"] = content
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return m.runtime.ResolveUserInteraction(id, data)
}

func (m *tuiModel) finishMCPInteraction(message string, addNotice bool) {
	m.pendingMCPInteraction = nil
	m.input.Focus()
	m.status = message
	if addNotice {
		m.addEntry("notice", message, false)
	}
	m.updateLayout()
	m.refreshPanels()
}

func (m *tuiModel) finishMCPURLOpen(message mcpOpenURLResultMsg) (tea.Model, tea.Cmd) {
	state := m.pendingMCPInteraction
	if state == nil || state.request.ID != message.interactionID {
		return m, nil
	}
	state.opening = false
	if message.err != nil {
		state.err = "Could not open the page: " + cleanInlineText(message.err.Error())
		m.status = "The MCP page was not opened."
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	}
	m.resolveMCPInteractionResponse(state.request.ID, "accept", nil)
	return m, nil
}

func (m *tuiModel) renderMCPInteraction() string {
	state := m.pendingMCPInteraction
	if state == nil {
		return ""
	}
	colors := m.themeColors()
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).Render("The MCP server needs your input · "+cleanInlineText(state.request.ServerName)))
	if state.message != "" {
		lines = append(lines, fitTerminalText(cleanInlineText(state.message), m.mcpInteractionInputWidth()))
	}
	if state.mode == "url" {
		lines = append(lines, fitTerminalText("Destination: "+mcpURLDisplay(state.target), m.mcpInteractionInputWidth()),
			lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Warning)).Render(fitTerminalText("Check the site before continuing. Lune will not collect page passwords.", m.mcpInteractionInputWidth())))
		if state.opening {
			lines = append(lines, "Opening the confirmed page…")
		} else {
			if state.err != "" {
				lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error)).Render(fitTerminalText(cleanInlineText(state.err), m.mcpInteractionInputWidth())))
			}
			lines = append(lines, fitTerminalText(m.keyLabel("mcp_url_open")+" open · "+m.keyLabel("mcp_url_decline")+" decline · Esc cancel", m.mcpInteractionInputWidth()))
		}
	} else {
		lines = append(lines, "Fields · Tab/Shift+Tab to move")
		start, end := mcpVisibleFieldRange(state.index, len(state.fields), 1)
		for index := start; index < end; index++ {
			field := state.fields[index]
			marker := " "
			if index == state.index {
				marker = "›"
			}
			label := field.title
			if field.required {
				label += " · required"
			}
			lines = append(lines, fitTerminalText(marker+" "+cleanInlineText(label), m.mcpInteractionInputWidth()))
			if field.description != "" {
				lines = append(lines, "  "+fitTerminalText(cleanInlineText(field.description), m.mcpInteractionInputWidth()))
			}
			if index == state.index {
				lines = append(lines, state.input.View())
			} else if field.value != "" {
				lines = append(lines, "  "+cleanInlineText(field.value))
			}
			if len(field.enum) > 0 {
				lines = append(lines, fitTerminalText("  Choices: "+strings.Join(field.enumLabels(), " · "), m.mcpInteractionInputWidth()))
			}
		}
		if start > 0 || end < len(state.fields) {
			lines = append(lines, fmt.Sprintf("Showing fields %d-%d of %d", start+1, end, len(state.fields)))
		}
		if state.err != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error)).Render(fitTerminalText(cleanInlineText(state.err), m.mcpInteractionInputWidth())))
		}
		lines = append(lines, fitTerminalText("Tab next · Enter submit · "+m.keyLabel("mcp_form_decline")+" decline · Esc cancel", m.mcpInteractionInputWidth()))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func mcpVisibleFieldRange(current, total, maximum int) (int, int) {
	if total <= maximum {
		return 0, total
	}
	start := max(0, current-maximum/2)
	if start+maximum > total {
		start = total - maximum
	}
	return start, start + maximum
}

func (m *tuiModel) mcpInteractionFooter() string {
	if m.pendingMCPInteraction == nil {
		return m.status
	}
	if m.pendingMCPInteraction.mode == "url" {
		return "MCP authorization · " + m.keyLabel("mcp_url_open") + " open · " + m.keyLabel("mcp_url_decline") + " decline · Esc cancel"
	}
	return "MCP form · Tab next field · Enter submit · " + m.keyLabel("mcp_form_decline") + " decline · Esc cancel"
}

func (m *tuiModel) mcpAuthFooter() string {
	return m.status + " · Esc cancel"
}

func parseMCPForm(raw json.RawMessage) ([]mcpInteractionField, error) {
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema.Type != "object" || schema.Properties == nil {
		return nil, errors.New("the server supplied an invalid form schema")
	}
	if len(schema.Properties) == 0 {
		return nil, errors.New("the server supplied a form without fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	for name := range required {
		if _, exists := schema.Properties[name]; !exists {
			return nil, fmt.Errorf("required field %q is missing from properties", name)
		}
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]mcpInteractionField, 0, len(names))
	for _, name := range names {
		var property struct {
			Type        string          `json:"type"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Format      string          `json:"format"`
			Default     json.RawMessage `json:"default"`
			Enum        []string        `json:"enum"`
			EnumNames   []string        `json:"enumNames"`
			MinLength   *int            `json:"minLength"`
			MaxLength   *int            `json:"maxLength"`
			Minimum     *float64        `json:"minimum"`
			Maximum     *float64        `json:"maximum"`
		}
		if err := json.Unmarshal(schema.Properties[name], &property); err != nil {
			return nil, fmt.Errorf("field %q has an invalid schema", name)
		}
		switch property.Type {
		case "string", "integer", "number", "boolean":
		default:
			return nil, fmt.Errorf("field %q uses unsupported type %q", name, property.Type)
		}
		if len(property.Enum) > 0 && property.Type != "string" {
			return nil, fmt.Errorf("field %q uses enum with a non-string type", name)
		}
		if len(property.EnumNames) > 0 && len(property.EnumNames) != len(property.Enum) {
			return nil, fmt.Errorf("field %q has a mismatched enumNames list", name)
		}
		if property.MinLength != nil && *property.MinLength < 0 || property.MaxLength != nil && *property.MaxLength < 0 {
			return nil, fmt.Errorf("field %q has a negative length constraint", name)
		}
		if property.MinLength != nil && property.MaxLength != nil && *property.MinLength > *property.MaxLength {
			return nil, fmt.Errorf("field %q has inconsistent length constraints", name)
		}
		if property.Minimum != nil && property.Maximum != nil && *property.Minimum > *property.Maximum {
			return nil, fmt.Errorf("field %q has inconsistent numeric constraints", name)
		}
		field := mcpInteractionField{
			name: name, title: property.Title, description: property.Description,
			typeName: property.Type, format: property.Format, required: required[name],
			enum: property.Enum, enumNames: property.EnumNames, defaultRaw: property.Default,
			minLength: property.MinLength, maxLength: property.MaxLength,
			minimum: property.Minimum, maximum: property.Maximum,
		}
		if field.title == "" {
			field.title = name
		}
		if len(field.defaultRaw) > 0 && string(field.defaultRaw) != "null" {
			value, err := mcpDefaultValue(field.typeName, field.defaultRaw)
			if err != nil {
				return nil, fmt.Errorf("field %q has an invalid default", name)
			}
			field.value = value
			if value != "" {
				if _, _, err := field.responseValue(); err != nil {
					return nil, fmt.Errorf("field %q has an invalid default: %w", name, err)
				}
			}
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func mcpDefaultValue(typeName string, raw json.RawMessage) (string, error) {
	switch typeName {
	case "string":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	case "boolean":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return strconv.FormatBool(value), nil
	case "integer", "number":
		var value json.Number
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value.String(), nil
	default:
		return "", errors.New("unsupported type")
	}
}

func (field mcpInteractionField) enumLabels() []string {
	labels := make([]string, len(field.enum))
	for index, value := range field.enum {
		labels[index] = cleanInlineText(value)
		if index < len(field.enumNames) && field.enumNames[index] != "" {
			labels[index] += " (" + cleanInlineText(field.enumNames[index]) + ")"
		}
	}
	return labels
}

func containsMCPChoice(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func parseMCPExternalURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, errors.New("URL contains whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, errors.New("expected an absolute http or https URL without user information")
	}
	return parsed, nil
}

func mcpURLDisplay(raw string) string {
	parsed, err := parseMCPExternalURL(raw)
	if err != nil {
		return "Invalid URL"
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	return parsed.String()
}
