//go:build lune_web

package app

import (
	"context"
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
	"github.com/afterlune/lune/internal/ptyterm"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/slashcmd"
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
	// stale is true when a saved configuration could not be applied yet
	// because a task was running. Guarded by webWorkspaces.mu.
	stale bool
}

type webWorkspaces struct {
	token       string
	diagnostics io.Writer
	initial     string
	store       *settings.Store
	sharedState bool

	lifecycleMu     sync.Mutex
	mu              sync.Mutex
	closing         bool
	needsSetup      bool
	pendingInitial  string
	order           []string
	entries         map[string]*webWorkspaceEntry
	activeTerminals map[ptyterm.Session]struct{}
	idleReloads     map[string]bool
}

type webWorkspaceView struct {
	ID            string                            `json:"id"`
	Path          string                            `json:"path"`
	Name          string                            `json:"name"`
	LastSessionID string                            `json:"lastSessionId"`
	CreatedAt     int64                             `json:"createdAt"`
	UpdatedAt     int64                             `json:"updatedAt"`
	LastOpenedAt  int64                             `json:"lastOpenedAt"`
	Sessions      []settings.WorkspaceSessionRecord `json:"sessions"`
}

func coreHasPendingWork(ctx context.Context, client *core.Core) (bool, error) {
	if client == nil {
		return false, nil
	}
	if len(client.ActiveTasks()) > 0 {
		return true, nil
	}
	const pageSize = 100
	for offset := 0; ; {
		sessions, err := client.ListSessionPage(ctx, offset, pageSize, true)
		if err != nil {
			return false, err
		}
		for _, session := range sessions {
			runtime, err := client.OpenSessionRuntime(ctx, session.ID)
			if err != nil {
				return false, err
			}
			state, err := runtime.State(ctx)
			closeErr := runtime.Close(ctx)
			if err != nil || closeErr != nil {
				return false, errors.Join(err, closeErr)
			}
			if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused || len(state.Queue) > 0 {
				return true, nil
			}
		}
		offset += len(sessions)
		if len(sessions) < pageSize {
			return len(client.ActiveTasks()) > 0, nil
		}
	}
}

func workspaceHasPendingWork(client *core.Core) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return coreHasPendingWork(ctx, client)
}

func (s *webWorkspaces) open(path string) (core.Workspace, error) {
	return s.openWithOptions(path, true)
}

func (s *webWorkspaces) openWithOptions(path string, touch bool) (core.Workspace, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	ws, entry, err := s.openLocked(path, touch)
	if err != nil {
		return core.Workspace{}, err
	}
	if entry != nil && s.store != nil {
		_, _ = s.syncEntrySessions(context.Background(), entry)
	}
	return ws, nil
}

