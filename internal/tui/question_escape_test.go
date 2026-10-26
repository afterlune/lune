package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

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
