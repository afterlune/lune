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
		m.view.invalidateBlocks()
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
	m.view.invalidateBlocks()
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
	m.view.invalidateBlocks()
}

func (m *tuiModel) finalizeTurnOutput(proposedPlan string, fallbackResponse json.RawMessage, executionID string) {
	m.flushReasoningEntry()
	m.markRunningSubagentsAndToolsDone()

	text := m.run.currentStreamText
	m.run.resetStream()

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
		if m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind != KindAssistant || !strings.Contains(strings.ToLower(m.entries[i].Text), "proposed_plan") {
			continue
		}
		m.entries[i].Text = tuiMarkdown.StripProposedPlan(m.entries[i].Text)
		if strings.TrimSpace(m.entries[i].Text) == "" {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
		}
		m.view.invalidateBlocks()
	}

	var turnDur time.Duration
	if assistantIndex := m.assistantEntryIndex(executionID); assistantIndex >= 0 {
		turnDur = m.entries[assistantIndex].TurnDuration
	} else if !m.run.taskStartedAt.IsZero() {
		turnDur = time.Since(m.run.taskStartedAt)
	}

	stream := tuiMarkdown.SplitProposedPlanStream(text)
	unusablePlan = unusablePlan || stream.Found && stream.Complete && !tuiMarkdown.HasPlanContent(stream.Plan)
	orderedPlan := strings.TrimSpace(proposedPlan) != "" && stream.Found && stream.Complete
	if orderedPlan {
		// Keep the response order visible: text before the marker, the formal plan
		// panel, then any ordinary model text after the closing marker.
		mainID := ""
		postPlanID := ""
		if executionID != "" {
			mainID = assistantOutputID(executionID)
			postPlanID = assistantPostPlanID(executionID)
		}
		m.upsertAssistantSegment(executionID, mainID, stream.Before, turnDur)
		m.upsertPlanEntry(executionID, proposedPlan)
		m.upsertAssistantSegment(executionID, postPlanID, stream.After, turnDur)
	} else {
		visibleText := tuiMarkdown.StripProposedPlan(text)
		if strings.TrimSpace(visibleText) == "" && unusablePlan {
			visibleText = unusablePlanMessage
		}
		if strings.TrimSpace(visibleText) != "" {
			id := ""
			if executionID != "" {
				id = assistantOutputID(executionID)
			}
			m.upsertAssistantSegment(executionID, id, visibleText, turnDur)
		} else if turnDur >= time.Second && len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindAssistant && m.entries[len(m.entries)-1].TurnDuration == 0 {
			m.entries[len(m.entries)-1].TurnDuration = turnDur
			m.view.invalidateBlocks()
		}
		if strings.TrimSpace(proposedPlan) != "" {
			m.upsertPlanEntry(executionID, proposedPlan)
		}
	}

	if strings.TrimSpace(proposedPlan) != "" {
		m.activePlanReview = &ActivePlanReview{
			Text:        proposedPlan,
			ExecutionID: executionID,
			Selected:    0,
		}
	}
}

func assistantOutputID(executionID string) string { return "asst-execution-" + executionID }

func assistantPostPlanID(executionID string) string {
	return assistantOutputID(executionID) + "-after-plan"
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
		m.view.invalidateBlocks()
		return
	}
	if id == "" {
		id = fmt.Sprintf("asst-%d", time.Now().UnixNano())
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID: id, ExecutionID: executionID, Kind: KindAssistant, Text: trimmed,
		TurnDuration: turnDur, Timestamp: time.Now(),
	})
	m.view.invalidateBlocks()
}

func (m *tuiModel) upsertPlanEntry(executionID, text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindPlan && m.entries[i].Text == trimmed &&
			(executionID == "" || m.entries[i].ExecutionID == executionID) {
			return
		}
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID: fmt.Sprintf("plan-%d", time.Now().UnixNano()), ExecutionID: executionID,
		Kind: KindPlan, Text: trimmed, Timestamp: time.Now(),
	})
	m.view.invalidateBlocks()
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
