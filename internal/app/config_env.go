package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// readConfigEnvironmentKeys validates and reads explicit references before any
// config migration. The returned values are used only for the in-memory config.
func readConfigEnvironmentKeys(document []byte) (map[string]string, error) {
	keys := make(map[string]string)
	_, _, err := mapConfigProviders(document, func(fields map[string]json.RawMessage) (bool, error) {
		raw, ok := fields[configAPIKeyEnvField]
		if !ok {
			return false, nil
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return false, fmt.Errorf("read apiKeyEnv: %w", err)
		}
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "=\x00") {
			return false, fmt.Errorf("apiKeyEnv must name a nonempty environment variable without padding, '=' or NUL")
		}
		for _, field := range []string{configAPIKeyField, configAPIKeyEncryptedField, configAPIKeyKeyringRefField} {
			if raw, exists := fields[field]; exists {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return false, fmt.Errorf("read %s: %w", field, err)
				}
				if value != "" {
					return false, fmt.Errorf("apiKeyEnv conflicts with nonempty %s; choose one credential source", field)
				}
			}
		}
		value, exists := os.LookupEnv(name)
		if !exists || strings.TrimSpace(value) == "" {
			return false, fmt.Errorf("apiKeyEnv environment variable %q is missing or empty", name)
		}
		keys[name] = value
		return false, nil
	})
	return keys, err
}
