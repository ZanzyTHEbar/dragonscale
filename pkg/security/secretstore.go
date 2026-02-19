package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrSecretNotFound is returned when a secret name has no entry.
var ErrSecretNotFound = errors.New("secret not found")

// SecretEntry is one stored secret, encrypted at rest.
type SecretEntry struct {
	Name       string `json:"name"`
	Ciphertext string `json:"ciphertext"` // Vault.Encrypt output (base64url)
}

// SecretStore maps logical secret names to encrypted ciphertext, persisted
// to a JSON file on disk. The Vault key is sourced from a KeyringProvider.
//
// Secrets never leave the store as plaintext except within a single scoped
// Resolve call, which zeros the returned byte slice when done. (Callers are
// responsible for this — Go does not guarantee zeroing.)
//
// Thread-safe.
type SecretStore struct {
	mu      sync.RWMutex
	path    string
	keyring KeyringProvider
	secrets map[string]SecretEntry // name → entry
}

// NewSecretStore creates a SecretStore backed by the given file path and keyring.
// The store is loaded from disk if the file exists; it is created on the first
// Set call if it does not.
func NewSecretStore(path string, keyring KeyringProvider) (*SecretStore, error) {
	ss := &SecretStore{
		path:    path,
		keyring: keyring,
		secrets: make(map[string]SecretEntry),
	}
	if err := ss.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secret store: load %s: %w", path, err)
	}
	return ss, nil
}

// Set encrypts value and stores it under name, overwriting any existing entry.
// Persists the store to disk.
func (ss *SecretStore) Set(name string, value []byte) error {
	if name == "" {
		return fmt.Errorf("secret name must not be empty")
	}
	vault, err := ss.vault()
	if err != nil {
		return err
	}
	ciphertext, err := vault.Encrypt(value)
	if err != nil {
		return fmt.Errorf("encrypt secret %q: %w", name, err)
	}

	ss.mu.Lock()
	ss.secrets[name] = SecretEntry{Name: name, Ciphertext: ciphertext}
	ss.mu.Unlock()

	return ss.save()
}

// Get decrypts and returns the secret stored under name.
// Returns ErrSecretNotFound if the name has no entry.
func (ss *SecretStore) Get(name string) ([]byte, error) {
	ss.mu.RLock()
	entry, ok := ss.secrets[name]
	ss.mu.RUnlock()

	if !ok {
		return nil, ErrSecretNotFound
	}

	vault, err := ss.vault()
	if err != nil {
		return nil, err
	}
	return vault.Decrypt(entry.Ciphertext)
}

// Delete removes the secret stored under name. No-op if it does not exist.
func (ss *SecretStore) Delete(name string) error {
	ss.mu.Lock()
	delete(ss.secrets, name)
	ss.mu.Unlock()
	return ss.save()
}

// List returns all registered secret names.
func (ss *SecretStore) List() []string {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	names := make([]string, 0, len(ss.secrets))
	for k := range ss.secrets {
		names = append(names, k)
	}
	return names
}

// Has reports whether a secret with the given name exists.
func (ss *SecretStore) Has(name string) bool {
	ss.mu.RLock()
	_, ok := ss.secrets[name]
	ss.mu.RUnlock()
	return ok
}

// vault constructs a Vault using the keyring's master key.
func (ss *SecretStore) vault() (*Vault, error) {
	key, err := ss.keyring.GetMasterKey()
	if err != nil {
		return nil, fmt.Errorf("get master key: %w", err)
	}
	return NewVault(key)
}

// load reads the store from disk. Not thread-safe — callers must hold the lock
// or call only before the store is shared.
func (ss *SecretStore) load() error {
	data, err := os.ReadFile(ss.path)
	if err != nil {
		return err
	}
	var entries []SecretEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse secret store: %w", err)
	}
	for _, e := range entries {
		ss.secrets[e.Name] = e
	}
	return nil
}

// save writes the store to disk atomically. Thread-safe.
func (ss *SecretStore) save() error {
	ss.mu.RLock()
	entries := make([]SecretEntry, 0, len(ss.secrets))
	for _, e := range ss.secrets {
		entries = append(entries, e)
	}
	ss.mu.RUnlock()

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secret store: %w", err)
	}

	dir := filepath.Dir(ss.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create secret store dir: %w", err)
	}

	tmp := ss.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write secret store: %w", err)
	}
	return os.Rename(tmp, ss.path)
}
