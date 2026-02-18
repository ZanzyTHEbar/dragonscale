package agent

import "context"

// KVDelegate is a minimal KV-like interface for offloading large artifacts
// (tool results, scratchpads) out of the LLM context window.
//
// Keys are logical paths (not filesystem paths).
type KVDelegate interface {
	Put(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Scan(ctx context.Context, prefix string) ([]string, error)
}
