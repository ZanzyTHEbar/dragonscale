package security

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
)

// envGetFunc is used by EnvKeyring to read environment variables.
// Decoupled to allow testing without os import in keyring.go.
func envGetFunc(key string) string {
	return os.Getenv(key)
}

// decodeKey attempts to decode a string as hex or base64 (URL or standard).
// Returns an error if the decoded length is not exactly 32 bytes.
func decodeKey(s string) ([]byte, error) {
	// Try hex first (64 chars)
	if len(s) == 64 {
		b, err := hex.DecodeString(s)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}

	// Try base64url (no padding)
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil && len(b) == 32 {
		return b, nil
	}

	// Try standard base64 with padding
	b, err = base64.StdEncoding.DecodeString(s)
	if err == nil && len(b) == 32 {
		return b, nil
	}

	return nil, fmt.Errorf("key must be a 64-char hex or 44-char base64 string encoding exactly 32 bytes")
}
