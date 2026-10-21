package markdown

import "testing"

func BenchmarkRenderMarkdown(b *testing.B) {
	sample := `# System Architecture
This document outlines the **core design** of the system.
Here are the primary components:
- Storage engine with SQLite and WAL mode
- Transport layer with WebSocket and JSON-RPC
- Terminal user interface with Lipgloss styling

` + "```go\nfunc Process(ctx context.Context) error {\n\treturn nil\n}\n```\n" + `
Please refer to ` + "`internal/tui/model.go`" + ` for details.`

	palette := testPalette()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Render(sample, 80, palette)
	}
}
