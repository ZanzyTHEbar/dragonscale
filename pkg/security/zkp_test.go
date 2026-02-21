package security

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchnorrKeypair(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	x, pub, err := SchnorrKeypair(key)
	require.NoError(t, err)
	assert.NotNil(t, x)
	assert.NotNil(t, pub)
	assert.True(t, curve.IsOnCurve(pub.X, pub.Y))
}

func TestSchnorrKeypairRejectsBadLength(t *testing.T) {
	t.Parallel()
	_, _, err := SchnorrKeypair([]byte("short"))
	assert.Error(t, err)
}

func TestSchnorrFullHandshake(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 42)
	}

	x, pub, err := SchnorrKeypair(masterKey)
	require.NoError(t, err)

	commit, err := ProverCommit()
	require.NoError(t, err)

	challenge, err := VerifierChallenge()
	require.NoError(t, err)

	response, err := ProverRespond(commit, challenge, x)
	require.NoError(t, err)

	valid := VerifierCheck(pub, commit.RX, commit.RY, challenge, response)
	assert.True(t, valid, "valid proof should verify")
}

func TestSchnorrRejectsWrongKey(t *testing.T) {
	t.Parallel()
	masterKey1 := make([]byte, 32)
	masterKey2 := make([]byte, 32)
	for i := range masterKey1 {
		masterKey1[i] = byte(i)
		masterKey2[i] = byte(i + 100)
	}

	x1, _, err := SchnorrKeypair(masterKey1)
	require.NoError(t, err)
	_, pub2, err := SchnorrKeypair(masterKey2)
	require.NoError(t, err)

	commit, err := ProverCommit()
	require.NoError(t, err)
	challenge, err := VerifierChallenge()
	require.NoError(t, err)
	response, err := ProverRespond(commit, challenge, x1)
	require.NoError(t, err)

	valid := VerifierCheck(pub2, commit.RX, commit.RY, challenge, response)
	assert.False(t, valid, "proof with wrong key should fail")
}

func TestZKPSessionManagerIssueAndValidate(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 7)
	}

	x, pub, err := SchnorrKeypair(masterKey)
	require.NoError(t, err)

	sm := NewZKPSessionManager(pub, time.Hour)

	commit, _ := ProverCommit()
	challenge, _ := VerifierChallenge()
	response, _ := ProverRespond(commit, challenge, x)

	st, err := sm.VerifyAndIssue(commit.RX, commit.RY, challenge, response)
	require.NoError(t, err)
	assert.True(t, st.IsValid())
	assert.Empty(t, cmp.Diff(1, sm.ActiveSessions()))

	assert.True(t, sm.ValidateToken(st.Token))
}

func TestZKPSessionManagerRejectsInvalidProof(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	_, pub, _ := SchnorrKeypair(masterKey)
	sm := NewZKPSessionManager(pub, time.Hour)

	_, err := sm.VerifyAndIssue(make([]byte, 32), make([]byte, 32), make([]byte, 32), make([]byte, 32))
	assert.Error(t, err)
	assert.Empty(t, cmp.Diff(0, sm.ActiveSessions()))
}

func TestZKPSessionManagerExpiry(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 3)
	}
	x, pub, _ := SchnorrKeypair(masterKey)
	sm := NewZKPSessionManager(pub, 1*time.Millisecond)

	commit, _ := ProverCommit()
	challenge, _ := VerifierChallenge()
	response, _ := ProverRespond(commit, challenge, x)

	st, err := sm.VerifyAndIssue(commit.RX, commit.RY, challenge, response)
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)
	assert.False(t, sm.ValidateToken(st.Token))
}

func TestZKPSessionManagerRevoke(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 5)
	}
	x, pub, _ := SchnorrKeypair(masterKey)
	sm := NewZKPSessionManager(pub, time.Hour)

	commit, _ := ProverCommit()
	challenge, _ := VerifierChallenge()
	response, _ := ProverRespond(commit, challenge, x)

	st, _ := sm.VerifyAndIssue(commit.RX, commit.RY, challenge, response)
	assert.True(t, sm.ValidateToken(st.Token))

	sm.RevokeToken(st.Token)
	assert.False(t, sm.ValidateToken(st.Token))
	assert.Empty(t, cmp.Diff(0, sm.ActiveSessions()))
}

func TestHandshakePayloadBinaryRoundTrip(t *testing.T) {
	t.Parallel()
	hp := HandshakePayload{Phase: 3}
	for i := 0; i < 32; i++ {
		hp.RX[i] = byte(i)
		hp.RY[i] = byte(i + 32)
		hp.Challenge[i] = byte(i + 64)
		hp.Response[i] = byte(i + 96)
	}

	data := hp.MarshalBinary()
	assert.Empty(t, cmp.Diff(129, len(data)))

	decoded, err := UnmarshalBinaryHandshake(data)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(hp, decoded))
}

func TestHandshakeResultBinaryRoundTrip(t *testing.T) {
	t.Parallel()
	hr := HandshakeResult{ExpiresUnix: time.Now().Unix()}
	for i := 0; i < 32; i++ {
		hr.SessionToken[i] = byte(i + 200)
	}

	data := hr.MarshalBinary()
	assert.Empty(t, cmp.Diff(40, len(data)))

	decoded, err := UnmarshalBinaryResult(data)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(hr, decoded))
}

func TestZKPSessionManagerCleanup(t *testing.T) {
	t.Parallel()
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 11)
	}
	x, pub, _ := SchnorrKeypair(masterKey)
	sm := NewZKPSessionManager(pub, 1*time.Millisecond)

	for i := 0; i < 5; i++ {
		commit, _ := ProverCommit()
		challenge, _ := VerifierChallenge()
		response, _ := ProverRespond(commit, challenge, x)
		_, _ = sm.VerifyAndIssue(commit.RX, commit.RY, challenge, response)
	}
	assert.Empty(t, cmp.Diff(5, sm.ActiveSessions()))

	time.Sleep(5 * time.Millisecond)
	cleaned := sm.Cleanup()
	assert.Empty(t, cmp.Diff(5, cleaned))
	assert.Empty(t, cmp.Diff(0, sm.ActiveSessions()))
}
