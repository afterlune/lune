package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func cursorTestModel() *tuiModel {
	m := newTUIModel(nil, nil, "cursor-test")
	m.width, m.height = 80, 32
	m.input.composer.SetValue("你好ab")
	m.input.composer.CursorEnd()
	m.updateLayout()
	return m
}

// Read the rendered frame, rather than the layout helper, to find the input.
func assertComposerCursor(t *testing.T, m *tuiModel) *tea.Cursor {
	t.Helper()
	view := m.View()
	if view.Cursor == nil {
		t.Fatal("missing real terminal cursor")
	}
	inputLines := strings.Split(ansi.Strip(m.input.composer.View()), "\n")
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	start := -1
	for i := range lines {
		if i+len(inputLines) > len(lines) {
			break
		}
		matches := true
		for j, line := range inputLines {
			if strings.TrimRight(lines[i+j], " ") != strings.TrimRight(" "+line, " ") {
				matches = false
				break
			}
		}
		if matches {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("input not found in frame:\n%s", ansi.Strip(view.Content))
	}
	relative := m.input.composer.Cursor()
	if relative == nil {
		t.Fatal("textarea did not expose its cursor")
	}
	if view.Cursor.X != 1+relative.X || view.Cursor.Y != start+relative.Y {
		t.Fatalf("cursor=(%d,%d), want=(%d,%d)", view.Cursor.X, view.Cursor.Y, 1+relative.X, start+relative.Y)
	}
	x, y := m.composerMouseOffset()
	if x != 1 || y != start {
		t.Fatalf("mouse origin=(%d,%d), rendered=(1,%d)", x, y, start)
	}
	if view.Cursor.Y < start || view.Cursor.Y >= start+len(inputLines) {
		t.Fatal("cursor outside composer")
	}
	if view.Cursor.Shape != tea.CursorBlock || view.Cursor.Blink {
		t.Fatal("composer cursor style changed")
	}
	if m.input.composer.VirtualCursor() {
		t.Fatal("virtual cursor would duplicate terminal cursor")
	}
	return view.Cursor
}

func TestStreamingOutputKeepsIMEAnchorAtComposer(t *testing.T) {
	m := cursorTestModel()
	m.run.running = true
	m.updateLayout()
	baseline := assertComposerCursor(t, m)
	if baseline.X != 9 {
		t.Fatalf("Chinese display width: got x=%d, want 9", baseline.X)
	}
	for _, text := range []string{"short", strings.Repeat("long output ", 120), strings.Repeat("streamed line\n", 2000), "```go\nfunc example() {}\n```"} {
		m.run.resetStream()
		m.run.appendStreamDelta(text)
		m.view.invalidateLive()
		m.updateLayout()
		cursor := assertComposerCursor(t, m)
		if cursor.Position != baseline.Position {
			t.Fatalf("output moved IME anchor from %v to %v", baseline.Position, cursor.Position)
		}
	}
}

func TestComposerCursorMatchesDynamicLayout(t *testing.T) {
	for _, scene := range []struct {
		name  string
		setup func(*tuiModel)
	}{
		{"plain", func(*tuiModel) {}},
		{"queued", func(m *tuiModel) {
			m.entries = append(m.entries, TranscriptEntry{Kind: KindQueued, Text: "queued follow-up"})
		}},
		{"autocomplete", func(m *tuiModel) {
			m.input.autocompleteOpen = true
			m.input.autocompleteItems = []AutocompleteItem{{Command: "/help", Description: "Help"}, {Command: "/plan", Description: "Plan"}}
		}},
		{"plan review", func(m *tuiModel) { m.activePlanReview = &ActivePlanReview{Text: "plan"} }},
		{"queue and plan", func(m *tuiModel) {
			m.entries = append(m.entries, TranscriptEntry{Kind: KindQueued, Text: "queued follow-up"})
			m.activePlanReview = &ActivePlanReview{Text: "plan"}
		}},
		{"scroll pill", func(m *tuiModel) { m.scrolledUp = true }},
		{"copy feedback", func(m *tuiModel) {
			m.input.copyFeedback = "Copied"
			m.input.copyFeedbackExpiresAt = time.Now().Add(time.Hour)
		}},
	} {
		t.Run(scene.name, func(t *testing.T) {
			m := cursorTestModel()
			scene.setup(m)
			for _, size := range []struct{ w, h int }{{80, 32}, {48, 24}, {120, 40}} {
				m.width, m.height = size.w, size.h
				m.updateLayout()
				assertComposerCursor(t, m)
			}
		})
	}
}

func TestComposerCursorTracksWrappingAndInternalScroll(t *testing.T) {
	for _, value := range []string{"", "中文 abc 中文", strings.Repeat("中a", 70), "one\ntwo\n三\nfour\nfive\nsix\nseven\neight"} {
		m := cursorTestModel()
		m.width = 40
		m.input.composer.SetValue(value)
		// SetValue leaves the insertion point at the end, including after newlines.
		m.updateLayout()
		assertComposerCursor(t, m)
		for i := 0; i < 10; i++ {
			m.input.composer.CursorUp()
			m.updateLayout()
			assertComposerCursor(t, m)
		}
	}
}

func TestComposerCursorVisibilityFollowsInputOwnership(t *testing.T) {
	m := cursorTestModel()
	assertComposerCursor(t, m)
	m.modals.shortcuts = true
	m.updateLayout()
	if m.View().Cursor != nil {
		t.Fatal("modal must hide composer cursor")
	}
	m.modals.close()
	m.updateLayout()
	assertComposerCursor(t, m)
	m.input.composer.Blur()
	if m.View().Cursor != nil {
		t.Fatal("blurred composer cursor visible")
	}
	m.input.composer.Focus()
	assertComposerCursor(t, m)
	m.width, m.height = 20, 8
	if m.View().Cursor != nil {
		t.Fatal("small-terminal message must hide cursor")
	}
}

func TestQuestionCursorOnlyAppearsWhenComposerAcceptsText(t *testing.T) {
	m := cursorTestModel()
	m.input.composer.SetValue("")
	m.activeQuestion = &ActiveQuestion{Request: questionFixture()}
	m.updateLayout()
	assertComposerCursor(t, m)
	m.activeQuestion.currentDraft().NotesOpen = true
	m.updateLayout()
	if m.View().Cursor != nil {
		t.Fatal("notes card owns text input")
	}
	m.activeQuestion.currentDraft().NotesOpen = false
	m.activeQuestion.currentDraft().Selected = len(m.activeQuestion.Request.Questions[0].Options)
	m.updateLayout()
	if m.View().Cursor != nil {
		t.Fatal("custom-answer card owns text input")
	}
	m.input.composer.SetValue("/status")
	m.updateLayout()
	assertComposerCursor(t, m)
	m.activeQuestion.Submitting = true
	if m.View().Cursor != nil {
		t.Fatal("submitting question disables composer")
	}
}
