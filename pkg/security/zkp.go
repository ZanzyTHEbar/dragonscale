// Package security provides the Schnorr ZKP session handshake for daemon
// authentication. The protocol proves knowledge of a shared secret (derived
// from the master key) without revealing it, in a single round-trip (~200 bytes).
//
// Protocol (Schnorr identification on P-256):
//
//	Prover                              Verifier
//	──────                              ────────
//	k ← rand; R = k·G              →   (commitment)
//	                                ←   c (32-byte challenge)
//	s = k − c·x mod n              →   (response)
//	                                    s·G + c·Y == R ?
//
// On success the verifier issues a session token (random 32 bytes, TTL 1h).
package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

var curve = elliptic.P256()

// SchnorrKeypair derives a Schnorr keypair from a 32-byte master key.
// x = SHA-256(masterKey || "schnorr-zkp") mod n
// Y = x·G
func SchnorrKeypair(masterKey []byte) (*big.Int, *ecdsa.PublicKey, error) {
	if len(masterKey) != 32 {
		return nil, nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}

	h := sha256.New()
	h.Write(masterKey)
	h.Write([]byte("schnorr-zkp"))
	xBytes := h.Sum(nil)

	x := new(big.Int).SetBytes(xBytes)
	x.Mod(x, curve.Params().N)

	if x.Sign() == 0 {
		return nil, nil, fmt.Errorf("degenerate key (zero scalar)")
	}

	px, py := curve.ScalarBaseMult(x.Bytes())
	pub := &ecdsa.PublicKey{
		Curve: curve,
		X:     px,
		Y:     py,
	}
	return x, pub, nil
}

// SchnorrCommitment is the prover's initial message.
type SchnorrCommitment struct {
	RX, RY []byte // compressed point R = k·G
	k      *big.Int
}

// ProverCommit generates a random nonce and returns the commitment.
func ProverCommit() (*SchnorrCommitment, error) {
	k, err := rand.Int(rand.Reader, curve.Params().N)
	if err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	rx, ry := curve.ScalarBaseMult(k.Bytes())
	return &SchnorrCommitment{
		RX: rx.Bytes(),
		RY: ry.Bytes(),
		k:  k,
	}, nil
}

// ProverRespond computes the response s = k - c·x mod n.
func ProverRespond(commit *SchnorrCommitment, challenge []byte, secretKey *big.Int) ([]byte, error) {
	n := curve.Params().N
	c := new(big.Int).SetBytes(challenge)
	c.Mod(c, n)

	cx := new(big.Int).Mul(c, secretKey)
	cx.Mod(cx, n)

	s := new(big.Int).Sub(commit.k, cx)
	s.Mod(s, n)

	sBytes := make([]byte, 32)
	sBuf := s.Bytes()
	copy(sBytes[32-len(sBuf):], sBuf)
	return sBytes, nil
}

// VerifierChallenge generates a random 32-byte challenge.
func VerifierChallenge() ([]byte, error) {
	c := make([]byte, 32)
	if _, err := rand.Read(c); err != nil {
		return nil, fmt.Errorf("generate challenge: %w", err)
	}
	return c, nil
}

// VerifierCheck verifies the Schnorr proof: s·G + c·Y == R.
func VerifierCheck(pubKey *ecdsa.PublicKey, rx, ry, challenge, response []byte) bool {
	n := curve.Params().N

	rX := new(big.Int).SetBytes(rx)
	rY := new(big.Int).SetBytes(ry)

	if rX.Sign() == 0 && rY.Sign() == 0 {
		return false
	}
	if !curve.IsOnCurve(rX, rY) {
		return false
	}

	s := new(big.Int).SetBytes(response)
	s.Mod(s, n)

	c := new(big.Int).SetBytes(challenge)
	c.Mod(c, n)

	// s·G
	sgx, sgy := curve.ScalarBaseMult(s.Bytes())

	// c·Y
	cyx, cyy := curve.ScalarMult(pubKey.X, pubKey.Y, c.Bytes())

	// s·G + c·Y
	checkX, checkY := curve.Add(sgx, sgy, cyx, cyy)

	return checkX.Cmp(rX) == 0 && checkY.Cmp(rY) == 0
}

// SessionToken is issued on successful ZKP handshake.
type SessionToken struct {
	Token     [32]byte
	ExpiresAt time.Time
}

// IsValid checks whether the token has not expired.
func (st SessionToken) IsValid() bool {
	return time.Now().Before(st.ExpiresAt)
}

// TokenHex returns the token as a hex string.
func (st SessionToken) TokenHex() string {
	return fmt.Sprintf("%x", st.Token)
}

