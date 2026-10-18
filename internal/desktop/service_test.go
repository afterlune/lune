package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
)

func TestPendingRequestChangedOnlyForInteractionEvents(t *testing.T) {
	for _, kind := range []core.EventKind{
		core.EventApprovalRequired,
		core.EventApprovalCancelled,
		core.EventUserQuestionRequired,
		core.EventUserInteractionRequired,
		core.EventUserInteractionCancelled,
		core.EventExecutionBlocked,
		core.EventModelRecoveryRequired,
		core.EventModelRecoveryResumed,
		core.EventToolRecoveryRequired,
	} {
		if !pendingRequestChanged(kind) {
			t.Errorf("pendingRequestChanged(%q) = false, want true", kind)
		}
	}

	for _, kind := range []core.EventKind{
		core.EventTaskStarted,
		core.EventTaskFinished,
		core.EventAgentStarted,
		core.EventToolStarted,
		core.EventToolFinished,
		core.EventModelStream,
	} {
		if pendingRequestChanged(kind) {
			t.Errorf("pendingRequestChanged(%q) = true, want false", kind)
		}
	}
}

func TestStripProposedPlanKeepsSurroundingResponse(t *testing.T) {
	got := stripProposedPlan("Before the plan.\n<proposed_plan>## Steps\n- Inspect files</proposed_plan>\nAfter the plan.")
	want := "Before the plan.\n\nAfter the plan."
	if got != want {
		t.Fatalf("stripProposedPlan() = %q, want %q", got, want)
	}
}

func TestStripProposedPlanLeavesIncompleteTagVisible(t *testing.T) {
	input := "The response contains <proposed_plan> but is incomplete"
	if got := stripProposedPlan(input); got != input {
		t.Fatalf("stripProposedPlan() = %q, want original", got)
	}
}

func TestTranscriptMessagesStripsProposedPlanFromAssistantHistory(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"Make a plan"}]},
		{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"Before the plan.\n<proposed_plan>## Steps\n- Inspect files</proposed_plan>\nAfter the plan."}]}
	]`)

	got := transcriptMessages(input)
	if len(got) != 2 {
		t.Fatalf("transcriptMessages() returned %d messages, want 2: %#v", len(got), got)
	}
	if got[0].Text != "Make a plan" {
		t.Fatalf("user transcript = %q, want original text", got[0].Text)
	}
	if got[1].Text != "Before the plan.\n\nAfter the plan." {
		t.Fatalf("assistant transcript = %q, want the plan block stripped", got[1].Text)
	}
}

func TestTranscriptMessagesOmitsAssistantMessageContainingOnlyAPlan(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"<proposed_plan>## Steps\n- Inspect files</proposed_plan>"}]}
	]`)

	if got := transcriptMessages(input); len(got) != 0 {
		t.Fatalf("transcriptMessages() = %#v, want no empty assistant message", got)
	}
}

func TestSearchSessionFindsBothRolesAndOmitsPlanBlock(t *testing.T) {
	saved := core.Session{
		ID: "session-1", Title: "Cache design", UpdatedAt: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC),
		History: json.RawMessage(`[
			{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"Cache should stay local."}]},
			{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"The cache is local.\n<proposed_plan>hidden cache details</proposed_plan>"}]}
		]`),
	}
	results := searchSession(saved, `D:\\project`, "CACHE")
	if len(results) != 2 {
		t.Fatalf("searchSession() returned %d results, want 2: %#v", len(results), results)
	}
	if results[0].result.Role != "assistant" || results[1].result.Role != "user" {
		t.Fatalf("search results roles = %q, %q; want newest assistant then user", results[0].result.Role, results[1].result.Role)
	}
	if !results[0].result.TitleMatched || results[0].result.SnippetMatch != "cache" {
		t.Fatalf("assistant match = %#v, want title match and case-insensitive message match", results[0].result)
	}
	if strings.Contains(strings.ToLower(results[0].result.SnippetBefore+results[0].result.SnippetMatch+results[0].result.SnippetAfter), "hidden") {
		t.Fatalf("search snippet includes stripped plan text: %#v", results[0].result)
	}
}

func TestSearchSessionCanReturnTitleOnlyMatch(t *testing.T) {
	saved := core.Session{
		ID: "session-2", Title: "Deployment notes", UpdatedAt: time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC),
		History: json.RawMessage(`[]`),
	}
	results := searchSession(saved, `D:\\project`, "DEPLOY")
	if len(results) != 1 || results[0].result.MatchKind != "title" || results[0].result.MessageID != "" {
		t.Fatalf("searchSession() = %#v, want one title-only result", results)
	}
}

func TestSaveAppearanceSettingsPersistsThemeAndOpacity(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := &DesktopService{store: store}

	view, err := service.SaveAppearanceSettings(AppearanceSettingsInput{
		Theme: "anime", WallpaperType: "static", PanelOpacity: 84,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Theme != "anime" || view.PanelOpacity != 84 || view.WallpaperURL != "" {
		t.Fatalf("SaveAppearanceSettings() = %#v", view)
	}
	loaded, err := store.Appearance()
	if err != nil || loaded.Theme != "anime" || loaded.PanelOpacity != 84 {
		t.Fatalf("Appearance() = %#v, %v", loaded, err)
	}
}

func TestSaveAppearanceSettingsPersistsAndServesLocalWallpaper(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("APPDATA", configRoot)
	wallpaperDir := filepath.Join(configRoot, "luneAgent", "wallpapers")
	if err := os.MkdirAll(wallpaperDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wallpaperDir, "wallpaper-1.webp"), []byte("image-data"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := &DesktopService{store: store}

	view, err := service.SaveAppearanceSettings(AppearanceSettingsInput{
		Theme: "anime", WallpaperType: "static", WallpaperFile: "wallpaper-1.webp",
		WallpaperName: "sakura-city.webp", PanelOpacity: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.WallpaperName != "sakura-city.webp" || view.WallpaperURL != "/wallpapers/wallpaper-1.webp" {
		t.Fatalf("SaveAppearanceSettings() = %#v", view)
	}
}

func TestWallpaperAssetHandlerServesOnlyWallpaperFiles(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("APPDATA", configRoot)
	wallpaperDir := filepath.Join(configRoot, "luneAgent", "wallpapers")
	if err := os.MkdirAll(wallpaperDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wallpaperDir, "wallpaper.png"), []byte("wallpaper-data"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := WallpaperAssetHandler(http.NotFoundHandler())

	request := httptest.NewRequest(http.MethodGet, "/wallpapers/wallpaper.png", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "wallpaper-data" {
		t.Fatalf("wallpaper response = %d %q", response.Code, response.Body.String())
	}

	traversal := httptest.NewRequest(http.MethodGet, "/wallpapers/../settings.db", nil)
	traversalResponse := httptest.NewRecorder()
	handler.ServeHTTP(traversalResponse, traversal)
	if traversalResponse.Code != http.StatusNotFound {
		t.Fatalf("traversal response status = %d, want 404", traversalResponse.Code)
	}
}
