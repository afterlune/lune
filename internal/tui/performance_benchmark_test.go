package tui

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkRefreshViewportContent_Scales(b *testing.B) {
	for _, entryCount := range []int{50, 500, 2000} {
		for _, variant := range []struct {
			name       string
			invalidate bool
		}{
			{name: "cached"},
			{name: "uncached", invalidate: true},
		} {
			b.Run(fmt.Sprintf("entries=%d/%s", entryCount, variant.name), func(b *testing.B) {
				m := benchmarkTranscriptModel(entryCount)
				m.refreshViewportContent()
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					if variant.invalidate {
						m.view.invalidateBlocks()
					}
					m.currentStreamText = fmt.Sprintf("Streaming token delta %d", i)
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
				m.currentStreamText = fmt.Sprintf("Streaming token delta %d", i)
				m.refreshViewportContent()
				_ = m.View()
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
