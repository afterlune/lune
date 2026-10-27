package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestToolRevealFrameSnapshots(t *testing.T) {
	now := time.Unix(100, 0)
	r := &toolRoundReveal{start: 0}
	entries := []TranscriptEntry{{Kind: KindTool}}
	ranges := []entryLineRange{{IsRoundHeader: true}, {StartLine: 1, EndLine: 4}}
	r.sync("group\nread a\na1\na2\na3", ranges, entries, true, now)
	want := []string{"group\nread a", "group\nread a\na1", "group\nread a\na1\na2", "group\nread a\na1\na2\na3"}
	for frame := range want {
		if frame > 0 {
			r.advance(now.Add(time.Duration(frame) * toolRevealRowInterval))
		}
		rows, rr := r.project()
		if got := strings.Join(rows, "\n"); got != want[frame] {
			t.Fatalf("frame %d:\n%s\nwant:\n%s", frame, got, want[frame])
		}
		for _, hit := range rr {
			if hit.EndLine >= len(rows) {
				t.Fatalf("hidden row included in hit map: %+v", hit)
			}
		}
	}
	// A following call is paced too, rather than suddenly joining the first.
	entries = append(entries, TranscriptEntry{Kind: KindTool})
	ranges = append(ranges, entryLineRange{EntryIndex: 1, StartLine: 5, EndLine: 7})
	at := now.Add(100 * time.Millisecond)
	r.sync("group\nread a\na1\na2\na3\nread b\nb1\nb2", ranges, entries, true, at)
	rows, _ := r.project()
	if strings.Contains(strings.Join(rows, "\n"), "read b") {
		t.Fatal("following call should join the row queue")
	}
	r.advance(at.Add(toolRevealRowInterval))
	rows, _ = r.project()
	if got := rows[len(rows)-1]; got != "read b" {
		t.Fatalf("next call title should appear before its result: %q", got)
	}
	r.advance(at.Add(toolRevealMaxDelay))
	if len(r.queue) != 0 {
		t.Fatal("burst did not catch up within 120ms")
	}
}

func TestToolRevealUpdatesStatusWithoutRestartingDeadline(t *testing.T) {
	now := time.Unix(200, 0)
	r := &toolRoundReveal{}
	entries := []TranscriptEntry{{Kind: KindTool}, {Kind: KindTool}}
	ranges := []entryLineRange{{IsRoundHeader: true}, {StartLine: 1, EndLine: 4}}
	r.sync("group\nrunning a\na1\na2\na3", ranges, entries[:1], true, now)
	r.advance(now.Add(toolRevealRowInterval))
	deadline := r.queue[0].deadline
	r.sync("group\nfinished a\na1\na2\na3\nread b\nb1", append(ranges, entryLineRange{EntryIndex: 1, StartLine: 5, EndLine: 6}), entries, true, now.Add(80*time.Millisecond))
	rows, _ := r.project()
	if rows[1] != "finished a" {
		t.Fatalf("status was delayed: %v", rows)
	}
	if r.queue[0].deadline != deadline {
		t.Fatal("new calls postponed existing rows")
	}
	r.advance(deadline)
	if r.items[0].shown != 4 {
		t.Fatal("older rows remained hidden at their deadline")
	}
	// Replacing a fixed-size running preview does not type it again.
	r.flush()
	r.sync("group\nfinished a\nnew1\nnew2\nnew3\nread b\nb1", append(ranges, entryLineRange{EntryIndex: 1, StartLine: 5, EndLine: 6}), entries, true, deadline)
	if len(r.queue) != 0 {
		t.Fatal("replacing already visible rows must not replay them")
	}
}

func TestToolRevealFirstTitleAfterReasoning(t *testing.T) {
	r := &toolRoundReveal{}
	entries := []TranscriptEntry{{Kind: KindReasoning}, {Kind: KindTool}}
	ranges := []entryLineRange{{IsRoundHeader: true}, {StartLine: 1, EndLine: 1}, {EntryIndex: 1, StartLine: 2, EndLine: 3}}
	r.sync("group\nthought\nread a\na1", ranges, entries, true, time.Unix(250, 0))
	rows, _ := r.project()
	if got := strings.Join(rows, "\n"); got != "group\nthought\nread a" {
		t.Fatalf("first call title should be immediate after reasoning: %q", got)
	}
}

