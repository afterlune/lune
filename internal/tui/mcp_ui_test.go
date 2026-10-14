package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/luneCore"
	"github.com/charmbracelet/colorprofile"
)

func newMCPInteractionUI(client *fakeResponder) *tuiModel {
	ui := newTUIModel(client, false)
	ui.sessionID = "mcp-interaction"
	runtime, _ := client.OpenSessionRuntime(context.Background(), ui.sessionID)
	ui.runtime = runtime
	ui.runtimeSessionID = ui.sessionID
	return ui
}

func TestTUIMCPCommands(t *testing.T) {
	client := &fakeResponder{mcpServers: []core.MCPServerStatus{
		{Name: "docs", Transport: "streamable-http", Endpoint: "https://mcp.example.test/v1/mcp", Enabled: true, Connected: true, ToolCount: 3},
		{Name: "local", Transport: "stdio", Command: "mcp-local --root ./docs"},
	}}
	ui := newTUIModel(client, false)
	ui.handleMCPCommand("/mcp list")
	if got := strings.Join(entryTexts(ui.entries), "\n"); !strings.Contains(got, "docs") || !strings.Contains(got, "3 tools") || !strings.Contains(got, "mcp-local --root ./docs") {
		t.Fatalf("MCP list output = %q", got)
	}

	_, command := ui.handleMCPCommand("/mcp enable docs")
	if command == nil || !ui.mcpCommandPending {
		t.Fatal("enable did not start an asynchronous operation")
	}
	ui.Update(command())
	if ui.mcpCommandPending || !strings.Contains(ui.status, "enabled") || len(client.mcpActions) != 1 || client.mcpActions[0] != "enable:docs" {
		t.Fatalf("enable result: pending=%v status=%q actions=%v", ui.mcpCommandPending, ui.status, client.mcpActions)
	}

	_, command = ui.handleMCPCommand("/mcp auth docs")
	if command == nil || ui.mcpAuthCancel == nil {
		t.Fatal("auth did not start with a cancellable context")
	}
	ui.Update(command())
	if ui.mcpAuthCancel != nil || len(client.mcpActions) != 2 || client.mcpActions[1] != "auth:docs" {
		t.Fatalf("auth completion left stale state: cancel=%v actions=%v", ui.mcpAuthCancel != nil, client.mcpActions)
	}

	_, command = ui.handleMCPCommand("/mcp disable docs")
	if command == nil {
		t.Fatal("disable did not start")
	}
	ui.Update(command())
	if len(client.mcpActions) != 3 || client.mcpActions[2] != "disable:docs" {
		t.Fatalf("disable actions = %v", client.mcpActions)
	}
}

func TestTUIMCPCommandErrorsAreVisible(t *testing.T) {
	client := &fakeResponder{mcpActionErr: errors.New("connection refused")}
	ui := newTUIModel(client, false)
	_, command := ui.handleMCPCommand("/mcp enable missing")
	if command == nil {
		t.Fatal("enable did not start")
	}
	ui.Update(command())
	if !strings.Contains(ui.status, "failed") || !strings.Contains(strings.Join(entryTexts(ui.entries), "\n"), "connection refused") {
		t.Fatalf("failure is not visible: status=%q entries=%v", ui.status, entryTexts(ui.entries))
	}

	ui.handleMCPCommand("/mcp nope")
	if !strings.Contains(strings.Join(entryTexts(ui.entries), "\n"), "Usage: /mcp") {
		t.Fatal("invalid command did not show usage")
	}

	ui.mcpCommandPending = true
	ui.input.SetValue("do not start during connect")
	_, command = ui.submit()
	if command != nil || len(client.inputs) != 0 || ui.input.Value() != "do not start during connect" {
		t.Fatal("task started or input was discarded while MCP state was changing")
	}
}

func TestTUIMCPAuthCanBeCancelled(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ctx, cancel := context.WithCancel(context.Background())
	ui.mcpAuthCancel = cancel
	ui.mcpCommandPending = true
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if !errors.Is(ctx.Err(), context.Canceled) || !strings.Contains(ui.status, "Cancelling") {
		t.Fatalf("Esc did not cancel MCP auth: err=%v status=%q", ctx.Err(), ui.status)
	}
	ui.finishMCPCommand(mcpCommandResultMsg{operation: "auth", name: "docs", err: ctx.Err()})
	if ui.mcpCommandPending || ui.mcpAuthCancel != nil || !strings.Contains(ui.status, "cancelled") {
		t.Fatalf("canceled OAuth left pending state: pending=%v cancel=%v status=%q", ui.mcpCommandPending, ui.mcpAuthCancel != nil, ui.status)
	}
}

