//go:build lune_web

package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/afterlune/lune/internal/engram"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/toolchains"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/runtimehost"
)

var errWebConfigRequired = errors.New("configure a model provider and default model in Settings before opening a workspace")
var errWebWorkspaceBusy = errors.New("workspace has running or queued work; retry after it finishes")

type webWorkspaceEntry struct {
	workspace core.Workspace
	info      os.FileInfo
	core      *core.Core
	host      *runtimehost.Server
}

type webWorkspaces struct {
	token       string
	diagnostics io.Writer
	initial     string
	store       *settings.Store

	lifecycleMu    sync.Mutex
	mu             sync.Mutex
	closing        bool
	needsSetup     bool
	pendingInitial string
	order          []string
	entries        map[string]*webWorkspaceEntry
}

func (s *webWorkspaces) open(path string) (core.Workspace, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.openLocked(path)
}

func (s *webWorkspaces) openLocked(path string) (core.Workspace, error) {
	if s.store == nil {
		return core.Workspace{}, errors.New("web settings store is unavailable")
	}
	if !filepath.IsAbs(path) {
		return core.Workspace{}, errors.New("workspace must be an absolute directory path")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return core.Workspace{}, err
	}
	for _, component := range strings.Split(filepath.ToSlash(canonical), "/") {
		if strings.EqualFold(component, ".lune") {
			return core.Workspace{}, errors.New("cannot open a Lune state directory as a workspace")
		}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return core.Workspace{}, err
	}
	if !info.IsDir() {
		return core.Workspace{}, errors.New("workspace must be a directory")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return core.Workspace{}, errors.New("workspace host is shutting down")
	}
	for _, id := range s.order {
		existing := s.entries[id]
		if existing == nil {
			continue
		}
		if os.SameFile(existing.info, info) {
			return existing.workspace, nil
		}
		if existing.workspace.Path == canonical {
			return core.Workspace{}, errors.New("the open workspace directory was replaced")
		}
	}

	projectConfig, configured, err := s.store.EffectiveWebConfig(canonical)
	if err != nil {
		return core.Workspace{}, err
	}
	if !configured {
		s.pendingInitial = canonical
		s.needsSetup = true
		return core.Workspace{}, errWebConfigRequired
	}

	stateDir := filepath.Join(canonical, ".lune")
	if stateInfo, lstatErr := os.Lstat(stateDir); lstatErr == nil && (!stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0) {
		return core.Workspace{}, errors.New(".lune must be a real directory in the workspace")
	} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return core.Workspace{}, lstatErr
	}

	var toolchainRoots []string
	if stored, found, tcErr := s.store.LoadWorkspaceCommandToolchainPaths(canonical); tcErr == nil && found {
		toolchainRoots = toolchains.AvailablePaths(stored)
	} else {
		toolchainRoots = toolchains.AutoDetectRoots()
		if len(toolchainRoots) > 0 {
			_ = s.store.SaveWorkspaceCommandToolchainPaths(canonical, toolchainRoots)
		}
	}

	coreCfg := core.Config{
		Workspace:             canonical,
		StateDir:              stateDir,
		ExclusiveState:        true,
		Logger:                slog.Default(),
		PersonalInstructions:  engram.Instructions(projectConfig.MCPServers),
		SandboxHelperPath:     findSandboxHelper(canonical),
		ProjectConfig:         &projectConfig,
		CommandToolchainPaths: toolchainRoots,
	}
	client, err := core.New(coreCfg)
	if err != nil {
		return core.Workspace{}, err
	}
	if err := client.SetPermissionMode(core.PermissionWorkspace); err != nil {
		_ = client.Close(context.Background())
		return core.Workspace{}, err
	}
	host, err := runtimehost.New(client, s.token)
	if err != nil {
		_ = client.Close(context.Background())
		return core.Workspace{}, err
	}
	enableCoreIntegrations(client, coreCfg, s.diagnostics)

	ws := core.Workspace{ID: rand.Text(), Path: canonical}
	if s.entries == nil {
		s.entries = make(map[string]*webWorkspaceEntry)
	}
	s.entries[ws.ID] = &webWorkspaceEntry{workspace: ws, info: info, core: client, host: host}
	s.order = append(s.order, ws.ID)
	if s.initial == "" {
		s.initial = ws.ID
	}
	s.needsSetup = false

	_ = s.store.TouchWorkspace(canonical)
	_ = s.store.SaveWebConnection("local", canonical, filepath.Base(canonical), nil, false)
	return ws, nil
}

