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
	if !m.run.running || m.runtime == nil || m.run.runningExecution == "" {
		return m, nil
	}

	if m.run.currentReasoningText != "" {
		dur := time.Since(m.run.reasoningStartTime)
		m.entries = append(m.entries, TranscriptEntry{
			Kind:              KindReasoning,
			Text:              m.run.currentReasoningText,
			ReasoningDuration: dur,
			ReasoningExpanded: false,
			Timestamp:         time.Now(),
		})
		m.run.resetReasoning()
		m.run.reasoningStartTime = time.Time{}
		m.view.invalidateBlocks()
	}
	if m.run.currentStreamText != "" {
		m.entries = append(m.entries, TranscriptEntry{
			ID:        fmt.Sprintf("asst-%d", time.Now().UnixNano()),
			Kind:      KindAssistant,
			Text:      m.run.currentStreamText,
			Timestamp: time.Now(),
		})
		m.run.resetStream()
		m.view.invalidateBlocks()
	}

	execID := m.run.runningExecution
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
	m.run.running = false
	m.run.thinking = false

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

// archivePlan persists the raw markdown content of an approved plan to
// .luneagent/plans/ and returns the archived path.
func archivePlan(workspace, planText string) (string, error) {
	if workspace == "" || planText == "" {
		return "", nil
	}
	plansDir := filepath.Join(workspace, ".luneagent", "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("plan-%s.md", time.Now().Format("20060102-150405"))
	targetPath := filepath.Join(plansDir, filename)
	if err := os.WriteFile(targetPath, []byte(planText), 0644); err != nil {
		return "", err
	}
	return targetPath, nil
}

// planArchiveCmd archives the approved plan in the background so the write never
// blocks the update goroutine.
func planArchiveCmd(workspace, planText string) tea.Cmd {
	if workspace == "" || planText == "" {
		return nil
	}
	return func() tea.Msg {
		path, err := archivePlan(workspace, planText)
		return planArchivedMsg{path: path, err: err}
	}
}

// handlePlanArchived reports an accepted plan archive.
func (m *tuiModel) handlePlanArchived(msg planArchivedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addSystemEntry("Could not archive the plan: " + msg.err.Error())
	} else if msg.path != "" {
		m.addSystemEntry(fmt.Sprintf("Archived plan: `%s`.", msg.path))
	}
	m.updateLayout()
	return m, nil
}