func TestTUIMCPFormAcceptDeclineAndValidation(t *testing.T) {
	client := &fakeResponder{}
	ui := newMCPInteractionUI(client)
	request := core.UserInteractionRequest{ID: "interaction-1", ServerName: "docs", Request: json.RawMessage(`{
		"method":"elicitation/create","params":{"mode":"form","message":"Enter the account email.","requestedSchema":{"type":"object","properties":{"email":{"type":"string","format":"email","title":"Email"},"code":{"type":"string","title":"Code"}},"required":["email","code"]}}
	}`)}
	ui.beginMCPInteraction(request)
	if ui.pendingMCPInteraction == nil || len(ui.pendingMCPInteraction.fields) != 2 {
		t.Fatal("valid form did not open")
	}
	if view := ui.View(); view.Cursor == nil || view.Cursor.Y >= ui.height || lipgloss.Height(view.Content) > ui.height {
		t.Fatalf("form cursor or panel is outside the visible terminal: cursor=%#v height=%d view=%d", view.Cursor, ui.height, lipgloss.Height(view.Content))
	}
	ui.pendingMCPInteraction.input.SetValue("123456")
	ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	ui.pendingMCPInteraction.input.SetValue("invalid")
	ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.pendingMCPInteraction == nil || !strings.Contains(ui.pendingMCPInteraction.err, "valid email") {
		t.Fatalf("invalid form was not kept open with an error: %#v", ui.pendingMCPInteraction)
	}
	ui.pendingMCPInteraction.input.SetValue("person@example.test")
	ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.pendingMCPInteraction != nil || len(client.interactionJSON) != 1 {
		t.Fatalf("valid form did not resolve: pending=%v response=%v", ui.pendingMCPInteraction != nil, client.interactionJSON)
	}
	var response struct {
		Action  string            `json:"action"`
		Content map[string]string `json:"content"`
	}
	if err := json.Unmarshal(client.interactionJSON[0], &response); err != nil {
		t.Fatal(err)
	}
	if response.Action != "accept" || response.Content["email"] != "person@example.test" || response.Content["code"] != "123456" {
		t.Fatalf("form response = %#v", response)
	}

	request.ID = "interaction-2"
	ui.beginMCPInteraction(request)
	ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	if ui.pendingMCPInteraction != nil || len(client.interactionJSON) != 2 || string(client.interactionJSON[1]) != `{"action":"decline"}` {
		t.Fatalf("decline response = %v", client.interactionJSON)
	}
}

func TestTUIMCPUnsupportedInteractionIsDeclined(t *testing.T) {
	client := &fakeResponder{}
	ui := newMCPInteractionUI(client)
	ui.beginMCPInteraction(core.UserInteractionRequest{
		ID: "unsupported", ServerName: "docs", Request: json.RawMessage(`{"method":"sampling/createMessage","params":{}}`),
	})
	if len(client.interactionJSON) != 1 || string(client.interactionJSON[0]) != `{"action":"decline"}` {
		t.Fatalf("unsupported request was not explicitly declined: %v", client.interactionJSON)
	}
}

func TestTUIMCPConcurrentInteractionDoesNotReplaceActiveForm(t *testing.T) {
	client := &fakeResponder{}
	ui := newMCPInteractionUI(client)
	ui.beginMCPInteraction(core.UserInteractionRequest{
		ID: "active", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","url":"https://accounts.example.test/consent"}}`),
	})
	ui.beginMCPInteraction(core.UserInteractionRequest{
		ID: "second", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","url":"https://accounts.example.test/other"}}`),
	})
	if ui.pendingMCPInteraction == nil || ui.pendingMCPInteraction.request.ID != "active" {
		t.Fatalf("second request replaced the active interaction: %#v", ui.pendingMCPInteraction)
	}
	if len(client.interactionJSON) != 1 || client.interactionIDs[0] != "second" || string(client.interactionJSON[0]) != `{"action":"decline"}` {
		t.Fatalf("concurrent request was not declined: ids=%v responses=%v", client.interactionIDs, client.interactionJSON)
	}
}

