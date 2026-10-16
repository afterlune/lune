package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func TestLightReferenceScreenshots(t *testing.T) {
	for _, size := range []struct{ width, height int }{{160, 50}, {100, 30}, {48, 18}} {
		for _, scenario := range []string{"startup", "commands", "question", "batch-partial", "answered", "plan-review", "working"} {
			t.Run(fmt.Sprintf("%s-%d", scenario, size.width), func(t *testing.T) {
				ui, _ := questionFixture()
				ui.preferences.Theme = themeLight
				ui.workspace = `D:\Git\luneAgent`
				ui.sessionSettings.ModelProfile = "lead"
				ui.applyResolvedTheme()
				wait := ui.question.request
				wait.Questions = []core.UserQuestionItem{
					{ID: "scope", Question: "这次分析希望回答什么问题？", Options: []core.UserQuestionOption{
						{Label: "整体代码审查（推荐）", Description: "梳理架构并找高优先级的可靠性、数据准确性和安全问题。"},
						{Label: "定位运行故障", Description: "围绕面板异常或日志报错追查具体原因。"},
						{Label: "架构与数据流", Description: "解释模块职责、数据来源以及端到端运行流程。"},
						{Label: "安全与性能", Description: "聚焦本地服务暴露面、数据处理和资源开销。"},
					}},
					{ID: "target", Question: "无法确认归属的会话，应该如何处理？", Options: []core.UserQuestionOption{{Label: "隐藏未确认会话（推荐）", Description: "只显示归属已经确认的记录。"}, {Label: "保留并标记", Description: "保留记录并明确标记未确认。"}}},
				}
				ui.entries = []tuiEntry{{role: "you", text: "分析项目并规划下一步"}, {role: "assistant", text: "初步确认这是一个本机运行的监控面板：Python 服务扫描会话日志，SQLite 保存运行记录和采样，浏览器通过 SSE 展示状态。\n\n请确认分析范围，我会据此继续检查。"}}
				ui.question = nil
				ui.openQuestion(wait)
				if scenario == "question" {
					ui.question.request.Questions = wait.Questions[:1]
					ui.question.responses = ui.question.responses[:1]
					ui.question.answers = ui.question.answers[:1]
				}
				if scenario == "batch-partial" {
					ui.question.responses[0].Confirmed = true
					ui.question.current = 1
					ui.question.loadCurrent()
				}
				if scenario != "question" && scenario != "batch-partial" {
					ui.question = nil
					ui.questionOpen = false
					ui.pendingRequest = nil
					ui.pendingRequests = nil
					ui.pending = false
				}
				switch scenario {
				case "startup":
					ui.entries = nil
				case "commands":
					ui.entries = nil
					ui.input.SetValue("/")
					ui.updateCommandSuggestions()
				case "answered":
					ui.addQuestionSummary(wait, []core.UserQuestionResponse{{QuestionID: "scope", Answer: "整体代码审查（推荐）"}, {QuestionID: "target", Answer: "隐藏未确认会话（推荐）"}})
				case "plan-review":
					ui.entries = append(ui.entries, tuiEntry{role: "plan", text: "# luneAgent 风险修复计划\n\n## 摘要\n\n修复动态内容渲染、会话归属与日志读取，保留现有 HTTP 接口和 SQLite 表结构。\n\n## 实施变更\n\n- **安全渲染**：使用 DOM 节点与 `textContent` 展示动态内容。\n- **严格归属**：仅展示已经确认归属的会话。\n- **有界读取**：首次只回填文件末尾 256 KiB，并从完整行边界开始。\n\n## 验收场景\n\n1. 页面只显示文本，不执行注入内容。\n2. 同时更新与读取会话状态时，快照保持一致。"}, tuiEntry{role: "turn_footer", text: "Worked for 1m 50s • 13:27"})
					ui.planReview = true
					ui.planMode = true
				case "working":
					ui.running = true
					ui.executionStarted = true
				}
				ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				ui.refreshPanels()
				view := ui.View()
				if lipgloss.Width(view.Content) > size.width || lipgloss.Height(view.Content) > size.height {
					t.Fatal("reference layout overflow")
				}
				if scenario == "plan-review" && strings.Contains(view.WindowTitle, "Action Required") {
					t.Fatal("plan review changed title to action required")
				}
				assertTUIScreenshotSVG(t, fmt.Sprintf("light-reference-%s-%d.svg", scenario, size.width), renderTUIScreenshotSVG(view.Content, ui.width, ui.height, themeLight))
			})
		}
	}
}
