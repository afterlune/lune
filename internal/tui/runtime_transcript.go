package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
)

func (m *tuiModel) flushReasoningEntry() {
	if m.currentReasoningText == "" {
		return
	}
	trimmed := strings.TrimSpace(m.currentReasoningText)
	dur := time.Since(m.reasoningStartTime)
	m.currentReasoningText = ""
	m.reasoningStartTime = time.Time{}
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
		m.invalidateEntryCache()
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		Kind:              KindReasoning,
		Text:              trimmed,
		ReasoningDuration: dur,
		ReasoningExpanded: false,
		Timestamp:         time.Now(),
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) flushStreamTextEntry() {
	if m.currentStreamText == "" {
		return
	}
	text := tuiMarkdown.StripProposedPlan(m.currentStreamText)
	m.currentStreamText = ""
	if strings.TrimSpace(text) == "" {
		return
	}
	m.entries = append(m.entries, TranscriptEntry{
		ID:          fmt.Sprintf("asst-%d", time.Now().UnixNano()),
		ExecutionID: m.runningExecution,
		Kind:        KindAssistant,
		Text:        text,
		Timestamp:   time.Now(),
	})
	m.invalidateEntryCache()
}

func (m *tuiModel) finalizeTurnOutput(proposedPlan string, fallbackResponse json.RawMessage, executionID string) {
	m.flushReasoningEntry()
	m.markRunningSubagentsAndToolsDone()

	text := m.currentStreamText
	m.currentStreamText = ""

	if len(fallbackResponse) > 0 {
		if responseText := extractResponseText(fallbackResponse); strings.TrimSpace(responseText) != "" {
			text = responseText
			if executionID != "" {
				stableID := assistantOutputID(executionID)
				for i := len(m.entries) - 1; i >= 0; i-- {
					if m.entries[i].Kind == KindUser {
						break
					}
					if m.entries[i].Kind == KindAssistant && m.entries[i].ExecutionID == executionID && m.entries[i].ID != stableID {
						m.entries = append(m.entries[:i], m.entries[i+1:]...)
					}
				}
			}
		}
	}

	// The proposed plan is authoritative only when the Core returns one complete
	// <proposed_plan>...</proposed_plan> block. Never infer a plan from a dangling
	// or literal tag in the assistant text: that used to turn ordinary replies
	// (including greetings that merely mention the tag) into empty plan reviews.
	proposedPlan = tuiMarkdown.CleanProposedPlanTags(proposedPlan)
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
		m.invalidateEntryCache()
	}

	text = tuiMarkdown.StripProposedPlan(text)

	var turnDur time.Duration
	assistantIndex := m.assistantEntryIndex(executionID)
	if assistantIndex >= 0 {
		turnDur = m.entries[assistantIndex].TurnDuration
	} else if !m.taskStartedAt.IsZero() {
		turnDur = time.Since(m.taskStartedAt)
	}

	if strings.TrimSpace(text) != "" {
		if assistantIndex >= 0 {
			m.entries[assistantIndex].Text = text
		} else {
			id := fmt.Sprintf("asst-%d", time.Now().UnixNano())
			if executionID != "" {
				id = assistantOutputID(executionID)
			}
			m.entries = append(m.entries, TranscriptEntry{
				ID: id, ExecutionID: executionID, Kind: KindAssistant, Text: text,
				TurnDuration: turnDur, Timestamp: time.Now(),
			})
		}
		m.invalidateEntryCache()
	} else if turnDur >= time.Second && len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindAssistant && m.entries[len(m.entries)-1].TurnDuration == 0 {
		m.entries[len(m.entries)-1].TurnDuration = turnDur
		m.invalidateEntryCache()
	}

	if strings.TrimSpace(proposedPlan) != "" {
		hasPlanEntry := len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindPlan && m.entries[len(m.entries)-1].Text == proposedPlan
		if !hasPlanEntry {
			m.entries = append(m.entries, TranscriptEntry{
				ID:        fmt.Sprintf("plan-%d", time.Now().UnixNano()),
				Kind:      KindPlan,
				Text:      proposedPlan,
				Timestamp: time.Now(),
			})
			m.invalidateEntryCache()
		}
		m.activePlanReview = &ActivePlanReview{
			Text:        proposedPlan,
			ExecutionID: executionID,
			Selected:    0,
		}
	}
}

func assistantOutputID(executionID string) string { return "asst-execution-" + executionID }

func (m *tuiModel) assistantEntryIndex(executionID string) int {
	if executionID == "" {
		return -1
	}
	id := assistantOutputID(executionID)
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].ID == id && m.entries[i].Kind == KindAssistant {
			return i
		}
	}
	return -1
}
