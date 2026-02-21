package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/agent"
)

func newDelegateKV(t *testing.T, agentID string) *agent.DelegateKV {
	t.Helper()
	db := newTestQueries(t)
	return agent.NewDelegateKV(db.delegate, agentID)
}

func TestDelegateKV_PutAndGet(t *testing.T) {
	kv := newDelegateKV(t, "agent-1")
	ctx := context.Background()

	require.NoError(t, kv.Put(ctx, "key1", []byte("hello world")))

	got, err := kv.Get(ctx, "key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello world"), got)
}

func TestDelegateKV_GetMissingKey(t *testing.T) {
	kv := newDelegateKV(t, "agent-1")
	ctx := context.Background()

	got, err := kv.Get(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got, "missing key should return nil, not error")
}

func TestDelegateKV_PutOverwrite(t *testing.T) {
	kv := newDelegateKV(t, "agent-1")
	ctx := context.Background()

	require.NoError(t, kv.Put(ctx, "k", []byte("v1")))
	require.NoError(t, kv.Put(ctx, "k", []byte("v2")))

	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), got, "second put should overwrite the first")
}

func TestDelegateKV_BinaryValues(t *testing.T) {
	kv := newDelegateKV(t, "agent-bin")
	ctx := context.Background()

	// Include bytes that need base64 encoding (null bytes, high bytes)
	data := []byte{0x00, 0xFF, 0x1F, 0x7E, 0x80, 0xAB}
	require.NoError(t, kv.Put(ctx, "binary-key", data))

	got, err := kv.Get(ctx, "binary-key")
	require.NoError(t, err)
	assert.Equal(t, data, got, "binary round-trip must be lossless")
}

func TestDelegateKV_Scan(t *testing.T) {
	kv := newDelegateKV(t, "agent-scan")
	ctx := context.Background()

	keys := []string{"prefix/a", "prefix/b", "prefix/c", "other/x"}
	for _, k := range keys {
		require.NoError(t, kv.Put(ctx, k, []byte(k)))
	}

	got, err := kv.Scan(ctx, "prefix/")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"prefix/a", "prefix/b", "prefix/c"}, got)
}

func TestDelegateKV_ScanEmpty(t *testing.T) {
	kv := newDelegateKV(t, "agent-scan-empty")
	ctx := context.Background()

	got, err := kv.Scan(ctx, "nothing/")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDelegateKV_ScanSorted(t *testing.T) {
	kv := newDelegateKV(t, "agent-sorted")
	ctx := context.Background()

	// Insert out of order
	for _, k := range []string{"z/c", "z/a", "z/b"} {
		require.NoError(t, kv.Put(ctx, k, []byte("v")))
	}

	got, err := kv.Scan(ctx, "z/")
	require.NoError(t, err)
	assert.Equal(t, []string{"z/a", "z/b", "z/c"}, got, "Scan must return keys sorted")
}

func TestDelegateKV_AgentIsolation(t *testing.T) {
	db := newTestQueries(t)
	kv1 := agent.NewDelegateKV(db.delegate, "agent-A")
	kv2 := agent.NewDelegateKV(db.delegate, "agent-B")
	ctx := context.Background()

	require.NoError(t, kv1.Put(ctx, "shared-key", []byte("from-A")))
	require.NoError(t, kv2.Put(ctx, "shared-key", []byte("from-B")))

	v1, err := kv1.Get(ctx, "shared-key")
	require.NoError(t, err)
	assert.Equal(t, []byte("from-A"), v1)

	v2, err := kv2.Get(ctx, "shared-key")
	require.NoError(t, err)
	assert.Equal(t, []byte("from-B"), v2)
}

func TestDelegateKV_EmptyKey_PutErrors(t *testing.T) {
	kv := newDelegateKV(t, "agent-1")
	err := kv.Put(context.Background(), "", []byte("val"))
	assert.Error(t, err, "empty key should be rejected")
}

func TestDelegateKV_EmptyKey_GetErrors(t *testing.T) {
	kv := newDelegateKV(t, "agent-1")
	_, err := kv.Get(context.Background(), "")
	assert.Error(t, err, "empty key should be rejected")
}
