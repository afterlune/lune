package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func TestTUIQuestionAndExecutionSVGs(t *testing.T) {
	for _, theme := range []themeName{themeLight, themeDark} {
		for _, scenario := range []string{"question", "question-notes", "question-error", "question-narrow", "execution", "table"} {
			t.Run(fmt.Sprintf("%s-%s", theme, scenario), func(t *testing.T) {
				ui, _ := questionFixture()
				ui.preferences.Theme = theme
				ui.applyResolvedTheme()
				ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
				ui.entries = []tuiEntry{{role: "you", text: "Inspect this project and explain the findings."}}
				switch scenario {
				case "question-notes":
					ui.question.notes = true
					ui.question.input.SetValue("Use the sample configuration, without real credentials.")
					ui.question.input.Focus()
				case "question-error":
					ui.question.err = "Could not submit answer. Your draft is preserved."
				case "question-narrow":
					ui.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
					ui.question.request.Questions[0].Question = strings.Repeat("Which environment should we inspect. ", 4) + "Which environment should we inspect?"
					ui.openQuestion(ui.question.request)
				case "execution":
					ui.questionOpen = false
					ui.question = nil
					ui.pending = false
					code := 1
					executionID := "execution-1"
					output := publictool.CommandOutput{Command: publictool.CommandInfo{ExecutionID: executionID, InvocationID: "command-1", Command: "go test ./internal/tui", Status: publictool.CommandFailed, ExitCode: &code}, Output: "--- FAIL: TestLayout\n    layout_test.go:42: column width mismatch\nFAIL\nadditional diagnostics\nlast diagnostic"}
					ui.updateToolActivity(core.ToolCall{ExecutionID: executionID, ID: "edit-1", InvocationID: "edit-1", Name: "edit_file", Arguments: json.RawMessage(`{"path":"internal/tui/layout.go"}`), Details: &publictool.ExecutionDetails{Changes: []publictool.FileChange{{Path: "internal/tui/layout.go", Added: 1, Removed: 1, Lines: []publictool.DiffLine{{Kind: "context", OldLine: 40, NewLine: 40, Text: "func contentWidth(width int) int {"}, {Kind: "removed", OldLine: 41, Text: "    return width"}, {Kind: "added", NewLine: 41, Text: "    return max(1, width - 2)"}, {Kind: "context", OldLine: 42, NewLine: 42, Text: "}"}}}}}})
					ui.updateToolActivity(core.ToolCall{ExecutionID: executionID, ID: "command-1", InvocationID: "command-1", Name: "command", Details: &publictool.ExecutionDetails{Command: &output}})
					ui.status = "Completed."
				case "table":
					ui.questionOpen = false
					ui.question = nil
					ui.pending = false
					ui.entries = append(ui.entries, tuiEntry{role: "assistant", text: "| 检查项 | 结果 | 文件 |\n| --- | --- | --- |\n| 问答交互 | 通过 | internal/tui/question.go |\n| 中文与 emoji 👩‍💻 | 已修复 | internal/tui/table.go |"})
					ui.status = "Completed."
				}
				ui.updateLayout()
				ui.refreshPanels()
				view := ui.View()
				if lipgloss.Width(view.Content) > ui.width || lipgloss.Height(view.Content) > ui.height {
					t.Fatalf("layout overflow: %dx%d in %dx%d", lipgloss.Width(view.Content), lipgloss.Height(view.Content), ui.width, ui.height)
				}
				assertTUIScreenshotSVG(t, fmt.Sprintf("%s-%s.svg", theme, scenario), renderTUIScreenshotSVG(view.Content, ui.width, ui.height, theme))
			})
		}
	}
}
