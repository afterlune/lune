package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
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

func TestStreamRenderThrottleSchedulesAndRejectsStaleFlush(t *testing.T) {
	m := newTUIModel(nil, nil, "stream-throttle")
	m.width, m.height = 100, 30
	m.run.appendStreamDelta("pending output")
	m.run.lastStreamRenderAt = time.Now()
	cmd := m.markStreamDirty()
	if cmd == nil || !m.run.streamRenderPending || m.view.liveDirty {
		t.Fatalf("throttled render state: cmd=%v pending=%v dirty=%v", cmd != nil, m.run.streamRenderPending, m.view.liveDirty)
	}
	message := cmd().(streamRenderMsg)
	updated, _ := m.Update(message)
	current := updated.(*tuiModel)
	if current.run.streamRenderPending || !strings.Contains(current.view.liveContent, "pending output") {
		t.Fatalf("render tick did not flush pending content: pending=%v content=%q", current.run.streamRenderPending, current.view.liveContent)
	}

	current.run.lastStreamRenderAt = time.Now()
	stale := current.markStreamDirty()
	if stale == nil {
		t.Fatal("expected a scheduled render")
	}
	current.run.resetStream()
	staleMessage := stale().(streamRenderMsg)
	current.view.liveDirty = false
	updated, _ = current.Update(staleMessage)
	if updated.(*tuiModel).view.liveDirty {
		t.Fatal("stale render timer dirtied a reset stream")
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
