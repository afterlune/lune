package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestDesktopInstancesCanOpenDifferentSessionsInSharedState(t *testing.T) {
	workspace, stateRoot := t.TempDir(), t.TempDir()
	stateDir := filepath.Join(stateRoot, "local")
	config := core.Config{
		Workspace: workspace, StateDir: stateDir, ModelConfig: &publicmodel.Config{
			Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
			Profiles:       map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}},
			DefaultProfile: "primary",
		},
		ModelProviders: map[string]publicmodel.Provider{"host": immediateDesktopProvider{}},
	}
	firstCore, err := core.New(config)
	if err != nil {
		t.Fatal(err)
	}
	firstActivity, err := OpenActivityStore(stateRoot)
	if err != nil {
		_ = firstCore.Close(context.Background())
		t.Fatal(err)
	}
	secondCore, err := core.New(config)
	if err != nil {
		_ = firstActivity.Close()
		_ = firstCore.Close(context.Background())
		t.Fatalf("open second Desktop Core in shared state: %v", err)
	}
	secondActivity, err := OpenActivityStore(stateRoot)
	if err != nil {
		_ = secondCore.Close(context.Background())
		_ = firstActivity.Close()
		_ = firstCore.Close(context.Background())
		t.Fatal(err)
	}
	first := &DesktopService{
		localCore: firstCore, workspace: workspace, activityStore: firstActivity,
		waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{},
	}
	second := &DesktopService{
		localCore: secondCore, workspace: workspace, activityStore: secondActivity,
		waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{},
	}
	t.Cleanup(func() {
		if err := second.CloseWorkspace(true); err != nil {
			t.Errorf("close second Desktop: %v", err)
		}
		if err := first.CloseWorkspace(true); err != nil {
			t.Errorf("close first Desktop: %v", err)
		}
	})

	sessionA, err := firstCore.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionB, err := firstCore.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.restoreSessionContexts(); err != nil {
		t.Fatal(err)
	}
	if err := second.restoreSessionContexts(); err != nil {
		t.Fatal(err)
	}
	if first.sessionContexts[sessionA.ID].runtime != nil || second.sessionContexts[sessionA.ID].runtime != nil {
		t.Fatal("restoring session metadata unexpectedly opened every session runtime")
	}
	if _, err := first.OpenSession(sessionA.ID); err != nil {
		t.Fatalf("first Desktop open session A: %v", err)
	}
	if _, err := second.OpenSession(sessionB.ID); err != nil {
		t.Fatalf("second Desktop open different session B: %v", err)
	}
	if _, err := second.OpenSession(sessionA.ID); !errors.Is(err, core.ErrSessionInUse) {
		t.Fatalf("second Desktop open session A = %v, want ErrSessionInUse", err)
	}
}
