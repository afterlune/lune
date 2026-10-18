package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type Bootstrap struct {
	Theme         string                     `json:"theme"`
	Appearance    AppearanceView             `json:"appearance"`
	Recent        []settings.RecentWorkspace `json:"recentWorkspaces"`
	Models        ModelSettingsView          `json:"models"`
	Workspace     string                     `json:"workspace"`
	SessionID     string                     `json:"sessionId"`
	LastWorkspace string                     `json:"lastWorkspace,omitempty"`
	LastSessionID string                     `json:"lastSessionId,omitempty"`
	Controls      SessionControlsView        `json:"controls"`
}

type AppearanceView struct {
	Theme         string `json:"theme"`
	WallpaperType string `json:"wallpaperType"`
	WallpaperFile string `json:"wallpaperFile"`
	WallpaperName string `json:"wallpaperName"`
	WallpaperURL  string `json:"wallpaperURL"`
	PanelOpacity  int    `json:"panelOpacity"`
}

type AppearanceSettingsInput struct {
	Theme         string `json:"theme"`
	WallpaperType string `json:"wallpaperType"`
	WallpaperFile string `json:"wallpaperFile"`
	WallpaperName string `json:"wallpaperName"`
	PanelOpacity  int    `json:"panelOpacity"`
}

type WallpaperSelection struct {
	File string `json:"file"`
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

type ProviderView struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	BaseURL          string `json:"baseURL"`
	HasAPIKey        bool   `json:"hasAPIKey"`
	ServerCompaction bool   `json:"serverCompaction"`
}

type ModelProfileView struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	MaxOutputTokens    int64  `json:"maxOutputTokens"`
	SupportsInputAudio bool   `json:"supportsInputAudio"`
}

type ModelSettingsView struct {
	Providers         []ProviderView     `json:"providers"`
	Models            []ModelProfileView `json:"profiles"`
	DefaultModel      string             `json:"defaultModel"`
	MaxParallelAgents int                `json:"maxParallelAgents"`
	Configured        bool               `json:"configured"`
}

type ProviderInput struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	BaseURL          string `json:"baseURL"`
	APIKey           string `json:"apiKey"`
	ServerCompaction bool   `json:"serverCompaction"`
}

type ModelProfileInput struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	MaxOutputTokens    int64  `json:"maxOutputTokens"`
	SupportsInputAudio bool   `json:"supportsInputAudio"`
}

type ModelSettingsInput struct {
	Providers         []ProviderInput     `json:"providers"`
	Models            []ModelProfileInput `json:"profiles"`
	DefaultModel      string              `json:"defaultModel"`
	MaxParallelAgents int                 `json:"maxParallelAgents"`
}

