package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	"github.com/go-keyring/keyring"
)

// Provider keys live under models.providers.<name>.apiKey or are referenced by
// apiKeyEnv. The desktop app keeps keys in settings.db encrypted with DPAPI.
// The TUI/CLI migrate keys into the platform credential store when available
// and resolve credentials only in memory for Core.
const (
	configAPIKeyField           = "apiKey"
	configAPIKeyEnvField        = "apiKeyEnv"
	configAPIKeyEncryptedField  = "apiKeyEncrypted"
	configAPIKeyKeyringRefField = "apiKeyKeyringRef"
	configKeyringService        = "luneAgent"
	configDocumentIndent        = "  "
)

var errConfigKeyringUnavailable = errors.New("provider keyring unavailable")

type providerKeyring interface {
	Available() bool
	Get(service, account string) ([]byte, error)
	Set(service, account string, secret []byte) error
	Delete(service, account string) error
}

type systemProviderKeyring struct{}

func (systemProviderKeyring) Available() bool { return keyring.Available() }
func (systemProviderKeyring) Get(service, account string) ([]byte, error) {
	return keyring.Get(service, account)
}
func (systemProviderKeyring) Set(service, account string, secret []byte) error {
	return keyring.Set(service, account, secret)
}
func (systemProviderKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type configSecretProtection struct {
	document     []byte
	changed      bool
	createdRefs  []string
	replacedRefs []string
}

// prepareUserConfig moves provider keys out of the user config when the
// platform has a secure credential store, then returns the document Core must
// read with the keys restored in memory.
func prepareUserConfig(path string, document []byte) ([]byte, error) {
	return prepareUserConfigWithDiagnostics(path, document, os.Stderr)
}

func prepareUserConfigWithDiagnostics(path string, document []byte, diagnostics io.Writer) ([]byte, error) {
	return prepareUserConfigWithKeyringAndDiagnostics(path, document, systemProviderKeyring{}, diagnostics)
}

func prepareUserConfigWithKeyring(path string, document []byte, store providerKeyring) ([]byte, error) {
	return prepareUserConfigWithKeyringAndDiagnostics(path, document, store, os.Stderr)
}

func prepareUserConfigWithKeyringAndDiagnostics(path string, document []byte, store providerKeyring, diagnostics io.Writer) ([]byte, error) {
	preflight, err := preflightUserConfig(document, store)
	if err != nil {
		return nil, err
	}
	return persistPreparedUserConfig(path, preflight, store, diagnostics)
}

func prepareValidatedUserConfig(path string, document []byte, diagnostics io.Writer) ([]byte, error) {
	return prepareValidatedUserConfigWithKeyring(path, document, systemProviderKeyring{}, diagnostics)
}

func prepareValidatedUserConfigWithKeyring(path string, document []byte, store providerKeyring, diagnostics io.Writer) ([]byte, error) {
	preflight, err := preflightUserConfig(document, store)
	if err != nil {
		return nil, err
	}
	var projectConfig core.ProjectConfig
	if err := json.Unmarshal(preflight.candidate, &projectConfig); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}
	if err := projectConfig.Validate(); err != nil {
		return nil, fmt.Errorf("validate project config: %w", err)
	}
	return persistPreparedUserConfig(path, preflight, store, diagnostics)
}

type userConfigPreflight struct {
	environmentKeys map[string]string
	defaulted       []byte
	candidate       []byte
	defaultsChanged bool
}

func preflightUserConfig(document []byte, store providerKeyring) (userConfigPreflight, error) {
	environmentKeys, err := readConfigEnvironmentKeys(document)
	if err != nil {
		return userConfigPreflight{}, err
	}
	defaulted, defaultsChanged, err := defaultResponsesServerCompaction(document)
	if err != nil {
		return userConfigPreflight{}, err
	}
	candidate, changed, err := revealConfigSecrets(defaulted, store, environmentKeys)
	if err != nil {
		return userConfigPreflight{}, err
	}
	if !changed {
		candidate = defaulted
	}
	return userConfigPreflight{
		environmentKeys: environmentKeys, defaulted: defaulted,
		candidate: candidate, defaultsChanged: defaultsChanged,
	}, nil
}

func persistPreparedUserConfig(path string, preflight userConfigPreflight, store providerKeyring, diagnostics io.Writer) ([]byte, error) {
	stored := preflight.defaulted
	protection, err := protectConfigSecrets(preflight.defaulted, store)
	if err != nil {
		return nil, err
	}
	writeConfig := preflight.defaultsChanged
	if protection.changed {
		stored = protection.document
		writeConfig = true
	}
	if writeConfig {
		if err := writeUserConfig(path, stored); err != nil {
			deleteConfigKeyringRefs(store, protection.createdRefs)
			return nil, fmt.Errorf("write protected config: %w", err)
		}
		deleteConfigKeyringRefs(store, protection.replacedRefs)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict config permissions: %w", err)
	}
	if hasPlaintextConfigSecrets(stored) {
		if diagnostics != nil {
			_, _ = fmt.Fprintf(diagnostics, "warning: %s stores provider API keys in plain text; %s\n",
				path, configProtectionHint())
		}
	}

	revealed, revealedChanged, err := revealConfigSecrets(stored, store, preflight.environmentKeys)
	if err != nil {
		return nil, err
	}
	if revealedChanged {
		return revealed, nil
	}
	return stored, nil
}