func (s *webWorkspaces) openLocked(path string, touch bool) (core.Workspace, *webWorkspaceEntry, error) {
	if s.store == nil {
		return core.Workspace{}, nil, errors.New("web settings store is unavailable")
	}
	if !filepath.IsAbs(path) {
		return core.Workspace{}, nil, errors.New("workspace must be an absolute directory path")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return core.Workspace{}, nil, err
	}
	for _, component := range strings.Split(filepath.ToSlash(canonical), "/") {
		if strings.EqualFold(component, ".lune") {
			return core.Workspace{}, nil, errors.New("cannot open a Lune state directory as a workspace")
		}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return core.Workspace{}, nil, err
	}
	if !info.IsDir() {
		return core.Workspace{}, nil, errors.New("workspace must be a directory")
	}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return core.Workspace{}, nil, errors.New("workspace host is shutting down")
	}
	for _, id := range s.order {
		existing := s.entries[id]
		if existing == nil {
			continue
		}
		if os.SameFile(existing.info, info) {
			ws := existing.workspace
			s.mu.Unlock()
			if touch {
				_ = s.store.SelectWorkspace(ws.ID)
			}
			return ws, existing, nil
		}
		if existing.workspace.Path == canonical {
			s.mu.Unlock()
			return core.Workspace{}, nil, errors.New("the open workspace directory was replaced")
		}
	}
	s.mu.Unlock()

	projectConfig, configured, err := s.store.EffectiveWebConfig(canonical)
	if err != nil {
		return core.Workspace{}, nil, err
	}
	if !configured {
		s.mu.Lock()
		if s.pendingInitial == "" {
			s.pendingInitial = canonical
		}
		s.needsSetup = true
		s.mu.Unlock()
		return core.Workspace{}, nil, errWebConfigRequired
	}

	stateDir := filepath.Join(canonical, ".lune")
	if stateInfo, lstatErr := os.Lstat(stateDir); lstatErr == nil && (!stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0) {
		return core.Workspace{}, nil, errors.New(".lune must be a real directory in the workspace")
	} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return core.Workspace{}, nil, lstatErr
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
		ExclusiveState:        !s.sharedState,
		Logger:                slog.Default(),
		PersonalInstructions:  engram.Instructions(projectConfig.MCPServers),
		SandboxHelperPath:     findSandboxHelper(canonical),
		ProjectConfig:         &projectConfig,
		CommandToolchainPaths: toolchainRoots,
	}
	client, err := core.New(coreCfg)
	if err != nil {
		return core.Workspace{}, nil, err
	}
	initialPerm := core.PermissionWorkspace
	if savedPerm, ok, permErr := s.store.LoadWorkspacePermissionMode(canonical); permErr == nil && ok {
		if parsed, parseErr := slashcmd.ParsePermissionMode(savedPerm); parseErr == nil {
			initialPerm = parsed
		}
	}
	if err := client.SetPermissionMode(initialPerm); err != nil {
		_ = client.Close(context.Background())
		return core.Workspace{}, nil, err
	}
	host, err := runtimehost.New(client, s.token)
	if err != nil {
		_ = client.Close(context.Background())
		return core.Workspace{}, nil, err
	}
	enableCoreIntegrations(client, coreCfg, s.diagnostics)

	rec, err := s.store.EnsureWorkspace(canonical, touch)
	if err != nil {
		_ = host.Shutdown(context.Background())
		_ = client.Close(context.Background())
		return core.Workspace{}, nil, err
	}

	ws := core.Workspace{ID: rec.ID, Path: canonical}
	entry := &webWorkspaceEntry{workspace: ws, info: info, core: client, host: host}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = host.Shutdown(context.Background())
		_ = client.Close(context.Background())
		return core.Workspace{}, nil, errors.New("workspace host is shutting down")
	}
	if s.entries == nil {
		s.entries = make(map[string]*webWorkspaceEntry)
	}
	s.entries[ws.ID] = entry
	s.order = append(s.order, ws.ID)
	if touch || s.initial == "" {
		s.initial = ws.ID
	}
	s.needsSetup = false
	s.mu.Unlock()

	_ = s.store.TouchWorkspace(canonical)
	_ = s.store.SaveWebConnection("local", canonical, filepath.Base(canonical), nil, false)
	return ws, entry, nil
}

func (s *webWorkspaces) syncEntrySessions(ctx context.Context, entry *webWorkspaceEntry) ([]settings.WorkspaceSessionRecord, error) {
	if s.store == nil || entry == nil || entry.core == nil {
		return []settings.WorkspaceSessionRecord{}, nil
	}
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sessions, err := entry.core.ListSessions(syncCtx, 500, true)
	if err != nil {
		return nil, err
	}
	records := make([]settings.WorkspaceSessionRecord, 0, len(sessions))
	for _, sess := range sessions {
		var updatedMs int64
		if !sess.UpdatedAt.IsZero() {
			updatedMs = sess.UpdatedAt.UTC().UnixMilli()
		}
		records = append(records, settings.WorkspaceSessionRecord{
			SessionID:   sess.ID,
			WorkspaceID: entry.workspace.ID,
			Title:       sess.Title,
			Archived:    sess.Archived,
			CreatedAt:   updatedMs,
			UpdatedAt:   updatedMs,
		})
	}
	if err := s.store.SyncWorkspaceSessions(entry.workspace.ID, records); err != nil {
		return nil, err
	}
	saved, err := s.store.ListWorkspaceSessions(entry.workspace.ID, true)
	if err != nil {
		return records, nil
	}
	if saved == nil {
		saved = []settings.WorkspaceSessionRecord{}
	}
	return saved, nil
}

