package observation

import (
	"context"
	"fmt"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

const kvPrefix = "obs:"

// Store persists observations to the agent_kv table via MemoryDelegate.
// Each session's observations are stored as a single JSON array under
// the key "obs:<session_key>".
type Store struct {
	delegate memory.MemoryDelegate
	agentID  string
}

func NewStore(delegate memory.MemoryDelegate, agentID string) *Store {
	return &Store{
		delegate: delegate,
		agentID:  agentID,
	}
}

func (s *Store) key(sessionKey string) string {
	return kvPrefix + sessionKey
}

// Load retrieves all observations for a session.
func (s *Store) Load(ctx context.Context, sessionKey string) ([]Observation, error) {
	data, err := s.delegate.GetKV(ctx, s.agentID, s.key(sessionKey))
	if err != nil {
		return nil, nil // Key not found is not an error
	}
	return UnmarshalObservations(data)
}

// Save persists the full observation list for a session.
func (s *Store) Save(ctx context.Context, sessionKey string, obs []Observation) error {
	data, err := MarshalObservations(obs)
	if err != nil {
		return fmt.Errorf("save observations: %w", err)
	}
	return s.delegate.UpsertKV(ctx, s.agentID, s.key(sessionKey), data)
}

// Append adds new observations to the existing list and persists.
func (s *Store) Append(ctx context.Context, sessionKey string, newObs []Observation) error {
	existing, err := s.Load(ctx, sessionKey)
	if err != nil {
		return err
	}
	all := append(existing, newObs...)
	return s.Save(ctx, sessionKey, all)
}
