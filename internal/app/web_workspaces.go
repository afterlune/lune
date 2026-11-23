//go:build lune_web

package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/runtimehost"
)

type webWorkspaces struct {
	token       string
	diagnostics io.Writer
	initial     string
	manager     core.WorkspaceManager

	mu      sync.Mutex
	closing bool
	opened  []core.Workspace
	hosts   map[string]*runtimehost.Server
}

func (s *webWorkspaces) open(path string) (core.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return core.Workspace{}, errors.New("workspace host is shutting down")
	}
	var config core.Config
	workspace, client, err := s.manager.OpenWorkspace(path, func(canonical string) (core.Config, error) {
		var err error
		config, err = workspaceCoreConfig(canonical, s.diagnostics)
		return config, err
	})
	if err != nil {
		return core.Workspace{}, err
	}
	if _, exists := s.hosts[workspace.ID]; exists {
		return workspace, nil
	}
	if err := client.SetPermissionMode(core.PermissionWorkspace); err != nil {
		return core.Workspace{}, err
	}
	host, err := runtimehost.New(client, s.token)
	if err != nil {
		return core.Workspace{}, err
	}
	if s.hosts == nil {
		s.hosts = make(map[string]*runtimehost.Server)
	}
	s.hosts[workspace.ID] = host
	s.opened = append(s.opened, workspace)
	enableCoreIntegrations(client, config, s.diagnostics)
	return workspace, nil
}

func (s *webWorkspaces) serve(w http.ResponseWriter, r *http.Request) {
	if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/workspaces/"); ok {
		id, tail, _ := strings.Cut(rest, "/")
		s.mu.Lock()
		host := s.hosts[id]
		s.mu.Unlock()
		if host == nil || tail != "runtime" {
			http.NotFound(w, r)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/v1/runtime"
		request.URL.RawPath = ""
		host.ServeHTTP(w, request)
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
		Path     string
		Language string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid workspace request", http.StatusBadRequest)
		return
	}
	var result any
	var err error
	switch r.URL.Path {
	case "/_lune/workspaces":
		language := ""
		if store, openErr := settings.OpenUser(); openErr == nil {
			language, _ = store.WebLanguage()
			store.Close()
		}
		s.mu.Lock()
		result = map[string]any{"initial": s.initial, "items": append([]core.Workspace(nil), s.opened...), "language": language}
		s.mu.Unlock()
	case "/_lune/language":
		var store *settings.Store
		store, err = settings.OpenUser()
		if err == nil {
			defer store.Close()
			err = store.SaveWebLanguage(input.Language)
		}
		result = map[string]string{"language": input.Language}
	case "/_lune/open-workspace":
		result, err = s.open(input.Path)
	case "/_lune/browse-directories":
		result, err = s.browse(input.Path)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *webWorkspaces) browse(path string) (any, error) {
	if path == "" {
		s.mu.Lock()
		if len(s.opened) != 0 {
			path = s.opened[0].Path
		}
		s.mu.Unlock()
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
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	recent := []string{}
	if store, err := settings.OpenUser(); err == nil {
		defer store.Close()
		if items, err := store.RecentWorkspaces(); err == nil {
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
	hosts := s.hosts
	s.mu.Unlock()
	results := make(chan error, len(hosts))
	for _, host := range hosts {
		go func() { results <- host.Shutdown(ctx) }()
	}
	var err error
	for range hosts {
		err = errors.Join(err, <-results)
	}
	return errors.Join(err, s.manager.Shutdown(ctx))
}
