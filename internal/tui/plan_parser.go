package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// matchChecklist reports whether a line is a markdown checklist item (- [ ], * [ ], etc.).
func matchChecklist(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "[ ]") || strings.Contains(lower, "[x]")
}

// isChecklistCompleted reports whether a checklist item is marked done ([x] or [X]).
func isChecklistCompleted(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "[x]")
}

// extractChecklistTitle extracts the text following [ ] or [x].
func extractChecklistTitle(line string) string {
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "[ ]")
	if idx < 0 {
		idx = strings.Index(lower, "[x]")
	}
	if idx < 0 {
		return ""
	}
	content := strings.TrimSpace(line[idx+3:])
	if matchNumberedItem(content) {
		content = extractNumberedTitle(content)
	}
	return strings.TrimSpace(content)
}

// matchNumberedItem checks if line starts with digits followed by dot/parenthesis, e.g. "1. " or "1) ".
func matchNumberedItem(line string) bool {
	idx := strings.IndexAny(line, ".):、")
	if idx <= 0 || idx > 4 {
		return false
	}
	numStr := strings.TrimSpace(line[:idx])
	_, err := strconv.Atoi(numStr)
	if err != nil {
		return false
	}
	rest := strings.TrimSpace(line[idx+1:])
	return rest != ""
}

// extractNumberedTitle strips the leading number and delimiter from a numbered list item.
func extractNumberedTitle(line string) string {
	idx := strings.IndexAny(line, ".):、")
	if idx <= 0 || idx >= len(line)-1 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(line[idx+1:])
}

// ParsePlanSteps parses markdown proposed plans into a structured sequence of PlanStep items.
func ParsePlanSteps(planText string) []PlanStep {
	lines := strings.Split(planText, "\n")
	var steps []PlanStep

	// Pass 1: Look for explicit markdown checklist items: [ ] or [x]
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if matchChecklist(line) {
			status := PlanStepPending
			if isChecklistCompleted(line) {
				status = PlanStepCompleted
			}
			title := extractChecklistTitle(line)
			if title != "" {
				steps = append(steps, PlanStep{
					Index:  len(steps) + 1,
					Title:  title,
					Status: status,
				})
			}
		}
	}
	if len(steps) > 0 {
		return steps
	}

	// Pass 2: Look for numbered list items: "1. ", "2. ", etc.
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if matchNumberedItem(line) {
			title := extractNumberedTitle(line)
			if title != "" {
				steps = append(steps, PlanStep{
					Index:  len(steps) + 1,
					Title:  title,
					Status: PlanStepPending,
				})
			}
		}
	}
	if len(steps) > 0 {
		return steps
	}

	// Pass 3: Look for bullet points (- or *) or heading steps (## Step 1)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "#") && (strings.Contains(lower, "step") || strings.Contains(lower, "阶段") || strings.Contains(lower, "步骤")) {
			title := strings.TrimLeft(line, "# \t")
			if title != "" {
				steps = append(steps, PlanStep{
					Index:  len(steps) + 1,
					Title:  title,
					Status: PlanStepPending,
				})
			}
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			title := strings.TrimSpace(line[2:])
			if title != "" {
				steps = append(steps, PlanStep{
					Index:  len(steps) + 1,
					Title:  title,
					Status: PlanStepPending,
				})
			}
		}
	}
	if len(steps) > 0 {
		return steps
	}

	// Fallback: Use single summary step
	firstLine := ""
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line != "" && !strings.HasPrefix(line, "#") {
			firstLine = line
			break
		}
	}
	if firstLine == "" {
		firstLine = "Execute proposed plan"
	}
	if len(firstLine) > 60 {
		firstLine = firstLine[:57] + "..."
	}
	return []PlanStep{
		{
			Index:  1,
			Title:  firstLine,
			Status: PlanStepPending,
		},
	}
}

// AdvanceToStep transitions all steps before stepIdx to completed and sets stepIdx to in-progress.
func (p *ActivePlanExecution) AdvanceToStep(stepIdx int) {
	if p == nil || stepIdx < 0 || stepIdx >= len(p.Steps) {
		return
	}
	for i := 0; i < stepIdx; i++ {
		p.Steps[i].Status = PlanStepCompleted
	}
	p.Steps[stepIdx].Status = PlanStepInProgress
	p.CurrentStepIndex = stepIdx
}

// CheckTextForAdvance inspects streamed text from the model and advances active step if a step marker is found.
func (p *ActivePlanExecution) CheckTextForAdvance(text string) bool {
	if p == nil || p.Completed || len(p.Steps) <= 1 {
		return false
	}
	lower := strings.ToLower(text)
	for i := p.CurrentStepIndex + 1; i < len(p.Steps); i++ {
		stepNum := p.Steps[i].Index
		patterns := []string{
			fmt.Sprintf("step %d", stepNum),
			fmt.Sprintf("step %d:", stepNum),
			fmt.Sprintf("step %d.", stepNum),
			fmt.Sprintf("阶段 %d", stepNum),
			fmt.Sprintf("步骤 %d", stepNum),
			fmt.Sprintf("第%d步", stepNum),
		}
		matched := false
		for _, pat := range patterns {
			if strings.Contains(lower, pat) {
				matched = true
				break
			}
		}
		if !matched && len(p.Steps[i].Title) >= 8 {
			titleLower := strings.ToLower(p.Steps[i].Title)
			if strings.Contains(lower, titleLower) {
				matched = true
			}
		}
		if matched {
			p.AdvanceToStep(i)
			return true
		}
	}
	return false
}

// MarkAllCompleted marks all steps as completed and marks the plan execution finished.
func (p *ActivePlanExecution) MarkAllCompleted() {
	if p == nil {
		return
	}
	for i := range p.Steps {
		p.Steps[i].Status = PlanStepCompleted
	}
	p.Completed = true
}
