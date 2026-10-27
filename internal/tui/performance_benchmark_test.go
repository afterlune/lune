package tui

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkRefreshViewportContent_Scales(b *testing.B) {
	for _, entryCount := range []int{50, 500, 2000} {
		for _, variant := range []struct {
			name          string
			invalidate    bool
			invalidateOne bool
		}{
			{name: "cached"},
			{name: "single-entry", invalidateOne: true},
			{name: "uncached", invalidate: true},
		} {
			b.Run(fmt.Sprintf("entries=%d/%s", entryCount, variant.name), func(b *testing.B) {
				m := benchmarkTranscriptModel(entryCount)
				m.refreshViewportContent()
				entryIndex := len(m.entries) - 1
				originalText := m.entries[entryIndex].Text
				updatedText := originalText + "\nUpdated benchmark entry."
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					if variant.invalidate {
						m.view.invalidateBlocks()
					}
					if variant.invalidateOne {
						if i%2 == 0 {
							m.entries[entryIndex].Text = updatedText
						} else {
							m.entries[entryIndex].Text = originalText
						}
						m.view.invalidateEntry(entryIndex)
					}
					m.run.currentStreamText = fmt.Sprintf("Streaming token delta %d", i)
					m.view.invalidateLive()
					m.refreshViewportContent()
				}
			})
		}
	}
}

func BenchmarkTranscriptFrame_Scales(b *testing.B) {
	for _, entryCount := range []int{50, 500, 2000} {
		b.Run(fmt.Sprintf("entries=%d", entryCount), func(b *testing.B) {
			m := benchmarkTranscriptModel(entryCount)
			m.refreshViewportContent()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				m.run.currentStreamText = fmt.Sprintf("Streaming token delta %d", i)
				m.view.invalidateLive()
				m.refreshViewportContent()
				_ = m.View()
			}
		})
	}
}

func BenchmarkRefreshExpandedToolRounds_LongHistory(b *testing.B) {
	for _, roundCount := range []int{50, 250, 500} {
		b.Run(fmt.Sprintf("rounds=%d", roundCount), func(b *testing.B) {
			m := newTUIModel(nil, nil, "expanded-round-bench")
			m.width, m.height = 120, 40
			for i := range roundCount {
				m.entries = append(m.entries,
					TranscriptEntry{
						Kind: KindTool, ToolName: "read_file", ToolCommand: fmt.Sprintf("Read file-%d.go", i),
						ToolOutput: "first line\nsecond line", RoundExpanded: true,
					},
					TranscriptEntry{Kind: KindAssistant, Text: fmt.Sprintf("Result %d", i)},
				)
			}
			m.updateLayout()
			entryIndex := len(m.entries) - 1
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				m.entries[entryIndex].Text = fmt.Sprintf("Updated result %d", i%2)
				m.view.invalidateEntry(entryIndex)
				m.refreshViewportContent()
			}
		})
	}
}

func benchmarkTranscriptModel(entryCount int) *tuiModel {
	m := newTUIModel(nil, nil, "bench-session")
	m.width = 120
	m.height = 40
	for i := range entryCount {
		if i%2 == 0 {
			m.entries = append(m.entries, TranscriptEntry{
				Kind: KindUser,
				Text: fmt.Sprintf("Turn %d: Analyze the performance of our database query layer", i/2),
			})
			continue
		}
		m.entries = append(m.entries, TranscriptEntry{
			Kind: KindAssistant,
			Text: fmt.Sprintf("Turn %d result:\n- Check **indexes** on columns\n- Use `EXPLAIN ANALYZE`\n- Review connection pool settings\n\n```sql\nSELECT * FROM users WHERE active = true;\n```", i/2),
		})
	}
	m.updateLayout()
	return m
}

func TestRefreshViewportContentUsesSoftWrapOnlyWhenNeeded(t *testing.T) {
	m := newTUIModel(nil, nil, "wrap-test")
	m.width = 80
	m.height = 24
	m.entries = []TranscriptEntry{{Kind: KindUser, Text: "short prompt"}}
	m.updateLayout()
	if m.viewport.SoftWrap {
		t.Fatal("viewport should skip soft-wrap scans when all transcript lines fit")
	}
	wrapped := m.viewport
	wrapped.SoftWrap = true
	if got, want := m.viewport.View(), wrapped.View(); got != want {
		t.Fatalf("disabling redundant soft wrap changed visible content:\n got: %q\nwant: %q", got, want)
	}

	m.entries = append(m.entries, TranscriptEntry{Kind: KindUser, Text: strings.Repeat("界", 100)})
	m.view.invalidateBlocks()
	m.refreshViewportContent()
	if m.viewport.SoftWrap {
		t.Fatal("user cards wrap their content to the viewport width before rendering")
	}
}
