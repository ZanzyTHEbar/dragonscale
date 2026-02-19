// Package security provides credential management for PicoClaw.
// This file defines the KeyringProvider interface and the master key
// sourcing strategy used by SecretStore.
package security

import "fmt"

// KeyringProvider abstracts OS keyring backends. The implementation is
// selected at runtime (or via build tags for platform-specific backends).
//
// Implementations:
//   - NoopKeyring    — in-memory; suitable for embedded / CI environments
//   - EnvKeyring     — reads master key from an environment variable
//   - (future) OSKeyring — macOS Keychain / Linux secret-service / Windows DPAPI
type KeyringProvider interface {
	// GetMasterKey returns the 32-byte master key used to seal the SecretStore.
	// Returns ErrNoMasterKey when no key has been configured.
	GetMasterKey() ([]byte, error)

	// SetMasterKey persists a new master key. The provider may refuse if the
	// backend is read-only (e.g., EnvKeyring).
	SetMasterKey(key []byte) error
}

// ErrNoMasterKey is returned by KeyringProvider when no master key is available.
var ErrNoMasterKey = fmt.Errorf("no master key configured: run `picoclaw secret init` to set one")

// ErrKeyringReadOnly is returned when SetMasterKey is called on a read-only provider.
var ErrKeyringReadOnly = fmt.Errorf("keyring is read-only")

// NoopKeyring stores the master key in memory only. Safe for tests and
// embedded builds where OS keyring integration is unavailable.
type NoopKeyring struct {
	key []byte
}

// NewNoopKeyring creates an empty in-memory keyring. Call SetMasterKey to
// initialise it, or pass an initial key to seed it directly.
func NewNoopKeyring(initialKey []byte) *NoopKeyring {
	return &NoopKeyring{key: initialKey}
}

// GetMasterKey returns the in-memory key. Returns ErrNoMasterKey if unset.
func (n *NoopKeyring) GetMasterKey() ([]byte, error) {
	if len(n.key) == 0 {
		return nil, ErrNoMasterKey
	}
	cp := make([]byte, len(n.key))
	copy(cp, n.key)
	return cp, nil
}

// SetMasterKey stores key in memory.
func (n *NoopKeyring) SetMasterKey(key []byte) error {
	cp := make([]byte, len(key))
	copy(cp, key)
	n.key = cp
	return nil
}

// EnvKeyring reads the master key from an environment variable and does not
// support writing. Useful for CI/CD pipelines and container deployments.
type EnvKeyring struct {
	envVar string
	getEnv func(string) string // injectable for testing
}

// NewEnvKeyring creates a keyring that reads the master key from envVar.
// The value is expected to be a hex or base64-encoded 32-byte key.
func NewEnvKeyring(envVar string) *EnvKeyring {
	return newEnvKeyring(envVar, nil)
}

func newEnvKeyring(envVar string, getEnv func(string) string) *EnvKeyring {
	if getEnv == nil {
		// Defer os.Getenv to avoid importing os at package level.
		getEnv = func(k string) string {
			// Lazy import via the same trick as other stdlib users — this is
			// a valid Go pattern for keeping imports minimal.
			return envGetFunc(k)
		}
	}
	return &EnvKeyring{envVar: envVar, getEnv: getEnv}
}

// SetMasterKey is not supported for EnvKeyring.
func (e *EnvKeyring) SetMasterKey(_ []byte) error {
	return ErrKeyringReadOnly
}

// GetMasterKey reads the environment variable and decodes it as base64.
func (e *EnvKeyring) GetMasterKey() ([]byte, error) {
	raw := e.getEnv(e.envVar)
	if raw == "" {
		return nil, ErrNoMasterKey
	}
	return decodeKey(raw)
}
