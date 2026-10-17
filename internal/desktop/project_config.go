package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	core "github.com/afterlune/luneCore"
)

type WorkspaceConfigView struct {
	ContextWindow         int64                 `json:"contextWindow"`
	AutoCompact           bool                  `json:"autoCompact"`
	AutoCompactThreshold  float64               `json:"autoCompactThreshold"`
	ReserveOutputTokens   int64                 `json:"reserveOutputTokens"`
	MaxAttachmentFiles    int                   `json:"maxAttachmentFiles"`
	MaxAttachmentBytes    int64                 `json:"maxAttachmentBytes"`
	MaxTotalBytes         int64                 `json:"maxTotalBytes"`
	CommandToolchainPaths []string              `json:"commandToolchainPaths"`
	MCPServers            []MCPServerConfigView `json:"mcpServers"`
}

type MCPServerConfigView struct {
	Name              string               `json:"name"`
	Transport         string               `json:"transport"`
	Command           string               `json:"command"`
	Args              []string             `json:"args"`
	URL               string               `json:"url"`
	ClientMetadataURL string               `json:"clientMetadataURL"`
	Env               []MCPEnvironmentView `json:"env"`
}

type MCPEnvironmentView struct {
	Name     string `json:"name"`
	HasValue bool   `json:"hasValue"`
}

type WorkspaceConfigInput struct {
	ContextWindow         int64                  `json:"contextWindow"`
	AutoCompact           bool                   `json:"autoCompact"`
	AutoCompactThreshold  float64                `json:"autoCompactThreshold"`
	ReserveOutputTokens   int64                  `json:"reserveOutputTokens"`
	MaxAttachmentFiles    int                    `json:"maxAttachmentFiles"`
	MaxAttachmentBytes    int64                  `json:"maxAttachmentBytes"`
	MaxTotalBytes         int64                  `json:"maxTotalBytes"`
	CommandToolchainPaths []string               `json:"commandToolchainPaths"`
	MCPServers            []MCPServerConfigInput `json:"mcpServers"`
}

type MCPServerConfigInput struct {
	Name              string                `json:"name"`
	OriginalName      string                `json:"originalName,omitempty"`
	Transport         string                `json:"transport"`
	Command           string                `json:"command"`
	Args              []string              `json:"args"`
	URL               string                `json:"url"`
	ClientMetadataURL string                `json:"clientMetadataURL"`
	Env               []MCPEnvironmentInput `json:"env"`
}

type MCPEnvironmentInput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func workspaceConfigView(config core.ProjectConfig, commandToolchainPaths []string) WorkspaceConfigView {
	view := WorkspaceConfigView{
		ContextWindow: config.ContextWindow, AutoCompact: config.AutoCompact,
		AutoCompactThreshold: config.AutoCompactThreshold, ReserveOutputTokens: config.ReserveOutputTokens,
		MaxAttachmentFiles: config.Attachments.MaxFiles, MaxAttachmentBytes: config.Attachments.MaxFileBytes,
		MaxTotalBytes: config.Attachments.MaxTotalBytes, CommandToolchainPaths: append([]string(nil), commandToolchainPaths...),
	}
	serverNames := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)
	for _, name := range serverNames {
		server := config.MCPServers[name]
		item := MCPServerConfigView{
			Name: name, Transport: string(server.Transport), Command: server.Command,
			Args: append([]string(nil), server.Args...), URL: server.URL, ClientMetadataURL: server.ClientMetadataURL,
		}
		envNames := make([]string, 0, len(server.Env))
		for envName := range server.Env {
			envNames = append(envNames, envName)
		}
		sort.Strings(envNames)
		for _, envName := range envNames {
			item.Env = append(item.Env, MCPEnvironmentView{Name: envName, HasValue: server.Env[envName] != ""})
		}
		view.MCPServers = append(view.MCPServers, item)
	}
	return view
}

func mergeWorkspaceConfig(current core.ProjectConfig, input WorkspaceConfigInput) (core.ProjectConfig, error) {
	config := current
	config.ContextWindow = input.ContextWindow
	config.AutoCompact = input.AutoCompact
	config.AutoCompactThreshold = input.AutoCompactThreshold
	if config.AutoCompact && config.AutoCompactThreshold == 0 {
		config.AutoCompactThreshold = core.DefaultAutoCompactThreshold
	}
	config.ReserveOutputTokens = input.ReserveOutputTokens
	config.Attachments.MaxFiles = input.MaxAttachmentFiles
	config.Attachments.MaxFileBytes = input.MaxAttachmentBytes
	config.Attachments.MaxTotalBytes = input.MaxTotalBytes
	config.MCPServers = make(map[string]core.MCPServerConfig, len(input.MCPServers))
	for _, item := range input.MCPServers {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return core.ProjectConfig{}, errors.New("MCP 服务名称不能为空")
		}
		if _, exists := config.MCPServers[name]; exists {
			return core.ProjectConfig{}, fmt.Errorf("MCP 服务名称重复: %s", name)
		}
		oldName := strings.TrimSpace(item.OriginalName)
		if oldName == "" {
			oldName = name
		}
		old := current.MCPServers[oldName]
		server := core.MCPServerConfig{
			Transport: core.MCPTransport(strings.TrimSpace(item.Transport)),
			Command:   item.Command, Args: append([]string(nil), item.Args...),
			URL: item.URL, ClientMetadataURL: item.ClientMetadataURL,
		}
		for _, env := range item.Env {
			envName := strings.TrimSpace(env.Name)
			if envName == "" {
				return core.ProjectConfig{}, fmt.Errorf("MCP 服务 %q 的环境变量名称不能为空", name)
			}
			if server.Env == nil {
				server.Env = make(map[string]string)
			}
			if _, exists := server.Env[envName]; exists {
				return core.ProjectConfig{}, fmt.Errorf("MCP 服务 %q 的环境变量重复: %s", name, envName)
			}
			value := env.Value
			if value == "" {
				value = old.Env[envName]
			}
			server.Env[envName] = value
		}
		config.MCPServers[name] = server
	}
	if config.MCPServers == nil {
		config.MCPServers = map[string]core.MCPServerConfig{}
	}
	if err := config.Validate(); err != nil {
		return core.ProjectConfig{}, fmt.Errorf("Desktop 项目配置无效: %w", err)
	}
	return config, nil
}

