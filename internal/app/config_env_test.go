package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

type observedProviderKeyring struct {
	testProviderKeyring
	calls int
}

func (k *observedProviderKeyring) Available() bool {
	k.calls++
	return k.testProviderKeyring.Available()
}
func (k *observedProviderKeyring) Get(service, account string) ([]byte, error) {
	k.calls++
	return k.testProviderKeyring.Get(service, account)
}
func (k *observedProviderKeyring) Set(service, account string, secret []byte) error {
	k.calls++
	return k.testProviderKeyring.Set(service, account, secret)
}
func (k *observedProviderKeyring) Delete(service, account string) error {
	k.calls++
	return k.testProviderKeyring.Delete(service, account)
}

func prepareConfigWithCapturedWarning(t *testing.T, path string, document []byte, store providerKeyring) ([]byte, string, error) {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = output
	defer func() { os.Stderr = previous; output.Close() }()
	prepared, prepareErr := prepareUserConfigWithKeyring(path, document, store)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	warning, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return prepared, string(warning), prepareErr
}

func TestEnvironmentConfigKeepsKeysInMemoryWithoutKeyring(t *testing.T) {
	const variable = "LUNE_TEST_HEADLESS_API_KEY"
	const secret = "env-secret-marker-\"quoted\"\n密钥"
	t.Setenv(variable, secret)
	for _, emptyKey := range []string{"", `,"apiKey":""`} {
		t.Run("empty key field="+emptyKey, func(t *testing.T) {
			document := []byte(strings.Replace(plaintextConfigDocument, `"apiKey": "plain-secret"`, `"apiKeyEnv":"`+variable+`"`+emptyKey, 1))
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, document, 0o644); err != nil {
				t.Fatal(err)
			}
			store := &observedProviderKeyring{testProviderKeyring: testProviderKeyring{available: true}}
			prepared, warning, err := prepareConfigWithCapturedWarning(t, path, document, store)
			if err != nil {
				t.Fatalf("prepare environment config: %v", err)
			}
			if warning != "" || store.calls != 0 {
				t.Fatalf("environment-only config used the keyring or warned: calls=%d warning=%q", store.calls, warning)
			}
			var config core.ProjectConfig
			if err := json.Unmarshal(prepared, &config); err != nil {
				t.Fatal(err)
			}
			if config.Models.Providers["host"].APIKey != secret || strings.Contains(string(prepared), `"apiKeyEnv"`) {
				t.Fatal("Core did not receive the exact key with its host-only reference removed")
			}
			stored, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(stored), "env-secret-marker") || !strings.Contains(string(stored), variable) || !strings.Contains(string(stored), "serverCompaction") {
				t.Fatal("config migration failed to retain only the environment reference")
			}
			// A later process resolves a fresh value without rewriting the reference.
			t.Setenv(variable, "rotated-env-secret")
			second, warning, err := prepareConfigWithCapturedWarning(t, path, stored, store)
			if err != nil || warning != "" || store.calls != 0 {
				t.Fatalf("reload environment config: error=%v warning=%q calls=%d", err, warning, store.calls)
			}
			if err := json.Unmarshal(second, &config); err != nil || config.Models.Providers["host"].APIKey != "rotated-env-secret" {
				t.Fatal("config reload did not resolve the current environment value")
			}
			again, err := os.ReadFile(path)
			if err != nil || string(again) != string(stored) {
				t.Fatal("environment resolution rewrote the stored config")
			}
		})
	}
}

