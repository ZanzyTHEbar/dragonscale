package agent

import (
	"context"
	"sync"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// ResilientKV wraps a KVDelegate with a circuit breaker that degrades gracefully
// on persistent failures. When the circuit is open, Put operations are silently
// dropped (with a warning log), while Get and Scan return empty results. This
// prevents transient DB hiccups from killing entire agent turns.
type ResilientKV struct {
	inner KVDelegate

	mu              sync.Mutex
	failureCount    int
	lastFailureTime time.Time
	circuitOpen     bool

	failureThreshold int
	resetTimeout     time.Duration
}

// ResilientKVOption configures a ResilientKV.
type ResilientKVOption func(*ResilientKV)

func WithFailureThreshold(n int) ResilientKVOption {
	return func(r *ResilientKV) { r.failureThreshold = n }
}

func WithResetTimeout(d time.Duration) ResilientKVOption {
	return func(r *ResilientKV) { r.resetTimeout = d }
}

func NewResilientKV(inner KVDelegate, opts ...ResilientKVOption) *ResilientKV {
	r := &ResilientKV{
		inner:            inner,
		failureThreshold: 5,
		resetTimeout:     30 * time.Second,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *ResilientKV) isOpen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.circuitOpen {
		return false
	}
	if time.Since(r.lastFailureTime) > r.resetTimeout {
		r.circuitOpen = false
		r.failureCount = 0
		logger.InfoCF("kv", "Circuit breaker reset, attempting KV operations again", nil)
		return false
	}
	return true
}

func (r *ResilientKV) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failureCount = 0
}

func (r *ResilientKV) recordFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failureCount++
	r.lastFailureTime = time.Now()
	if r.failureCount >= r.failureThreshold {
		r.circuitOpen = true
		logger.WarnCF("kv", "Circuit breaker opened after repeated failures",
			map[string]interface{}{"failures": r.failureCount, "threshold": r.failureThreshold})
	}
}

func (r *ResilientKV) Put(ctx context.Context, key string, value []byte) error {
	if r.isOpen() {
		logger.WarnCF("kv", "Circuit open, dropping KV Put",
			map[string]interface{}{"key": key})
		return nil
	}
	err := r.inner.Put(ctx, key, value)
	if err != nil {
		r.recordFailure()
		return err
	}
	r.recordSuccess()
	return nil
}

func (r *ResilientKV) Get(ctx context.Context, key string) ([]byte, error) {
	if r.isOpen() {
		return nil, nil
	}
	data, err := r.inner.Get(ctx, key)
	if err != nil {
		r.recordFailure()
		return nil, err
	}
	r.recordSuccess()
	return data, nil
}

func (r *ResilientKV) Scan(ctx context.Context, prefix string) ([]string, error) {
	if r.isOpen() {
		return nil, nil
	}
	keys, err := r.inner.Scan(ctx, prefix)
	if err != nil {
		r.recordFailure()
		return nil, err
	}
	r.recordSuccess()
	return keys, nil
}
