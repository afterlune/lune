package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
)

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

	document, err := prepareUserConfig(path, []byte(plaintextConfigDocument))
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

// TestPrepareUserConfigSkipsProtectionWithoutDPAPI keeps other platforms on the
// documented plaintext path instead of failing to start.
func TestPrepareUserConfigSkipsProtectionWithoutDPAPI(t *testing.T) {
	if settings.SecretsProtected() {
		t.Skip("this platform protects secrets")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plaintextConfigDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := prepareUserConfig(path, []byte(plaintextConfigDocument))
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(document) {
		t.Fatal("an unsupported platform must keep the config untouched")
	}
}

func TestPrepareUserConfigRejectsMalformedDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := prepareUserConfig(path, []byte("{not json")); err == nil {
		t.Fatal("malformed config was accepted")
	}
}

func TestPrepareUserConfigIgnoresEmptyKeys(t *testing.T) {
	document := []byte(`{"models":{"providers":{"host":{"protocol":"responses","apiKey":""}}}}`)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUserConfig(path, document)
	if err != nil {
		t.Fatalf("prepare user config: %v", err)
	}
	if hasPlaintextConfigSecrets(prepared) {
		t.Fatal("an empty key must not count as a stored secret")
	}
}
