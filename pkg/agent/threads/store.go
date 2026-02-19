// Package threads provides the business-logic operations layer for agent
// sub-threads within a conversation: creation, listing, and message management.
// Sub-threads allow parallel or branching dialogue tracks without forking the
// parent conversation.
package threads

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"strings"

	"github.com/sipeed/picoclaw/pkg/ids"
	sqlc "github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

// Store wraps the SQLC queries for thread operations.
// Construct with New.
type Store struct {
	q *sqlc.Queries
}

// New returns a Store backed by the provided SQLC queries handle.
func New(q *sqlc.Queries) *Store {
	return &Store{q: q}
}

// ─── Create ──────────────────────────────────────────────────────────────────

// CreateParams configures the Create operation.
type CreateParams struct {
	ConversationID string
	Title          *string
	Metadata       map[string]any
}

// Create creates a new thread within a conversation.
func (s *Store) Create(ctx context.Context, p CreateParams) (sqlc.AgentThread, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return sqlc.AgentThread{}, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return sqlc.AgentThread{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}

	metaJSON, _ := jsonv2.Marshal(p.Metadata)

	return s.q.CreateAgentThread(ctx, sqlc.CreateAgentThreadParams{
		ID:             ids.New(),
		ConversationID: convID,
		Title:          p.Title,
		MetadataJson:   metaJSON,
	})
}

// ─── List ────────────────────────────────────────────────────────────────────

// ListParams configures the List operation.
type ListParams struct {
	ConversationID string
}

// List returns all threads for the given conversation.
func (s *Store) List(ctx context.Context, p ListParams) ([]sqlc.AgentThread, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return nil, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return nil, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}
	return s.q.ListAgentThreadsByConversationID(ctx,
		sqlc.ListAgentThreadsByConversationIDParams{ConversationID: convID})
}

// ─── AddMessage ──────────────────────────────────────────────────────────────

// AddMessageParams configures the AddMessage operation.
type AddMessageParams struct {
	ThreadID string
	// Role defaults to "user" if empty.
	Role     string
	Content  string
	Metadata map[string]any
}

// AddMessage appends a message to a thread.
func (s *Store) AddMessage(ctx context.Context, p AddMessageParams) (sqlc.AgentThreadMessage, error) {
	threadIDStr := strings.TrimSpace(p.ThreadID)
	if threadIDStr == "" {
		return sqlc.AgentThreadMessage{}, pcerrors.New(pcerrors.CodeInvalidArgument, "thread_id is required")
	}
	threadID, err := ids.Parse(threadIDStr)
	if err != nil {
		return sqlc.AgentThreadMessage{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse thread_id %q", threadIDStr)
	}

	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = "user"
	}

	content := strings.TrimSpace(p.Content)
	if content == "" {
		return sqlc.AgentThreadMessage{}, pcerrors.New(pcerrors.CodeInvalidArgument, "content is empty")
	}

	metaJSON, _ := jsonv2.Marshal(p.Metadata)

	return s.q.AddAgentThreadMessage(ctx, sqlc.AddAgentThreadMessageParams{
		ID:           ids.New(),
		ThreadID:     threadID,
		Role:         role,
		Content:      content,
		MetadataJson: metaJSON,
	})
}

// ─── ListMessages ────────────────────────────────────────────────────────────

// ListMessagesParams configures the ListMessages operation.
type ListMessagesParams struct {
	ThreadID string
	// Limit is clamped to [1, 500]; defaults to 50.
	Limit int
}

// ListMessages returns messages for a thread in chronological order.
// Internally fetches in descending order and reverses so the caller always
// receives oldest-first.
func (s *Store) ListMessages(ctx context.Context, p ListMessagesParams) ([]sqlc.AgentThreadMessage, error) {
	threadIDStr := strings.TrimSpace(p.ThreadID)
	if threadIDStr == "" {
		return nil, pcerrors.New(pcerrors.CodeInvalidArgument, "thread_id is required")
	}
	threadID, err := ids.Parse(threadIDStr)
	if err != nil {
		return nil, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse thread_id %q", threadIDStr)
	}

	limit := int64(p.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := s.q.ListAgentThreadMessagesByThreadIDDescLimit(ctx,
		sqlc.ListAgentThreadMessagesByThreadIDDescLimitParams{
			ThreadID: threadID,
			Limit:    limit,
		})
	if err != nil {
		return nil, err
	}

	// Reverse to chronological order.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}
