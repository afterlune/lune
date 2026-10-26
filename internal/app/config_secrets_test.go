package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
)

type testProviderKeyring struct {
	available bool
	secrets   map[string][]byte
	setErr    error
}

func (k *testProviderKeyring) Available() bool { return k.available }
func (k *testProviderKeyring) Get(_, account string) ([]byte, error) {
	secret, ok := k.secrets[account]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return append([]byte(nil), secret...), nil
}
func (k *testProviderKeyring) Set(_, account string, secret []byte) error {
	if k.setErr != nil {
		return k.setErr
	}
	if k.secrets == nil {
		k.secrets = make(map[string][]byte)
	}
	k.secrets[account] = append([]byte(nil), secret...)
	return nil
}
func (k *testProviderKeyring) Delete(_, account string) error {
	delete(k.secrets, account)
	return nil
}

// plaintextConfigDocument mirrors the shape of a user config with a plain key.
const plaintextConfigDocument = `{
  "models": {
    "providers": {"host": {"protocol": "responses", "baseURL": "https://example.invalid", "apiKey": "plain-secret"}},
    "profiles": {"primary": {"provider": "host", "model": "test-model"}},
    "defaultProfile": "primary"
  },
  "runtime": {}
}
`

// TestPrepareUserConfigKeepsTheKeyReadable covers the contract with Core: after
// preparation the document still exposes the original key.
func TestPrepareUserConfigKeepsTheKeyReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
		t.Fatal(err)
	}

	document, err := prepareUserConfigWithKeyring(path, []byte(plaintextConfigDocument), &testProviderKeyring{})
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	if !strings.Contains(string(document), "plain-secret") {
		t.Fatalf("the document handed to Core lost the key: %s", document)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX mode bits, so the owner-only check only holds where
	// the platform implements them.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions = %v, want owner-only", info.Mode().Perm())
	}
}

