package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Plans remain readable in the response flow in both themes and narrow terminals.
func TestInlinePlanMarkdownFitsTerminal(t *testing.T) {
	previous := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previous) })
	const text = "## 目标\n\n短行。\n\n- 检查中文 mixed internal/tui/model_view.go 的边界；" +
		"重复说明以触发折行，并且确保完整正文仍然可读。\n  - 嵌套列表\n\n```go\n\tprintln(\"hello\") // 注释\n```\n\n" +
		"**粗体与 `inline code`** 最后一段。"
	for _, mode := range []ThemeMode{ThemeModeLight, ThemeModeDark} {
		SetThemeMode(mode)
		cardR, cardG, cardB, ok := parseHexRGB(activeTheme().CardBg)
		if !ok {
			t.Fatalf("%s theme has no card background", mode)
		}
		cardSGR := fmt.Sprintf("48;2;%d;%d;%d", cardR, cardG, cardB)
		for _, width := range []int{24, 48, 100} {
			rendered := renderEntry(TranscriptEntry{Kind: KindPlan, Text: text}, width)
			if strings.Contains(rendered, cardSGR) {
				t.Fatalf("%s plan body still paints the card surface: %q", mode, rendered)
			}
			lines := strings.Split(rendered, "\n")
			if !strings.Contains(ansi.Strip(lines[0]), "Proposed Plan") {
				t.Fatalf("%s plan title missing: %q", mode, ansi.Strip(lines[0]))
			}
			if strings.Contains(ansi.Strip(rendered), "│") {
				t.Fatalf("%s plan body still has a separate rail: %s", mode, ansi.Strip(rendered))
			}
			for _, marker := range []string{"目标", "短行", "嵌套列表", "println", "最后一段"} {
				if !strings.Contains(ansi.Strip(rendered), marker) {
					t.Fatalf("%s plan lost %q: %s", mode, marker, ansi.Strip(rendered))
				}
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("%s plan row %d width = %d, want <= %d: %q", mode, i, got, width, line)
				}
			}
		}
	}
}

// TestTaskRuleSeparatesTurns verifies every user turn after the first is
// introduced by a full-width solid rule.
func TestTaskRuleSeparatesTurns(t *testing.T) {
	previous := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previous) })
	SetThemeMode(ThemeModeDark)

	m := newTUIModel(nil, nil, "task-rules")
	m.width, m.height = 100, 40
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "first task"},
		{Kind: KindAssistant, Text: "first answer"},
		{Kind: KindUser, Text: "second task"},
		{Kind: KindAssistant, Text: "second answer"},
	}
	m.updateLayout()

	lines := strings.Split(ansi.Strip(m.viewport.View()), "\n")
	rule := strings.Repeat("─", max(20, m.width-4))
	var rules []int
	firstUser, secondUser := -1, -1
	for i, line := range lines {
		if strings.HasPrefix(line, rule) {
			rules = append(rules, i)
		}
		if firstUser < 0 && strings.Contains(line, "first task") {
			firstUser = i
		}
		if strings.Contains(line, "second task") {
			secondUser = i
		}
	}
	if firstUser < 0 || secondUser < 0 {
		t.Fatalf("expected both user turns in the transcript, got:\n%s", strings.Join(lines, "\n"))
	}
	if len(rules) != 1 {
		t.Fatalf("expected exactly one task rule, got rows %v", rules)
	}
	if rules[0] < firstUser || rules[0] > secondUser {
		t.Fatalf("task rule row %d must sit between the two user turns (%d, %d)", rules[0], firstUser, secondUser)
	}
}

// Plan sections must not introduce a new task boundary inside a response.
func TestPlanRemainsBetweenSurroundingText(t *testing.T) {
	m := newTUIModel(nil, nil, "inline-plan")
	m.width, m.height = 100, 60
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "plan this"},
		{Kind: KindAssistant, Text: "before-marker"},
		{Kind: KindPlan, Text: "## Steps\n1. Do it"},
		{Kind: KindAssistant, Text: "after-marker"},
		{Kind: KindUser, Text: "next-task-marker"},
	}
	m.updateLayout()
	view := ansi.Strip(m.viewport.View())
	before := strings.Index(view, "before-marker")
	plan := strings.Index(view, "Proposed Plan")
	after := strings.Index(view, "after-marker")
	rule := strings.Index(view, strings.Repeat("─", m.width-4))
	next := strings.Index(view, "next-task-marker")
	if before < 0 || !(before < plan && plan < after && after < rule && rule < next) {
		t.Fatalf("plan must stay in the response before the next task boundary:\n%s", view)
	}
	if strings.Count(view, strings.Repeat("─", m.width-4)) != 1 {
		t.Fatalf("plan introduced extra task separators:\n%s", view)
	}
}

func TestPlanReviewPositionStaysFixed(t *testing.T) {
	m := newTUIModel(nil, nil, "plan-layout")
	m.width, m.height = 120, 40
	m.entries = []TranscriptEntry{{Kind: KindPlan, Text: strings.Repeat("- plan body\n", 80)}}
	m.activePlanReview = &ActivePlanReview{Text: "plan", Selected: 0}
	m.input.composer.SetValue("composer-marker")
	m.updateLayout()
	var reviewY, composerY = -1, -1
	for _, state := range []struct {
		scrolled bool
		feedback string
		failed   bool
	}{
		{}, {scrolled: true}, {scrolled: true, feedback: "Copied"},
		{scrolled: true, feedback: "Copy failed", failed: true},
		{feedback: "Copied"}, {feedback: "Copy failed", failed: true}, {},
	} {
		m.scrolledUp = state.scrolled
		m.input.copyFeedback, m.input.copyFeedbackError = state.feedback, state.failed
		m.input.copyFeedbackExpiresAt = time.Now().Add(time.Minute)
		if state.scrolled {
			m.viewport.SetYOffset(10)
		} else {
			m.viewport.GotoBottom()
		}
		m.updateLayout()
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		find := func(marker string) int {
			for y, line := range lines {
				if strings.Contains(line, marker) {
					return y
				}
			}
			return -1
		}
		ry, cy := find("Implement this plan?"), find("composer-marker")
		if reviewY < 0 {
			reviewY, composerY = ry, cy
		}
		if ry != reviewY || cy != composerY || ry < 0 || cy < 0 {
			t.Fatalf("review/composer moved: got (%d,%d), want (%d,%d), state=%+v", ry, cy, reviewY, composerY, state)
		}
		_, mouseY := m.composerMouseOffset()
		if mouseY != cy {
			t.Fatalf("composer mouse row %d differs from rendered row %d", mouseY, cy)
		}
		if len(lines) != m.height {
			t.Fatalf("frame height = %d, want %d", len(lines), m.height)
		}
		if strings.TrimSpace(lines[cy-2]) != "" {
			t.Fatal("a blank gap must separate review choices from the composer band")
		}
		marker := state.feedback
		if marker == "" && state.scrolled {
			marker = "Back to bottom"
		}
		if marker != "" {
			if y := find(marker); y != 3+m.viewport.Height() || y >= ry {
				t.Fatalf("hint %q appeared on row %d instead of the fixed row above the review", marker, y)
			}
		}
	}
}
