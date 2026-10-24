package settings

// ProtectSecret encrypts a secret with the platform credential store. Windows
// uses DPAPI; other platforms report that they cannot protect secrets at rest.
func ProtectSecret(secret string) ([]byte, error) {
	return protectSecret(secret)
}

// UnprotectSecret decrypts a secret written by ProtectSecret.
func UnprotectSecret(encrypted []byte) (string, error) {
	return unprotectSecret(encrypted)
}

// SecretsProtected reports whether the platform can encrypt secrets at rest.
func SecretsProtected() bool {
	return secretsProtected
}