// TestPrepareUserConfigProtectsKeysOnWindows covers the at-rest migration.
func TestPrepareUserConfigProtectsKeysOnWindows(t *testing.T) {
	if !settings.SecretsProtected() {
		t.Skip("secret protection is only available on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUserConfig(path, []byte(plaintextConfigDocument)); err != nil {
		t.Fatalf("prepare user config: %v", err)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "plain-secret") {
		t.Fatalf("the stored config kept the plain key: %s", stored)
	}
	if !strings.Contains(string(stored), configAPIKeyEncryptedField) {
		t.Fatalf("the stored config has no encrypted key: %s", stored)
	}

	// The next start must read the same key back out of the protected file.
	second, err := prepareUserConfig(path, stored)
	if err != nil {
		t.Fatalf("prepare protected config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(second, &document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(second), "plain-secret") {
		t.Fatalf("the protected config did not decrypt: %s", second)
	}
	// A protected file must not be rewritten on every start.
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(stored) {
		t.Fatal("a protected config was rewritten on every start")
	}
}

// TestPrepareUserConfigKeepsPlaintextWithoutDPAPI keeps other platforms on the
// documented plaintext path while applying config defaults.
func TestPrepareUserConfigKeepsPlaintextWithoutDPAPI(t *testing.T) {
	if settings.SecretsProtected() || runtime.GOOS == "linux" {
		t.Skip("Windows uses DPAPI and Linux may use Secret Service")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := prepareUserConfigWithKeyring(path, []byte(plaintextConfigDocument), &testProviderKeyring{})
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(document) {
		t.Fatal("the in-memory and stored config should match")
	}
	if !strings.Contains(string(stored), `"serverCompaction":true`) {
		t.Fatalf("Responses provider was not migrated to server compaction by default: %s", stored)
	}
	if !strings.Contains(string(stored), "plain-secret") || strings.Contains(string(stored), configAPIKeyEncryptedField) {
		t.Fatalf("unsupported platform should retain the plaintext key: %s", stored)
	}
}

func TestPrepareUserConfigDefaultsServerCompactionForResponsesProviders(t *testing.T) {
	document := []byte(`{"models":{"providers":{"deepseek":{"protocol":"responses"},"disabled":{"protocol":"responses","serverCompaction":false},"anthropic":{"protocol":"anthropic-messages"}}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUserConfigWithKeyring(path, document, &testProviderKeyring{})
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared) != string(stored) {
		t.Fatalf("prepared config differs from persisted migration:\nprepared: %s\nstored: %s", prepared, stored)
	}

	var root struct {
		Models struct {
			Providers map[string]map[string]json.RawMessage `json:"providers"`
		} `json:"models"`
	}
	if err := json.Unmarshal(stored, &root); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := json.Unmarshal(root.Models.Providers["deepseek"]["serverCompaction"], &enabled); err != nil || !enabled {
		t.Fatalf("missing Responses setting = %v (error %v), want true", enabled, err)
	}
	if err := json.Unmarshal(root.Models.Providers["disabled"]["serverCompaction"], &enabled); err != nil || enabled {
		t.Fatalf("explicit false setting = %v (error %v), want false", enabled, err)
	}
	if _, exists := root.Models.Providers["anthropic"]["serverCompaction"]; exists {
		t.Fatal("non-Responses provider received a server compaction default")
	}
}

func TestPrepareUserConfigMigratesKeysToLinuxSecretService(t *testing.T) {
	if runtime.GOOS != "linux" || settings.SecretsProtected() {
		t.Skip("Linux Secret Service migration only applies on Linux")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &testProviderKeyring{available: true}

	document, err := prepareUserConfigWithKeyring(path, []byte(plaintextConfigDocument), store)
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "plain-secret") || !strings.Contains(string(stored), configAPIKeyKeyringRefField) {
		t.Fatalf("stored config should contain only a keyring reference: %s", stored)
	}
	if !strings.Contains(string(document), "plain-secret") || strings.Contains(string(document), configAPIKeyKeyringRefField) {
		t.Fatalf("Core document should contain the key but not the keyring reference: %s", document)
	}
	if len(store.secrets) != 1 {
		t.Fatalf("stored keyring items = %d, want 1", len(store.secrets))
	}
	for _, secret := range store.secrets {
		if string(secret) != "plain-secret" {
			t.Fatalf("keyring secret = %q, want original API key", secret)
		}
	}

	second, err := prepareUserConfigWithKeyring(path, stored, store)
	if err != nil {
		t.Fatalf("prepare keyring-backed config: %v", err)
	}
	if !strings.Contains(string(second), "plain-secret") {
		t.Fatalf("keyring-backed config did not resolve: %s", second)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(stored) {
		t.Fatal("keyring-backed config was rewritten on every start")
	}
}

func TestPrepareUserConfigFallsBackWhenLinuxSecretServiceCannotStore(t *testing.T) {
	if runtime.GOOS != "linux" || settings.SecretsProtected() {
		t.Skip("Linux Secret Service fallback only applies on Linux")
	}
	for _, test := range []struct {
		name  string
		store *testProviderKeyring
	}{
		{name: "unavailable", store: &testProviderKeyring{}},
		{name: "store failure", store: &testProviderKeyring{available: true, setErr: errors.New("service locked")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
				t.Fatal(err)
			}
			warningFile, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			previousStderr := os.Stderr
			os.Stderr = warningFile
			t.Cleanup(func() { os.Stderr = previousStderr })

			document, err := prepareUserConfigWithKeyring(path, []byte(plaintextConfigDocument), test.store)
			if err != nil {
				t.Fatalf("prepare user config: %v", err)
			}
			if err := warningFile.Close(); err != nil {
				t.Fatal(err)
			}
			os.Stderr = previousStderr
			warning, err := os.ReadFile(warningFile.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(warning), "stores provider API keys in plain text") || !strings.Contains(string(warning), "Secret Service") || !strings.Contains(string(warning), "apiKeyEnv") {
				t.Fatalf("warning = %q, want plaintext and Secret Service fallback guidance", warning)
			}
			if !strings.Contains(string(document), "plain-secret") {
				t.Fatalf("fallback document lost the API key: %s", document)
			}
			stored, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root struct {
				Models struct {
					Providers map[string]map[string]json.RawMessage `json:"providers"`
				} `json:"models"`
			}
			if err := json.Unmarshal(stored, &root); err != nil {
				t.Fatal(err)
			}
			var key string
			if err := json.Unmarshal(root.Models.Providers["host"][configAPIKeyField], &key); err != nil || key != "plain-secret" {
				t.Fatalf("fallback API key = %q (error %v), want plaintext key", key, err)
			}
			var enabled bool
			if err := json.Unmarshal(root.Models.Providers["host"]["serverCompaction"], &enabled); err != nil || !enabled {
				t.Fatalf("Responses default = %v (error %v), want true", enabled, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("config permissions = %v, want owner-only", info.Mode().Perm())
			}
		})
	}
}

func TestPrepareUserConfigFailsClosedForMissingKeyringReference(t *testing.T) {
	if runtime.GOOS != "linux" || settings.SecretsProtected() {
		t.Skip("keyring references are created on Linux")
	}
	document := []byte(`{"models":{"providers":{"host":{"apiKeyKeyringRef":"missing"}}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareUserConfigWithKeyring(path, document, &testProviderKeyring{available: true})
	if err == nil || !strings.Contains(err.Error(), "resolve apiKey from Linux Secret Service") {
		t.Fatalf("missing keyring reference error = %v, want fail-closed resolution error", err)
	}
}

func TestPrepareUserConfigRejectsMalformedDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := prepareUserConfigWithKeyring(path, []byte("{not json"), &testProviderKeyring{}); err == nil {
		t.Fatal("malformed config was accepted")
	}
}

func TestPrepareUserConfigIgnoresEmptyKeys(t *testing.T) {
	document := []byte(`{"models":{"providers":{"host":{"protocol":"responses","apiKey":""}}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUserConfigWithKeyring(path, document, &testProviderKeyring{})
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	if hasPlaintextConfigSecrets(prepared) {
		t.Fatal("an empty key must not count as a stored secret")
	}
}