func (s *DesktopService) Bootstrap() (Bootstrap, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	appearance, err := s.store.Appearance()
	if err != nil {
		return Bootstrap{}, err
	}
	recent, err := s.store.RecentWorkspaces()
	if err != nil {
		return Bootstrap{}, err
	}
	modelConfig, err := s.store.LoadModelConfig()
	if err != nil {
		return Bootstrap{}, err
	}
	restore := settings.DesktopRestore{}
	if s.store != nil {
		if loaded, loadErr := s.store.DesktopRestore(); loadErr == nil {
			restore = loaded
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Bootstrap{
		Theme: appearance.Theme, Appearance: appearanceView(appearance), Recent: recent,
		Models: modelSettingsView(modelConfig), Workspace: s.workspace, SessionID: s.activeSessionIDLocked(),
		LastWorkspace: restore.Workspace, LastSessionID: restore.SessionID,
	}
	if s.activeCoreLocked() != nil {
		if s.activeContext != nil {
			result.Controls = sessionControls(s.activeContext)
		}
	}
	return result, nil
}

func modelSettingsView(config publicmodel.Config) ModelSettingsView {
	view := ModelSettingsView{DefaultModel: config.DefaultModel, MaxParallelAgents: config.MaxParallelAgents, Configured: len(config.Providers) > 0 && len(config.Models) > 0}
	for name, provider := range config.Providers {
		view.Providers = append(view.Providers, ProviderView{
			Name: name, Protocol: provider.Protocol, BaseURL: provider.BaseURL,
			HasAPIKey: strings.TrimSpace(provider.APIKey) != "", ServerCompaction: provider.ServerCompaction,
		})
	}
	for name, profile := range config.Models {
		view.Models = append(view.Models, ModelProfileView{
			Name: name, Provider: profile.Provider, Model: profile.Model,
			MaxOutputTokens: profile.MaxOutputTokens, SupportsInputAudio: profile.SupportsInputAudio,
		})
	}
	sort.Slice(view.Providers, func(i, j int) bool { return view.Providers[i].Name < view.Providers[j].Name })
	sort.Slice(view.Models, func(i, j int) bool { return view.Models[i].Name < view.Models[j].Name })
	return view
}

func (s *DesktopService) projectModelsView() *ModelSettingsView {
	view := modelSettingsView(s.projectConfig.ModelConfig)
	return &view
}

func (s *DesktopService) SaveModelSettings(input ModelSettingsInput) (ModelSettingsView, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	current, err := s.store.LoadModelConfig()
	if err != nil {
		return ModelSettingsView{}, err
	}
	config := publicmodel.Config{
		Providers:    make(map[string]publicmodel.ProviderConfig, len(input.Providers)),
		Models:       make(map[string]publicmodel.ModelProfile, len(input.Models)),
		DefaultModel: input.DefaultModel, MaxParallelAgents: input.MaxParallelAgents,
	}
	for _, provider := range input.Providers {
		apiKey := strings.TrimSpace(provider.APIKey)
		if apiKey == "" {
			apiKey = current.Providers[provider.Name].APIKey
		}
		config.Providers[provider.Name] = publicmodel.ProviderConfig{
			Protocol: provider.Protocol, BaseURL: provider.BaseURL, APIKey: apiKey, ServerCompaction: provider.ServerCompaction,
		}
	}
	for _, profile := range input.Models {
		config.Models[profile.Name] = publicmodel.ModelProfile{
			Provider: profile.Provider, Model: profile.Model, MaxOutputTokens: profile.MaxOutputTokens,
			SupportsInputAudio: profile.SupportsInputAudio,
		}
	}
	s.mu.Lock()
	if s.reconfiguring {
		s.mu.Unlock()
		return ModelSettingsView{}, errors.New("设置正在应用")
	}
	workspace, sessionID := s.workspace, s.activeSessionIDLocked()
	savedControls := s.captureControlsLocked()
	for _, sessionContext := range s.sessionContexts {
		if sessionContext == nil || sessionContext.runtime == nil {
			continue
		}
		state, stateErr := sessionContext.runtime.State(context.Background())
		if stateErr != nil {
			s.mu.Unlock()
			return ModelSettingsView{}, stateErr
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused {
			s.mu.Unlock()
			return ModelSettingsView{}, errors.New("请等待所有会话任务结束后再修改模型设置")
		}
		pending, pendingErr := sessionContext.runtime.PendingRequests(context.Background())
		if pendingErr != nil {
			s.mu.Unlock()
			return ModelSettingsView{}, pendingErr
		}
		if len(pending) != 0 {
			s.mu.Unlock()
			return ModelSettingsView{}, errors.New("请先处理所有会话中的待处理请求，再修改模型设置")
		}
	}
	if workspace != "" {
		if err := s.closeWorkspaceLocked(false); err != nil {
			s.mu.Unlock()
			return ModelSettingsView{}, err
		}
		s.reconfiguring = true
	}
	s.mu.Unlock()
	if workspace != "" {
		defer func() {
			s.mu.Lock()
			s.reconfiguring = false
			s.mu.Unlock()
		}()
	}
	if err := s.store.SaveModelConfig(config); err != nil {
		if workspace != "" {
			if reopenErr := s.startWorkspace(workspace); reopenErr == nil && sessionID != "" {
				s.restoreControls(savedControls)
				_, reopenErr = s.openSession(sessionID, true)
				err = errors.Join(err, reopenErr)
			} else {
				err = errors.Join(err, reopenErr)
			}
		}
		return ModelSettingsView{}, err
	}
	if workspace != "" {
		if err := s.startWorkspace(workspace); err != nil {
			return ModelSettingsView{}, err
		}
		s.restoreControls(savedControls)
		if sessionID != "" {
			if _, err := s.openSession(sessionID, true); err != nil {
				return ModelSettingsView{}, err
			}
		}
	}
	return modelSettingsView(config), nil
}

func appearanceView(appearance settings.Appearance) AppearanceView {
	view := AppearanceView{
		Theme: appearance.Theme, WallpaperType: appearance.WallpaperType,
		WallpaperFile: appearance.WallpaperFile, WallpaperName: appearance.WallpaperName,
		PanelOpacity: appearance.PanelOpacity,
	}
	if appearance.WallpaperFile != "" {
		if path, err := wallpaperPath(appearance.WallpaperFile); err == nil {
			if _, err := os.Stat(path); err == nil {
				view.WallpaperURL = "/wallpapers/" + appearance.WallpaperFile
			}
		}
	}
	return view
}

func (s *DesktopService) SaveAppearanceSettings(input AppearanceSettingsInput) (AppearanceView, error) {
	appearance := settings.Appearance{
		Theme: input.Theme, WallpaperType: input.WallpaperType,
		WallpaperFile: input.WallpaperFile, WallpaperName: input.WallpaperName,
		PanelOpacity: input.PanelOpacity,
	}
	if input.WallpaperFile != "" {
		if appearance.WallpaperName == "" {
			appearance.WallpaperName = input.WallpaperFile
		}
		if err := validateWallpaperFile(input.WallpaperType, input.WallpaperFile); err != nil {
			return AppearanceView{}, err
		}
		wallpaperPath, err := wallpaperPath(input.WallpaperFile)
		if err != nil {
			return AppearanceView{}, err
		}
		if _, err := os.Stat(wallpaperPath); err != nil {
			return AppearanceView{}, fmt.Errorf("找不到所选壁纸文件: %w", err)
		}
	}
	previous, err := s.store.Appearance()
	if err != nil {
		return AppearanceView{}, err
	}
	if err := s.store.SaveAppearance(appearance); err != nil {
		return AppearanceView{}, err
	}
	if previous.WallpaperFile != "" && previous.WallpaperFile != appearance.WallpaperFile {
		if path, pathErr := wallpaperPath(previous.WallpaperFile); pathErr == nil {
			_ = os.Remove(path)
		}
	}
	return appearanceView(appearance), nil
}

func (s *DesktopService) PickWallpaper(wallpaperType string) (WallpaperSelection, error) {
	filters, extensions, err := wallpaperPickerOptions(wallpaperType)
	if err != nil {
		return WallpaperSelection{}, err
	}
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app == nil {
		return WallpaperSelection{}, errors.New("桌面文件选择器尚未就绪")
	}
	source, err := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseFiles: true, CanChooseDirectories: false, AllowsOtherFileTypes: false,
		Title: "选择壁纸", ButtonText: "选择", Filters: filters,
	}).PromptForSingleSelection()
	if err != nil {
		return WallpaperSelection{}, err
	}
	if source == "" {
		return WallpaperSelection{}, nil
	}
	ext := strings.ToLower(filepath.Ext(source))
	if _, ok := extensions[ext]; !ok {
		return WallpaperSelection{}, fmt.Errorf("不支持此壁纸格式: %s", ext)
	}
	info, err := os.Stat(source)
	if err != nil {
		return WallpaperSelection{}, fmt.Errorf("读取壁纸文件: %w", err)
	}
	if !info.Mode().IsRegular() {
		return WallpaperSelection{}, errors.New("壁纸必须是普通文件")
	}
	if info.Size() <= 0 || info.Size() > maxWallpaperBytes {
		return WallpaperSelection{}, fmt.Errorf("壁纸文件不能超过 %d MiB", maxWallpaperBytes>>20)
	}
	dir, err := wallpaperDirectory()
	if err != nil {
		return WallpaperSelection{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return WallpaperSelection{}, fmt.Errorf("创建壁纸目录: %w", err)
	}
	target, err := os.CreateTemp(dir, "wallpaper-*"+ext)
	if err != nil {
		return WallpaperSelection{}, fmt.Errorf("创建壁纸副本: %w", err)
	}
	name := filepath.Base(target.Name())
	cleanup := func() { _ = target.Close(); _ = os.Remove(target.Name()) }
	input, err := os.Open(source)
	if err != nil {
		cleanup()
		return WallpaperSelection{}, fmt.Errorf("打开壁纸文件: %w", err)
	}
	written, copyErr := io.Copy(target, io.LimitReader(input, maxWallpaperBytes+1))
	closeInputErr := input.Close()
	closeTargetErr := target.Close()
	if copyErr != nil || closeInputErr != nil || closeTargetErr != nil {
		_ = os.Remove(target.Name())
		return WallpaperSelection{}, errors.Join(copyErr, closeInputErr, closeTargetErr)
	}
	if written > maxWallpaperBytes {
		_ = os.Remove(target.Name())
		return WallpaperSelection{}, fmt.Errorf("壁纸文件不能超过 %d MiB", maxWallpaperBytes>>20)
	}
	return WallpaperSelection{
		File: name, Name: filepath.Base(source), Type: wallpaperType,
		URL: "/wallpapers/" + name,
	}, nil
}

func (s *DesktopService) DiscardWallpaper(file string) error {
	if file == "" {
		return nil
	}
	appearance, err := s.store.Appearance()
	if err != nil {
		return err
	}
	if file == appearance.WallpaperFile {
		return nil
	}
	path, err := wallpaperPath(file)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("清理未保存的壁纸: %w", err)
	}
	return nil
}

func wallpaperPickerOptions(wallpaperType string) ([]application.FileFilter, map[string]struct{}, error) {
	switch wallpaperType {
	case "static":
		return []application.FileFilter{{DisplayName: "静态图片 (*.png;*.jpg;*.jpeg;*.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"}}, map[string]struct{}{".png": {}, ".jpg": {}, ".jpeg": {}, ".webp": {}}, nil
	case "dynamic":
		return []application.FileFilter{{DisplayName: "动态壁纸 (*.gif;*.mp4;*.webm)", Pattern: "*.gif;*.mp4;*.webm"}}, map[string]struct{}{".gif": {}, ".mp4": {}, ".webm": {}}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported wallpaper type %q", wallpaperType)
	}
}

func wallpaperDirectory() (string, error) {
	dir, err := settings.UserSettingsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wallpapers"), nil
}

func wallpaperPath(file string) (string, error) {
	if file == "" || filepath.Base(file) != file || strings.ContainsAny(file, `/\\`) {
		return "", errors.New("invalid wallpaper file name")
	}
	dir, err := wallpaperDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, file), nil
}