func TestInvalidEnvironmentConfigFailsBeforeMigration(t *testing.T) {
	const secret = "env-secret-must-not-appear"
	t.Setenv("LUNE_TEST_ENV_VALID", secret)
	t.Setenv("LUNE_TEST_ENV_EMPTY", "")
	t.Setenv("LUNE_TEST_ENV_WHITESPACE", " \n\t")
	t.Setenv("LUNE_TEST_ENV_MISSING", "")
	if err := os.Unsetenv("LUNE_TEST_ENV_MISSING"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, fields, want string
	}{
		{"missing variable", `"apiKeyEnv":"LUNE_TEST_ENV_MISSING"`, "LUNE_TEST_ENV_MISSING"},
		{"empty variable", `"apiKeyEnv":"LUNE_TEST_ENV_EMPTY"`, "LUNE_TEST_ENV_EMPTY"},
		{"whitespace variable", `"apiKeyEnv":"LUNE_TEST_ENV_WHITESPACE"`, "LUNE_TEST_ENV_WHITESPACE"},
		{"empty reference", `"apiKeyEnv":""`, "nonempty environment variable"},
		{"null reference", `"apiKeyEnv":null`, "nonempty environment variable"},
		{"invalid reference type", `"apiKeyEnv":42`, "read apiKeyEnv"},
		{"padded reference", `"apiKeyEnv":" LUNE_TEST_ENV_VALID "`, "without padding"},
		{"invalid reference name", `"apiKeyEnv":"KEY=VALUE"`, "environment variable"},
		{"plaintext conflict", `"apiKeyEnv":"LUNE_TEST_ENV_VALID","apiKey":"` + secret + `"`, "conflicts with nonempty apiKey"},
		{"encrypted conflict", `"apiKeyEnv":"LUNE_TEST_ENV_VALID","apiKeyEncrypted":"` + secret + `"`, "conflicts with nonempty apiKeyEncrypted"},
		{"keyring conflict", `"apiKeyEnv":"LUNE_TEST_ENV_VALID","apiKeyKeyringRef":"` + secret + `"`, "conflicts with nonempty apiKeyKeyringRef"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := []byte(`{"models":{"providers":{"host":{"protocol":"responses",` + test.fields + `}}}}`)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, document, 0o600); err != nil {
				t.Fatal(err)
			}
			store := &observedProviderKeyring{testProviderKeyring: testProviderKeyring{available: true}}
			prepared, warning, err := prepareConfigWithCapturedWarning(t, path, document, store)
			if err == nil || !strings.Contains(err.Error(), `provider "host"`) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected provider-specific %q error, got %v", test.want, err)
			}
			if prepared != nil || strings.Contains(err.Error()+warning, secret) || store.calls != 0 {
				t.Fatal("invalid environment config exposed a key or touched the keyring")
			}
			stored, readErr := os.ReadFile(path)
			if readErr != nil || string(stored) != string(document) {
				t.Fatal("invalid credentials must fail before any config migration")
			}
		})
	}
}

func TestEnvironmentConfigMixesWithExistingKeyringAndPlaintextProviders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux keyring migration")
	}
	const variable = "LUNE_TEST_MIXED_API_KEY"
	t.Setenv(variable, "environment-secret")
	document := []byte(`{"models":{"providers":{
		"environment":{"protocol":"responses","apiKeyEnv":"` + variable + `"},
		"existing":{"protocol":"responses","apiKeyKeyringRef":"existing-ref"},
		"plaintext":{"protocol":"responses","apiKey":"plain-secret"}
	}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testProviderKeyring{available: true, secrets: map[string][]byte{"existing-ref": []byte("keyring-secret")}}
	prepared, warning, err := prepareConfigWithCapturedWarning(t, path, document, store)
	if err != nil || warning != "" {
		t.Fatalf("prepare mixed sources: %v, warning=%q", err, warning)
	}
	var config core.ProjectConfig
	if err := json.Unmarshal(prepared, &config); err != nil {
		t.Fatal(err)
	}
	for provider, want := range map[string]string{"environment": "environment-secret", "existing": "keyring-secret", "plaintext": "plain-secret"} {
		if config.Models.Providers[provider].APIKey != want {
			t.Fatalf("provider %s did not resolve its own credential source", provider)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"environment-secret", "keyring-secret", "plain-secret"} {
		if strings.Contains(string(stored), key) {
			t.Fatal("mixed-source migration wrote a plaintext key to the config")
		}
	}
	if len(store.secrets) != 2 {
		t.Fatal("only the plaintext provider should create a new keyring entry")
	}
	for _, key := range store.secrets {
		if string(key) == "environment-secret" {
			t.Fatal("the environment provider must not be migrated into the keyring")
		}
	}
}
