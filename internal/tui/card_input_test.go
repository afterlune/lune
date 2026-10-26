package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func TestInteractionCardsLetComposerCommandsThrough(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*tuiModel)
	}{
		{"recovery", func(m *tuiModel) { m.activeRecovery = &ActiveRecovery{} }},
		{"tool recovery", func(m *tuiModel) { m.activeToolRecovery = &ActiveToolRecovery{} }},
		{"approval", func(m *tuiModel) {
			m.activeApproval = &ActiveApproval{Request: core.ApprovalRequest{ID: "approval", Tool: "command"}}
		}},
		{"plan review", func(m *tuiModel) { m.activePlanReview = &ActivePlanReview{Text: "Plan"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTUIModel(nil, nil, "card-input")
			test.setup(m)
			kind := m.activeCardKind()
			for _, ch := range "/status" {
				m.handleKeyPress(tea.KeyPressMsg{Code: ch, Text: string(ch)})
			}
			if got := m.input.composer.Value(); got != "/status" {
				t.Fatalf("composer = %q, want /status", got)
			}
			before := len(m.entries)
			m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.input.composer.Value() != "" || len(m.entries) <= before {
				t.Fatal("Enter did not dispatch the composer command")
			}
			if m.activeCardKind() != kind || m.run.running {
				t.Fatal("Enter activated or resolved the interaction instead of /status")
			}
		})
	}
}

func TestBackgroundQuestionSubmissionDoesNotStealApprovalKeys(t *testing.T) {
	m := newTUIModel(nil, nil, "card-priority")
	m.activeApproval = &ActiveApproval{Request: core.ApprovalRequest{ID: "approval", Tool: "command"}}
	m.activeQuestion = &ActiveQuestion{Submitting: true}
	view, _ := m.overlaySection(100)
	if view == "" || m.activeCardKind() != cardApproval {
		t.Fatal("approval must be the visible interaction")
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.activeApproval.Selected != 1 {
		t.Fatal("a lower-priority submitting question blocked the visible approval")
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.input.composer.Value() != "/" || !m.activeQuestion.Submitting {
		t.Fatal("typing should reach the composer without changing the background question")
	}
	m.input.composer.Reset()
	m.Update(tea.PasteMsg{Content: "/status"})
	if m.input.composer.Value() != "/status" {
		t.Fatal("a lower-priority question captured or blocked pasted composer input")
	}
}

func TestEscapeClearsComposerBeforeResolvingApproval(t *testing.T) {
	m := newTUIModel(nil, nil, "card-input")
	m.activeApproval = &ActiveApproval{Request: core.ApprovalRequest{ID: "approval", Tool: "command"}}
	m.input.composer.SetValue("unfinished input")
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.composer.Value() != "" || m.activeApproval == nil {
		t.Fatal("Esc should clear the draft before resolving the approval")
	}
}

func TestEscapeEditsOnlyTheCurrentQuestionDraft(t *testing.T) {
	m := questionNavigationModel()
	q := m.activeQuestion
	q.Drafts[0].Custom = "keep first draft"
	q.CurrentIndex = 1
	draft := q.currentDraft()
	draft.Selected = len(q.Request.Questions[1].Options)
	draft.Custom = "clear second draft"
	revision := q.draftRevision
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if q.Drafts[0].Custom != "keep first draft" || draft.Custom != "" {
		t.Fatal("Esc modified the wrong question draft")
	}
	if q.draftRevision <= revision {
		t.Fatal("the draft change was not scheduled for persistence")
	}
}
