package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/afterlune/luneCore"
)

type tuiTeamActivity struct {
	teamRunID string
	agentID   string
	name      string
	profile   string
	provider  string
	model     string
	status    string
	message   string
}

func (m *tuiModel) updateTeamActivity(event core.Event) {
	if event.SessionID != m.sessionID || event.AgentID == "" || event.AgentRole != "worker" {
		return
	}
	teamRunID := event.TeamRunID
	if teamRunID == "" {
		teamRunID = "agents:" + event.ExecutionID
	}
	if teamRunID == "agents:" {
		teamRunID = "agents:session"
	}
	index := -1
	for current := range m.teamActivities {
		activity := &m.teamActivities[current]
		if activity.teamRunID == teamRunID && activity.agentID == event.AgentID {
			index = current
			break
		}
	}
	if index < 0 {
		m.teamActivities = append(m.teamActivities, tuiTeamActivity{teamRunID: teamRunID, agentID: event.AgentID})
		index = len(m.teamActivities) - 1
	}
	activity := &m.teamActivities[index]
	if event.AgentName != "" {
		activity.name = event.AgentName
	}
	if event.ModelProfile != "" {
		activity.profile = event.ModelProfile
	}
	if event.Provider != "" {
		activity.provider = event.Provider
	}
	if event.Model != "" {
		activity.model = event.Model
	}
	activity.status = teamActivityStatus(event)
	activity.message = cleanInlineText(event.Err)
	if len([]rune(activity.message)) > 120 {
		activity.message = string([]rune(activity.message)[:117]) + "..."
	}
	if len(m.teamActivities) > 200 {
		m.teamActivities = append([]tuiTeamActivity(nil), m.teamActivities[len(m.teamActivities)-200:]...)
	}
}

func teamActivityStatus(event core.Event) string {
	if event.Kind == core.EventAgentStarted {
		return "running"
	}
	switch event.Status {
	case core.TaskCompleted:
		return "done"
	case core.TaskFailed:
		return "failed"
	case core.TaskPaused:
		return "paused"
	default:
		return string(event.Status)
	}
}

func (m *tuiModel) teamActivityDetails(colors themeColors) string {
	if len(m.teamActivities) == 0 {
		return ""
	}
	var details strings.Builder
	details.WriteString("Team members")
	groups := make(map[string]int)
	for _, activity := range m.teamActivities {
		group, exists := groups[activity.teamRunID]
		if !exists {
			group = len(groups) + 1
			groups[activity.teamRunID] = group
			fmt.Fprintf(&details, "\n  Team %d", group)
		}
		name := activity.name
		if name == "" {
			name = activity.agentID
		}
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color(teamActivityColor(activity.status, colors))).Render(activityBadge(activity.status))
		fmt.Fprintf(&details, "\n    %s %s · %s", icon, name, activity.status)
		if activity.profile != "" && activity.profile != activity.agentID {
			details.WriteString(" · profile=" + activity.profile)
		}
		model := activity.model
		if activity.provider != "" {
			if model == "" {
				model = activity.provider
			} else {
				model = activity.provider + "/" + model
			}
		}
		if model != "" {
			details.WriteString(" · " + model)
		}
		if activity.message != "" {
			details.WriteString(" · " + activity.message)
		}
	}
	return details.String()
}

func teamStatusGroup(activities []tuiTeamActivity) string {
	for index := len(activities) - 1; index >= 0; index-- {
		if activities[index].status == "running" && activities[index].teamRunID != "" {
			return activities[index].teamRunID
		}
	}
	for index := len(activities) - 1; index >= 0; index-- {
		if activities[index].teamRunID != "" {
			return activities[index].teamRunID
		}
	}
	return ""
}

func (m *tuiModel) teamStatusLabel() string {
	group := teamStatusGroup(m.teamActivities)
	if group == "" {
		return ""
	}
	members := make(map[string]string)
	for _, activity := range m.teamActivities {
		if activity.teamRunID == group && activity.agentID != "" {
			members[activity.agentID] = activity.status
		}
	}
	active := 0
	for _, status := range members {
		if status == "running" {
			active++
		}
	}
	if strings.HasPrefix(group, "agents:") {
		if active == 1 {
			return "agents: 1 running"
		}
		if active > 1 {
			return fmt.Sprintf("agents: %d running", active)
		}
		return fmt.Sprintf("agents: %d complete", len(members))
	}
	return fmt.Sprintf("team: %d (%d active)", len(members), active)
}

func (m *tuiModel) teamStatusColor(activeColor, idleColor string) string {
	group := teamStatusGroup(m.teamActivities)
	for _, activity := range m.teamActivities {
		if activity.teamRunID == group && activity.status == "running" {
			return activeColor
		}
	}
	return idleColor
}

func teamActivityColor(status string, colors themeColors) string {
	switch status {
	case "running":
		return colors.Accent
	case "done":
		return colors.Success
	case "failed":
		return colors.Error
	case "paused":
		return colors.Warning
	default:
		return colors.Muted
	}
}
