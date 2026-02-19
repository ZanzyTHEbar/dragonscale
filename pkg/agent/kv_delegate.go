package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
)

// DelegateKV implements KVDelegate on top of any backend that supports
// agent-scoped key/value storage. It is the default backing store for
// OffloadingToolRuntime.
//
// Values are base64url-encoded so arbitrary binary payloads (e.g. JSON
// serialised tool results) survive the string-only storage layer intact.
type DelegateKV struct {
	backend kvBackend
	agentID string
	maxScan int // max keys returned by Scan; 0 = use defaultScanLimit
}

const defaultScanLimit = 1000

// kvBackend is the minimal interface we need from the memory delegate.
// LibSQLDelegate satisfies this interface.
type kvBackend interface {
	GetKV(ctx context.Context, agentID, key string) (string, error)
	UpsertKV(ctx context.Context, agentID, key, value string) error
	ListKVByPrefix(ctx context.Context, agentID, prefix string, limit int) (map[string]string, error)
}

// NewDelegateKV creates a KVDelegate backed by the given kvBackend, scoped
// to agentID. agentID is prepended to every key so multiple agents can share
// the same storage without collision.
func NewDelegateKV(backend kvBackend, agentID string) *DelegateKV {
	return &DelegateKV{backend: backend, agentID: agentID, maxScan: defaultScanLimit}
}

// Put encodes value as base64url and upserts it under key.
func (k *DelegateKV) Put(ctx context.Context, key string, value []byte) error {
	if key == "" {
		return fmt.Errorf("kv put: key must not be empty")
	}
	encoded := base64.URLEncoding.EncodeToString(value)
	return k.backend.UpsertKV(ctx, k.agentID, key, encoded)
}

// Get retrieves the value stored under key and decodes it from base64url.
// Returns nil, nil when the key does not exist.
func (k *DelegateKV) Get(ctx context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("kv get: key must not be empty")
	}
	encoded, err := k.backend.GetKV(ctx, k.agentID, key)
	if err != nil {
		return nil, err
	}
	if encoded == "" {
		return nil, nil
	}
	return base64.URLEncoding.DecodeString(encoded)
}

// Scan returns all keys that begin with prefix, sorted lexicographically.
func (k *DelegateKV) Scan(ctx context.Context, prefix string) ([]string, error) {
	limit := k.maxScan
	if limit <= 0 {
		limit = defaultScanLimit
	}
	rows, err := k.backend.ListKVByPrefix(ctx, k.agentID, prefix, limit)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}
