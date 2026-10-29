package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
)

const unusablePlanMessage = "The model did not return a concrete plan. Ask it to provide the implementation steps."

func (m *tuiModel) flushReasoningEntry() {
	if m.run.currentReasoningText == "" {
		return
	}
	trimmed := strings.TrimSpace(m.run.currentReasoningText)
	dur := time.Since(m.run.reasoningStartTime)
	m.run.resetReasoning()
	m.run.reasoningStartTime = time.Time{}
	if trimmed == "" {
		return
	}
	// Merge into the trailing reasoning entry if no assistant/tool entry intervened,
	// avoiding fragmented micro-duration "Thought for 8ms" blocks.
	if len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindReasoning {
		last := &m.entries[len(m.entries)-1]
		if last.Text != "" && !strings.HasSuffix(last.Text, "\n") {
			last.Text += "\n"
		}
		last.Text += trimmed
		last.ReasoningDuration += dur
		m.view.invalidateEntry(len(m.entries) - 1)
		return
	}
	m.beginRoundIfNeeded()
	m.entries = append(m.entries, TranscriptEntry{
		Kind:              KindReasoning,
		Text:              trimmed,
		ReasoningDuration: dur,
		ReasoningExpanded: false,
		Timestamp:         time.Now(),
	})
	m.view.invalidateContent()
}

func (m *tuiModel) flushStreamTextEntry() {
	if m.run.currentStreamText == "" {
		return
	}
	text := tuiMarkdown.StripProposedPlan(m.run.currentStreamText)
	m.run.resetStream()
	if strings.TrimSpace(text) == "" {
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID:          fmt.Sprintf("asst-%d", time.Now().UnixNano()),
		ExecutionID: m.run.runningExecution,
		Kind:        KindAssistant,
		Text:        text,
		Timestamp:   time.Now(),
	})
	m.view.invalidateContent()
}

func (m *tuiModel) finalizeSteeredSegment() {
	m.flushReasoningEntry()
	text := strings.TrimSpace(m.run.currentStreamText)
	m.run.resetStream()
	m.run.resetReasoning()
	m.run.reasoningStartTime = time.Time{}
	m.run.streamFirstTokenTime = time.Time{}
	m.run.streamGenerationTokens = 0
	m.run.thinking = false
	if text != "" {
		executionID := m.run.runningExecution
		segmentID := steeredAssistantOutputID(executionID, m.run.streamSegment)
		stream := tuiMarkdown.SplitProposedPlanStream(text)
		m.upsertAssistantSegment(executionID, segmentID, stream.Before, 0)
		if stream.Found && tuiMarkdown.HasPlanContent(stream.Plan) {
			m.entries = append(m.entries, TranscriptEntry{
				ID:          fmt.Sprintf("plan-steered-%s-%d", executionID, m.run.streamSegment),
				ExecutionID: executionID, Kind: KindPlan, Text: stream.Plan, Timestamp: time.Now(),
			})
		}
		m.upsertAssistantSegment(executionID, segmentID+"-after-plan", stream.After, 0)
	}
	m.run.streamSegment++
	m.activePlanReview = nil
	m.view.invalidateBlocks()
}

