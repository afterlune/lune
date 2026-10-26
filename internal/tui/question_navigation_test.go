package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func questionNavigationModel() *tuiModel {
	m := newTUIModel(nil, nil, "question-navigation")
	m.width, m.height = 120, 45
	request := questionFixture()
	request.Questions = []core.UserQuestionItem{
		{ID: "first", Question: "First?", Options: []core.UserQuestionOption{{Label: "A"}, {Label: "B"}}},
		{ID: "second", Question: "Second?", Options: []core.UserQuestionOption{{Label: "C"}, {Label: "D", Recommended: true}}},
		{ID: "third", Question: "Third?"},
	}
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	return m
}

func pressQuestionKey(m *tuiModel, key rune) tea.Cmd {
	_, cmd := m.handleKeyPress(tea.KeyPressMsg{Code: key})
	return cmd
}

func TestQuestionNavigationKeepsIndependentDrafts(t *testing.T) {
	m := questionNavigationModel()
	q := m.activeQuestion
	pressQuestionKey(m, tea.KeyLeft)
	if q.CurrentIndex != 0 {
		t.Fatal("left must stop at the first question")
	}
	pressQuestionKey(m, tea.KeyDown)
	pressQuestionKey(m, tea.KeyTab)
	m.Update(tea.PasteMsg{Content: "第一题备注"})
	pressQuestionKey(m, tea.KeyRight)
	if q.CurrentIndex != 1 || q.currentDraft().Selected != 1 || q.currentDraft().NotesOpen {
		t.Fatalf("new question must have its own recommended selection and input state: %#v", q)
	}
	pressQuestionKey(m, '3') // None of the above.
	m.Update(tea.PasteMsg{Content: "第二题自定义"})
	pressQuestionKey(m, tea.KeyRight)
	m.Update(tea.PasteMsg{Content: "第三题自定义"})
	pressQuestionKey(m, tea.KeyRight)
	if q.CurrentIndex != 2 {
		t.Fatal("right must stop at the last question")
	}
	pressQuestionKey(m, tea.KeyLeft)
	if q.currentDraft().Selected != 2 || q.currentDraft().Custom != "第二题自定义" {
		t.Fatalf("custom answer was lost while browsing: %#v", q.currentDraft())
	}
	pressQuestionKey(m, tea.KeyLeft)
	if q.currentDraft().Selected != 1 || q.currentDraft().NoteText != "第一题备注" || !q.currentDraft().NotesOpen {
		t.Fatalf("selection, notes or input focus were lost: %#v", q.currentDraft())
	}
	if len(q.confirmedAnswers()) != 0 || len(m.entries) != 0 {
		t.Fatal("navigation must not confirm answers or add a transcript summary")
	}
	request := q.Request
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	if m.activeQuestion != q || q.currentDraft().NoteText != "第一题备注" {
		t.Fatal("pending-request refresh discarded the current drafts")
	}
	view := ansi.Strip(renderQuestionPanel(q, 120))
	if !strings.Contains(view, "←/→ switch question") || !strings.Contains(view, "Question 1/3 (3 unanswered)") {
		t.Fatalf("navigation and confirmation status are missing:\n%s", view)
	}
}

func TestQuestionNavigationRevisesAndSubmitsInQuestionOrder(t *testing.T) {
	m := questionNavigationModel()
	q := m.activeQuestion
	pressQuestionKey(m, tea.KeyRight)
	pressQuestionKey(m, tea.KeyRight)
	m.Update(tea.PasteMsg{Content: "Third answer"})
	if cmd := pressQuestionKey(m, tea.KeyEnter); cmd != nil || q.Submitting || q.CurrentIndex != 0 {
		t.Fatal("confirming the last card must return to skipped questions")
	}
	if len(m.entries) != 1 || m.entries[0].AnsweredQuestions[0].Question != "Third?" {
		t.Fatalf("out-of-order confirmation recorded the wrong question: %#v", m.entries)
	}
	pressQuestionKey(m, tea.KeyEnter) // Confirm first; second remains unanswered.
	pressQuestionKey(m, tea.KeyLeft)
	if !q.currentDraft().Confirmed {
		t.Fatal("browsing a confirmed question must preserve confirmation")
	}
	pressQuestionKey(m, tea.KeyDown) // Revise A to B.
	if q.currentDraft().Confirmed || len(q.confirmedAnswers()) != 1 || len(m.entries[0].AnsweredQuestions) != 1 {
		t.Fatal("editing a confirmed answer must remove its old confirmation and summary")
	}
	pressQuestionKey(m, tea.KeyRight)
	if cmd := pressQuestionKey(m, tea.KeyEnter); cmd != nil || q.CurrentIndex != 0 {
		t.Fatal("the revised first answer must be reconfirmed before submitting")
	}
	cmd := pressQuestionKey(m, tea.KeyEnter)
	want := []core.UserQuestionResponse{
		{QuestionID: "first", Answer: "B"},
		{QuestionID: "second", Answer: "D"},
		{QuestionID: "third", Answer: "Third answer"},
	}
	if cmd == nil || !q.Submitting || !reflect.DeepEqual(q.confirmedAnswers(), want) {
		t.Fatalf("final answers were not ordered or did not contain the revision: %#v", q.confirmedAnswers())
	}
	if len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 3 || m.entries[0].AnsweredQuestions[0].Answer != "B" || m.entries[0].AnsweredQuestions[2].Answer != "Third answer" {
		t.Fatalf("summary duplicated or misplaced answers: %#v", m.entries)
	}
	drafts := append([]questionDraft(nil), q.Drafts...)
	pressQuestionKey(m, tea.KeyRight)
	pressQuestionKey(m, tea.KeyDown)
	m.Update(tea.PasteMsg{Content: "ignored during submission"})
	if q.CurrentIndex != 0 || !reflect.DeepEqual(drafts, q.Drafts) {
		t.Fatal("submission must lock navigation and answer editing")
	}
	m.Update(cmd()) // Missing runtime reports a submission failure.
	if q.Submitting || q.Error == "" {
		t.Fatal("submission failure must unlock the saved batch for retry")
	}
	if retry := pressQuestionKey(m, tea.KeyEnter); retry == nil || len(m.entries) != 1 || !reflect.DeepEqual(q.confirmedAnswers(), want) {
		t.Fatal("retry lost answers or duplicated their transcript summary")
	}
}

