package markdown

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

func testPalette() Palette {
	return Palette{
		TextPrimary: "#E6EDF3", TextMuted: "#8B949E", AccentBlue: "#58A6FF",
		InlineCodeFg: "#79C0FF", CodeBlockBg: "#161B22", Border: "#30363D",
	}
}

func TestSplitCodeComment(t *testing.T) {
	code, comment := splitCodeComment("gofmt -l internal/desktop # 期望无输出")
	if code != "gofmt -l internal/desktop " || comment != "# 期望无输出" {
		t.Fatalf("unexpected splitCodeComment result: code=%q, comment=%q", code, comment)
	}
	quotedCode, quotedComment := splitCodeComment(`git commit -m "fix #123 issue" # trailing comment`)
	if quotedCode != `git commit -m "fix #123 issue" ` || quotedComment != "# trailing comment" {
		t.Fatalf("expected # inside quotes to be preserved, got code=%q, comment=%q", quotedCode, quotedComment)
	}
}

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

	rendered := Render(md, 70, testPalette())
	t.Logf("Rendered Markdown (width 70):\n%s\n", rendered)

	lines := strings.Split(rendered, "\n")
	for idx, line := range lines {
		w := lipgloss.Width(line)
		if w > 74 {
			t.Errorf("Line %d width %d exceeds target 70: %s", idx, w, line)
		}
	}
}

func TestRenderPlanPreservesStructureAndSpacing(t *testing.T) {
	text := "# Title\n1. First item with enough text to wrap across the card width.\n2. Second item\n\n## Details\n- nested detail"
	rendered := ansi.Strip(RenderPlan(text, 48, testPalette()))
	if !strings.Contains(rendered, "# Title") || !strings.Contains(rendered, "## Details") {
		t.Fatalf("plan headings lost their Markdown markers: %q", rendered)
	}
	if !strings.Contains(rendered, "# Title\n\n1. First") {
		t.Fatalf("expected breathing room after a plan heading: %q", rendered)
	}
	if !strings.Contains(rendered, "1. First") || !strings.Contains(rendered, "2. Second") || !strings.Contains(rendered, "• nested detail") {
		t.Fatalf("plan list structure was not preserved: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("plan line exceeds render width 48 (%d): %q", width, line)
		}
	}
}

func TestRenderMarkdownSeparatesHeadingsFromBody(t *testing.T) {
	rendered := ansi.Strip(Render("# Overview\nBody text", 48, testPalette()))
	if !strings.Contains(rendered, "Overview\n\nBody text") {
		t.Fatalf("expected a blank line after the heading, got %q", rendered)
	}
}

func TestRenderMarkdownCodeBlockUsesRectangularSurface(t *testing.T) {
	rendered := ansi.Strip(Render("```go\nx\nlonger line\n```", 32, testPalette()))
	for i, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width != 32 {
			t.Fatalf("code block row %d width = %d, want 32: %q", i, width, line)
		}
	}
}

func TestFormatInlineRestoresNestedTokens(t *testing.T) {
	input := "**Bold `inline code`** then `plain code` and internal/tui/view.go"
	rendered := formatInline(input, testPalette())
	if strings.Contains(rendered, "\x00") || strings.Contains(rendered, "TOKEN_") {
		t.Fatalf("inline formatter leaked an internal token: %q", rendered)
	}
	if got, want := ansi.Strip(rendered), "Bold inline code then plain code and internal/tui/view.go"; got != want {
		t.Fatalf("unexpected rendered inline text: got %q, want %q", got, want)
	}
}

func TestStripProposedPlan(t *testing.T) {
	// Preamble + plan + suffix
	t1 := "Thinking notes.\n<proposed_plan>## Step 1\nRun tests</proposed_plan>\nFollow-up question."
	if got := StripProposedPlan(t1); got != "Thinking notes.\n\nFollow-up question." && got != "Thinking notes.\nFollow-up question." {
		t.Errorf("Unexpected strip result: %q", got)
	}

	// Only plan
	t2 := "<proposed_plan>## Step 1\nRun tests</proposed_plan>"
	if got := StripProposedPlan(t2); got != "" {
		t.Errorf("Expected empty string for plan-only text, got: %q", got)
	}

	// Unclosed tag: the dangling tag is removed, but following prose is kept.
	t3 := "Preamble.\n<proposed_plan>## Incomplete plan"
	if got := StripProposedPlan(t3); got != "Preamble.\n## Incomplete plan" {
		t.Errorf("Expected preamble and following prose, got: %q", got)
	}

	// A literal tag mention in ordinary prose must not swallow the sentence.
	t5 := "我会先阅读相关代码，再用 <proposed_plan> 给你完整方案。"
	if got := StripProposedPlan(t5); got != "我会先阅读相关代码，再用  给你完整方案。" {
		t.Errorf("Expected prose preserved, got: %q", got)
	}

	// extractProposedPlan must ignore dangling tags.
	if plan, _ := ExtractProposedPlan(t3); plan != "" {
		t.Errorf("Expected no plan from unclosed tag, got: %q", plan)
	}

	// No tag
	t4 := "Just plain message."
	if got := StripProposedPlan(t4); got != t4 {
		t.Errorf("Expected unchanged message, got: %q", got)
	}
}