func validateWallpaperFile(wallpaperType, file string) error {
	_, extensions, err := wallpaperPickerOptions(wallpaperType)
	if err != nil {
		return err
	}
	if _, ok := extensions[strings.ToLower(filepath.Ext(file))]; !ok {
		return errors.New("壁纸文件格式与选择的静态/动态类型不匹配")
	}
	_, err = wallpaperPath(file)
	return err
}

func WallpaperAssetHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wallpapers/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		file := strings.TrimPrefix(r.URL.Path, prefix)
		if file == "" || filepath.Base(file) != file || strings.ContainsAny(file, `/\\`) {
			http.NotFound(w, r)
			return
		}
		path, err := wallpaperPath(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch strings.ToLower(filepath.Ext(file)) {
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".jpg", ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".webp":
			w.Header().Set("Content-Type", "image/webp")
		case ".gif":
			w.Header().Set("Content-Type", "image/gif")
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case ".webm":
			w.Header().Set("Content-Type", "video/webm")
		}
		http.ServeFile(w, r, path)
	})
}

func (s *DesktopService) SetTheme(theme string) error {
	appearance, err := s.store.Appearance()
	if err != nil {
		return err
	}
	appearance.Theme = theme
	return s.store.SaveAppearance(appearance)
}

func (s *DesktopService) PickWorkspace() (string, error) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app == nil {
		return "", errors.New("桌面文件选择器尚未就绪")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: false,
		Title: "打开工作区", ButtonText: "打开文件夹",
	}).PromptForSingleSelection()
}

func (s *DesktopService) PickCommandToolchainFolder() (string, error) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app == nil {
		return "", errors.New("桌面文件选择器尚未就绪")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: false,
		Title: "选择命令工具链目录", ButtonText: "选择目录",
	}).PromptForSingleSelection()
}
