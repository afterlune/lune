package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestPlanCardHasRectangularSurface(t *testing.T) {
	previous := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previous) })
	const text = "## 目标\n\n短行。\n\n- 检查中文 mixed internal/tui/model_view.go 的边界；" +
		"重复说明以触发折行，并且确保背景右边缘没有缺口。\n  - 嵌套列表\n\n```go\n\tprintln(\"hello\") // 注释\n```\n\n" +
		"**粗体与 `inline code`** 最后一段。"
	for _, mode := range []ThemeMode{ThemeModeLight, ThemeModeDark} {
		SetThemeMode(mode)
		for _, width := range []int{24, 48, 100} {
			rendered := renderEntry(TranscriptEntry{Kind: KindPlan, Text: text}, width)
			vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(strings.Count(rendered, "\n")+1))
			vp.SetContent(rendered)
			rendered = vp.View()
			lines := strings.Split(rendered, "\n")
			if strings.TrimSpace(ansi.Strip(lines[1])) != "" {
				t.Fatalf("expected a blank line between plan title and card: %q", lines[1])
			}
			cells := uv.NewStyledString(rendered).Lines(ansi.GraphemeWidth)
			for _, cell := range cells[1] {
				if cell.Style.Bg != nil {
					t.Fatal("the gap below Proposed Plan must have no card background")
				}
			}
			r, g, b, _ := parseHexRGB(activeTheme().CardBg)
			codeR, codeG, codeB, _ := parseHexRGB(activeTheme().CodeBlockBg)
			for y := 2; y < len(lines); y++ {
				if got := ansi.StringWidth(lines[y]); got != width {
					t.Fatalf("%s card row %d width = %d, want %d: %q", mode, y, got, width, lines[y])
				}
				x := 0
				for _, cell := range cells[y] {
					if cell.Width == 0 {
						continue
					}
					if cell.Style.Bg == nil {
						t.Fatalf("%s card has no background at (%d,%d): %q", mode, x, y, lines[y])
					}
					cr, cg, cb, _ := cell.Style.Bg.RGBA()
					isCard := int(cr>>8) == r && int(cg>>8) == g && int(cb>>8) == b
					isCode := int(cr>>8) == codeR && int(cg>>8) == codeG && int(cb>>8) == codeB
					if !isCard && !isCode {
						t.Fatalf("%s card background differs at (%d,%d): %v", mode, x, y, cell.Style.Bg)
					}
					if (x < 2 || x >= width-2) && !isCard {
						t.Fatalf("%s card edge should keep its surface color at (%d,%d)", mode, x, y)
					}
					x += cell.Width
				}
				if x != width {
					t.Fatalf("%s card row %d covers %d cells, want %d", mode, y, x, width)
				}
			}
		}
	}
}

func TestPlanReviewPositionStaysFixed(t *testing.T) {
	m := newTUIModel(nil, nil, "plan-layout")
	m.width, m.height = 120, 40
	m.entries = []TranscriptEntry{{Kind: KindPlan, Text: strings.Repeat("- plan body\n", 80)}}
	m.activePlanReview = &ActivePlanReview{Text: "plan", Selected: 0}
	m.composer.SetValue("composer-marker")
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
		m.copyFeedback, m.copyFeedbackError = state.feedback, state.failed
		m.copyFeedbackExpiresAt = time.Now().Add(time.Minute)
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