// SessionManager tracks active session tokens for daemon auth.
type ZKPSessionManager struct {
	mu       sync.RWMutex
	pubKey   *ecdsa.PublicKey
	sessions map[[32]byte]SessionToken
	ttl      time.Duration
}

// NewZKPSessionManager creates a session manager for the given public key.
func NewZKPSessionManager(pubKey *ecdsa.PublicKey, ttl time.Duration) *ZKPSessionManager {
	if ttl == 0 {
		ttl = time.Hour
	}
	return &ZKPSessionManager{
		pubKey:   pubKey,
		sessions: make(map[[32]byte]SessionToken),
		ttl:      ttl,
	}
}

// VerifyAndIssue performs the verifier side of the ZKP handshake.
// On success, returns a new session token.
func (sm *ZKPSessionManager) VerifyAndIssue(rx, ry, challenge, response []byte) (SessionToken, error) {
	if !VerifierCheck(sm.pubKey, rx, ry, challenge, response) {
		return SessionToken{}, errors.New("ZKP verification failed: invalid proof")
	}

	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return SessionToken{}, fmt.Errorf("generate session token: %w", err)
	}

	st := SessionToken{
		Token:     token,
		ExpiresAt: time.Now().Add(sm.ttl),
	}

	sm.mu.Lock()
	sm.sessions[token] = st
	sm.mu.Unlock()

	return st, nil
}

// ValidateToken checks whether a token is known and not expired.
func (sm *ZKPSessionManager) ValidateToken(token [32]byte) bool {
	sm.mu.RLock()
	st, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if !ok {
		return false
	}
	if !st.IsValid() {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return false
	}
	return true
}

// RevokeToken removes a session token.
func (sm *ZKPSessionManager) RevokeToken(token [32]byte) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// Cleanup removes all expired sessions.
func (sm *ZKPSessionManager) Cleanup() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	expired := 0
	for k, st := range sm.sessions {
		if !st.IsValid() {
			delete(sm.sessions, k)
			expired++
		}
	}
	return expired
}

// ActiveSessions returns the number of active sessions.
func (sm *ZKPSessionManager) ActiveSessions() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// HandshakePayload is the wire format for the ZKP handshake over the socket.
// Total: 32 + 32 + 32 + 32 + 32 = 160 bytes (under 200-byte target).
type HandshakePayload struct {
	RX        [32]byte `json:"rx"`
	RY        [32]byte `json:"ry"`
	Challenge [32]byte `json:"challenge"`
	Response  [32]byte `json:"response"`
	Phase     uint8    `json:"phase"` // 1=commit, 2=challenge, 3=response
}

// MarshalBinary encodes the handshake payload as a compact binary frame.
// Format: [1 phase][32 RX][32 RY][32 challenge][32 response] = 129 bytes
func (hp HandshakePayload) MarshalBinary() []byte {
	buf := make([]byte, 129)
	buf[0] = hp.Phase
	copy(buf[1:33], hp.RX[:])
	copy(buf[33:65], hp.RY[:])
	copy(buf[65:97], hp.Challenge[:])
	copy(buf[97:129], hp.Response[:])
	return buf
}

// UnmarshalBinaryHandshake decodes a compact binary handshake payload.
func UnmarshalBinaryHandshake(data []byte) (HandshakePayload, error) {
	if len(data) < 129 {
		return HandshakePayload{}, fmt.Errorf("handshake payload too short: %d < 129", len(data))
	}
	var hp HandshakePayload
	hp.Phase = data[0]
	copy(hp.RX[:], data[1:33])
	copy(hp.RY[:], data[33:65])
	copy(hp.Challenge[:], data[65:97])
	copy(hp.Response[:], data[97:129])
	return hp, nil
}

// HandshakeResult is the verifier's response after a successful handshake.
type HandshakeResult struct {
	SessionToken [32]byte `json:"session_token"`
	ExpiresUnix  int64    `json:"expires_unix"`
}

// MarshalBinary encodes the result as [32 token][8 expires_unix] = 40 bytes.
func (hr HandshakeResult) MarshalBinary() []byte {
	buf := make([]byte, 40)
	copy(buf[:32], hr.SessionToken[:])
	binary.BigEndian.PutUint64(buf[32:40], uint64(hr.ExpiresUnix))
	return buf
}

// UnmarshalBinaryResult decodes a binary handshake result.
func UnmarshalBinaryResult(data []byte) (HandshakeResult, error) {
	if len(data) < 40 {
		return HandshakeResult{}, fmt.Errorf("handshake result too short: %d < 40", len(data))
	}
	var hr HandshakeResult
	copy(hr.SessionToken[:], data[:32])
	hr.ExpiresUnix = int64(binary.BigEndian.Uint64(data[32:40]))
	return hr, nil
}
