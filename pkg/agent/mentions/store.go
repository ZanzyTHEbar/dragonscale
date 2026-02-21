// Package mentions provides the operations layer for agent mentions —
// @-style references to conversations, threads, or documents embedded in
// messages. Mentions are stored in agent_mentions for fast cross-entity lookup.
package mentions

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/dserrors"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	sqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

// Store wraps the SQLC queries for mention operations.
// Construct with New.
type Store struct {
	q *sqlc.Queries
}

// New returns a Store backed by the provided SQLC queries handle.
func New(q *sqlc.Queries) *Store {
	return &Store{q: q}
}

// ─── Add ─────────────────────────────────────────────────────────────────────

// AddParams configures the Add operation.
type AddParams struct {
	// ConversationID is the conversation that contains the message with the mention.
	ConversationID string
	// MessageID is the message that contains the mention.
	MessageID string
	// Kind describes the type of entity being mentioned (e.g. "conv", "thread", "doc").
	Kind string
	// TargetID is the UUID of the entity being mentioned.
	TargetID string
	// Raw is the raw mention token as it appeared in the message (e.g. "@conv:abc123").
	Raw string
	// Metadata is optional JSON-serialisable additional context.
	Metadata map[string]any
}

// Add records a new mention.
func (s *Store) Add(ctx context.Context, p AddParams) (sqlc.AgentMention, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return sqlc.AgentMention{}, dserrors.New(dserrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return sqlc.AgentMention{}, dserrors.Wrapf(dserrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}

	msgIDStr := strings.TrimSpace(p.MessageID)
	if msgIDStr == "" {
		return sqlc.AgentMention{}, dserrors.New(dserrors.CodeInvalidArgument, "message_id is required")
	}
	msgID, err := ids.Parse(msgIDStr)
	if err != nil {
		return sqlc.AgentMention{}, dserrors.Wrapf(dserrors.CodeInvalidArgument, err, "parse message_id %q", msgIDStr)
	}

	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		return sqlc.AgentMention{}, dserrors.New(dserrors.CodeInvalidArgument, "kind is required")
	}

	targetIDStr := strings.TrimSpace(p.TargetID)
	if targetIDStr == "" {
		return sqlc.AgentMention{}, dserrors.New(dserrors.CodeInvalidArgument, "target_id is required")
	}
	targetID, err := ids.Parse(targetIDStr)
	if err != nil {
		return sqlc.AgentMention{}, dserrors.Wrapf(dserrors.CodeInvalidArgument, err, "parse target_id %q", targetIDStr)
	}

	metaJSON, _ := jsonv2.Marshal(p.Metadata)

	return s.q.AddAgentMention(ctx, sqlc.AddAgentMentionParams{
		ID:             ids.New(),
		ConversationID: convID,
		MessageID:      msgID,
		Kind:           kind,
		TargetID:       targetID,
		Raw:            strings.TrimSpace(p.Raw),
		MetadataJson:   metaJSON,
	})
}

// ─── ListByConversation ───────────────────────────────────────────────────────

// ListByConversationParams configures the ListByConversation operation.
type ListByConversationParams struct {
	ConversationID string
}

// ListByConversation returns all mentions recorded within a conversation.
func (s *Store) ListByConversation(ctx context.Context, p ListByConversationParams) ([]sqlc.AgentMention, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return nil, dserrors.New(dserrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return nil, dserrors.Wrapf(dserrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}
	return s.q.ListAgentMentionsByConversationID(ctx,
		sqlc.ListAgentMentionsByConversationIDParams{ConversationID: convID})
}
