package tui

import (
	"fmt"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestRunStateStreamsIntoBoundedTailAndKeepsCanonicalText(t *testing.T) {
	run := newRunState()
	for line := 0; line < 1000; line++ {
		run.appendStreamDelta("line\n")
	}
	if strings.Count(run.currentStreamText, "line\n") != 1000 {
		t.Fatalf("canonical stream lost text: %d lines", strings.Count(run.currentStreamText, "line\n"))
	}
	if got := strings.Count(run.assistantTail.RenderInput(), "\n"); got > liveMarkdownTailLines {
		t.Fatalf("live tail retained %d separators, want at most %d", got, liveMarkdownTailLines)
	}
}

func TestRunStateTracksVisibleStreamTextIncrementally(t *testing.T) {
	run := newRunState()
	run.appendStreamDelta(" \n")
	if !run.streamTextEmpty() {
		t.Fatal("whitespace-only stream should stay visually empty")
	}
	run.appendStreamDelta(" visible")
	if run.streamTextEmpty() {
		t.Fatal("visible stream delta was not recorded")
	}
	run.resetStream()
	if !run.streamTextEmpty() {
		t.Fatal("reset stream retained visible-text state")
	}
	run.currentStreamText = "restored text"
	if run.streamTextEmpty() {
		t.Fatal("directly restored stream text should use the exact fallback")
	}
}

func TestRunStateIncrementallySplitsProposedPlanAcrossDeltas(t *testing.T) {
	run := newRunState()
	for _, delta := range []string{
		"Start.\n`<proposed_plan>`\n",
		"<proposed_",
		"plan>## Steps\n1. Read files\n",
		"</proposed_",
		"plan>\nDone.",
	} {
		run.appendStreamDelta(delta)
	}
	if !run.planFound || !run.planComplete {
		t.Fatalf("plan stream state = found:%v complete:%v", run.planFound, run.planComplete)
	}
	if got := strings.TrimSpace(run.planBeforeTail.RenderInput()); !strings.Contains(got, "Start.") || !strings.Contains(got, "`<proposed_plan>`") {
		t.Fatalf("plan preamble = %q", got)
	}
	if got := strings.TrimSpace(run.planTail.RenderInput()); !strings.Contains(got, "## Steps") || !strings.Contains(got, "Read files") {
		t.Fatalf("plan body = %q", got)
	}
	if got := strings.TrimSpace(run.planAfterTail.RenderInput()); got != "Done." {
		t.Fatalf("plan suffix = %q", got)
	}
	if strings.Contains(run.currentStreamText, "<proposed_plan>") == false || !strings.Contains(run.currentStreamText, "Done.") {
		t.Fatalf("canonical stream changed: %q", run.currentStreamText)
	}
}

func TestStreamDirtyInvalidatesLiveOutputImmediately(t *testing.T) {
	m := newTUIModel(nil, nil, "stream-live")
	m.width, m.height = 100, 30
	m.run.running = true
	event := core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream:  &core.ModelStreamEvent{Type: "text_delta", Delta: "pending output"},
	}
	updated, _ := m.handleRuntimeEvent(event)
	current := updated.(*tuiModel)
	if !strings.Contains(current.view.liveContent, "pending output") {
		t.Fatalf("the next event update should show the delta without a timer: %q", current.view.liveContent)
	}
}

func TestSubagentStreamStaysInLiveAreaUntilSettled(t *testing.T) {
	m := newTUIModel(nil, nil, "subagent-live")
	m.width, m.height = 100, 30
	m.run.running = true
	m.entries = []TranscriptEntry{
		{ID: "subagent-a", Kind: KindSubagent, AgentID: "a", AgentName: "researcher", SubagentRunning: true},
		{ID: "subagent-b", Kind: KindSubagent, AgentID: "b", AgentName: "reviewer", SubagentRunning: true},
	}
	m.refreshViewportContent()
	cached := m.view.cachedBlocks[0]
	m.view.liveDirty = false

	for _, delta := range []string{"**live ", "output**"} {
		m.upsertSubagentStream(core.Event{
			AgentID: "a", AgentName: "researcher",
			Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: delta},
		})
	}
	m.upsertSubagentStream(core.Event{
		AgentID: "b", AgentName: "reviewer",
		Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: "parallel output"},
	})
	if got := m.entries[0].Text; got != "**live output**" {
		t.Fatalf("canonical subagent stream = %q", got)
	}
	if m.view.transcriptDirty || m.view.cachedBlocks[0] != cached {
		t.Fatal("subagent deltas must not invalidate the historical transcript")
	}
	m.refreshLiveContent(m.width - 4)
	if !strings.Contains(m.view.liveContent, "**live output**") || !strings.Contains(m.view.liveContent, "parallel output") {
		t.Fatalf("live area should show the unfinished Markdown literally: %q", m.view.liveContent)
	}
	if researcher, reviewer := strings.Index(m.view.liveContent, "Subagent researcher"), strings.Index(m.view.liveContent, "Subagent reviewer"); researcher < 0 || reviewer < 0 || researcher > reviewer {
		t.Fatalf("concurrent subagent tails should stay in stream start order: %q", m.view.liveContent)
	}
	m.entries[0].SubagentExpanded = true
	if rendered := renderSubagentEntry(m.entries[0], 96); strings.Contains(rendered, "**live output**") || !strings.Contains(rendered, "live output below") {
		t.Fatalf("expanded running card should point to the live tail instead of rendering full text: %q", rendered)
	}

	m.settleSubagentStream("a")
	m.upsertSubagentEntry("a", "researcher", "", false, 0)
	if len(m.run.subagentStreams) != 1 || m.run.subagentStreams["b"] == nil {
		t.Fatal("settling one agent should keep the other agent's stream active")
	}
	if got := m.entries[0].SubagentPreview; got != "**live output**" {
		t.Fatalf("settled preview = %q", got)
	}
	if !m.view.transcriptDirty {
		t.Fatal("settling should invalidate the transcript once")
	}
	if rendered := renderSubagentEntry(m.entries[0], 96); !strings.Contains(rendered, "live output") || strings.Contains(rendered, "**live output**") {
		t.Fatalf("completed expanded card should render full Markdown: %q", rendered)
	}
}

func BenchmarkLiveStreamFrame_LongResponse(b *testing.B) {
	m := newTUIModel(nil, nil, "long-stream-bench")
	m.width, m.height = 120, 40
	m.run.running = true
	m.run.appendStreamDelta(strings.Repeat("streaming response line\n", 20_000))
	m.view.invalidateLive()
	m.refreshLiveContent(m.width - 4)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		m.run.appendStreamDelta(fmt.Sprintf("delta %d\n", index))
		m.view.invalidateLive()
		m.refreshLiveContent(m.width - 4)
	}
}

func BenchmarkSubagentLiveStreamFrame_LongHistory(b *testing.B) {
	m := benchmarkTranscriptModel(2000)
	m.entries = append([]TranscriptEntry{{
		ID: "subagent-bench", Kind: KindSubagent, AgentID: "bench", AgentName: "benchmark", SubagentRunning: true,
	}}, m.entries...)
	m.run.running = true
	m.view.invalidateBlocks()
	m.refreshViewportContent()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		m.upsertSubagentStream(core.Event{
			AgentID: "bench", AgentName: "benchmark",
			Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: fmt.Sprintf("delta %d\n", index)},
		})
		m.refreshLiveContent(m.width - 4)
		_ = m.View()
	}
}