func (s *DesktopService) GetWorkspaceConfig() (WorkspaceConfigView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localCore == nil {
		return WorkspaceConfigView{}, errors.New("请先打开工作区")
	}
	return workspaceConfigView(s.projectConfig, s.commandToolchainPaths), nil
}

// SaveWorkspaceConfig persists Desktop-owned project settings and restarts all
// Local and Worktree Cores while preserving the selected session.
func (s *DesktopService) SaveWorkspaceConfig(input WorkspaceConfigInput) (WorkspaceConfigView, error) {
	s.mu.Lock()
	if s.reconfiguring {
		s.mu.Unlock()
		return WorkspaceConfigView{}, errors.New("项目设置正在应用")
	}
	if s.localCore == nil || strings.TrimSpace(s.workspace) == "" {
		s.mu.Unlock()
		return WorkspaceConfigView{}, errors.New("请先打开工作区")
	}
	if s.mcpOperations > 0 {
		s.mu.Unlock()
		return WorkspaceConfigView{}, errors.New("请等待 MCP 操作完成后再修改项目设置")
	}
	current := s.projectConfig
	config, err := mergeWorkspaceConfig(current, input)
	if err != nil {
		s.mu.Unlock()
		return WorkspaceConfigView{}, err
	}
	commandToolchainPaths, err := normalizeCommandToolchainPaths(input.CommandToolchainPaths)
	if err != nil {
		s.mu.Unlock()
		return WorkspaceConfigView{}, err
	}
	workspace, activeSession := s.workspace, s.sessionID
	oldStoredConfig, found, err := s.store.LoadWorkspaceConfig(workspace)
	if err != nil {
		s.mu.Unlock()
		return WorkspaceConfigView{}, err
	}
	oldStoredConfig.ModelConfig = current.ModelConfig
	oldStoredPaths, pathsFound, err := s.store.LoadWorkspaceCommandToolchainPaths(workspace)
	if err != nil {
		s.mu.Unlock()
		return WorkspaceConfigView{}, err
	}
	for _, sessionContext := range s.sessionContexts {
		if sessionContext == nil || sessionContext.runtime == nil {
			continue
		}
		state, stateErr := sessionContext.runtime.State(context.Background())
		if stateErr != nil {
			s.mu.Unlock()
			return WorkspaceConfigView{}, stateErr
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused {
			s.mu.Unlock()
			return WorkspaceConfigView{}, errors.New("请等待所有会话任务结束后再修改项目设置")
		}
		pending, pendingErr := sessionContext.runtime.PendingRequests(context.Background())
		if pendingErr != nil {
			s.mu.Unlock()
			return WorkspaceConfigView{}, pendingErr
		}
		if len(pending) != 0 {
			s.mu.Unlock()
			return WorkspaceConfigView{}, errors.New("请先处理所有会话中的待处理请求，再修改项目设置")
		}
	}
	if err := s.closeWorkspaceLocked(false); err != nil {
		s.mu.Unlock()
		return WorkspaceConfigView{}, err
	}
	s.reconfiguring = true
	s.mu.Unlock()

	reopen := func(configFound bool, previous core.ProjectConfig, previousPaths []string, previousPathsFound bool) error {
		if rollbackErr := s.store.RestoreWorkspaceSettings(workspace, previous, configFound, previousPaths, previousPathsFound); rollbackErr != nil {
			return rollbackErr
		}
		if startErr := s.startWorkspace(workspace); startErr != nil {
			return startErr
		}
		if activeSession != "" {
			if _, openErr := s.openSession(activeSession, true); openErr != nil {
				return openErr
			}
		}
		return nil
	}
	finish := func() {
		s.mu.Lock()
		s.reconfiguring = false
		s.mu.Unlock()
	}

	if err := s.store.SaveWorkspaceSettings(workspace, config, commandToolchainPaths); err != nil {
		rollbackErr := reopen(found, oldStoredConfig, oldStoredPaths, pathsFound)
		finish()
		return WorkspaceConfigView{}, errors.Join(err, rollbackErr)
	}
	if err := s.startWorkspace(workspace); err != nil {
		rollbackErr := reopen(found, oldStoredConfig, oldStoredPaths, pathsFound)
		finish()
		return WorkspaceConfigView{}, errors.Join(err, rollbackErr)
	}
	if activeSession != "" {
		if _, err := s.openSession(activeSession, true); err != nil {
			s.mu.Lock()
			closeErr := s.closeWorkspaceLocked(false)
			s.mu.Unlock()
			rollbackErr := errors.Join(closeErr, reopen(found, oldStoredConfig, oldStoredPaths, pathsFound))
			finish()
			return WorkspaceConfigView{}, errors.Join(err, rollbackErr)
		}
	}
	finish()
	return workspaceConfigView(config, commandToolchainPaths), nil
}