func TestToolRevealKeepsRowsInTranscriptOrderWhenOutputGrows(t *testing.T) {
	now := time.Unix(275, 0)
	r := &toolRoundReveal{start: 0}
	entries := []TranscriptEntry{{Kind: KindTool}, {Kind: KindTool}}
	r.sync("group\nrunning a\nprogress", []entryLineRange{
		{IsRoundHeader: true}, {EntryIndex: 0, StartLine: 1, EndLine: 2},
	}, entries[:1], true, now)

	at := now.Add(40 * time.Millisecond)
	r.sync("group\nfinished a\nline 1\nline 2\nline 3\nread b\nb1", []entryLineRange{
		{IsRoundHeader: true}, {EntryIndex: 0, StartLine: 1, EndLine: 4}, {EntryIndex: 1, StartLine: 5, EndLine: 6},
	}, entries, true, at)
	r.advance(at.Add(20 * time.Millisecond))
	rows, _ := r.project()
	text := strings.Join(rows, "\n")
	if !strings.Contains(text, "line 3") || strings.Contains(text, "read b") {
		t.Fatalf("later tool title jumped ahead of earlier output rows:\n%s", text)
	}
}

func TestToolRevealCatchUpLeavesRecentRowsQueued(t *testing.T) {
	now := time.Unix(290, 0)
	first := &toolRevealItem{index: 0, lines: []string{"read a", "old result"}, shown: 1}
	second := &toolRevealItem{index: 1, lines: []string{"read b", "new result"}}
	r := &toolRoundReveal{
		start:  0,
		header: "group",
		items:  []*toolRevealItem{first, second},
		queue: []toolRevealStep{
			{item: first, row: 1, deadline: now.Add(-time.Millisecond)},
			{item: second, row: 0, deadline: now.Add(100 * time.Millisecond)},
			{item: second, row: 1, deadline: now.Add(100 * time.Millisecond)},
		},
		nextAt: now,
	}
	if !r.advance(now) {
		t.Fatal("expired row was not advanced")
	}
	if first.shown != 2 || second.shown != 0 || len(r.queue) != 2 {
		t.Fatalf("catch-up crossed into recent rows: first=%d second=%d queue=%d", first.shown, second.shown, len(r.queue))
	}
	rows, _ := r.project()
	if got := strings.Join(rows, "\n"); strings.Contains(got, "read b") {
		t.Fatalf("recent tool call appeared during old-row catch-up:\n%s", got)
	}
}

func toolRevealModel(history int) *tuiModel {
	m := benchmarkTranscriptModel(history)
	m.run.running = true
	m.run.taskStartedAt = time.Now()
	m.entries = append(m.entries, TranscriptEntry{
		ID: "read-a", Kind: KindTool, ToolCallID: "read-a", ToolName: "read_file", ToolCommand: "Read a.go", ToolOutput: "a1\na2\na3",
	})
	m.view.animateTools = true
	m.view.invalidateContent()
	m.updateLayout()
	return m
}

func TestToolRevealModelFramesAndLifecycle(t *testing.T) {
	m := toolRevealModel(10)
	r := m.view.toolReveal
	if r == nil || len(r.queue) == 0 {
		t.Fatal("live tool output should have pending rows")
	}
	if got := m.entries[len(m.entries)-1].ToolOutput; got != "a1\na2\na3" {
		t.Fatalf("presentation changed canonical output: %q", got)
	}
	if strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "a3") {
		t.Fatal("all result rows appeared in the first frame")
	}
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, "Read a.go") || strings.Contains(screen, "a3") {
		t.Fatalf("initial terminal frame must show the title before the full result:\n%s", screen)
	}
	generation := m.view.toolRevealGeneration
	m.Update(toolRevealFrameMsg{generation: generation, bindingGeneration: m.bindingGeneration, at: r.queue[0].deadline})
	if len(r.queue) != 0 || !strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "a3") {
		t.Fatal("final frame did not show the complete result")
	}
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, "a3") {
		t.Fatalf("terminal frame lost revealed output:\n%s", screen)
	}
	if len(m.view.entryLineRanges) == 0 {
		t.Fatal("visible frame lost its hit ranges")
	}
	// Resizing restores a complete document and invalidates scheduled frames.
	m = toolRevealModel(10)
	stale := toolRevealFrameMsg{generation: m.view.toolRevealGeneration, bindingGeneration: m.bindingGeneration, at: time.Now().Add(time.Second)}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	if m.view.toolRevealWindowed || (m.view.toolReveal != nil && len(m.view.toolReveal.queue) > 0) {
		t.Fatal("resize should finish pending rows")
	}
	before := strings.Join(m.view.transcriptLines, "\n")
	m.Update(stale)
	if after := strings.Join(m.view.transcriptLines, "\n"); after != before {
		t.Fatal("stale frame changed the resized view")
	}
	m.resetSessionState()
	m.Update(stale)
	if m.view.toolReveal != nil || len(m.entries) != 0 {
		t.Fatal("old frame crossed a session reset")
	}
}

