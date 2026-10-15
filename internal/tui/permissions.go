package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

type tuiPermissionOption struct {
	mode        core.PermissionMode
	label       string
	description string
}

var tuiPermissionOptions = []tuiPermissionOption{
	{mode: core.PermissionReadOnly, label: "Read-only", description: "Read workspace files; ask before web; block writes and shell"},
	{mode: core.PermissionWorkspace, label: "Workspace", description: "Workspace file operations and sandboxed commands; network needs plan approval"},
	{mode: core.PermissionAsk, label: "Ask", description: "Read workspace files; approve other tool operations in the plan"},
	{mode: core.PermissionFullAccess, label: "Full access", description: "Current-user files and network stay in the OS sandbox; high-risk commands need plan approval"},
}

var planReviewChoices = [...]string{
	"Implement in this session",
	"Implement in a new session",
	"Stay read-only",
}

func (m *tuiModel) acceptPlan() (tea.Model, tea.Cmd) {
	return m.implementProposedPlan(core.PlanImplementSameContext)
}

func (m *tuiModel) selectPlanReviewOption() (tea.Model, tea.Cmd) {
	switch min(max(m.planReviewIndex, 0), len(planReviewChoices)-1) {
	case 0:
		return m.acceptPlan()
	case 1:
		return m.implementProposedPlan(core.PlanImplementNewContext)
	default:
		m.keepPlanReadOnly()
		return m, nil
	}
}

func (m *tuiModel) keepPlanReadOnly() {
	if err := m.setPlanMode(true); err != nil {
		m.status = "Could not lock permissions to Read-only: " + err.Error()
		m.addEntry("error", m.status, true)
		return
	}
	m.planReview = false
	m.proposedPlan = nil
	m.planAuthorization = core.PlanAuthorization{}
	m.status = "Plan saved · Read-only permissions remain locked."
	m.updateLayout()
}

func (m *tuiModel) implementProposedPlan(mode core.PlanImplementationMode) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.proposedPlan == nil || m.planSourceExecutionID == "" {
		m.status = "There is no completed plan to implement."
		return m, nil
	}
	if m.planMode {
		if err := m.setPlanMode(false); err != nil {
			m.status = "Could not restore permissions before implementation: " + err.Error()
			m.addEntry("error", m.status, true)
			return m, nil
		}
	}
	runtime := m.runtime
	client := m.client
	sourceID := m.planSourceExecutionID
	authorization := m.planAuthorization
	m.planReview = false
	m.resetInput()
	m.running = true
	m.runtimeTask = true
	m.status = "Starting the approved plan…"
	implementationCmd := tea.Cmd(func() tea.Msg {
		implementation, err := runtime.ImplementPlan(context.Background(), sourceID, mode, authorization)
		if err != nil {
			return taskFinishedMsg{result: taskResult{err: err}}
		}
		waitRuntime := runtime
		if implementation.SessionID != runtime.SessionID() {
			waitRuntime, err = client.OpenSessionRuntime(context.Background(), implementation.SessionID)
			if err != nil {
				return taskFinishedMsg{result: taskResult{targetSessionID: implementation.SessionID, err: err}}
			}
		}
		result, err := waitRuntime.WaitTurn(context.Background(), implementation.Submission.ExecutionID)
		return taskFinishedMsg{result: taskResult{response: result.Response, coreResult: result, targetSessionID: implementation.SessionID, err: err}}
	})
	return m, tea.Batch(implementationCmd, m.startWindowTitleAnimation())
}