func TestTUIMCPURLRequiresConfirmationAndHidesQuery(t *testing.T) {
	client := &fakeResponder{}
	ui := newMCPInteractionUI(client)
	ui.beginMCPInteraction(core.UserInteractionRequest{
		ID: "url-1", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","message":"Grant access to documents.","url":"https://accounts.example.test/consent?state=secret#private"}}`),
	})
	if ui.pendingMCPInteraction == nil || len(client.openMCPURLs) != 0 || len(client.interactionJSON) != 0 {
		t.Fatal("URL elicitation should wait for the user")
	}
	view := ui.View().Content
	if !strings.Contains(view, "https://accounts.example.test/consent") || strings.Contains(view, "state=secret") || strings.Contains(view, "private") {
		t.Fatalf("URL display did not redact query and fragment: %q", view)
	}
	_, command := ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	if command == nil || len(client.openMCPURLs) != 0 {
		t.Fatal("page opened before the asynchronous confirmation action ran")
	}
	ui.Update(command())
	if len(client.openMCPURLs) != 1 || client.openMCPURLs[0] != "docs https://accounts.example.test/consent?state=secret#private" {
		t.Fatalf("open URL calls = %v", client.openMCPURLs)
	}
	if ui.pendingMCPInteraction != nil || len(client.interactionJSON) != 1 || string(client.interactionJSON[0]) != `{"action":"accept"}` {
		t.Fatalf("successful URL confirmation response = %v", client.interactionJSON)
	}
}

func TestTUIMCPURLOpenFailureKeepsConfirmationOpen(t *testing.T) {
	client := &fakeResponder{mcpActionErr: errors.New("browser unavailable")}
	ui := newMCPInteractionUI(client)
	ui.beginMCPInteraction(core.UserInteractionRequest{
		ID: "url-2", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","url":"https://accounts.example.test/consent"}}`),
	})
	_, command := ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	ui.Update(command())
	if ui.pendingMCPInteraction == nil || !strings.Contains(ui.pendingMCPInteraction.err, "browser unavailable") || len(client.interactionJSON) != 0 {
		t.Fatalf("open failure did not preserve the pending request: state=%#v responses=%v", ui.pendingMCPInteraction, client.interactionJSON)
	}
	if !strings.Contains(ui.View().Content, "browser unavailable") {
		t.Fatal("browser failure is not visible in the URL confirmation panel")
	}
}

func TestParseMCPFormDefaultsAndRejectsInvalidSchemas(t *testing.T) {
	fields, err := parseMCPForm(json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","default":4},"enabled":{"type":"boolean","default":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[0].name != "count" || fields[0].value != "4" || fields[1].value != "true" {
		t.Fatalf("parsed fields = %#v", fields)
	}
	for _, raw := range []string{
		`{"type":"object","properties":{}}`,
		`{"type":"object","properties":{"value":{"type":"array"}}}`,
		`{"type":"object","properties":{"value":{"type":"integer","minimum":5,"maximum":2}}}`,
		`{"type":"object","properties":{"value":{"type":"string","enum":["one"],"enumNames":["one","two"]}}}`,
		`{"type":"object","properties":{},"required":["missing"]}`,
	} {
		if _, err := parseMCPForm(json.RawMessage(raw)); err == nil {
			t.Errorf("parseMCPForm(%s) succeeded, want invalid schema error", raw)
		}
	}
	field := mcpInteractionField{typeName: "integer", value: "123456789012345678901234567890"}
	value, include, err := field.responseValue()
	if err != nil || !include || value.(json.Number).String() != field.value {
		t.Fatalf("large integer response = %#v, %v, %v", value, include, err)
	}
	uriField := mcpInteractionField{typeName: "string", format: "uri", value: "urn:isbn:9780131103627"}
	if _, include, err := uriField.responseValue(); err != nil || !include {
		t.Fatalf("absolute non-hierarchical URI was rejected: include=%v err=%v", include, err)
	}
}

