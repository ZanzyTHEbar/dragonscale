package security

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

var (
	ErrDecryptFailed = errors.New("decryption failed: ciphertext tampered or wrong key")
	ErrKeyLength     = fmt.Errorf("key must be exactly %d bytes", chacha20poly1305.KeySize)
)

// Vault encrypts and decrypts secrets using XChaCha20-Poly1305 (AEAD).
// XChaCha20 uses a 24-byte nonce, making random nonces safe even at high volume.
type Vault struct {
	key []byte
}

// NewVault creates a Vault from a 32-byte key. Returns an error if the key
// length is wrong.
func NewVault(key []byte) (*Vault, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, ErrKeyLength
	}
	k := make([]byte, chacha20poly1305.KeySize)
	copy(k, key)
	return &Vault{key: k}, nil
}

// GenerateKey produces a cryptographically random 32-byte key suitable for NewVault.
func GenerateKey() ([]byte, error) {
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// Encrypt seals plaintext using XChaCha20-Poly1305 and returns a base64url-encoded
// string containing nonce + ciphertext.
func (v *Vault) Encrypt(plaintext []byte) (string, error) {
	aead, err := chacha20poly1305.NewX(v.key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decodes a base64url string and opens it with XChaCha20-Poly1305.
func (v *Vault) Decrypt(encoded string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	aead, err := chacha20poly1305.NewX(v.key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	if len(data) < aead.NonceSize() {
		return nil, ErrDecryptFailed
	}

	nonce := data[:aead.NonceSize()]
	ciphertext := data[aead.NonceSize():]

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// EncryptString is a convenience wrapper around Encrypt.
func (v *Vault) EncryptString(s string) (string, error) {
	return v.Encrypt([]byte(s))
}

// DecryptString is a convenience wrapper around Decrypt.
func (v *Vault) DecryptString(encoded string) (string, error) {
	plaintext, err := v.Decrypt(encoded)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
