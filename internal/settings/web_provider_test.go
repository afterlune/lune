package settings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestTestWebProviderUsesTransientCredentialsWithoutSaving(t *testing.T) {
	secret := "test-transient-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("authorization header was not set")
		}
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","display_name":"Model A","context_window":200000,"max_output_tokens":32000,"input_modalities":["text","image"]}]}`))
	}))
	defer server.Close()
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	models, err := store.TestWebProvider(context.Background(), "", ProviderTestInput{
		Provider: "temporary", Protocol: "responses", BaseURL: server.URL + "/v1", APIKey: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "model-a" || models[0].DisplayName != "Model A" ||
		models[0].ContextWindow != 200000 || models[0].MaxOutputTokens != 32000 || !models[0].SupportsInputImages || models[0].SupportsInputAudio {
		t.Fatalf("test result = %#v", models)
	}
	view, err := store.WebConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SavedKeys) != 0 {
		t.Fatalf("transient credentials were persisted: %#v", view.SavedKeys)
	}
}

func TestTestWebProviderDoesNotFollowRedirects(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetCalled = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.TestWebProvider(context.Background(), "", ProviderTestInput{Provider: "temporary", Protocol: "responses", BaseURL: redirect.URL + "/v1"})
	if err == nil {
		t.Fatal("expected redirect to be rejected")
	}
	if targetCalled {
		t.Fatal("provider test followed a redirect")
	}
}