func TestExtractProposedPlanRequiresOneCompleteBlock(t *testing.T) {
	plan, remaining := ExtractProposedPlan("Intro.\n<proposed_plan>## Steps\n1. Do it</proposed_plan>\nOutro.")
	if plan != "## Steps\n1. Do it" {
		t.Fatalf("unexpected plan: %q", plan)
	}
	if remaining != "Intro.\n\nOutro." && remaining != "Intro.\nOutro." {
		t.Fatalf("unexpected remaining text: %q", remaining)
	}
	compatPlan, compatRemaining := ExtractProposedPlan("Intro.\n<proposed_plan>## Steps\n1. Do it<proposed_plan_end/>\nOutro.")
	if compatPlan != "## Steps\n1. Do it" {
		t.Fatalf("unexpected compatibility plan: %q", compatPlan)
	}
	if compatRemaining != "Intro.\n\nOutro." && compatRemaining != "Intro.\nOutro." {
		t.Fatalf("unexpected compatibility remaining text: %q", compatRemaining)
	}

	for _, text := range []string{
		"<proposed_plan>unfinished",
		"<proposed_plan></proposed_plan>",
		"<proposed_plan>one</proposed_plan><proposed_plan>two</proposed_plan>",
		"<proposed_plan>one</proposed_plan><proposed_plan_end/>",
		"</proposed_plan>orphaned",
		"<proposed_plan_end/>orphaned",
	} {
		if plan, _ := ExtractProposedPlan(text); plan != "" {
			t.Errorf("extractProposedPlan(%q) = %q, want no plan", text, plan)
		}
	}
}

func TestSplitProposedPlanStreamStylesPartialAndCompatCompleteBlocks(t *testing.T) {
	opening := SplitProposedPlanStream("Intro. <proposed_plan>")
	if !opening.Found || opening.Complete || opening.Before != "Intro." || opening.Plan != "" {
		t.Fatalf("opening marker did not immediately enter plan mode: %#v", opening)
	}

	partial := SplitProposedPlanStream("Intro.\n<proposed_plan>\n## Steps\n1. Fin")
	if !partial.Found || partial.Complete || partial.Before != "Intro." || !strings.Contains(partial.Plan, "1. Fin") || partial.After != "" {
		t.Fatalf("unexpected partial stream split: %#v", partial)
	}

	complete := SplitProposedPlanStream("Intro.\n<proposed_plan>\n## Steps\n1. Finish\n<proposed_plan_end/>\nOutro.")
	if !complete.Found || !complete.Complete || complete.Before != "Intro." || !strings.Contains(complete.Plan, "1. Finish") || complete.After != "Outro." {
		t.Fatalf("unexpected complete compatibility stream split: %#v", complete)
	}
	if strings.Contains(complete.Before+complete.Plan+complete.After, "proposed_plan_end") {
		t.Fatalf("compatibility marker leaked from stream split: %#v", complete)
	}

	prose := SplitProposedPlanStream("Use `<proposed_plan>` in the documentation.")
	if prose.Found || prose.Complete || !strings.Contains(prose.Before, "`<proposed_plan>`") {
		t.Fatalf("inline tag mention was treated as a plan: %#v", prose)
	}
}

func TestProposedPlanParsingProtectsCodeSpans(t *testing.T) {
	text := "Use ``<proposed_plan>`` and a fence:\n```go\n// <proposed_plan>not a plan</proposed_plan>\n```\n" +
		"<proposed_plan>## Real plan</proposed_plan>"
	plan, remaining := ExtractProposedPlan(text)
	if plan != "## Real plan" {
		t.Fatalf("extracted plan = %q, want real block only", plan)
	}
	if !strings.Contains(remaining, "``<proposed_plan>``") || !strings.Contains(remaining, "// <proposed_plan>not a plan</proposed_plan>") {
		t.Fatalf("code examples were changed while extracting a plan: %q", remaining)
	}
	if got := StripProposedPlan(text); strings.Contains(got, "## Real plan") || !strings.Contains(got, "<proposed_plan>not a plan") {
		t.Fatalf("strip removed code examples or retained the actual plan: %q", got)
	}
	partial := "Example: `unfinished <proposed_plan>literal"
	stream := SplitProposedPlanStream(partial)
	if stream.Found || stream.Before != partial {
		t.Fatalf("an incomplete inline-code span was treated as a plan: %#v", stream)
	}

	marker := "\x00LUNE_CODE_0\x00"
	withMarker := marker + " `code <proposed_plan>` <proposed_plan>## Kept</proposed_plan>"
	plan, remaining = ExtractProposedPlan(withMarker)
	if plan != "## Kept" || !strings.HasPrefix(remaining, marker) || !strings.Contains(remaining, "`code <proposed_plan>`") {
		t.Fatalf("a literal placeholder collided with protected code: plan=%q remaining=%q", plan, remaining)
	}
}

func TestInlineMarkdownTokenizationSafety(t *testing.T) {
	input := "请检查 **TUI.md** 以及 `go test -v ./...` 的输出。"
	formatted := formatInline(input, testPalette())
	stripped := ansi.Strip(formatted)
	if strings.Contains(stripped, "218m") {
		t.Fatalf("corrupted ANSI sequence leaked into text: %q", stripped)
	}
	if !strings.Contains(stripped, "TUI.md") {
		t.Fatalf("expected stripped output to contain TUI.md, got %q", stripped)
	}
}