func (s *webWorkspaces) workspaceView(ws core.Workspace) webWorkspaceView {
	name := filepath.Base(ws.Path)
	if name == "" || name == string(filepath.Separator) {
		name = ws.Path
	}
	view := webWorkspaceView{
		ID:       ws.ID,
		Path:     ws.Path,
		Name:     name,
		Sessions: []settings.WorkspaceSessionRecord{},
	}
	if s.store != nil {
		if rec, found, err := s.store.WorkspaceByID(ws.ID); err == nil && found {
			view.Name = rec.Name
			view.LastSessionID = rec.LastSessionID
			view.CreatedAt = rec.CreatedAt
			view.UpdatedAt = rec.UpdatedAt
			view.LastOpenedAt = rec.LastOpenedAt
		}
		if sessions, err := s.store.ListWorkspaceSessions(ws.ID, true); err == nil && sessions != nil {
			view.Sessions = sessions
		}
	}
	return view
}

func (s *webWorkspaces) closeWorkspace(id string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closeWorkspaceLocked(id, true)
}

func (s *webWorkspaces) closeWorkspaceLocked(id string, persistClose bool) error {
	s.mu.Lock()
	entry := s.entries[id]
	if entry == nil {
		s.mu.Unlock()
		return errors.New("workspace not found")
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if busy, err := workspaceHasPendingWork(entry.core); err != nil {
		return err
	} else if busy {
		return errWebWorkspaceBusy
	}
	if s.store != nil {
		_, _ = s.syncEntrySessions(ctx, entry)
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
	if persistClose && s.store != nil {
		_ = s.store.CloseWorkspace(id)
	}
	if opened := s.listOpened(); len(opened) > 0 {
		s.mu.Lock()
		if s.initial == "" || s.entries[s.initial] == nil {
			s.initial = opened[0].ID
		}
		s.mu.Unlock()
	}
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
	originalOrder := append([]string(nil), s.order...)
	originalInitial := s.initial
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
	restoreWorkspaceOrder := func() {
		s.mu.Lock()
		seen := make(map[string]bool, len(s.entries))
		ordered := make([]string, 0, len(s.entries))
		for _, id := range originalOrder {
			if s.entries[id] != nil && !seen[id] {
				ordered = append(ordered, id)
				seen[id] = true
			}
		}
		for _, id := range s.order {
			if s.entries[id] != nil && !seen[id] {
				ordered = append(ordered, id)
				seen[id] = true
			}
		}
		s.order = ordered
		if s.entries[originalInitial] != nil {
			s.initial = originalInitial
		} else if s.entries[s.initial] == nil {
			s.initial = ""
			if len(ordered) > 0 {
				s.initial = ordered[0]
			}
		}
		s.mu.Unlock()
	}

	for _, entry := range targets {
		busy, err := workspaceHasPendingWork(entry.core)
		if err != nil {
			restoreWorkspaceOrder()
			return s.listOpened(), false, err
		}
		if busy {
			restoreWorkspaceOrder()
			return s.listOpened(), false, errWebWorkspaceBusy
		}
	}

	var opened []core.Workspace
	reopenedTargets := 0
	for _, entry := range targets {
		if err := s.closeWorkspaceLocked(entry.workspace.ID, false); err != nil {
			restoreWorkspaceOrder()
			return s.listOpened(), false, err
		}
		ws, newEntry, err := s.openLocked(entry.workspace.Path, false)
		if err != nil && !errors.Is(err, errWebConfigRequired) {
			restoreWorkspaceOrder()
			return s.listOpened(), false, err
		}
		if err == nil {
			reopenedTargets++
			if newEntry != nil && s.store != nil {
				_, _ = s.syncEntrySessions(context.Background(), newEntry)
			}
			opened = append(opened, ws)
		}
	}
	restoreWorkspaceOrder()
	pendingOpened := false
	if len(targets) == 0 && len(s.listOpened()) == 0 && pendingPath != "" {
		if ws, newEntry, err := s.openLocked(pendingPath, true); err != nil && !errors.Is(err, errWebConfigRequired) {
			return s.listOpened(), false, err
		} else if err == nil {
			if newEntry != nil && s.store != nil {
				_, _ = s.syncEntrySessions(context.Background(), newEntry)
			}
			opened = append(opened, ws)
			pendingOpened = true
		}
	}
	reloaded := pendingOpened || (len(targets) > 0 && reopenedTargets == len(targets))
	return s.listOpened(), reloaded, nil
}

func (s *webWorkspaces) configSavedResult(scope string) (map[string]any, error) {
	opened := s.listOpened()
	s.mu.Lock()
	pendingPath := s.pendingInitial
	initial := s.initial
	needsSetup := s.needsSetup
	s.mu.Unlock()
	reloaded := false
	reloadPending := false
	var reconnectError string
	if (len(opened) == 0 && pendingPath != "") || len(opened) > 0 {
		var err error
		opened, reloaded, err = s.reloadAffected(scope)
		if err != nil {
			reconnectError = err.Error()
			reloaded = false
			opened = s.listOpened()
			if errors.Is(err, errWebWorkspaceBusy) {
				reloadPending = true
				s.deferReload(scope)
			}
		}
	}
	s.mu.Lock()
	initial = s.initial
	needsSetup = s.needsSetup
	s.mu.Unlock()
	result := map[string]any{
		"ok": true, "reloaded": reloaded, "reloadPending": reloadPending, "requiresReconnect": !reloaded && !reloadPending,
		"initial": initial, "needsSetup": needsSetup, "workspaces": opened,
	}
	if reconnectError != "" {
		result["reconnectError"] = reconnectError
	}
	return result, nil
}

func (s *webWorkspaces) listOpened() []core.Workspace {
	s.mu.Lock()
	entriesCopy := make(map[string]*webWorkspaceEntry, len(s.entries))
	for id, entry := range s.entries {
		entriesCopy[id] = entry
	}
	orderCopy := append([]string(nil), s.order...)
	store := s.store
	s.mu.Unlock()

	items := make([]core.Workspace, 0, len(entriesCopy))
	seen := make(map[string]struct{}, len(entriesCopy))
	if store != nil {
		if recs, err := store.ListWorkspaces(true); err == nil {
			for _, rec := range recs {
				if entry := entriesCopy[rec.ID]; entry != nil {
					items = append(items, entry.workspace)
					seen[rec.ID] = struct{}{}
				}
			}
		}
	}
	for _, id := range orderCopy {
		if _, ok := seen[id]; ok {
			continue
		}
		if entry := entriesCopy[id]; entry != nil {
			items = append(items, entry.workspace)
		}
	}
	return items
}

func (s *webWorkspaces) listOpenedDetailed() []webWorkspaceView {
	opened := s.listOpened()
	if len(opened) == 0 {
		return []webWorkspaceView{}
	}
	byID := make(map[string]settings.WorkspaceRecord)
	if s.store != nil {
		if recs, err := s.store.ListWorkspacesWithSessions(true); err == nil {
			for _, rec := range recs {
				byID[rec.ID] = rec
			}
		}
	}
	views := make([]webWorkspaceView, 0, len(opened))
	for _, ws := range opened {
		if rec, ok := byID[ws.ID]; ok {
			sessions := rec.Sessions
			if sessions == nil {
				sessions = []settings.WorkspaceSessionRecord{}
			}
			views = append(views, webWorkspaceView{
				ID:            ws.ID,
				Path:          ws.Path,
				Name:          rec.Name,
				LastSessionID: rec.LastSessionID,
				CreatedAt:     rec.CreatedAt,
				UpdatedAt:     rec.UpdatedAt,
				LastOpenedAt:  rec.LastOpenedAt,
				Sessions:      sessions,
			})
			continue
		}
		views = append(views, s.workspaceView(ws))
	}
	return views
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
		if entry == nil || (tail != "runtime" && tail != "terminal") {
			http.NotFound(w, r)
			return
		}
		if tail == "terminal" {
			s.serveTerminal(w, r, entry.workspace.Path)
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
		ID             string                  `json:"id"`
		SessionID      string                  `json:"sessionId"`
		Path           string                  `json:"path"`
		Language       string                  `json:"language"`
		Theme          string                  `json:"theme"`
		Scope          string                  `json:"scope"`
		Document       map[string]any          `json:"document"`
		ImportID       string                  `json:"importId"`
		Text           string                  `json:"text"`
		Mode           string                  `json:"mode"`
		Address        string                  `json:"address"`
		Name           string                  `json:"name"`
		Token          *string                 `json:"token"`
		RememberToken  bool                    `json:"rememberToken"`
		Provider       string                  `json:"provider"`
		Protocol       string                  `json:"protocol"`
		BaseURL        string                  `json:"baseURL"`
		APIKey         *string                 `json:"apiKey"`
		APIKeyEnv      string                  `json:"apiKeyEnv"`
		PermissionMode string                  `json:"permissionMode"`
		Preferences    *settings.UIPreferences `json:"preferences"`
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
		opened := s.listOpenedDetailed()
		s.mu.Lock()
		initial := s.initial
		needsSetup := s.needsSetup
		defaultPath := s.pendingInitial
		s.mu.Unlock()
		if len(opened) > 0 {
			initial = opened[0].ID
			s.mu.Lock()
			s.initial = initial
			s.mu.Unlock()
			if defaultPath == "" {
				defaultPath = opened[0].Path
			}
		}
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
		var ws core.Workspace
		ws, err = s.open(input.Path)
		if err == nil {
			result = s.workspaceView(ws)
		}
	case "/_lune/close-workspace":
		err = s.closeWorkspace(input.ID)
		result = map[string]any{"ok": err == nil}
	case "/_lune/workspaces/select":
		s.mu.Lock()
		entry := s.entries[input.ID]
		if entry != nil {
			s.initial = input.ID
		}
		s.mu.Unlock()
		if entry == nil {
			err = errors.New("workspace not found")
		} else if s.store != nil {
			err = s.store.SelectWorkspace(input.ID)
		}
		result = map[string]any{"ok": err == nil}
	case "/_lune/workspaces/active-session":
		s.mu.Lock()
		entry := s.entries[input.ID]
		s.mu.Unlock()
		if entry == nil {
			err = errors.New("workspace not found")
		} else if strings.TrimSpace(input.SessionID) == "" {
			err = errors.New("sessionId is required")
		} else if s.store != nil {
			_, _ = s.syncEntrySessions(r.Context(), entry)
			err = s.store.RememberWorkspaceActiveSession(input.ID, input.SessionID)
		}
		result = map[string]any{"ok": err == nil}
	case "/_lune/workspaces/sessions/sync":
		s.mu.Lock()
		entry := s.entries[input.ID]
		s.mu.Unlock()
		if entry == nil {
			err = errors.New("workspace not found")
		} else {
			var sessions []settings.WorkspaceSessionRecord
			sessions, err = s.syncEntrySessions(r.Context(), entry)
			if err == nil {
				result = map[string]any{"ok": true, "sessions": sessions}
			}
		}
	case "/_lune/workspaces/delegation":
		result = map[string]any{"items": s.delegationViews(input.ID)}
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
	case "/_lune/workspaces/permission":
		var mode core.PermissionMode
		mode, err = slashcmd.ParsePermissionMode(input.PermissionMode)
		if err == nil {
			targetPath := strings.TrimSpace(input.Path)
			var liveCore *core.Core
			if input.ID != "" {
				s.mu.Lock()
				entry := s.entries[input.ID]
				if entry != nil {
					targetPath = entry.workspace.Path
					liveCore = entry.core
				}
				s.mu.Unlock()
			}
			if liveCore != nil {
				err = liveCore.SetPermissionMode(mode)
			}
			if err == nil && s.store != nil && targetPath != "" {
				err = s.store.SaveWorkspacePermissionMode(targetPath, string(mode))
			}
			if err == nil {
				result = map[string]any{"mode": string(mode)}
			}
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
	seenRecent := map[string]struct{}{}
	if s.store != nil {
		if recs, err := s.store.ListWorkspaces(false); err == nil {
			for _, rec := range recs {
				if _, ok := seenRecent[rec.Path]; !ok {
					seenRecent[rec.Path] = struct{}{}
					recent = append(recent, rec.Path)
				}
			}
		}
		if items, err := s.store.RecentWorkspaces(); err == nil {
			for _, item := range items {
				if _, ok := seenRecent[item.Path]; !ok {
					seenRecent[item.Path] = struct{}{}
					recent = append(recent, item.Path)
				}
			}
		}
	}
	return map[string]any{"path": path, "parent": parent, "directories": directories, "recent": recent}, nil
}

func (s *webWorkspaces) shutdown(ctx context.Context) error {
	s.closeAllTerminals()
	s.mu.Lock()
	s.closing = true
	entries := make([]*webWorkspaceEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	if s.store != nil {
		for _, entry := range entries {
			_, _ = s.syncEntrySessions(ctx, entry)
		}
	}
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

// webDelegationView reports the delegation limits the running Core actually uses.
type webDelegationView struct {
	WorkspaceID      string `json:"workspaceId"`
	Enabled          bool   `json:"enabled"`
	Parallel         int    `json:"parallel"`
	ChildrenPerAgent int    `json:"childrenPerAgent"`
	Depth            int    `json:"depth"`
	Profiles         int    `json:"profiles"`
	ConfigStale      bool   `json:"configStale"`
}

func (s *webWorkspaces) delegationViews(id string) []webDelegationView {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	type snapshot struct {
		id    string
		core  *core.Core
		stale bool
	}
	var snaps []snapshot
	ids := s.order
	if id != "" {
		ids = []string{id}
	}
	for _, wid := range ids {
		if entry := s.entries[wid]; entry != nil {
			snaps = append(snaps, snapshot{id: wid, core: entry.core, stale: entry.stale})
		}
	}
	s.mu.Unlock()
	views := make([]webDelegationView, 0, len(snaps))
	for _, snap := range snaps {
		limits := snap.core.ExecutionDefaults()
		views = append(views, webDelegationView{
			WorkspaceID:      snap.id,
			Enabled:          limits.MaxTeamChildrenPerAgent > 0 && limits.MaxTeamDepth > 0,
			Parallel:         snap.core.MaxParallelAgents(),
			ChildrenPerAgent: limits.MaxTeamChildrenPerAgent,
			Depth:            limits.MaxTeamDepth,
			Profiles:         len(snap.core.ListModelProfiles()),
			ConfigStale:      snap.stale,
		})
	}
	return views
}

// deferReload marks matching workspaces stale and reloads them once their
// active, queued, and paused work has finished, so saved limits are applied.
func (s *webWorkspaces) deferReload(scope string) {
	canonicalScope := strings.TrimSpace(scope)
	if canonicalScope != "" {
		if resolved, err := filepath.EvalSymlinks(canonicalScope); err == nil {
			canonicalScope = resolved
		}
		canonicalScope = filepath.Clean(canonicalScope)
	}
	s.mu.Lock()
	for _, id := range s.order {
		if entry := s.entries[id]; entry != nil && (canonicalScope == "" || entry.workspace.Path == canonicalScope) {
			entry.stale = true
		}
	}
	if s.idleReloads == nil {
		s.idleReloads = map[string]bool{}
	}
	if s.idleReloads[canonicalScope] {
		s.mu.Unlock()
		return
	}
	s.idleReloads[canonicalScope] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.idleReloads, canonicalScope)
			s.mu.Unlock()
		}()
		deadline := time.Now().Add(6 * time.Hour)
		for time.Now().Before(deadline) {
			time.Sleep(time.Second)
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing {
				return
			}
			if _, reloaded, err := s.reloadAffected(canonicalScope); err == nil {
				if reloaded {
					return
				}
				return
			} else if !errors.Is(err, errWebWorkspaceBusy) {
				return
			}
		}
	}()
}
