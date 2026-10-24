//go:build !windows

package settings

import "errors"

// secretsProtected reports whether protectSecret can encrypt at rest.
const secretsProtected = false

func protectSecret(string) ([]byte, error) {
	return nil, errors.New("API key encryption is available only on Windows")
}

func unprotectSecret([]byte) (string, error) {
	return "", errors.New("API key decryption is available only on Windows")
}