// defaultResponsesServerCompaction enables provider compaction for existing
// Responses configs that predate the option. Explicit true and false values are
// both preserved so users can still disable an incompatible endpoint.
func defaultResponsesServerCompaction(document []byte) ([]byte, bool, error) {
	defaulted, changed, err := mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		rawProtocol, ok := fields["protocol"]
		if !ok {
			return false, nil
		}
		var protocol string
		if err := json.Unmarshal(rawProtocol, &protocol); err != nil {
			return false, fmt.Errorf("read protocol: %w", err)
		}
		if protocol != "responses" {
			return false, nil
		}
		if raw, exists := fields["serverCompaction"]; exists {
			var configured *bool
			if err := json.Unmarshal(raw, &configured); err != nil {
				return false, fmt.Errorf("read serverCompaction: %w", err)
			}
			if configured != nil {
				return false, nil
			}
		}
		encoded, err := json.Marshal(true)
		if err != nil {
			return false, err
		}
		fields["serverCompaction"] = encoded
		return true, nil
	})
	if err != nil || !changed {
		return document, false, err
	}
	return defaulted, true, nil
}

// configProtectionHint explains what protects the keys on this platform.
func configProtectionHint() string {
	if settings.SecretsProtected() {
		return "keep only files you trust in this directory"
	}
	if runtime.GOOS == "linux" {
		return "Linux Secret Service is unavailable or could not store the key; use apiKeyEnv for environment-provided keys, or keep this file private"
	}
	return "use apiKeyEnv for environment-provided keys, or keep this file private"
}

// protectConfigSecrets removes plaintext provider keys when a secure store is
// available. A failed Secret Service write leaves the original document intact
// so startup can continue with the existing plaintext fallback and warning.
func protectConfigSecrets(document []byte, store providerKeyring) (configSecretProtection, error) {
	if settings.SecretsProtected() {
		protected, changed, err := mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
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
		return configSecretProtection{document: protected, changed: changed}, err
	}
	if runtime.GOOS != "linux" || store == nil || !hasPlaintextConfigSecrets(document) || !store.Available() {
		return configSecretProtection{}, nil
	}

	createdRefs := make([]string, 0)
	replacedRefs := make([]string, 0)
	protected, changed, err := mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
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

		reference, err := newConfigKeyringReference()
		if err != nil {
			return false, fmt.Errorf("create keyring reference: %w", err)
		}
		if err := store.Set(configKeyringService, reference, []byte(key)); err != nil {
			return false, fmt.Errorf("%w: %v", errConfigKeyringUnavailable, err)
		}
		createdRefs = append(createdRefs, reference)

		if oldRaw, ok := fields[configAPIKeyKeyringRefField]; ok {
			var oldReference string
			if err := json.Unmarshal(oldRaw, &oldReference); err != nil {
				return false, fmt.Errorf("read apiKeyKeyringRef: %w", err)
			}
			if oldReference != "" && oldReference != reference {
				replacedRefs = append(replacedRefs, oldReference)
			}
		}
		encoded, err := json.Marshal(reference)
		if err != nil {
			return false, err
		}
		fields[configAPIKeyKeyringRefField] = encoded
		delete(fields, configAPIKeyField)
		return true, nil
	})
	if err != nil {
		deleteConfigKeyringRefs(store, createdRefs)
		if errors.Is(err, errConfigKeyringUnavailable) {
			return configSecretProtection{}, nil
		}
		return configSecretProtection{}, err
	}
	return configSecretProtection{
		document: protected, changed: changed,
		createdRefs: createdRefs, replacedRefs: replacedRefs,
	}, nil
}

func newConfigKeyringReference() (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func deleteConfigKeyringRefs(store providerKeyring, references []string) {
	if store == nil {
		return
	}
	for _, reference := range references {
		_ = store.Delete(configKeyringService, reference)
	}
}

// revealConfigSecrets resolves provider keys only in the document handed to Core.
// References that cannot be resolved are errors, never empty API keys.
func revealConfigSecrets(document []byte, store providerKeyring, environmentKeys map[string]string) ([]byte, bool, error) {
	return mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		if raw, ok := fields[configAPIKeyEnvField]; ok {
			var name string
			if err := json.Unmarshal(raw, &name); err != nil {
				return false, fmt.Errorf("read apiKeyEnv: %w", err)
			}
			key, ok := environmentKeys[name]
			if !ok {
				return false, fmt.Errorf("apiKeyEnv %q was not resolved", name)
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return false, err
			}
			fields[configAPIKeyField] = encoded
			delete(fields, configAPIKeyEnvField)
			return true, nil
		}
		changed := false
		if raw, ok := fields[configAPIKeyEncryptedField]; ok {
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
			changed = true
		}
		if raw, ok := fields[configAPIKeyKeyringRefField]; ok {
			if plain, exists := fields[configAPIKeyField]; exists {
				var key string
				if err := json.Unmarshal(plain, &key); err != nil {
					return false, fmt.Errorf("read apiKey: %w", err)
				}
				if key != "" {
					delete(fields, configAPIKeyKeyringRefField)
					return true, nil
				}
			}
			var reference string
			if err := json.Unmarshal(raw, &reference); err != nil {
				return false, fmt.Errorf("read apiKeyKeyringRef: %w", err)
			}
			if reference == "" {
				return false, errors.New("apiKeyKeyringRef is empty")
			}
			if store == nil {
				return false, errors.New("resolve apiKey: Linux Secret Service is unavailable")
			}
			secret, err := store.Get(configKeyringService, reference)
			if err != nil {
				return false, fmt.Errorf("resolve apiKey from Linux Secret Service: %w", err)
			}
			if len(secret) == 0 {
				return false, errors.New("resolve apiKey from Linux Secret Service: stored key is empty")
			}
			encoded, err := json.Marshal(string(secret))
			if err != nil {
				return false, err
			}
			fields[configAPIKeyField] = encoded
			delete(fields, configAPIKeyKeyringRefField)
			changed = true
		}
		return changed, nil
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
