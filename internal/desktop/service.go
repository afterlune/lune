package desktop

import (
	"context"
	"errors"
	"sync"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type DesktopService struct {
	mu                    sync.Mutex
	workspaceMu           sync.RWMutex
	workers               sync.WaitGroup
	workspaceContext      context.Context
	workspaceCancel       context.CancelFunc
	store                 *settings.Store
	app                   *application.App
	appContext            context.Context
	localCore             *core.Core
	activityStore         *ActivityStore
	workspace             string
	activeContext         *desktopSessionContext
	sessionContexts       map[string]*desktopSessionContext
	stateRoot             string
	projectConfig         core.ProjectConfig
	commandToolchainPaths []string
	reconfiguring         bool
	mcpOperations         int
	waiting               map[string]bool
	suppressedActivity    map[string]struct{}
}

type desktopSessionContext struct {
	opMu           sync.Mutex
	permissionMode core.PermissionMode
	core           *core.Core
	runtime        *core.SessionRuntime
	sessionID      string
	workspace      string
	mode           string
	stateDir       string
	worktreePath   string
	repositoryRoot string
	eventCancel    context.CancelFunc
	eventDone      chan struct{}
	planMode       bool
}

const (
	workspaceModeLocal    = "local"
	workspaceModeWorktree = "worktree"
	maxWallpaperBytes     = 100 << 20
)

func NewDesktopService() (*DesktopService, error) {
	store, err := settings.OpenUser()
	if err != nil {
		return nil, err
	}
	return &DesktopService{
		store: store, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{},
	}, nil
}

func (s *DesktopService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appContext = ctx
	s.app = application.Get()
	return nil
}

func (s *DesktopService) ServiceShutdown() error {
	closeErr := s.CloseWorkspace(true)
	if s.store != nil {
		closeErr = errors.Join(closeErr, s.store.Close())
	}
	return closeErr
}