func TestToolRevealFlushesForAttentionAndFailure(t *testing.T) {
	m := toolRevealModel(10)
	last := len(m.entries) - 1
	m.entries[last].ToolFailed = true
	m.view.animateTools = true
	m.view.invalidateEntry(last)
	m.refreshViewportContent()
	if m.view.toolReveal != nil && len(m.view.toolReveal.queue) != 0 {
		t.Fatal("failure details should appear immediately")
	}
	m = toolRevealModel(10)
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventTaskFinished})
	if m.view.toolReveal != nil && len(m.view.toolReveal.queue) != 0 {
		t.Fatal("task completion left hidden rows")
	}
	if !strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "a3") {
		t.Fatal("completion dropped pending output")
	}
}

func TestToolRevealHistoryDoesNotAnimate(t *testing.T) {
	m := toolRevealModel(10)
	m.view.invalidateBlocks()
	m.refreshViewportContent()
	if r := m.view.toolReveal; r != nil && len(r.queue) != 0 {
		t.Fatal("history rebuild must not replay tool rows")
	}
}

func TestToolRevealIgnoresNonDisplayStreamDeltas(t *testing.T) {
	m := toolRevealModel(10)
	for _, stream := range []core.ModelStreamEvent{
		{Type: "tool_call_delta", Delta: "arguments"},
		{Type: "text_delta", Purpose: "memory", Delta: "internal"},
		{Type: "reasoning_delta", Delta: "thinking"},
	} {
		m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventModelStream, Stream: &stream})
		if len(m.view.toolReveal.queue) == 0 {
			t.Fatalf("non-display event interrupted the row queue: %+v", stream)
		}
	}
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventModelStream, Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: "visible response"}})
	if len(m.view.toolReveal.queue) != 0 {
		t.Fatal("visible assistant response should finish pending rows")
	}
}

func TestToolRevealRuntimeBatchAndImmediateStatus(t *testing.T) {
	m := newTUIModel(nil, nil, "tool-batch")
	m.width, m.height = 100, 35
	m.run.running = true
	m.run.taskStartedAt = time.Now()
	m.updateLayout()
	tool := func(id string) core.Event {
		return core.Event{Kind: core.EventToolStarted, Tool: core.ToolCall{ID: id, Name: "read_file", Started: true}}
	}
	_, cmd := m.Update(runtimeEventMsg{event: tool("a"), batch: []core.Event{tool("b"), tool("c")}})
	if len(m.entries) != 3 {
		t.Fatalf("visual pacing delayed actual tool events: %d", len(m.entries))
	}
	if cmd == nil || !m.view.toolRevealPending {
		t.Fatal("runtime batch did not schedule its presentation frame")
	}
	r := m.view.toolReveal
	if r == nil || len(r.queue) == 0 {
		t.Fatal("batched calls appeared all at once")
	}
	if rows, _ := r.project(); len(rows) != 2 {
		t.Fatalf("first frame should contain the group and first title, got %d rows", len(rows))
	}
	m.Update(runtimeEventMsg{event: core.Event{Kind: core.EventToolFinished, Tool: core.ToolCall{ID: "a", Name: "read_file"}}})
	if m.entries[0].ToolRunning {
		t.Fatal("finished tool status was delayed")
	}
	rows, _ := m.view.toolReveal.project()
	if strings.Contains(ansi.Strip(rows[1]), "Running") {
		t.Fatalf("visible title kept its old running status: %s", rows[1])
	}
}