func (s *webWorkspaces) closeWorkspace(id string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closeWorkspaceLocked(id)
}

func (s *webWorkspaces) closeWorkspaceLocked(id string) error {
	s.mu.Lock()
	entry := s.entries[id]
	if entry == nil {
		s.mu.Unlock()
		return errors.New("workspace not found")
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if len(entry.core.ActiveTasks()) > 0 {
		return errWebWorkspaceBusy
	}
	if err := entry.core.Close(ctx); err != nil {
		if strings.Contains(err.Error(), "cannot close core while a task is running") || strings.Contains(err.Error(), "runtime configuration changes are active") {
			return errWebWorkspaceBusy
		}
		return err
	}

	s.mu.Lock()
	if s.entries[id] == entry {
		delete(s.entries, id)
		filtered := make([]string, 0, len(s.order))
		for _, existing := range s.order {
			if existing != id {
				filtered = append(filtered, existing)
			}
		}
		s.order = filtered
		if s.initial == id {
			if len(s.order) > 0 {
				s.initial = s.order[0]
			} else {
				s.initial = ""
			}
		}
	}
	s.mu.Unlock()
	return entry.host.Shutdown(ctx)
}

func (s *webWorkspaces) reloadAffected(scope string) ([]core.Workspace, bool, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.reloadAffectedLocked(scope)
}

func (s *webWorkspaces) reloadAffectedLocked(scope string) ([]core.Workspace, bool, error) {
	canonicalScope := strings.TrimSpace(scope)
	if canonicalScope != "" {
		if resolved, err := filepath.EvalSymlinks(canonicalScope); err == nil {
			canonicalScope = resolved
		}
		canonicalScope = filepath.Clean(canonicalScope)
	}

	s.mu.Lock()
	var targets []*webWorkspaceEntry
	for _, id := range s.order {
		entry := s.entries[id]
		if entry == nil {
			continue
		}
		if canonicalScope == "" || entry.workspace.Path == canonicalScope {
			targets = append(targets, entry)
		}
	}
	pendingPath := s.pendingInitial
	s.mu.Unlock()

	for _, entry := range targets {
		if len(entry.core.ActiveTasks()) > 0 {
			return s.listOpened(), false, errWebWorkspaceBusy
		}
	}
	for _, entry := range targets {
		if err := s.closeWorkspaceLocked(entry.workspace.ID); err != nil {
			return s.listOpened(), false, err
		}
	}

	var opened []core.Workspace
	for _, entry := range targets {
		ws, err := s.openLocked(entry.workspace.Path)
		if err != nil && !errors.Is(err, errWebConfigRequired) {
			return s.listOpened(), len(targets) > 0, err
		}
		if err == nil {
			opened = append(opened, ws)
		}
	}
	if len(targets) == 0 && len(s.listOpened()) == 0 && pendingPath != "" {
		if ws, err := s.openLocked(pendingPath); err != nil && !errors.Is(err, errWebConfigRequired) {
			return s.listOpened(), false, err
		} else if err == nil {
			opened = append(opened, ws)
		}
	}
	return s.listOpened(), len(targets) > 0 || len(opened) > 0, nil
}

func (s *webWorkspaces) configSavedResult(scope string) (map[string]any, error) {
	opened := s.listOpened()
	s.mu.Lock()
	pendingPath := s.pendingInitial
	initial := s.initial
	needsSetup := s.needsSetup
	s.mu.Unlock()
	reloaded := false
	var reconnectError string
	if (len(opened) == 0 && pendingPath != "") || len(opened) > 0 {
		var err error
		opened, reloaded, err = s.reloadAffected(scope)
		if err != nil {
			reconnectError = err.Error()
			reloaded = false
			opened = s.listOpened()
		}
	}
	if reloaded {
		s.mu.Lock()
		initial = s.initial
		needsSetup = s.needsSetup
		s.mu.Unlock()
	}
	result := map[string]any{
		"ok": true, "reloaded": reloaded, "requiresReconnect": !reloaded,
		"initial": initial, "needsSetup": needsSetup, "workspaces": opened,
	}
	if reconnectError != "" {
		result["reconnectError"] = reconnectError
	}
	return result, nil
}

func (s *webWorkspaces) listOpened() []core.Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]core.Workspace, 0, len(s.order))
	for _, id := range s.order {
		if entry := s.entries[id]; entry != nil {
			items = append(items, entry.workspace)
		}
	}
	return items
}