func TestTUIMCPScreenshotSVGsMatchFixtures(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		theme := theme
		for _, scenario := range []string{"list", "enable", "auth", "form", "url", "result"} {
			scenario := scenario
			t.Run(fmt.Sprintf("%s-%s", theme, scenario), func(t *testing.T) {
				ui := newMCPScreenshotFixture(theme, scenario)
				name := fmt.Sprintf("%s-mcp-%s.svg", theme, scenario)
				view := ui.View()
				if lipgloss.Height(view.Content) > ui.height {
					t.Fatalf("MCP state exceeds terminal height: %d > %d", lipgloss.Height(view.Content), ui.height)
				}
				assertTUIScreenshotSVG(t, name, renderTUIScreenshotSVG(view.Content, ui.width, ui.height, theme))
			})
		}
		for _, scenario := range []string{"form", "url"} {
			scenario := scenario
			t.Run(fmt.Sprintf("%s-narrow-%s", theme, scenario), func(t *testing.T) {
				ui := newMCPScreenshotFixture(theme, scenario)
				ui.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
				name := fmt.Sprintf("%s-mcp-narrow-%s.svg", theme, scenario)
				view := ui.View()
				if lipgloss.Height(view.Content) > ui.height {
					t.Fatalf("narrow MCP state exceeds terminal height: %d > %d", lipgloss.Height(view.Content), ui.height)
				}
				assertTUIScreenshotSVG(t, name, renderTUIScreenshotSVG(view.Content, ui.width, ui.height, theme))
			})
		}
		for _, scenario := range []string{"form-error", "operation-error", "url-error"} {
			scenario := scenario
			t.Run(fmt.Sprintf("%s-%s", theme, scenario), func(t *testing.T) {
				ui := newMCPScreenshotFixture(theme, scenario)
				name := fmt.Sprintf("%s-mcp-%s.svg", theme, scenario)
				view := ui.View()
				if lipgloss.Height(view.Content) > ui.height {
					t.Fatalf("MCP error state exceeds terminal height: %d > %d", lipgloss.Height(view.Content), ui.height)
				}
				assertTUIScreenshotSVG(t, name, renderTUIScreenshotSVG(view.Content, ui.width, ui.height, theme))
			})
		}
	}
}

func newMCPScreenshotFixture(theme themeName, scenario string) *tuiModel {
	client := &fakeResponder{mcpServers: []core.MCPServerStatus{
		{Name: "docs", Transport: "streamable-http", Endpoint: "https://mcp.example.test/v1/mcp", Enabled: true, Connected: true, ToolCount: 3},
		{Name: "local-search", Transport: "stdio", Command: "mcp-local-search --root ./docs"},
	}}
	ui := newConversationFixture(theme, "short")
	ui.client = client
	ui.sessionID = "mcp-screenshot"
	runtime, _ := client.OpenSessionRuntime(context.Background(), ui.sessionID)
	ui.runtime = runtime
	ui.runtimeSessionID = ui.sessionID
	ui.colorProfile = colorprofile.TrueColor
	switch scenario {
	case "list":
		ui.showMCPServers(client.mcpServers)
	case "enable":
		ui.status = "MCP server enabled: local-search."
		ui.addEntry("notice", "MCP server enabled: local-search. 3 tools discovered and added to the mcp category.", false)
	case "auth":
		ui.status = "Waiting for MCP authorization for docs…"
		ui.mcpCommandPending = true
		ui.mcpAuthCancel = func() {}
		ui.addEntry("notice", "Authorization opened in your browser. Finish sign-in, then return to Lune.", false)
	case "form", "form-error":
		request := core.UserInteractionRequest{ID: "snapshot-form", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"form","message":"Enter a one-time code to continue.","requestedSchema":{"type":"object","properties":{"code":{"type":"string","title":"Code","description":"Use the code from your authenticator."}},"required":["code"]}}}`)}
		ui.beginMCPInteraction(request)
		if scenario == "form-error" {
			ui.pendingMCPInteraction.input.SetValue("")
			ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		}
	case "url":
		ui.beginMCPInteraction(core.UserInteractionRequest{ID: "snapshot-url", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","message":"Grant access to your document account.","url":"https://accounts.example.test/consent?state=redacted#private"}}`)})
	case "result":
		ui.entries = []tuiEntry{
			{role: "you", text: "Summarize the latest documentation screenshot."},
			{role: "assistant", text: "Here is the page summary.\nThe linked resource was read from the same server.\n\n[Image · image/png · 3.4 KiB · MCP attachment]"},
			{role: "notice", text: "User-only server notes are shown here and stay out of model context."},
		}
		ui.refreshPanels()
	case "operation-error":
		ui.status = "MCP enable failed."
		ui.addEntry("error", "MCP enable failed. connection refused", true)
	case "url-error":
		client.mcpActionErr = errors.New("browser unavailable")
		ui.beginMCPInteraction(core.UserInteractionRequest{ID: "snapshot-url-error", ServerName: "docs", Request: json.RawMessage(`{"method":"elicitation/create","params":{"mode":"url","message":"Grant access to your document account.","url":"https://accounts.example.test/consent"}}`)})
		_, command := ui.handleMCPInteractionKey(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		if command != nil {
			ui.Update(command())
		}
	}
	ui.updateLayout()
	ui.refreshPanels()
	return ui
}

func entryTexts(entries []tuiEntry) []string {
	texts := make([]string, len(entries))
	for index, entry := range entries {
		texts[index] = entry.text
	}
	return texts
}
