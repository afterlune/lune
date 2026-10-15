package tui

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

func TestConversationTablesFitUnicodeWidths(t *testing.T) {
	input := "| 检查项 | 结果 | 文件 |\n| --- | --- | --- |\n| 配置与依赖 👩‍💻 | 通过 | D:/Git/luneAgent/internal/tui/table.go |\n| 中文换行 | 需要处理的一段比较长的文字 | /tmp/config.json |"
	for _, width := range []int{120, 80, 40, 20} {
		text := renderConversationText(input, width)
		for _, line := range strings.Split(text, "\n") {
			if uniseg.StringWidth(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
		if width == 20 && !strings.Contains(text, "检查项:") {
			t.Fatal("narrow table did not switch to records")
		}
	}
}

func TestConversationTablesPreserveCodeAndIncompleteStreams(t *testing.T) {
	code := "```text\n| A | B |\n| --- | --- |\n```"
	if got := renderConversationText(code, 60); !strings.Contains(got, "| --- | --- |") {
		t.Fatalf("code was laid out as a table: %q", got)
	}
	if got := renderConversationText("| A | B |\n| --", 60); !strings.Contains(got, "| --") {
		t.Fatal("incomplete source vanished")
	}
	cells := tableCells("| a\\|b | `x|y` |")
	if len(cells) != 2 || cells[0] != "a|b" || cells[1] != "`x|y`" {
		t.Fatalf("cells=%#v", cells)
	}
}