func (m *tuiModel) finalizeTurnOutput(proposedPlan string, fallbackResponse json.RawMessage, executionID string, showPlanReview ...bool) {
	active := m.run.runningExecution == "" || m.run.runningExecution == executionID
	text := ""
	segment := 0
	if active {
		m.flushReasoningEntry()
		m.markRunningSubagentsAndToolsDone()
		text = m.run.currentStreamText
		m.run.resetStream()
		m.view.invalidateLive()
		segment = m.run.streamSegment
	} else {
		// A prior result may arrive after the next queued turn starts. Its receipt
		// cannot flush, reset, or borrow the next turn's live buffers.
		for _, entry := range m.entries {
			if entry.Kind == KindUser && entry.ExecutionID == executionID && entry.ToolCallID != "" && entry.ToolCallID != executionID {
				segment++
			}
		}
	}

	if len(fallbackResponse) > 0 {
		if responseText := extractResponseText(fallbackResponse); strings.TrimSpace(responseText) != "" {
			text = responseText
		}
	}

	// The proposed plan is authoritative only when the Core returns one complete
	// <proposed_plan>...</proposed_plan> block. Never infer a plan from a dangling
	// or literal tag in the assistant text: that used to turn ordinary replies
	// (including greetings that merely mention the tag) into empty plan reviews.
	hadProposedPlan := proposedPlan != ""
	proposedPlan = tuiMarkdown.CleanProposedPlanTags(proposedPlan)
	unusablePlan := hadProposedPlan && !tuiMarkdown.HasPlanContent(proposedPlan)
	if unusablePlan {
		proposedPlan = ""
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if !active && m.entries[i].ExecutionID != executionID {
			continue
		}
		if active && m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind != KindAssistant || !strings.Contains(strings.ToLower(m.entries[i].Text), "proposed_plan") {
			continue
		}
		m.entries[i].Text = tuiMarkdown.StripProposedPlan(m.entries[i].Text)
		if strings.TrimSpace(m.entries[i].Text) == "" {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			m.view.invalidateBlocks()
		} else {
			m.view.invalidateEntry(i)
		}
	}

	var turnDur time.Duration
	finalOutputID := assistantOutputIDForSegment(executionID, segment)
	if assistantIndex := m.assistantEntryIndexByID(executionID, finalOutputID); assistantIndex >= 0 {
		turnDur = m.entries[assistantIndex].TurnDuration
	} else if active && !m.run.taskStartedAt.IsZero() {
		turnDur = time.Since(m.run.taskStartedAt)
	}

	stream := tuiMarkdown.SplitProposedPlanStream(text)
	unusablePlan = unusablePlan || stream.Found && stream.Complete && !tuiMarkdown.HasPlanContent(stream.Plan)
	orderedPlan := strings.TrimSpace(proposedPlan) != "" && stream.Found && stream.Complete
	planID := planOutputIDForSegment(executionID, segment)
	if orderedPlan {
		// Keep the response order visible: text before the marker, the formal plan
		// panel, then any ordinary model text after the closing marker.
		mainID := ""
		postPlanID := ""
		if executionID != "" {
			mainID = finalOutputID
			if segment == 0 {
				postPlanID = assistantPostPlanID(executionID)
			} else {
				postPlanID = mainID + "-after-plan"
			}
		}
		m.upsertAssistantSegment(executionID, mainID, stream.Before, turnDur)
		m.upsertPlanEntry(executionID, proposedPlan, planID)
		m.upsertAssistantSegment(executionID, postPlanID, stream.After, turnDur)
	} else {
		visibleText := tuiMarkdown.StripProposedPlan(text)
		if strings.TrimSpace(visibleText) == "" && unusablePlan {
			visibleText = unusablePlanMessage
		}
		if strings.TrimSpace(visibleText) != "" {
			id := ""
			if executionID != "" {
				id = finalOutputID
			}
			m.upsertAssistantSegment(executionID, id, visibleText, turnDur)
		} else if turnDur >= time.Second && len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindAssistant && m.entries[len(m.entries)-1].TurnDuration == 0 {
			m.entries[len(m.entries)-1].TurnDuration = turnDur
			m.view.invalidateEntry(len(m.entries) - 1)
		}
		if strings.TrimSpace(proposedPlan) != "" {
			m.upsertPlanEntry(executionID, proposedPlan, planID)
		}
	}

	showReview := len(showPlanReview) == 0 || showPlanReview[0]
	if active && strings.TrimSpace(proposedPlan) != "" && showReview {
		m.activePlanReview = &ActivePlanReview{
			Text:        proposedPlan,
			ExecutionID: executionID,
			Selected:    0,
		}
	}
}

func assistantOutputID(executionID string) string { return "asst-execution-" + executionID }

func assistantOutputIDForSegment(executionID string, segment int) string {
	if segment <= 0 || executionID == "" {
		return assistantOutputID(executionID)
	}
	return fmt.Sprintf("%s-segment-%d", assistantOutputID(executionID), segment)
}

