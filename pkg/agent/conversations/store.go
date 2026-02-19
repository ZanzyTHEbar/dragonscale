// Package conversations provides the business-logic operations layer for
// conversation management (create, list, edit, fork, merge, ancestry, graph).
// It sits above the raw SQLC-generated queries and below any HTTP/CLI handler.
package conversations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sipeed/picoclaw/pkg/ids"
	sqlc "github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

// Store wraps the SQLC queries for conversation operations.
// Construct with New.
type Store struct {
	q *sqlc.Queries
}

// New returns a Store backed by the provided SQLC queries handle.
func New(q *sqlc.Queries) *Store {
	return &Store{q: q}
}

// ─── List ────────────────────────────────────────────────────────────────────

// ListParams configures the List operation.
type ListParams struct {
	// Limit is clamped to [1, 200]; defaults to 20.
	Limit int
}

// List returns up to Limit conversations ordered by creation time (newest first).
func (s *Store) List(ctx context.Context, p ListParams) ([]sqlc.AgentConversation, error) {
	limit := int64(p.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	return s.q.ListAgentConversations(ctx, sqlc.ListAgentConversationsParams{Limit: limit})
}

// ─── Create ──────────────────────────────────────────────────────────────────

// CreateParams configures the Create operation.
type CreateParams struct {
	Title *string
}

// Create creates a new, empty conversation.
func (s *Store) Create(ctx context.Context, p CreateParams) (sqlc.AgentConversation, error) {
	return s.q.CreateAgentConversation(ctx, sqlc.CreateAgentConversationParams{
		ID:    ids.New(),
		Title: p.Title,
	})
}

// ─── EditMessage ──────────────────────────────────────────────────────────────

// EditMessageParams configures the EditMessage operation.
type EditMessageParams struct {
	// MessageID is the UUID (string) of the message to edit.
	MessageID string
	// NewText is the replacement content. Must be non-empty.
	NewText string
	// Editor is recorded in the revision history. Defaults to "user".
	Editor string
	// Metadata is serialised to JSON and stored on the revision row.
	Metadata map[string]any
}

// EditMessage updates a message's content and records the old value in the
// revision table for audit/undo purposes.
func (s *Store) EditMessage(ctx context.Context, p EditMessageParams) (sqlc.AgentMessage, error) {
	msgIDStr := strings.TrimSpace(p.MessageID)
	if msgIDStr == "" {
		return sqlc.AgentMessage{}, pcerrors.New(pcerrors.CodeInvalidArgument, "message_id is required")
	}
	msgID, err := ids.Parse(msgIDStr)
	if err != nil {
		return sqlc.AgentMessage{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse message_id %q", msgIDStr)
	}

	newText := strings.TrimSpace(p.NewText)
	if newText == "" {
		return sqlc.AgentMessage{}, pcerrors.New(pcerrors.CodeInvalidArgument, "new content is empty")
	}

	editor := strings.TrimSpace(p.Editor)
	if editor == "" {
		editor = "user"
	}

	prev, err := s.q.GetAgentMessageByID(ctx, sqlc.GetAgentMessageByIDParams{ID: msgID})
	if err != nil {
		return sqlc.AgentMessage{}, err
	}

	metaJSON, _ := json.Marshal(p.Metadata)

	// Best-effort revision record — a failure here does not abort the edit.
	_, _ = s.q.AddAgentMessageRevision(ctx, sqlc.AddAgentMessageRevisionParams{
		ID:           ids.New(),
		MessageID:    msgID,
		Editor:       editor,
		OldContent:   prev.Content,
		NewContent:   newText,
		MetadataJson: metaJSON,
	})

	updated, err := s.q.UpdateAgentMessageContent(ctx, sqlc.UpdateAgentMessageContentParams{
		Content: newText,
		ID:      msgID,
	})
	if err != nil {
		return sqlc.AgentMessage{}, err
	}
	return updated, nil
}

// ─── Fork ─────────────────────────────────────────────────────────────────────

// ForkFromCheckpointParams configures the ForkFromCheckpoint operation.
type ForkFromCheckpointParams struct {
	// FromConversationID is the UUID (string) of the source conversation.
	FromConversationID string
	// CheckpointName identifies the snapshot to fork from.
	CheckpointName string
	// Title is the optional title for the newly forked conversation.
	Title *string
}

// ForkFromCheckpoint creates a new conversation branched off at the state
// captured by the named checkpoint. Messages up to 200 are seeded into the
// new conversation; a fork-lineage record is written to agent_conversation_forks.
func (s *Store) ForkFromCheckpoint(ctx context.Context, p ForkFromCheckpointParams) (sqlc.AgentConversation, error) {
	fromIDStr := strings.TrimSpace(p.FromConversationID)
	if fromIDStr == "" {
		return sqlc.AgentConversation{}, pcerrors.New(pcerrors.CodeInvalidArgument, "from_conversation_id is required")
	}
	fromID, err := ids.Parse(fromIDStr)
	if err != nil {
		return sqlc.AgentConversation{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse from_conversation_id %q", fromIDStr)
	}

	cpName := strings.TrimSpace(p.CheckpointName)
	if cpName == "" {
		return sqlc.AgentConversation{}, pcerrors.New(pcerrors.CodeInvalidArgument, "checkpoint_name is required")
	}

	cp, err := s.q.GetAgentCheckpointByConversationIDAndName(ctx,
		sqlc.GetAgentCheckpointByConversationIDAndNameParams{
			ConversationID: fromID,
			Name:           cpName,
		})
	if err != nil {
		return sqlc.AgentConversation{}, err
	}

	runState, err := s.q.GetAgentRunStateByID(ctx, sqlc.GetAgentRunStateByIDParams{ID: cp.RunStateID})
	if err != nil {
		return sqlc.AgentConversation{}, err
	}

	type msgSnapshot struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type snapshot struct {
		Messages []msgSnapshot `json:"messages"`
	}
	var snap snapshot
	_ = json.Unmarshal(runState.SnapshotJson, &snap)

	conv, err := s.q.CreateAgentConversation(ctx, sqlc.CreateAgentConversationParams{
		ID:    ids.New(),
		Title: p.Title,
	})
	if err != nil {
		return sqlc.AgentConversation{}, err
	}

	forkMeta := map[string]any{
		"checkpoint_name": cpName,
		"run_state_id":    cp.RunStateID.String(),
	}
	forkMetaJSON, _ := json.Marshal(forkMeta)

	_, _ = s.q.CreateAgentConversationFork(ctx, sqlc.CreateAgentConversationForkParams{
		ID:                   ids.New(),
		ParentConversationID: fromID,
		ChildConversationID:  conv.ID,
		CheckpointID:         cp.ID,
		MetadataJson:         forkMetaJSON,
	})

	// Seed messages from snapshot — cap to 200 to prevent pathological snapshots.
	msgs := snap.Messages
	if len(msgs) > 200 {
		msgs = msgs[len(msgs)-200:]
	}

	seedMeta := map[string]any{
		"seeded_from_conversation_id": fromID.String(),
		"checkpoint_name":             cpName,
		"run_state_id":                cp.RunStateID.String(),
	}
	seedMetaJSON, _ := json.Marshal(seedMeta)

	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		_, _ = s.q.AddAgentMessage(ctx, sqlc.AddAgentMessageParams{
			ID:             ids.New(),
			ConversationID: conv.ID,
			Role:           m.Role,
			Content:        m.Content,
			MetadataJson:   seedMetaJSON,
		})
	}

	return conv, nil
}

// ─── Merge ────────────────────────────────────────────────────────────────────

// MergeAsLinkedContextParams configures the MergeAsLinkedContext operation.
type MergeAsLinkedContextParams struct {
	// BaseConversationID and OtherConversationID must differ.
	BaseConversationID  string
	OtherConversationID string
	// Title is the optional title for the merged conversation.
	Title *string
}

// MergeAsLinkedContext creates a new conversation that carries link records
// to both source conversations. No messages are copied; linked context is
// injected at runtime as a compact view. A system note is written so the
// conversation is self-describing.
func (s *Store) MergeAsLinkedContext(ctx context.Context, p MergeAsLinkedContextParams) (sqlc.AgentConversation, error) {
	baseIDStr := strings.TrimSpace(p.BaseConversationID)
	if baseIDStr == "" {
		return sqlc.AgentConversation{}, pcerrors.New(pcerrors.CodeInvalidArgument, "base_conversation_id is required")
	}
	baseID, err := ids.Parse(baseIDStr)
	if err != nil {
		return sqlc.AgentConversation{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse base_conversation_id %q", baseIDStr)
	}

	otherIDStr := strings.TrimSpace(p.OtherConversationID)
	if otherIDStr == "" {
		return sqlc.AgentConversation{}, pcerrors.New(pcerrors.CodeInvalidArgument, "other_conversation_id is required")
	}
	otherID, err := ids.Parse(otherIDStr)
	if err != nil {
		return sqlc.AgentConversation{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse other_conversation_id %q", otherIDStr)
	}

	if baseID == otherID {
		return sqlc.AgentConversation{}, pcerrors.New(pcerrors.CodeInvalidArgument, "base and other conversations must differ")
	}

	conv, err := s.q.CreateAgentConversation(ctx, sqlc.CreateAgentConversationParams{
		ID:    ids.New(),
		Title: p.Title,
	})
	if err != nil {
		return sqlc.AgentConversation{}, err
	}

	meta := map[string]any{"source": "merge_as_linked_context"}
	metaJSON, _ := json.Marshal(meta)

	_, _ = s.q.CreateAgentConversationLink(ctx, sqlc.CreateAgentConversationLinkParams{
		ID:                   ids.New(),
		ConversationID:       conv.ID,
		LinkedConversationID: baseID,
		Kind:                 "merge",
		MetadataJson:         metaJSON,
	})
	_, _ = s.q.CreateAgentConversationLink(ctx, sqlc.CreateAgentConversationLinkParams{
		ID:                   ids.New(),
		ConversationID:       conv.ID,
		LinkedConversationID: otherID,
		Kind:                 "merge",
		MetadataJson:         metaJSON,
	})

	note := "This conversation was created by merging as linked context.\n" +
		"Linked conversations:\n" +
		"- @conv:" + baseID.String() + "\n" +
		"- @conv:" + otherID.String() + "\n\n" +
		"These links are user-attached context; the agent will be shown compact context from them at the start of each turn.\n"

	_, _ = s.q.AddAgentMessage(ctx, sqlc.AddAgentMessageParams{
		ID:             ids.New(),
		ConversationID: conv.ID,
		Role:           "system",
		Content:        note,
		MetadataJson:   metaJSON,
	})

	return conv, nil
}

// ─── Ancestry ────────────────────────────────────────────────────────────────

// AncestryParams configures the Ancestry operation.
type AncestryParams struct {
	ConversationID string
}

// AncestryResult is the structured result of an Ancestry query.
type AncestryResult struct {
	Conversation sqlc.AgentConversation       `json:"conversation"`
	ForkParent   *sqlc.AgentConversationFork  `json:"fork_parent,omitempty"`
	ForkChildren []sqlc.AgentConversationFork `json:"fork_children"`
	Links        []sqlc.AgentConversationLink `json:"links"`
}

// Ancestry returns the fork lineage and merge links for a conversation.
func (s *Store) Ancestry(ctx context.Context, p AncestryParams) (AncestryResult, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return AncestryResult{}, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return AncestryResult{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}

	conv, err := s.q.GetAgentConversation(ctx, sqlc.GetAgentConversationParams{ID: convID})
	if err != nil {
		return AncestryResult{}, err
	}

	var parent *sqlc.AgentConversationFork
	if fp, err := s.q.GetAgentConversationForkByChildConversationID(ctx,
		sqlc.GetAgentConversationForkByChildConversationIDParams{ChildConversationID: convID}); err == nil {
		parent = &fp
	}

	children, _ := s.q.ListAgentConversationForksByParentConversationID(ctx,
		sqlc.ListAgentConversationForksByParentConversationIDParams{ParentConversationID: convID})
	links, _ := s.q.ListAgentConversationLinksByConversationID(ctx,
		sqlc.ListAgentConversationLinksByConversationIDParams{ConversationID: convID})

	return AncestryResult{
		Conversation: conv,
		ForkParent:   parent,
		ForkChildren: children,
		Links:        links,
	}, nil
}

// ─── Links ────────────────────────────────────────────────────────────────────

// LinksListParams configures the LinksList operation.
type LinksListParams struct {
	ConversationID string
}

// LinksList returns all conversation links for the given conversation.
func (s *Store) LinksList(ctx context.Context, p LinksListParams) ([]sqlc.AgentConversationLink, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return nil, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return nil, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}
	return s.q.ListAgentConversationLinksByConversationID(ctx,
		sqlc.ListAgentConversationLinksByConversationIDParams{ConversationID: convID})
}

// LinksRemoveParams configures the LinksRemove operation.
type LinksRemoveParams struct {
	ConversationID       string
	LinkedConversationID string
	// Kind defaults to "merge".
	Kind string
}

// LinksRemove deletes a specific conversation link.
func (s *Store) LinksRemove(ctx context.Context, p LinksRemoveParams) error {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	convID, err := ids.Parse(convIDStr)
	if err != nil {
		return pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}

	linkedIDStr := strings.TrimSpace(p.LinkedConversationID)
	if linkedIDStr == "" {
		return pcerrors.New(pcerrors.CodeInvalidArgument, "linked_conversation_id is required")
	}
	linkedID, err := ids.Parse(linkedIDStr)
	if err != nil {
		return pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse linked_conversation_id %q", linkedIDStr)
	}

	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = "merge"
	}

	return s.q.DeleteAgentConversationLink(ctx, sqlc.DeleteAgentConversationLinkParams{
		ConversationID:       convID,
		LinkedConversationID: linkedID,
		Kind:                 kind,
	})
}

// ─── Graph ────────────────────────────────────────────────────────────────────

// GraphParams configures the Graph operation.
type GraphParams struct {
	ConversationID string
	// Depth controls how many hops of fork lineage to traverse. Clamped [0,10].
	Depth int
}

// GraphNode is a single conversation node in the fork/link graph.
type GraphNode struct {
	ID    string  `json:"id"`
	Title *string `json:"title,omitempty"`
}

// GraphEdge is a directed edge between two conversation nodes.
type GraphEdge struct {
	// Type is "fork" or "link".
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
	// CheckpointID is set on fork edges.
	CheckpointID *string `json:"checkpoint_id,omitempty"`
	// Kind is set on link edges (e.g. "merge").
	Kind *string `json:"kind,omitempty"`
}

// GraphResult is the full fork/link graph rooted at a conversation.
type GraphResult struct {
	RootID string      `json:"root_id"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
}

// Graph builds a depth-limited fork/link graph centred on a conversation.
//   - Fork edges are traversed up (to parent) and down (to children) up to Depth hops.
//   - Link edges are included for visited nodes but NOT traversed.
func (s *Store) Graph(ctx context.Context, p GraphParams) (GraphResult, error) {
	convIDStr := strings.TrimSpace(p.ConversationID)
	if convIDStr == "" {
		return GraphResult{}, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id is required")
	}
	rootID, err := ids.Parse(convIDStr)
	if err != nil {
		return GraphResult{}, pcerrors.Wrapf(pcerrors.CodeInvalidArgument, err, "parse conversation_id %q", convIDStr)
	}

	depth := p.Depth
	if depth < 0 {
		depth = 0
	}
	if depth > 10 {
		depth = 10
	}

	nodes := map[string]GraphNode{}
	edges := map[string]GraphEdge{}
	visited := map[string]bool{}

	var visit func(id ids.UUID, remaining int) error
	visit = func(id ids.UUID, remaining int) error {
		key := id.String()
		if visited[key] {
			return nil
		}
		visited[key] = true

		conv, err := s.q.GetAgentConversation(ctx, sqlc.GetAgentConversationParams{ID: id})
		if err != nil {
			return err
		}
		nodes[key] = GraphNode{ID: conv.ID.String(), Title: conv.Title}

		// Include link edges but do not traverse them.
		if lks, err := s.q.ListAgentConversationLinksByConversationID(ctx,
			sqlc.ListAgentConversationLinksByConversationIDParams{ConversationID: id}); err == nil {
			for _, l := range lks {
				from := id.String()
				to := l.LinkedConversationID.String()
				kind := l.Kind
				edges["link:"+from+":"+to+":"+kind] = GraphEdge{
					Type: "link", From: from, To: to, Kind: &kind,
				}
			}
		}

		// Fork parent (upward traversal).
		if fp, err := s.q.GetAgentConversationForkByChildConversationID(ctx,
			sqlc.GetAgentConversationForkByChildConversationIDParams{ChildConversationID: id}); err == nil {
			from := fp.ParentConversationID.String()
			to := fp.ChildConversationID.String()
			cpID := fp.CheckpointID.String()
			edges["fork:"+from+":"+to] = GraphEdge{
				Type: "fork", From: from, To: to, CheckpointID: &cpID,
			}
			if remaining > 0 {
				_ = visit(fp.ParentConversationID, remaining-1)
			}
		}

		// Fork children (downward traversal).
		if children, err := s.q.ListAgentConversationForksByParentConversationID(ctx,
			sqlc.ListAgentConversationForksByParentConversationIDParams{ParentConversationID: id}); err == nil {
			for _, c := range children {
				from := c.ParentConversationID.String()
				to := c.ChildConversationID.String()
				cpID := c.CheckpointID.String()
				edges["fork:"+from+":"+to] = GraphEdge{
					Type: "fork", From: from, To: to, CheckpointID: &cpID,
				}
				if remaining > 0 {
					_ = visit(c.ChildConversationID, remaining-1)
				}
			}
		}

		return nil
	}

	if err := visit(rootID, depth); err != nil {
		return GraphResult{}, err
	}

	outNodes := make([]GraphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, n)
	}
	outEdges := make([]GraphEdge, 0, len(edges))
	for _, e := range edges {
		outEdges = append(outEdges, e)
	}

	return GraphResult{
		RootID: rootID.String(),
		Nodes:  outNodes,
		Edges:  outEdges,
	}, nil
}