func (s *webWorkspaces) reloadWorkspace(id string) error {
	s.mu.Lock()
	entry := s.entries[id]
	s.mu.Unlock()
	if entry == nil {
		return errors.New("workspace not found")
	}
	_, _, err := s.reloadAffected(entry.workspace.Path)
	return err
}

func (s *webWorkspaces) serve(w http.ResponseWriter, r *http.Request) {
	if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/workspaces/"); ok {
		id, tail, _ := strings.Cut(rest, "/")
		s.mu.Lock()
		entry := s.entries[id]
		s.mu.Unlock()
		if entry == nil || tail != "runtime" {
			http.NotFound(w, r)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/v1/runtime"
		request.URL.RawPath = ""
		entry.host.ServeHTTP(w, request)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID            string                  `json:"id"`
		Path          string                  `json:"path"`
		Language      string                  `json:"language"`
		Theme         string                  `json:"theme"`
		Scope         string                  `json:"scope"`
		Document      map[string]any          `json:"document"`
		ImportID      string                  `json:"importId"`
		Text          string                  `json:"text"`
		Mode          string                  `json:"mode"`
		Address       string                  `json:"address"`
		Name          string                  `json:"name"`
		Token         *string                 `json:"token"`
		RememberToken bool                    `json:"rememberToken"`
		Provider      string                  `json:"provider"`
		Protocol      string                  `json:"protocol"`
		BaseURL       string                  `json:"baseURL"`
		APIKey        *string                 `json:"apiKey"`
		APIKeyEnv     string                  `json:"apiKeyEnv"`
		Preferences   *settings.UIPreferences `json:"preferences"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var result any
	var err error
	switch r.URL.Path {
	case "/_lune/workspaces":
		language, theme := "", ""
		var recent []settings.RecentWorkspace
		if s.store != nil {
			language, _ = s.store.WebLanguage()
			theme, _ = s.store.WebTheme()
			recent, _ = s.store.RecentWorkspaces()
		}
		opened := s.listOpened()
		s.mu.Lock()
		initial := s.initial
		needsSetup := s.needsSetup
		defaultPath := s.pendingInitial
		if initial == "" && len(opened) > 0 {
			initial = opened[0].ID
		}
		if defaultPath == "" && len(opened) > 0 {
			defaultPath = opened[0].Path
		}
		s.mu.Unlock()
		if defaultPath == "" {
			defaultPath, _ = os.UserHomeDir()
		}
		result = map[string]any{
			"initial":     initial,
			"items":       opened,
			"recent":      recent,
			"needsSetup":  needsSetup,
			"defaultPath": defaultPath,
			"language":    language,
			"theme":       theme,
			"preferences": settings.DefaultUIPreferences(),
		}
		if s.store != nil {
			if preferences, prefsErr := s.store.WebUIPreferences(); prefsErr == nil {
				result.(map[string]any)["preferences"] = preferences
			}
		}
	case "/_lune/language":
		if s.store != nil {
			err = s.store.SaveWebLanguage(input.Language)
		}
		result = map[string]string{"language": input.Language}
	case "/_lune/preferences":
		if s.store != nil {
			if input.Preferences != nil {
				err = s.store.SaveWebUIPreferences(*input.Preferences)
			} else if input.Language != "" {
				err = s.store.SaveWebLanguage(input.Language)
			}
			if err == nil && input.Theme != "" {
				err = s.store.SaveWebTheme(input.Theme)
			}
		}
		preferences := settings.DefaultUIPreferences()
		if s.store != nil && err == nil {
			preferences, err = s.store.WebUIPreferences()
		}
		result = map[string]any{"ok": err == nil, "preferences": preferences}
	case "/_lune/open-workspace":
		result, err = s.open(input.Path)
	case "/_lune/close-workspace":
		err = s.closeWorkspace(input.ID)
		result = map[string]any{"ok": err == nil}
	case "/_lune/reload-workspace":
		err = s.reloadWorkspace(input.ID)
		result = map[string]any{"ok": err == nil}
	case "/_lune/browse-directories":
		result, err = s.browse(input.Path)
	case "/_lune/config/get":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			result, err = s.store.WebConfig(input.Scope)
		}
	case "/_lune/config/import":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			result, err = s.store.PrepareWebImport(input.Text)
		}
	case "/_lune/providers/models":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			var models []settings.ProviderModelInfo
			models, err = s.store.FetchWebProviderModels(r.Context(), input.Scope, input.Provider)
			result = map[string]any{"models": models}
		}
	case "/_lune/providers/test":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			started := time.Now()
			var apiKey *string
			if input.APIKey != nil {
				apiKey = input.APIKey
			}
			models, testErr := s.store.TestWebProvider(r.Context(), input.Scope, settings.ProviderTestInput{
				Provider: input.Provider, Protocol: input.Protocol, BaseURL: input.BaseURL, APIKey: apiKey, APIKeyEnv: input.APIKeyEnv,
			})
			if testErr != nil {
				result = map[string]any{"ok": false, "durationMs": time.Since(started).Milliseconds(), "error": "Provider connection failed. Check the URL, credentials, and network."}
			} else {
				result = map[string]any{"ok": true, "durationMs": time.Since(started).Milliseconds(), "modelCount": len(models), "models": models}
			}
		}
	case "/_lune/config/save":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			err = s.store.SaveWebConfig(input.Scope, input.Document, input.ImportID)
			if err == nil {
				result, err = s.configSavedResult(input.Scope)
			}
		}
	case "/_lune/config/reset":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			err = s.store.ResetWebWorkspaceConfig(input.Scope)
			if err == nil {
				result, err = s.configSavedResult(input.Scope)
			}
		}
	case "/_lune/connections/list":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			var items []settings.WebConnection
			items, err = s.store.WebConnections()
			result = map[string]any{"items": items, "notice": ""}
		}
	case "/_lune/connections/save":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			mode := input.Mode
			if mode == "" {
				mode = "remote"
			}
			err = s.store.SaveWebConnection(mode, input.Address, input.Name, input.Token, input.RememberToken)
			result = map[string]any{"ok": err == nil}
		}
	case "/_lune/connections/delete":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			err = s.store.DeleteWebConnection(input.Mode, input.Address)
			result = map[string]any{"ok": err == nil}
		}
	case "/_lune/connections/token":
		if s.store == nil {
			err = errors.New("settings database is unavailable")
		} else {
			var tok string
			tok, err = s.store.WebRemoteToken(input.Address)
			result = map[string]string{"token": tok}
		}
	default:
		http.NotFound(w, r)
		return
	}

	if err != nil {
		if errors.Is(err, errWebWorkspaceBusy) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, errWebConfigRequired) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "needsSetup": true})
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *webWorkspaces) browse(path string) (any, error) {
	if strings.TrimSpace(path) == "" {
		s.mu.Lock()
		if len(s.order) > 0 && s.entries[s.order[0]] != nil {
			path = s.entries[s.order[0]].workspace.Path
		} else if s.pendingInitial != "" {
			path = s.pendingInitial
		}
		s.mu.Unlock()
		if path == "" {
			path, _ = os.UserHomeDir()
		}
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("enter an absolute directory path")
	}
	path = filepath.Clean(path)
	folders, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	directories := make([]string, 0, len(folders))
	for _, folder := range folders {
		if folder.IsDir() && !strings.EqualFold(folder.Name(), ".lune") {
			directories = append(directories, filepath.Join(path, folder.Name()))
		}
	}
	sort.Strings(directories)
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	recent := []string{}
	if s.store != nil {
		if items, err := s.store.RecentWorkspaces(); err == nil {
			for _, item := range items {
				recent = append(recent, item.Path)
			}
		}
	}
	return map[string]any{"path": path, "parent": parent, "directories": directories, "recent": recent}, nil
}

func (s *webWorkspaces) shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	entries := make([]*webWorkspaceEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	results := make(chan error, len(entries)*2)
	for _, entry := range entries {
		go func(e *webWorkspaceEntry) {
			results <- e.host.Shutdown(ctx)
			results <- e.core.Shutdown(ctx)
		}(entry)
	}
	var err error
	for range entries {
		err = errors.Join(err, <-results, <-results)
	}
	return err
}