func steeredAssistantOutputID(executionID string, segment int) string {
	return fmt.Sprintf("%s-steered-%d", assistantOutputID(executionID), segment)
}

func assistantPostPlanID(executionID string) string {
	return assistantOutputID(executionID) + "-after-plan"
}

func planOutputIDForSegment(executionID string, segment int) string {
	if executionID == "" {
		return ""
	}
	return fmt.Sprintf("plan-execution-%s-segment-%d", executionID, segment)
}

func (m *tuiModel) upsertAssistantSegment(executionID, id, text string, turnDur time.Duration) {
	trimmed := strings.TrimSpace(text)
	index := m.assistantEntryIndexByID(executionID, id)
	if trimmed == "" {
		if index >= 0 {
			m.entries = append(m.entries[:index], m.entries[index+1:]...)
			m.view.invalidateBlocks()
		}
		return
	}
	if index >= 0 {
		m.entries[index].Text = trimmed
		if turnDur > 0 && m.entries[index].TurnDuration == 0 {
			m.entries[index].TurnDuration = turnDur
		}
		m.view.invalidateEntry(index)
		return
	}
	if id == "" {
		id = fmt.Sprintf("asst-%d", time.Now().UnixNano())
	}
	m.insertExecutionEntry(TranscriptEntry{ID: id, ExecutionID: executionID, Kind: KindAssistant, Text: trimmed, TurnDuration: turnDur, Timestamp: time.Now()})
	m.view.invalidateContent()
}

func (m *tuiModel) upsertPlanEntry(executionID, text string, entryIDs ...string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	entryID := ""
	if len(entryIDs) > 0 {
		entryID = entryIDs[0]
	}
	if entryID != "" {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].ID == entryID && m.entries[i].Kind == KindPlan {
				m.entries[i].Text = trimmed
				m.view.invalidateEntry(i)
				return
			}
		}
	} else {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindPlan && m.entries[i].Text == trimmed &&
				(executionID == "" || m.entries[i].ExecutionID == executionID) {
				return
			}
		}
	}
	if entryID == "" {
		entryID = fmt.Sprintf("plan-%d", time.Now().UnixNano())
	}
	m.insertExecutionEntry(TranscriptEntry{ID: entryID, ExecutionID: executionID, Kind: KindPlan, Text: trimmed, Timestamp: time.Now()})
	m.view.invalidateContent()
}

func (m *tuiModel) assistantEntryIndex(executionID string) int {
	if executionID == "" {
		return -1
	}
	return m.assistantEntryIndexByID(executionID, assistantOutputID(executionID))
}

func (m *tuiModel) assistantEntryIndexByID(executionID, id string) int {
	if executionID == "" || id == "" {
		return -1
	}
	// A resumed execution must render after the question answer that admitted
	// it. The original assistant entry belongs to the pre-question turn and
	// updating it would make the resumed response appear inside the old plan.
	boundary := -1
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindQuestionAnswered && m.entries[i].ExecutionID == executionID {
			boundary = i
			break
		}
	}
	for i := len(m.entries) - 1; i > boundary; i-- {
		if m.entries[i].ID == id && m.entries[i].Kind == KindAssistant {
			return i
		}
	}
	return -1
}

func (m *tuiModel) insertExecutionEntry(entry TranscriptEntry) {
	at := len(m.entries)
	if entry.ExecutionID != "" && m.run.runningExecution != "" && entry.ExecutionID != m.run.runningExecution {
		last := -1
		for i, existing := range m.entries {
			if existing.ExecutionID == entry.ExecutionID {
				last = i
			}
		}
		if last >= 0 {
			at = last + 1
		} else {
			for i, existing := range m.entries {
				if existing.ExecutionID == m.run.runningExecution {
					at = i
					break
				}
			}
		}
	}
	m.entries = append(m.entries, TranscriptEntry{})
	copy(m.entries[at+1:], m.entries[at:])
	m.entries[at] = entry
	if at < len(m.entries)-1 {
		m.view.invalidateBlocks()
	}
}
