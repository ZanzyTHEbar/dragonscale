package observation

import (
	"context"
	"log/slog"
	"sync"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// Manager orchestrates the observation lifecycle: threshold detection,
// observer/reflector invocation, persistence, and async execution.
type Manager struct {
	store     *Store
	observer  *Observer
	reflector *Reflector

	mu       sync.Mutex
	running  map[string]bool // sessionKey -> running flag to prevent concurrent runs
}

// ManagerConfig bundles configuration for the observation system.
type ManagerConfig struct {
	Observer  ObserverConfig
	Reflector ReflectorConfig
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Observer:  DefaultObserverConfig(),
		Reflector: DefaultReflectorConfig(),
	}
}

// NewManager creates an observation Manager backed by the given delegate.
func NewManager(delegate memory.MemoryDelegate, agentID string, callModel ModelFunc, cfg ManagerConfig) *Manager {
	store := NewStore(delegate, agentID)
	return &Manager{
		store:     store,
		observer:  NewObserver(callModel, cfg.Observer),
		reflector: NewReflector(callModel, cfg.Reflector),
		running:   make(map[string]bool),
	}
}

// MaybeObserveAsync checks if observation is needed and runs it in a background
// goroutine if so. Non-blocking; safe to call on every agent turn.
func (m *Manager) MaybeObserveAsync(ctx context.Context, sessionKey string, tailMessages []MessagePair) {
	if !m.observer.ShouldObserve(tailMessages) {
		return
	}

	if !m.tryAcquire(sessionKey) {
		return
	}

	// Copy messages to avoid data races with the caller.
	msgs := make([]MessagePair, len(tailMessages))
	copy(msgs, tailMessages)

	go func() {
		defer m.release(sessionKey)
		m.runObservation(ctx, sessionKey, msgs)
	}()
}

// LoadBlock returns the formatted observation block for system prompt injection.
// Returns empty string if no observations exist.
func (m *Manager) LoadBlock(ctx context.Context, sessionKey string) string {
	obs, err := m.store.Load(ctx, sessionKey)
	if err != nil {
		slog.Warn("failed to load observations", "session", sessionKey, "error", err)
		return ""
	}
	return FormatBlock(obs)
}

// Store returns the underlying observation store for direct access.
func (m *Manager) Store() *Store {
	return m.store
}

func (m *Manager) runObservation(ctx context.Context, sessionKey string, messages []MessagePair) {
	existing, err := m.store.Load(ctx, sessionKey)
	if err != nil {
		slog.Error("observation: failed to load existing", "session", sessionKey, "error", err)
		return
	}

	newObs, err := m.observer.Observe(ctx, messages, existing)
	if err != nil {
		slog.Error("observation: observer failed", "session", sessionKey, "error", err)
		return
	}

	if len(newObs) == 0 {
		return
	}

	all := append(existing, newObs...)

	if m.reflector.ShouldReflect(all) {
		pruned, err := m.reflector.Reflect(ctx, all)
		if err != nil {
			slog.Error("observation: reflector failed", "session", sessionKey, "error", err)
			// Save unpruned observations rather than losing them
		} else {
			all = pruned
		}
	}

	if err := m.store.Save(ctx, sessionKey, all); err != nil {
		slog.Error("observation: failed to save", "session", sessionKey, "error", err)
	}

	slog.Info("observation: updated",
		"session", sessionKey,
		"new", len(newObs),
		"total", len(all))
}

func (m *Manager) tryAcquire(sessionKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[sessionKey] {
		return false
	}
	m.running[sessionKey] = true
	return true
}

func (m *Manager) release(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, sessionKey)
}