func (m *tuiModel) updatePlanAuthorization(line string) (tea.Model, tea.Cmd, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "/plan" || !m.planReview {
		return m, nil, false
	}
	switch {
	case len(fields) == 2 && fields[1] == "implement":
		model, command := m.implementProposedPlan(core.PlanImplementSameContext)
		return model, command, true
	case len(fields) == 3 && fields[1] == "implement" && fields[2] == "new":
		model, command := m.implementProposedPlan(core.PlanImplementNewContext)
		return model, command, true
	case len(fields) == 3 && fields[1] == "implement" && fields[2] == "same":
		model, command := m.implementProposedPlan(core.PlanImplementSameContext)
		return model, command, true
	case len(fields) == 2 && fields[1] == "stay":
		m.keepPlanReadOnly()
	case len(fields) == 2 && fields[1] == "allow-network":
		m.planAuthorization.AllowNetwork = !m.planAuthorization.AllowNetwork
		m.status = fmt.Sprintf("Plan network access: %t.", m.planAuthorization.AllowNetwork)
	case len(fields) == 3 && fields[1] == "allow-tool":
		m.planAuthorization.AllowedTools = appendUnique(m.planAuthorization.AllowedTools, fields[2])
		m.status = "Allowed tool added to this plan."
	case len(fields) >= 3 && fields[1] == "allow-command":
		command := strings.TrimSpace(strings.TrimPrefix(line, "/plan allow-command"))
		m.planAuthorization.ApprovedHighRiskCommands = appendUnique(m.planAuthorization.ApprovedHighRiskCommands, command)
		m.status = "Exact high-risk command added to this plan."
	case len(fields) >= 3 && fields[1] == "allow-path":
		path := fields[2]
		if !filepath.IsAbs(path) {
			m.status = "Approved paths must be absolute."
			return m, nil, true
		}
		access := publictool.PathReadWrite
		if len(fields) >= 4 && fields[3] == "read" {
			access = publictool.PathRead
		} else if len(fields) >= 4 && fields[3] != "read_write" {
			m.status = "Path access must be read or read_write."
			return m, nil, true
		}
		m.planAuthorization.Paths = append(m.planAuthorization.Paths, publictool.PathGrant{Path: path, Access: access})
		m.status = "Path grant added to this plan."
	default:
		m.status = "Plan options: /plan allow-network, allow-path <absolute-path> [read|read_write], allow-tool <name>, allow-command <exact>, implement [same|new], stay."
	}
	m.resetInput()
	return m, nil, true
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (m *tuiModel) togglePlanMode() (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Plan mode can only be changed while idle."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	enable := !m.planMode
	if err := m.setPlanMode(enable); err != nil {
		m.status = "Could not change Plan mode: " + err.Error()
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if enable {
		m.status = "Ready"
		m.addEntry("notice", "Plan mode enabled · Read-only permissions locked.", false)
	} else {
		m.status = "Ready"
		m.addEntry("notice", "Plan mode off · "+permissionModeLabel(m.currentPermissionMode())+" permissions restored.", false)
	}
	m.resetInput()
	return m, nil
}

func (m *tuiModel) setPlanMode(enabled bool) error {
	if m.client == nil {
		return fmt.Errorf("permission settings are unavailable")
	}
	if enabled {
		if !m.planMode {
			m.permissionModeBeforePlan = m.currentPermissionMode()
			if m.permissionModeBeforePlan == "" {
				m.permissionModeBeforePlan = m.client.PermissionMode()
			}
		} else if m.permissionModeBeforePlan == "" {
			m.permissionModeBeforePlan = m.currentPermissionMode()
		}
		if err := m.client.SetPermissionMode(core.PermissionReadOnly); err != nil {
			return err
		}
		m.permissionMode = core.PermissionReadOnly
		m.planMode = true
		return nil
	}
	if !m.planMode {
		return nil
	}
	restore := m.permissionModeBeforePlan
	if restore == "" {
		restore = core.PermissionReadOnly
	}
	if err := m.client.SetPermissionMode(restore); err != nil {
		return err
	}
	m.permissionMode = restore
	m.permissionModeBeforePlan = ""
	m.planMode = false
	return nil
}

func (m *tuiModel) openPermissionMenu() (tea.Model, tea.Cmd) {
	if m.planMode {
		m.permissionMenu = false
		m.status = "Permissions are locked to Read-only while Plan mode is active."
		m.resetInput()
		return m, nil
	}
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionIndex = 0
	for index, option := range tuiPermissionOptions {
		if option.mode == m.currentPermissionMode() {
			m.permissionIndex = index
			break
		}
	}
	m.permissionMenu = true
	m.resetInput()
	m.status = "Choose a permission mode; Enter applies it, Esc cancels."
	m.refreshPanels()
	return m, nil
}

func (m *tuiModel) cyclePermissionMode() (tea.Model, tea.Cmd) {
	if m.planMode {
		m.status = "Ready"
		return m, nil
	}
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	currentIndex := -1
	for index, option := range tuiPermissionOptions {
		if option.mode == m.currentPermissionMode() {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		currentIndex = 0
	}
	next := tuiPermissionOptions[(currentIndex+1)%len(tuiPermissionOptions)]
	return m.changePermissionMode(next.mode, false)
}

func (m *tuiModel) selectPermissionMode() (tea.Model, tea.Cmd) {
	if m.permissionIndex < 0 || m.permissionIndex >= len(tuiPermissionOptions) {
		m.permissionMenu = false
		return m, nil
	}
	option := tuiPermissionOptions[m.permissionIndex]
	return m.applyPermissionMode(option.mode)
}

func (m *tuiModel) setPermissionModeFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Usage: /permissions [read-only|workspace|ask|full-access]"
		m.addEntry("error", m.status, true)
		return m, nil
	}
	return m.applyPermissionMode(core.PermissionMode(fields[1]))
}

func (m *tuiModel) applyPermissionMode(mode core.PermissionMode) (tea.Model, tea.Cmd) {
	return m.changePermissionMode(mode, true)
}

func (m *tuiModel) changePermissionMode(mode core.PermissionMode, resetInput bool) (tea.Model, tea.Cmd) {
	if m.planMode && mode != core.PermissionReadOnly {
		m.status = "Permissions are locked to Read-only while Plan mode is active."
		return m, nil
	}
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	valid := false
	for _, option := range tuiPermissionOptions {
		if option.mode == mode {
			valid = true
			break
		}
	}
	if !valid {
		m.status = "Unknown permission mode."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if err := m.client.SetPermissionMode(mode); err != nil {
		m.status = err.Error()
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionMode = mode
	m.permissionMenu = false
	if resetInput {
		m.resetInput()
	} else {
		m.updateCommandSuggestions()
		m.updateLayout()
	}
	// The active mode is already visible in the footer metadata. Keep the
	// keyboard hints stable and avoid adding every Shift+Tab change to chat.
	m.status = "Ready"
	m.refreshPanels()
	return m, nil
}

func (m *tuiModel) currentPermissionMode() core.PermissionMode {
	if m.permissionMode != "" {
		return m.permissionMode
	}
	if m.client != nil {
		return m.client.PermissionMode()
	}
	return core.PermissionReadOnly
}

func permissionModeLabel(mode core.PermissionMode) string {
	for _, option := range tuiPermissionOptions {
		if option.mode == mode {
			return option.label
		}
	}
	return string(mode)
}

func (m *tuiModel) resolveToolApproval(approved bool) {
	if m.pendingApproval == nil {
		return
	}
	request := m.pendingApproval
	m.pendingApproval = nil
	if m.runtime != nil {
		if err := m.runtime.ResolveApproval(request.request.ID, approved); err != nil {
			m.status = "Could not resolve tool approval: " + err.Error()
			m.addEntry("error", m.status, true)
			return
		}
	}
	if approved {
		m.status = "Tool call approved once."
		m.addEntry("notice", "Approved this tool call once.", false)
	} else {
		m.status = "Tool call denied."
		m.addEntry("notice", "Denied this tool call.", false)
	}
}

func formatToolApproval(request core.ApprovalRequest, workspace ...string) string {
	root := ""
	if len(workspace) > 0 {
		root = workspace[0]
	}
	return formatToolApprovalWithKeys(request, root, "Y", "N")
}

func formatToolApprovalWithKeys(request core.ApprovalRequest, workspace, approveKey, denyKey string) string {
	display := humanizedToolSummary(request.Tool, request.Arguments, workspace)
	warning := ""
	details := ""
	if request.Tool == "command" {
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(request.Arguments, &args)
		details = "\nCommand: " + cleanInlineText(args.Command)
		warning = "\nApproved shell commands run as your user and can access files outside the workspace."
	}
	return fmt.Sprintf("Allow %s once?%s%s\nPress %s to approve once, %s to deny, or Esc to interrupt the task.", display, details, warning, approveKey, denyKey)
}
