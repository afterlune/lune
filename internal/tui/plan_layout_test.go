package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestPlanRailReplacesCardSurface pins the plan body to a muted vertical rail
// that starts directly under the purple marker and runs to the last body line,
// instead of a filled card surface.
func TestPlanRailReplacesCardSurface(t *testing.T) {
	previous := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previous) })
	const text = "## 目标\n\n短行。\n\n- 检查中文 mixed internal/tui/model_view.go 的边界；" +
		"重复说明以触发折行，并且确保竖线右侧没有缺口。\n  - 嵌套列表\n\n```go\n\tprintln(\"hello\") // 注释\n```\n\n" +
		"**粗体与 `inline code`** 最后一段。"
	const railOnly = "  │"
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
			if got := strings.TrimRight(ansi.Strip(lines[1]), " "); got != railOnly {
				t.Fatalf("%s rail must start under the title, got %q", mode, got)
			}
			for i, line := range lines[1:] {
				stripped := strings.TrimRight(ansi.Strip(line), " ")
				if stripped != railOnly && !strings.HasPrefix(stripped, railOnly+" ") {
					t.Fatalf("%s plan row %d lost the rail: %q", mode, i+1, stripped)
				}
				if got := ansi.StringWidth(stripped); got > width {
					t.Fatalf("%s plan row %d width = %d, want <= %d: %q", mode, i+1, got, width, stripped)
				}
			}
			if last := strings.TrimRight(ansi.Strip(lines[len(lines)-1]), " "); !strings.HasPrefix(last, railOnly) {
				t.Fatalf("%s rail must reach the last body line, got %q", mode, last)
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

// TestPlanBodyIsDelimitedByRules verifies the plan body is closed by a rule on
// both sides without leaving a dangling rule at the end of the transcript.
func TestPlanBodyIsDelimitedByRules(t *testing.T) {
	previous := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previous) })
	SetThemeMode(ThemeModeDark)

	rule := strings.Repeat("─", max(20, 100-4))
	rowsFor := func(entries []TranscriptEntry) ([]string, []int, int) {
		m := newTUIModel(nil, nil, "plan-rules")
		m.width, m.height = 100, 40
		m.entries = entries
		m.updateLayout()
		lines := strings.Split(ansi.Strip(m.viewport.View()), "\n")
		var rules []int
		title := -1
		for i, line := range lines {
			if strings.HasPrefix(line, rule) {
				rules = append(rules, i)
			}
			if title < 0 && strings.Contains(line, "Proposed Plan") {
				title = i
			}
		}
		return lines, rules, title
	}

	lines, rules, title := rowsFor([]TranscriptEntry{
		{Kind: KindUser, Text: "plan this"},
		{Kind: KindAssistant, Text: "here it is"},
		{Kind: KindPlan, Text: "## Steps\n1. Do it"},
		{Kind: KindAssistant, Text: "after the plan"},
		{Kind: KindUser, Text: "next task"},
	})
	if title < 0 {
		t.Fatalf("expected the plan title, got:\n%s", strings.Join(lines, "\n"))
	}
	if len(rules) != 3 {
		t.Fatalf("expected rules above/below the plan and before the next task, got rows %v", rules)
	}
	if rules[0] > title || rules[1] < title || rules[2] < title {
		t.Fatalf("unexpected rule rows %v around the plan title %d", rules, title)
	}
	after := -1
	for i, line := range lines {
		if strings.Contains(line, "after the plan") {
			after = i
		}
	}
	if after < 0 || rules[1] > after {
		t.Fatalf("the plan must be closed by a rule before the following text, got rows %v, text %d", rules, after)
	}

	lines, rules, title = rowsFor([]TranscriptEntry{
		{Kind: KindAssistant, Text: "intro"},
		{Kind: KindPlan, Text: "## Steps\n1. Do it"},
	})
	if title < 0 || len(rules) != 1 || rules[0] > title {
		t.Fatalf("expected a single rule above the trailing plan, got rows %v, title %d", rules, title)
	}
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last >= 0 && strings.HasPrefix(lines[last], rule) {
		t.Fatalf("the transcript must not end with a dangling rule: %q", lines[last])
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
