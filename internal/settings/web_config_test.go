package settings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestWebConfigUnifiedSQLiteAndInheritance(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Empty DB yields configured=false gracefully.
	workspace := t.TempDir()
	if _, configured, err := store.EffectiveWebConfig(workspace); err != nil || configured {
		t.Fatalf("empty EffectiveWebConfig = (%v, %v), want (false, nil)", configured, err)
	}

	// Save global provider + model + API key -> stored in DB, redacted in WebConfig, restored in EffectiveWebConfig.
	err = store.SaveWebConfig("", map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"deepseek": map[string]any{
					"protocol": "responses",
					"baseURL":  "https://api.deepseek.com",
					"apiKey":   "sk-test-secret",
				},
			},
			"profiles": map[string]any{
				"chat": map[string]any{
					"provider":    "deepseek",
					"model":       "deepseek-chat",
					"displayName": "DeepSeek V3",
				},
			},
			"defaultProfile": "chat",
		},
	}, "")
	if err != nil {
		t.Fatalf("SaveWebConfig global: %v", err)
	}

	view, err := store.WebConfig("")
	if err != nil {
		t.Fatal(err)
	}
	encodedView, _ := json.Marshal(view.Document)
	if string(encodedView) == "" || len(view.SavedKeys) != 1 || view.SavedKeys[0] != "deepseek" {
		t.Fatalf("WebConfig view unexpected: %#v", view)
	}
	if _, hasAPIKey := view.Document["models"].(map[string]any)["providers"].(map[string]any)["deepseek"].(map[string]any)["apiKey"]; hasAPIKey {
		t.Fatal("WebConfig leaked apiKey in public document")
	}

	// Update omitting apiKey keeps existing secret.
	err = store.SaveWebConfig("", map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"deepseek": map[string]any{
					"protocol": "responses",
					"baseURL":  "https://api.deepseek.com",
				},
			},
			"profiles": map[string]any{
				"chat": map[string]any{
					"provider": "deepseek",
					"model":    "deepseek-chat",
				},
			},
			"defaultProfile": "chat",
		},
	}, "")
	if err != nil {
		t.Fatalf("update without apiKey: %v", err)
	}

	cfg, configured, err := store.EffectiveWebConfig(workspace)
	if err != nil || !configured {
		t.Fatalf("EffectiveWebConfig = (%v, %v)", configured, err)
	}
	if cfg.Models.Providers["deepseek"].APIKey != "sk-test-secret" {
		t.Fatalf("APIKey = %q, want sk-test-secret", cfg.Models.Providers["deepseek"].APIKey)
	}

	// Workspace override + reset.
	err = store.SaveWebConfig(workspace, map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"deepseek": map[string]any{
					"protocol": "responses",
					"baseURL":  "https://proxy.example.com",
					"apiKey":   "sk-workspace-key",
				},
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("workspace override: %v", err)
	}
	wsView, err := store.WebConfig(workspace)
	if err != nil || len(wsView.InheritedProfiles) != 1 || wsView.InheritedProfiles[0] != "chat" {
		t.Fatalf("workspace view = %#v, err=%v", wsView, err)
	}
	cfg, configured, err = store.EffectiveWebConfig(workspace)
	if err != nil || !configured || cfg.Models.Providers["deepseek"].APIKey != "sk-workspace-key" {
		t.Fatalf("workspace EffectiveWebConfig = (%#v, %v, %v)", cfg.Models.Providers["deepseek"], configured, err)
	}
	if err := store.ResetWebWorkspaceConfig(workspace); err != nil {
		t.Fatal(err)
	}
	cfg, configured, err = store.EffectiveWebConfig(workspace)
	if err != nil || !configured || cfg.Models.Providers["deepseek"].APIKey != "sk-test-secret" {
		t.Fatalf("post-reset EffectiveWebConfig = %q, err=%v", cfg.Models.Providers["deepseek"].APIKey, err)
	}

	// Explicit empty string clears key; apiKeyEnv resolves from environment.
	t.Setenv("TEST_LUNE_KEY", "sk-from-env")
	err = store.SaveWebConfig("", map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"deepseek": map[string]any{
					"protocol":  "responses",
					"baseURL":   "https://api.deepseek.com",
					"apiKeyEnv": "TEST_LUNE_KEY",
				},
			},
			"profiles": map[string]any{
				"chat": map[string]any{"provider": "deepseek", "model": "deepseek-chat"},
			},
			"defaultProfile": "chat",
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err = store.EffectiveWebConfig("")
	if err != nil || cfg.Models.Providers["deepseek"].APIKey != "sk-from-env" {
		t.Fatalf("env key = %q, err=%v", cfg.Models.Providers["deepseek"].APIKey, err)
	}
}