func TestToolRevealInputPreservesClickedRow(t *testing.T) {
	m := toolRevealModel(50)
	r := m.view.toolReveal
	rows, _ := r.project()
	if len(rows) != 2 {
		t.Fatalf("expected title-only initial frame: %v", rows)
	}
	titleDoc := r.offset + 1
	// A growing live-output band may resize the viewport between reveal frames.
	windowStart := m.view.toolRevealWindowStart
	m.view.liveContent += "\nextra live row\nextra live row"
	m.view.liveDirty = false
	m.updateLayout()
	screenRow := titleDoc - windowStart - m.viewport.YOffset()
	title := ansi.Strip(m.view.transcriptLines[titleDoc])
	height := m.viewport.Height()
	m.Update(tea.MouseClickMsg{X: 0, Y: screenRow + 3, Button: tea.MouseLeft})
	if m.view.toolRevealWindowed {
		t.Fatal("input must restore the complete scroll document")
	}
	actual := m.viewport.YOffset() + screenRow
	if got := ansi.Strip(m.view.transcriptLines[actual]); got != title {
		t.Fatalf("row under pointer changed: %q -> %q", title, got)
	}
	if !m.scrolledUp {
		t.Fatal("preserving a row above new output should detach bottom following")
	}
	if !m.input.selecting || m.input.selectStartDocRow != actual {
		t.Fatal("mouse selection did not start on the anchored row")
	}
	m.updateLayout()
	if m.viewport.Height() != height {
		t.Fatal("live-band resizing moved the viewport during selection")
	}
	m.Update(tea.MouseReleaseMsg{X: 10, Y: screenRow + 3, Button: tea.MouseLeft})
	if !strings.Contains(m.input.selectedText, "Read") {
		t.Fatalf("selection copied a different row: %q", m.input.selectedText)
	}
}

func TestToolRevealClickInComposerKeepsBottomFollowing(t *testing.T) {
	m := toolRevealModel(50)
	if m.view.toolReveal == nil || len(m.view.toolReveal.queue) == 0 || m.scrolledUp {
		t.Fatal("expected pending reveal while following the bottom")
	}
	x, y := m.composerMouseOffset()
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !m.input.composerSelecting {
		t.Fatal("click did not start composer selection")
	}
	if m.scrolledUp {
		t.Fatal("clicking the composer detached transcript bottom following")
	}
	if m.view.toolRevealWindowed || len(m.view.toolReveal.queue) != 0 {
		t.Fatal("composer input must still finish the pending reveal")
	}
}

func TestTrailingRoundRefreshesWhenTaskStateChanges(t *testing.T) {
	m := newTUIModel(nil, nil, "trailing-round-state")
	m.width, m.height = 100, 35
	m.entries = []TranscriptEntry{{Kind: KindTool, ToolName: "read_file", ToolCommand: "Read a.go"}}
	m.updateLayout()
	if strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "⠋") {
		t.Fatal("completed tool round rendered as live")
	}

	m.run.running = true
	m.view.invalidateLive()
	m.updateLayout()
	if !strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "⠋") {
		t.Fatal("task start did not refresh trailing round state")
	}

	m.run.running = false
	m.view.invalidateLive()
	m.updateLayout()
	if strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "⠋") {
		t.Fatal("task completion left trailing round spinner visible")
	}
}

func TestTranscriptAndLiveContentStayStableDuringComposerSelection(t *testing.T) {
	m := toolRevealModel(10)
	x, y := m.composerMouseOffset()
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	transcriptBefore := strings.Join(m.view.transcriptLines, "\n")
	liveBefore := m.view.liveContent

	last := len(m.entries) - 1
	m.entries[last].ToolOutput += "\na4"
	m.entries[last].ToolExpanded = true
	m.view.invalidateEntry(last)
	m.run.taskActivity = "Updating a file"
	m.view.invalidateLive()
	m.refreshViewportContent()
	if got := strings.Join(m.view.transcriptLines, "\n"); got != transcriptBefore {
		t.Fatal("transcript moved during composer selection")
	}
	if m.view.liveContent != liveBefore {
		t.Fatal("live band moved during composer selection")
	}

	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !strings.Contains(strings.Join(m.view.transcriptLines, "\n"), "a4") || !strings.Contains(m.view.liveContent, "Updating a file") {
		t.Fatal("deferred content was not rendered when composer selection ended")
	}
}

func BenchmarkToolRevealFrame_LongHistory(b *testing.B) {
	m := toolRevealModel(2000)
	r := m.view.toolReveal
	item := r.items[len(r.items)-1]
	now := time.Unix(300, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item.shown = 1
		r.queue = append(r.queue[:0], toolRevealStep{item: item, row: 1, deadline: now.Add(toolRevealMaxDelay)})
		r.nextAt = now
		r.advance(now.Add(toolRevealRowInterval))
		m.publishToolReveal(true)
		_ = m.View()
	}
	b.StopTimer()
	if m.view.transcriptDirty {
		b.Fatal("animation invalidated historical rendering")
	}
}
