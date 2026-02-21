package security

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVault_RoundTrip(t *testing.T) {
	t.Parallel()
	key, err := GenerateKey()
	require.NoError(t, err)
	require.Len(t, key, 32)

	v, err := NewVault(key)
	require.NoError(t, err)

	tests := []string{
		"sk-abc123XYZdefghijklmnopqrs",
		"",
		"short",
		"a longer secret with spaces and special chars: !@#$%^&*()",
	}
	for _, secret := range tests {
		t.Run(secret, func(t *testing.T) {
			enc, err := v.EncryptString(secret)
			require.NoError(t, err)
			assert.NotEqual(t, secret, enc)

			dec, err := v.DecryptString(enc)
			require.NoError(t, err)
			assert.Empty(t, cmp.Diff(secret, dec))
		})
	}
}

func TestVault_DifferentCiphertexts(t *testing.T) {
	t.Parallel()
	key, _ := GenerateKey()
	v, _ := NewVault(key)

	enc1, _ := v.EncryptString("same secret")
	enc2, _ := v.EncryptString("same secret")
	assert.NotEqual(t, enc1, enc2, "random nonces should produce different ciphertexts")
}

func TestVault_WrongKey(t *testing.T) {
	t.Parallel()
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	v1, _ := NewVault(key1)
	v2, _ := NewVault(key2)

	enc, err := v1.EncryptString("secret data")
	require.NoError(t, err)

	_, err = v2.DecryptString(enc)
	assert.ErrorIs(t, err, ErrDecryptFailed)
}

func TestVault_TamperedCiphertext(t *testing.T) {
	t.Parallel()
	key, _ := GenerateKey()
	v, _ := NewVault(key)

	enc, err := v.EncryptString("tamper test")
	require.NoError(t, err)

	tampered := enc[:len(enc)-2] + "XX"
	_, err = v.DecryptString(tampered)
	assert.Error(t, err)
}

func TestVault_InvalidKeyLength(t *testing.T) {
	t.Parallel()
	_, err := NewVault([]byte("too-short"))
	assert.ErrorIs(t, err, ErrKeyLength)

	_, err = NewVault(make([]byte, 64))
	assert.ErrorIs(t, err, ErrKeyLength)
}

func TestVault_EmptyInput(t *testing.T) {
	t.Parallel()
	key, _ := GenerateKey()
	v, _ := NewVault(key)

	enc, err := v.EncryptString("")
	require.NoError(t, err)

	dec, err := v.DecryptString(enc)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("", dec))
}

func TestVault_BinaryData(t *testing.T) {
	t.Parallel()
	key, _ := GenerateKey()
	v, _ := NewVault(key)

	binary := []byte{0x00, 0x01, 0xFF, 0xFE, 0x80, 0x7F}
	enc, err := v.Encrypt(binary)
	require.NoError(t, err)

	dec, err := v.Decrypt(enc)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(binary, dec))
}