func TestWebImportAndConnectionsPersistence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	imp, err := store.PrepareWebImport(`{
		"models": {
			"providers": {
				"openai": {"protocol": "responses", "baseURL": "https://api.openai.com/v1", "apiKey": "sk-imported"}
			},
			"profiles": {
				"gpt5": {"provider": "openai", "model": "gpt-5"}
			},
			"defaultProfile": "gpt5"
		},
		"mcpServers": {"engram": {"transport": "stdio", "command": "engram"}}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.SuppliedKeys) != 1 || imp.SuppliedKeys[0] != "openai" || !imp.HasMCP {
		t.Fatalf("unexpected import preview: %#v", imp)
	}
	if err := store.SaveWebConfig("", imp.Document, imp.ID); err != nil {
		t.Fatal(err)
	}
	cfg, configured, err := store.EffectiveWebConfig("")
	if err != nil || !configured || cfg.Models.Providers["openai"].APIKey != "sk-imported" || cfg.MCPServers["engram"].Command != "engram" {
		t.Fatalf("imported EffectiveWebConfig failed: %#v, %v, %v", cfg, configured, err)
	}

	// Remote connections + token persistence + session memory.
	tok := "remote-bearer-token"
	if err := store.SaveWebConnection("remote", "wss://core.example.com:18092/v1/runtime", "Prod Core", &tok, true); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberWebSession("remote", "wss://core.example.com:18092/v1/runtime", "sess-123"); err != nil {
		t.Fatal(err)
	}
	conns, err := store.WebConnections()
	if err != nil || len(conns) != 1 || !conns[0].Remembered || conns[0].Session != "sess-123" {
		t.Fatalf("WebConnections = %#v, err=%v", conns, err)
	}
	gotToken, err := store.WebRemoteToken("wss://core.example.com:18092/v1/runtime")
	if err != nil || gotToken != tok {
		t.Fatalf("WebRemoteToken = %q, err=%v", gotToken, err)
	}
	if err := store.DeleteWebConnection("remote", "wss://core.example.com:18092/v1/runtime"); err != nil {
		t.Fatal(err)
	}
	if gotAfter, err := store.WebRemoteToken("wss://core.example.com:18092/v1/runtime"); err != nil || gotAfter != "" {
		t.Fatalf("token not deleted: %q, %v", gotAfter, err)
	}
}

func TestFetchWebProviderModels(t *testing.T) {
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-openai-test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"deepseek-chat","display_name":"DeepSeek Chat"}]}`))
	}))
	defer openaiSrv.Close()

	anthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sk-anth-test" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-7-sonnet-20250219","display_name":"Claude 3.7 Sonnet"}]}`))
	}))
	defer anthSrv.Close()

	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SaveWebConfig("", map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"openai": map[string]any{"protocol": "responses", "baseURL": openaiSrv.URL, "apiKey": "sk-openai-test"},
				"anth":   map[string]any{"protocol": "anthropic-messages", "baseURL": anthSrv.URL, "apiKey": "sk-anth-test"},
			},
			"profiles": map[string]any{},
		},
	}, ""); err != nil {
		t.Fatal(err)
	}

	models, err := store.FetchWebProviderModels(t.Context(), "", "openai")
	if err != nil || len(models) != 2 || models[0].ID != "deepseek-chat" || models[1].ID != "gpt-5" {
		t.Fatalf("openai models = (%#v, %v)", models, err)
	}
	anthModels, err := store.FetchWebProviderModels(t.Context(), "", "anth")
	if err != nil || len(anthModels) != 1 || anthModels[0].DisplayName != "Claude 3.7 Sonnet" {
		t.Fatalf("anthropic models = (%#v, %v)", anthModels, err)
	}
}
