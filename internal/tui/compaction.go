package tui

import (
	"fmt"
	"strings"

	core "github.com/afterlune/luneCore"
)

// handleCompactionEvent keeps the footer honest while Core compacts the context:
// the session totals never change, the context budget switches to the compacted
// estimate as soon as Core reports it, and manual compaction markers survive until
// the next authoritative usage report.
func (m *tuiModel) handleCompactionEvent(details core.ContextCompactionDetails) {
	mode := details.Mode
	if mode == "" {
		mode = "local"
	}
	manual := details.Reason == "manual"
	switch details.Stage {
	case "started":
		m.usage.beginCompaction()
		main := m.ensureAgentMetrics("lead")
		main.Activity = "Compacting context"
	case "completed":
		m.usage.finishCompaction(details.AfterTokens)
		if m.run.running {
			m.ensureAgentMetrics("lead").Activity = "Thinking"
		}
		if !manual {
			m.addSystemEntry(formatCompactionCompleted(details, mode))
		}
	case "fallback":
		m.usage.finishCompaction(0)
		if !manual {
			message := "Provider compaction unavailable; using a local summary"
			if mode == "local" {
				message = "Local compaction did not reach its target; keeping the best summary"
			}
			if reason := sanitizeAndTruncateLine(strings.TrimSpace(details.Err), 160); reason != "" {
				message += ": " + strings.TrimSuffix(reason, ".")
			}
			m.addSystemEntry(message + ".")
		}
	case "skipped":
		m.usage.finishCompaction(0)
		if !manual && details.Err != "" {
			m.addSystemEntry("Automatic compaction skipped: " + details.Err)
		}
	case "failed":
		m.usage.finishCompaction(0)
		if !manual {
			if details.Err != "" {
				m.addSystemEntry("Context compaction failed: " + details.Err)
			} else {
				m.addSystemEntry("Context compaction failed.")
			}
		}
	default:
		return
	}
	m.updateLayout()
}

// formatCompactionCompleted describes a finished compaction without inventing
// numbers Core did not report (server compaction has no after-token count).
func formatCompactionCompleted(details core.ContextCompactionDetails, mode string) string {
	before, after := details.BeforeTokens, details.AfterTokens
	switch {
	case mode == "server" && before > 0:
		return fmt.Sprintf("Context compacted by the provider: was %s tokens.", formatCompactTokens(before))
	case mode == "server":
		return "Context compacted by the provider."
	case before > 0 && after > 0:
		return fmt.Sprintf("Context compacted: %s → %s tokens (local summary).",
			formatCompactTokens(before), formatCompactTokens(after))
	default:
		return "Context compacted (local summary)."
	}
}

func formatManualCompactionResult(result core.CompactionResult) string {
	if result.Backend == "server" {
		if result.Before > 0 {
			return fmt.Sprintf("Compacted %d older turns with provider compaction (previous input %s tokens).",
				result.Turns, formatCompactTokens(result.Before))
		}
		return fmt.Sprintf("Compacted %d older turns with provider compaction.", result.Turns)
	}
	message := fmt.Sprintf("Compacted %d older turns locally", result.Turns)
	if result.Before > 0 && result.After > 0 {
		message += fmt.Sprintf(": %s → %s tokens (local summary).",
			formatCompactTokens(result.Before), formatCompactTokens(result.After))
	} else {
		message += " (local summary)."
	}
	if result.Fallback {
		reason := sanitizeAndTruncateLine(strings.TrimSpace(result.FallbackReason), 160)
		if reason == "" {
			message += " Fallback was applied."
		} else {
			message += " Fallback: " + reason
		}
	}
	return message
}

// compactionSettingsLabel describes the project's auto-compaction policy in the
// diagnostics panels; it is display-only and never writes configuration.
func (m *tuiModel) compactionSettingsLabel() string {
	if m.client == nil {
		return ""
	}
	defaults := m.client.ProjectContextDefaults()
	if !defaults.AutoCompact {
		return "off"
	}
	ratio := defaults.AutoCompactThreshold
	if ratio <= 0 {
		ratio = core.DefaultAutoCompactThreshold
	}
	window := defaults.ContextWindow
	if window <= 0 {
		window = m.usage.contextWindow
	}
	threshold := compactionThresholdTokens(window, defaults.ReserveOutputTokens, ratio)
	return fmt.Sprintf("on at %.0f%% (%s tokens)", ratio*100, formatTokens(threshold))
}

// compactionThresholdTokens mirrors Core's auto-compaction threshold so the
// diagnostics panels can show when the next automatic pass will start.
func compactionThresholdTokens(window, reserve int64, ratio float64) int64 {
	usable := window - reserve
	if usable <= 0 || ratio <= 0 {
		return 0
	}
	threshold := int64(float64(usable) * ratio)
	if threshold < 1 {
		return 1
	}
	return threshold
}