func TestQuestionNavigationLeavesComposerInControl(t *testing.T) {
	m := questionNavigationModel()
	q := m.activeQuestion
	pressQuestionKey(m, tea.KeyTab) // Leave question notes open.
	m.input.composer.SetValue("please continue")
	pressQuestionKey(m, tea.KeyLeft)
	m.Update(tea.PasteMsg{Content: "x"})
	if q.CurrentIndex != 0 || q.currentDraft().NoteText != "" || m.input.composer.Value() != "please continuxe" {
		t.Fatalf("card captured composer movement or paste: composer=%q draft=%#v", m.input.composer.Value(), q.currentDraft())
	}
	pressQuestionKey(m, tea.KeyRight)
	if q.CurrentIndex != 0 {
		t.Fatal("right arrow must stay in the nonempty composer")
	}
}

func TestQuestionTextRevisionRequiresConfirmation(t *testing.T) {
	for _, notes := range []bool{false, true} {
		name := "custom answer"
		if notes {
			name = "notes"
		}
		t.Run(name, func(t *testing.T) {
			m := questionNavigationModel()
			q := m.activeQuestion
			draft := q.currentDraft()
			if notes {
				draft.NotesOpen = true
				draft.NoteText = "original"
			} else {
				draft.Selected = len(q.Request.Questions[0].Options)
				draft.Custom = "original"
			}
			pressQuestionKey(m, tea.KeyEnter)
			pressQuestionKey(m, tea.KeyLeft)
			m.Update(tea.PasteMsg{Content: "改"})
			if draft.Confirmed || len(m.entries) != 0 {
				t.Fatal("editing the only confirmed answer must remove the old summary")
			}
			pressQuestionKey(m, tea.KeyBackspace)
			if notes && draft.NoteText != "original" || !notes && draft.Custom != "original" {
				t.Fatalf("backspace must remove the entire pasted Unicode character: %#v", draft)
			}
			pressQuestionKey(m, tea.KeyEnter)
			pressQuestionKey(m, tea.KeyLeft)
			if len(m.entries) != 1 || !draft.Confirmed {
				t.Fatal("reconfirming the edited answer must recreate one summary")
			}
			pressQuestionKey(m, tea.KeyEsc)
			if notes {
				if draft.NotesOpen || !draft.Confirmed || draft.NoteText != "original" {
					t.Fatal("closing notes must retain the answer and its confirmation")
				}
			} else if draft.Custom != "" || draft.Confirmed || len(m.entries) != 0 {
				t.Fatal("clearing a custom answer must invalidate its old confirmation")
			}
		})
	}
}

func TestSingleQuestionNavigationIsNoOp(t *testing.T) {
	m := newTUIModel(nil, nil, "single-question")
	m.activeQuestion = newActiveQuestion(questionFixture())
	pressQuestionKey(m, tea.KeyLeft)
	pressQuestionKey(m, tea.KeyRight)
	if m.activeQuestion.CurrentIndex != 0 || len(m.activeQuestion.confirmedAnswers()) != 0 {
		t.Fatal("single-card navigation must not confirm or change the question")
	}
	if strings.Contains(ansi.Strip(renderQuestionPanel(m.activeQuestion, 120)), "switch question") {
		t.Fatal("single-question card must not advertise navigation")
	}
}
