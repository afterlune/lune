package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

func TestLipglossTableWrapping(t *testing.T) {
	headers := []string{"阶段", "模块", "关键动作", "交付物"}
	rows := [][]string{
		{"阶段 1：重构 Core Engine", "internal/agentgraph", "引入并实现 RunOne(ctx, turn, opts)；复用 existing compiler 与 checkpointing。", "internal/agentgraph/engine.go"},
	}

	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		Headers(headers...).
		Rows(rows...).
		Wrap(true).
		Width(70)

	out := tbl.Render()
	t.Logf("Rendered table (width 70):\n%s\n", out)
	for _, line := range strings.Split(out, "\n") {
		w := lipgloss.Width(line)
		if w > 72 {
			t.Errorf("Line width %d exceeds target width 70: %s", w, line)
		}
	}
}

func TestAnsiWrapMixed(t *testing.T) {
	text := "这是一个用于测试中英文混排自动折行的长句子 This is a long sentence for testing word wrap in terminals 带有代码 `internal/agentgraph/engine.go` 和强调 **阶段 1** 以及标点符号。"
	wrapped := ansi.Wrap(text, 40, " ,.:;!?、。，；！？")
	t.Logf("Wrapped (width 40):\n%s\n", wrapped)
	for _, line := range strings.Split(wrapped, "\n") {
		w := lipgloss.Width(line)
		if w > 40 {
			t.Errorf("Line width %d exceeds 40: %s", w, line)
		}
	}
}

func TestRenderMarkdownComplete(t *testing.T) {
	md := `# 标题一
这是一段很长的介绍段落，用于测试正文是否能够正常按指定的终端宽度进行折行，而不是直接超长输出导致终端光标混乱。

## 实施步骤
| 阶段 | 模块 | 关键动作 | 交付物 |
| :--- | :--- | :--- | :--- |
| **阶段 1：重构 Core Engine** | ` + "`internal/agentgraph`" + ` | 引入并实现 ` + "`RunOne(ctx, turn, opts)`" + `；复用 existing compiler 与 checkpointing。 | ` + "`internal/agentgraph/engine.go`" + ` |
| **阶段 2：重写 SessionRuntime** | ` + "`session_runtime.go`" + ` | SubmitTurn 内部替换为基于 RunOne 的按轮单次循环；清理冗余事件通道。 | 运行测试无回归 |

### 详细要点
- **第一项**：这是一个非常长的列表项，包含了许多重要的说明文字，用来验证列表项折行时第二行是否带有悬挂缩进。
- **第二项**：包含代码引用 ` + "`internal/tui/view.go`" + ` 的简短说明。

> 这是一个引用块，用于提示重要信息，注意观察引用线的对齐。

---
` + "```go\nfunc main() {\n\tprintln(\"hello world\")\n}\n```"

	rendered := RenderMarkdown(md, 70)
	t.Logf("Rendered Markdown (width 70):\n%s\n", rendered)

	lines := strings.Split(rendered, "\n")
	for idx, line := range lines {
		w := lipgloss.Width(line)
		if w > 74 {
			t.Errorf("Line %d width %d exceeds target 70: %s", idx, w, line)
		}
	}
}

func TestStripProposedPlan(t *testing.T) {
	// Preamble + plan + suffix
	t1 := "Thinking notes.\n<proposed_plan>## Step 1\nRun tests</proposed_plan>\nFollow-up question."
	if got := stripProposedPlan(t1); got != "Thinking notes.\n\nFollow-up question." && got != "Thinking notes.\nFollow-up question." {
		t.Errorf("Unexpected strip result: %q", got)
	}

	// Only plan
	t2 := "<proposed_plan>## Step 1\nRun tests</proposed_plan>"
	if got := stripProposedPlan(t2); got != "" {
		t.Errorf("Expected empty string for plan-only text, got: %q", got)
	}

	// Unclosed tag: the dangling tag is removed, but following prose is kept.
	t3 := "Preamble.\n<proposed_plan>## Incomplete plan"
	if got := stripProposedPlan(t3); got != "Preamble.\n## Incomplete plan" {
		t.Errorf("Expected preamble and following prose, got: %q", got)
	}

	// A literal tag mention in ordinary prose must not swallow the sentence.
	t5 := "我会先阅读相关代码，再用 <proposed_plan> 给你完整方案。"
	if got := stripProposedPlan(t5); got != "我会先阅读相关代码，再用  给你完整方案。" {
		t.Errorf("Expected prose preserved, got: %q", got)
	}

	// extractProposedPlan must ignore dangling tags.
	if plan, _ := extractProposedPlan(t3); plan != "" {
		t.Errorf("Expected no plan from unclosed tag, got: %q", plan)
	}

	// No tag
	t4 := "Just plain message."
	if got := stripProposedPlan(t4); got != t4 {
		t.Errorf("Expected unchanged message, got: %q", got)
	}
}

func TestExtractProposedPlanRequiresOneCompleteBlock(t *testing.T) {
	plan, remaining := extractProposedPlan("Intro.\n<proposed_plan>## Steps\n1. Do it</proposed_plan>\nOutro.")
	if plan != "## Steps\n1. Do it" {
		t.Fatalf("unexpected plan: %q", plan)
	}
	if remaining != "Intro.\n\nOutro." && remaining != "Intro.\nOutro." {
		t.Fatalf("unexpected remaining text: %q", remaining)
	}

	for _, text := range []string{
		"<proposed_plan>unfinished",
		"<proposed_plan></proposed_plan>",
		"<proposed_plan>one</proposed_plan><proposed_plan>two</proposed_plan>",
		"</proposed_plan>orphaned",
	} {
		if plan, _ := extractProposedPlan(text); plan != "" {
			t.Errorf("extractProposedPlan(%q) = %q, want no plan", text, plan)
		}
	}
}

func TestAutocompleteScrolling(t *testing.T) {
	items := make([]AutocompleteItem, 25)
	for i := 0; i < 25; i++ {
		items[i] = AutocompleteItem{
			Command:     strings.Repeat("/cmd", 1) + string(rune('a'+i)),
			Description: "Description for item",
		}
	}

	// When selected is 0: shows top 7 items
	rendered0 := renderAutocompletePopup(items, 0, 80)
	lines0 := strings.Split(rendered0, "\n")
	if len(lines0) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines0))
	}
	if !strings.Contains(lines0[0], "› /cmda") {
		t.Errorf("Expected first line to be selected /cmda, got: %s", lines0[0])
	}

	// When selected is 12 (middle): shows 7 items centered around 12
	rendered12 := renderAutocompletePopup(items, 12, 80)
	lines12 := strings.Split(rendered12, "\n")
	if len(lines12) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines12))
	}
	hasSelected12 := false
	for _, l := range lines12 {
		if strings.Contains(l, "› ") {
			hasSelected12 = true
		}
	}
	if !hasSelected12 {
		t.Errorf("Expected selected marker in lines12")
	}

	// When selected is 24 (last item): shows bottom 7 items
	rendered24 := renderAutocompletePopup(items, 24, 80)
	lines24 := strings.Split(rendered24, "\n")
	if len(lines24) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines24))
	}
	lastLine := lines24[len(lines24)-1]
	if !strings.Contains(lastLine, "› ") {
		t.Errorf("Expected last line to be selected, got: %s", lastLine)
	}
}
