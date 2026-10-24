package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// TestEscOnQuestionKeepsPausedTask covers C4: Esc and Ctrl+C on a question card
// must not throw the paused checkpoint away.
func TestEscOnQuestionKeepsPausedTask(t *testing.T) {
	for _, key := range []tea.Key{{Code: tea.KeyEsc}, {Code: 'c', Mod: tea.ModCtrl}} {
		m := newTUIModel(nil, nil, "session")
		request := questionFixture()
		m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
		m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: request.ExecutionID}

		mod, cmd := m.Update(tea.KeyPressMsg(key))
		m = mod.(*tuiModel)

		if cmd != nil {
			t.Fatalf("%q started a command; a pending question must stay non-destructive", key.String())
		}
		if m.activeQuestion == nil {
			t.Fatalf("%q closed the question card", key.String())
		}
		if m.pausedCheckpoint == nil {
			t.Fatalf("%q dropped the paused checkpoint", key.String())
		}
		if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "waiting for your answer") {
			t.Fatalf("%q did not explain how to leave the question: %+v", key.String(), m.entries)
		}
	}
}

func TestEscOnQuestionDeduplicatesNotice(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	request := questionFixture()
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: request.ExecutionID}

	for i := 0; i < 5; i++ {
		mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
		m = mod.(*tuiModel)
	}

	systemCount := 0
	for _, entry := range m.entries {
		if entry.Kind == KindSystem && strings.Contains(entry.Text, "waiting for your answer") {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Fatalf("expected exactly 1 system notice after multiple Esc presses, got %d", systemCount)
	}
}

func TestCtrlCTwiceExitsOnQuestion(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	request := questionFixture()
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: request.ExecutionID}

	// First Ctrl+C explains
	mod, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if cmd != nil {
		t.Fatalf("first Ctrl+C should not quit or run command")
	}

	// Second Ctrl+C quits
	_, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatalf("second Ctrl+C should return Quit command")
	}
}
