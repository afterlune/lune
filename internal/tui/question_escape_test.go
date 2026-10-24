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
