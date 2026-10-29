package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func TestOverlayClippingKeepsEverySelectedRow(t *testing.T) {
	for selected := 0; selected < 30; selected++ {
		lines := make([]string, 30)
		for i := range lines {
			lines[i] = fmt.Sprintf("row %d", i)
		}
		lines[selected] = fmt.Sprintf("› selected %d", selected)
		for rows := 1; rows < len(lines); rows++ {
			got := fitOverlayHeight(strings.Join(lines, "\n"), rows)
			if strings.Count(got, "\n")+1 > rows {
				t.Fatalf("height exceeds %d", rows)
			}
			if !strings.Contains(got, lines[selected]) {
				t.Fatalf("selected=%d height=%d lost selection:\n%s", selected, rows, got)
			}
		}
	}
}

func TestPasteRespectsKeyboardOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*tuiModel)
		filter string
	}{
		{name: "session search", setup: func(m *tuiModel) { m.modals.sessions.open = true }, filter: "你好"},
		{name: "delete confirmation", setup: func(m *tuiModel) { m.modals.sessions.open = true; m.modals.sessions.confirmDelete = true }},
		{name: "help", setup: func(m *tuiModel) { m.modals.shortcuts = true }},
		{name: "effort picker", setup: func(m *tuiModel) { m.effortPicker = &ActiveEffortPicker{} }},
		{name: "discard in flight", setup: func(m *tuiModel) { m.run.discardInFlight = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := cursorTestModel()
			before := m.input.composer.Value()
			tc.setup(m)
			m.Update(tea.PasteMsg{Content: "你好"})
			if got := m.input.composer.Value(); got != before {
				t.Fatalf("paste changed background draft: %q", got)
			}
			if got := m.modals.sessions.filter; got != tc.filter {
				t.Fatalf("search=%q want %q", got, tc.filter)
			}
		})
	}
}

func TestSessionSearchPasteFiltersAndLeavesConfirmationUntouched(t *testing.T) {
	m := cursorTestModel()
	m.modals.sessions.open = true
	m.modals.sessions.items = []core.Session{{ID: "one", Title: "目标会话"}, {ID: "two", Title: "Other"}}
	m.Update(tea.PasteMsg{Content: "目标\r\n会话"})
	if m.modals.sessions.filter != "目标 会话" {
		t.Fatalf("multiline paste was not flattened: %q", m.modals.sessions.filter)
	}
	m.modals.sessions.filter = ""
	m.Update(tea.PasteMsg{Content: "目标"})
	if got := m.modals.sessions.filtered; len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("pasted search did not filter sessions: %+v", got)
	}
	m.modals.sessions.confirmDelete = true
	m.Update(tea.PasteMsg{Content: "y"})
	if !m.modals.sessions.confirmDelete || m.modals.sessions.filter != "目标" {
		t.Fatal("paste changed the delete confirmation or its search")
	}
}

func TestModalPasteDoesNotChangeBackgroundQuestion(t *testing.T) {
	m := questionNavigationModel()
	m.modals.shortcuts = true
	draft := m.activeQuestion.currentDraft()
	draft.NotesOpen = true
	m.Update(tea.PasteMsg{Content: "hidden text"})
	if draft.NoteText != "" || m.input.composer.Value() != "" {
		t.Fatal("modal paste reached a background text input")
	}
}
