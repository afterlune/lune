package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/afterlune/lune/internal/settings"
)

// Provider keys live in the user config document under
// models.providers.<name>.apiKey. The desktop app keeps its keys in settings.db
// encrypted with DPAPI; the TUI/CLI read a plain JSON file, so on a platform that
// can encrypt secrets the keys move to models.providers.<name>.apiKeyEncrypted
// and are decrypted in memory before Core reads the document.
const (
	configAPIKeyField          = "apiKey"
	configAPIKeyEncryptedField = "apiKeyEncrypted"
	configDocumentIndent       = "  "
)

// prepareUserConfig encrypts plaintext provider keys at rest and returns the
// document Core must read.
func prepareUserConfig(path string, document []byte) ([]byte, error) {
	stored := document
	protected, changed, err := protectConfigSecrets(document)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := writeUserConfig(path, protected); err != nil {
			return nil, fmt.Errorf("write protected config: %w", err)
		}
		stored = protected
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict config permissions: %w", err)
	}
	if hasPlaintextConfigSecrets(stored) {
		fmt.Fprintf(os.Stderr, "warning: %s stores provider API keys in plain text; %s\n",
			path, configProtectionHint())
	}

	revealed, revealedChanged, err := revealConfigSecrets(stored)
	if err != nil {
		return nil, err
	}
	if revealedChanged {
		return revealed, nil
	}
	return document, nil
}

// configProtectionHint explains what protects the keys on this platform.
func configProtectionHint() string {
	if settings.SecretsProtected() {
		return "keep only files you trust in this directory"
	}
	return "API key encryption is only available on Windows, so keep this file private"
}

// protectConfigSecrets replaces plaintext provider keys with encrypted ones. It
// reports changed=false when there is nothing to protect or when the platform
// cannot encrypt, so a config that cannot be protected is never rewritten.
func protectConfigSecrets(document []byte) ([]byte, bool, error) {
	if !settings.SecretsProtected() {
		return nil, false, nil
	}
	return mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		raw, ok := fields[configAPIKeyField]
		if !ok {
			return false, nil
		}
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			return false, fmt.Errorf("read apiKey: %w", err)
		}
		if key == "" {
			return false, nil
		}
		encrypted, err := settings.ProtectSecret(key)
		if err != nil {
			return false, fmt.Errorf("encrypt apiKey: %w", err)
		}
		encoded, err := json.Marshal(encrypted)
		if err != nil {
			return false, err
		}
		fields[configAPIKeyEncryptedField] = encoded
		delete(fields, configAPIKeyField)
		return true, nil
	})
}

// revealConfigSecrets restores encrypted provider keys for Core.
func revealConfigSecrets(document []byte) ([]byte, bool, error) {
	return mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		raw, ok := fields[configAPIKeyEncryptedField]
		if !ok {
			return false, nil
		}
		var encrypted []byte
		if err := json.Unmarshal(raw, &encrypted); err != nil {
			return false, fmt.Errorf("read apiKeyEncrypted: %w", err)
		}
		key, err := settings.UnprotectSecret(encrypted)
		if err != nil {
			return false, fmt.Errorf("decrypt apiKey: %w", err)
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			return false, err
		}
		fields[configAPIKeyField] = encoded
		return true, nil
	})
}

// hasPlaintextConfigSecrets reports whether any provider still stores a plain
// text key.
func hasPlaintextConfigSecrets(document []byte) bool {
	found := false
	_, _, err := mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		raw, ok := fields[configAPIKeyField]
		if !ok {
			return false, nil
		}
		var key string
		if json.Unmarshal(raw, &key) == nil && key != "" {
			found = true
		}
		return false, nil
	})
	return err == nil && found
}

// mapConfigProviders rewrites every models.providers entry mutate changes and
// returns the re-encoded document.
func mapConfigProviders(document []byte, mutate func(map[string]json.RawMessage) (bool, error)) ([]byte, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, false, fmt.Errorf("parse config document: %w", err)
	}
	models := map[string]json.RawMessage{}
	if raw, ok := root["models"]; ok {
		if err := json.Unmarshal(raw, &models); err != nil {
			return nil, false, fmt.Errorf("parse models: %w", err)
		}
	}
	providers := map[string]json.RawMessage{}
	if raw, ok := models["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return nil, false, fmt.Errorf("parse providers: %w", err)
		}
	}

	changed := false
	for name, raw := range providers {
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, false, fmt.Errorf("parse provider %q: %w", name, err)
		}
		providerChanged, err := mutate(fields)
		if err != nil {
			return nil, false, fmt.Errorf("provider %q: %w", name, err)
		}
		if !providerChanged {
			continue
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, false, err
		}
		providers[name] = encoded
		changed = true
	}
	if !changed {
		return nil, false, nil
	}

	encodedProviders, err := json.Marshal(providers)
	if err != nil {
		return nil, false, err
	}
	models["providers"] = encodedProviders
	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, false, err
	}
	root["models"] = encodedModels
	encodedRoot, err := json.MarshalIndent(root, "", configDocumentIndent)
	if err != nil {
		return nil, false, err
	}
	return append(encodedRoot, '\n'), true, nil
}

// writeUserConfig replaces the config atomically so a crash cannot truncate it.
func writeUserConfig(path string, document []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".config-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(document); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
