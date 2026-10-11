package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/afterlune/luneCore"
)

type agentUsageSummary struct {
	requests       int
	measured       int
	input          int64
	estimatedInput int64
	output         int64
	reasoning      int64
	total          int64
	cached         int64
}

func (m *tuiModel) usageByAgentDetails() string {
	if len(m.agentUsage) == 0 && len(m.usageRequests) == 0 {
		if !m.agentUsageComplete && m.sessionUsage.requests > 0 {
			return "Recorded usage by agent · older requests may be missing\nNo retained requests are available to rebuild the breakdown."
		}
		return ""
	}
	summaries := m.agentUsage
	heading := "Session usage by agent"
	if len(summaries) == 0 {
		// Older clients and test fixtures may only provide the bounded request list.
		summaries = make(map[string]agentUsageSummary)
		for _, record := range m.usageRequests {
			summary := summaries[record.agentID]
			summary.requests++
			if record.available {
				summary.measured++
				summary.input += record.usage.InputTokens
				summary.output += record.usage.OutputTokens
				summary.reasoning += record.usage.OutputTokensDetails.ReasoningTokens
				summary.total += record.usage.TotalTokens
				summary.cached += record.usage.InputTokensDetails.CachedTokens
			}
			summary.estimatedInput += record.estimatedInputTokens
			summaries[record.agentID] = summary
		}
		heading = fmt.Sprintf("Recent usage by agent (%d requests)", len(m.usageRequests))
	} else if m.agentUsageComplete {
		requests := 0
		for _, summary := range summaries {
			requests += summary.requests
		}
		heading = fmt.Sprintf("Session usage by agent (all %d requests)", requests)
	} else {
		heading = "Recorded usage by agent · older requests may be missing"
	}
	agents := make([]string, 0, len(summaries))
	for agentID := range summaries {
		agents = append(agents, agentID)
	}
	sort.Strings(agents)
	var details strings.Builder
	details.WriteString(heading)
	for _, agentID := range agents {
		summary := summaries[agentID]
		label := agentID
		if label == "" {
			label = "Unattributed"
		}
		fmt.Fprintf(&details, "\n%s · %d request(s) · %d metered", label, summary.requests, summary.measured)
		if summary.estimatedInput > 0 {
			fmt.Fprintf(&details, " · est input ~%s", compactTokens(summary.estimatedInput))
		}
		if summary.measured == 0 {
			details.WriteString(" · usage unavailable")
			continue
		}
		fmt.Fprintf(&details, " · in %s · out %s · reason %s · total %s · cache %s (%s)",
			compactTokens(summary.input), compactTokens(summary.output), compactTokens(summary.reasoning),
			compactTokens(summary.total), compactTokens(summary.cached), cacheHitRate(summary.input, summary.cached))
	}
	return details.String()
}

func usageSummaryFromTotals(totals core.UsageTotals) agentUsageSummary {
	return agentUsageSummary{
		requests: totals.Requests, measured: totals.Measured, input: totals.Input, estimatedInput: totals.EstimatedInput, output: totals.Output,
		reasoning: totals.Reasoning, total: totals.Total, cached: totals.Cached,
	}
}

func contextUsageLabel(inputTokens, contextWindow int64, estimated bool) string {
	if contextWindow < 0 || inputTokens < 0 {
		return "ctx —"
	}
	prefix := "ctx "
	if estimated {
		prefix = "ctx ~"
	}
	if contextWindow == 0 {
		if inputTokens == 0 {
			return ""
		}
		return prefix + compactTokens(inputTokens) + "/unknown"
	}
	if inputTokens == 0 {
		return prefix + "—/" + compactTokens(contextWindow)
	}
	return fmt.Sprintf("%s%s/%s (%.0f%%)", prefix, compactTokens(inputTokens), compactTokens(contextWindow), float64(inputTokens)*100/float64(contextWindow))
}
