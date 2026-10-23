package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// interruptRunningTask halts an ongoing execution, preserves the execution checkpoint,
// and presents a rich checkpoint status card in the transcript.
func (m *tuiModel) interruptRunningTask() (tea.Model, tea.Cmd) {
	if !m.running || m.runtime == nil || m.runningExecution == "" {
		return m, nil
	}

	if m.currentReasoningText != "" {
		dur := time.Since(m.reasoningStartTime)
		m.entries = append(m.entries, TranscriptEntry{
			Kind:              KindReasoning,
			Text:              m.currentReasoningText,
			ReasoningDuration: dur,
			ReasoningExpanded: false,
			Timestamp:         time.Now(),
		})
		m.currentReasoningText = ""
		m.reasoningStartTime = time.Time{}
		m.view.invalidateBlocks()
	}
	if m.currentStreamText != "" {
		m.entries = append(m.entries, TranscriptEntry{
			ID:        fmt.Sprintf("asst-%d", time.Now().UnixNano()),
			Kind:      KindAssistant,
			Text:      m.currentStreamText,
			Timestamp: time.Now(),
		})
		m.currentStreamText = ""
		m.view.invalidateBlocks()
	}

	execID := m.runningExecution
	toolCount := 0
	lastToolName := ""
	lastToolCmd := ""
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind == KindTool {
			toolCount++
			if lastToolName == "" {
				lastToolName = m.entries[i].ToolName
				lastToolCmd = m.entries[i].ToolCommand
			}
		}
	}

	m.pausedCheckpoint = &PausedCheckpoint{
		ExecutionID:  execID,
		ToolCount:    toolCount,
		LastToolName: lastToolName,
	}

	m.runtime.Interrupt(execID)
	m.running = false
	m.thinking = false

	card := m.formatCheckpointCard(m.pausedCheckpoint, lastToolCmd)
	m.addSystemEntry(card)
	m.refreshViewportContent()
	m.updateLayout()
	return m, nil
}

// formatCheckpointCard constructs a markdown status block summarizing an interrupted task checkpoint.
func (m *tuiModel) formatCheckpointCard(cp *PausedCheckpoint, lastToolCmd string) string {
	if cp == nil {
		return "Interrupted."
	}
	var lines []string
	lines = append(lines, "**Task Execution Paused (Checkpoint Saved)**")
	if cp.ExecutionID != "" {
		lines = append(lines, fmt.Sprintf("• **Execution ID**: `%s`", cp.ExecutionID))
	}
	if cp.ToolCount > 0 {
		toolDesc := fmt.Sprintf("%d tool step(s) completed", cp.ToolCount)
		if cp.LastToolName != "" {
			toolDesc += fmt.Sprintf(" (last: `%s`)", cp.LastToolName)
		}
		lines = append(lines, "• **Progress**: "+toolDesc)
		if lastToolCmd != "" && lastToolCmd != cp.LastToolName {
			cmdPreview := lastToolCmd
			if len(cmdPreview) > 80 {
				cmdPreview = cmdPreview[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("• **Last Command**: `%s`", cmdPreview))
		}
	} else {
		lines = append(lines, "• **Progress**: Interrupted during model output generation")
	}
	lines = append(lines, "• **Actions**:")
	lines = append(lines, "  - Type `/continue` to continue execution from this checkpoint")
	lines = append(lines, "  - Type `/discard` to abandon this execution checkpoint")
	return strings.Join(lines, "\n")
}

// archivePlan persists the raw markdown content of an approved plan to .luneagent/plans/.
func (m *tuiModel) archivePlan(planText string) string {
	if m.workspace == "" || planText == "" {
		return ""
	}
	plansDir := filepath.Join(m.workspace, ".luneagent", "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		return ""
	}
	filename := fmt.Sprintf("plan-%s.md", time.Now().Format("20060102-150405"))
	targetPath := filepath.Join(plansDir, filename)
	if err := os.WriteFile(targetPath, []byte(planText), 0644); err != nil {
		return ""
	}
	return targetPath
}
