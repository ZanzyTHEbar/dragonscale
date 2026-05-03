package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

// SessionMessageLister lists session messages in chronological order.
type SessionMessageLister interface {
	ListSessionMessages(ctx context.Context, agentID, sessionKey, role string, limit int) ([]*memory.RecallItem, error)
}

// DAGToolDeps provides dependencies for DAG tools.
type DAGToolDeps struct {
	Queries   *memsqlc.Queries
	Lister    SessionMessageLister
	Delegate  memory.MemoryDelegate
	AgentID   string
	SessionFn func() string // returns current session key
}

func recallRowToItem(row memsqlc.ListSessionMessagesPagedRow) *memory.RecallItem {
	return &memory.RecallItem{
		ID:         row.ID,
		AgentID:    row.AgentID,
		SessionKey: row.SessionKey,
		Role:       row.Role,
		Sector:     row.Sector,
		Importance: row.Importance,
		Salience:   row.Salience,
		DecayRate:  row.DecayRate,
		Content:    row.Content,
		Tags:       row.Tags,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func (d DAGToolDeps) loadSessionWindow(ctx context.Context, sessionKey string, start, end int) ([]*memory.RecallItem, error) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}

	// Prefer DB-paged reads for deterministic large-session behavior.
	if d.Queries != nil {
		rows, err := d.Queries.ListSessionMessagesPaged(ctx, memsqlc.ListSessionMessagesPagedParams{
			AgentID:    d.AgentID,
			SessionKey: sessionKey,
			Role:       "",
			Lim:        int64(end - start),
			Off:        int64(start),
		})
		if err != nil {
			return nil, err
		}
		items := make([]*memory.RecallItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, recallRowToItem(row))
		}
		return items, nil
	}

	if d.Lister == nil {
		return nil, errors.New("session message lister not configured")
	}
	rows, err := d.Lister.ListSessionMessages(ctx, d.AgentID, sessionKey, "", end+32)
	if err != nil {
		return nil, err
	}
	if start >= len(rows) {
		return []*memory.RecallItem{}, nil
	}
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}

// DagExpandTool recovers original messages for a DAG node (lossless expand).
type DagExpandTool struct {
	deps DAGToolDeps
}

// NewDagExpandTool creates a dag_expand tool.
func NewDagExpandTool(deps DAGToolDeps) *DagExpandTool {
	return &DagExpandTool{deps: deps}
}

func (t *DagExpandTool) Name() string { return "dag_expand" }

func (t *DagExpandTool) Description() string {
	return "Recover original messages covered by a DAG node. Use after dag_describe to expand a node (e.g. chunk-1) into its full message content. Provides lossless node→message recovery."
}

func (t *DagExpandTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"node_id": map[string]interface{}{
				"type":        "string",
				"description": "DAG node ID (e.g. chunk-1, section-1)",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Session to query (default: current session)",
			},
		},
		"required": []string{"node_id"},
	}
}

func (t *DagExpandTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	nodeID, _ := args["node_id"].(string)
	if nodeID == "" {
		return ErrorResult("node_id is required")
	}

	sessionKey, _ := args["session_key"].(string)
	if sessionKey == "" {
		sessionKey = ResolveSessionKey(ctx, t.deps.SessionFn)
	}
	if sessionKey == "" {
		sessionKey = "default"
	}

	if strings.HasPrefix(nodeID, DAGRecoveryNodePrefix) {
		return t.expandRecoveryNode(ctx, sessionKey, nodeID)
	}

	if t.deps.Queries == nil {
		return ErrorResult("dag query store is not configured")
	}

	snap, err := t.deps.Queries.GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
		AgentID:    t.deps.AgentID,
		SessionKey: sessionKey,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrorResult(fmt.Sprintf("no DAG snapshot for session %q (conversation not yet compressed)", sessionKey))
		}
		return ErrorResult(fmt.Sprintf("no DAG snapshot for session %q: %v", sessionKey, err))
	}

	node, err := t.deps.Queries.GetDAGNodeBySnapshotAndNodeID(ctx, memsqlc.GetDAGNodeBySnapshotAndNodeIDParams{
		SnapshotID: snap.ID,
		NodeID:     nodeID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrorResult(fmt.Sprintf("node %q not found in DAG", nodeID))
		}
		return ErrorResult(fmt.Sprintf("node %q not found: %v", nodeID, err))
	}

	if t.deps.Lister == nil {
		if t.deps.Queries == nil {
			return ErrorResult("session message reader is not configured")
		}
	}

	start, end := int(node.StartIdx), int(node.EndIdx)
	msgs, err := t.deps.loadSessionWindow(ctx, sessionKey, start, end)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list messages: %v", err))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] messages %d-%d:\n\n", nodeID, start, end))
	for i, m := range msgs {
		sb.WriteString(fmt.Sprintf("%s (%d): %s\n\n", m.Role, start+i, m.Content))
	}
	return SilentResult(sb.String())
}

func (t *DagExpandTool) expandRecoveryNode(ctx context.Context, sessionKey, nodeID string) *ToolResult {
	if t.deps.Delegate == nil {
		return ErrorResult("dag recovery store is not configured")
	}

	raw, err := t.deps.Delegate.GetKV(ctx, t.deps.AgentID, DAGRecoveryKVKey(sessionKey, nodeID))
	if err != nil || strings.TrimSpace(raw) == "" {
		return ErrorResult(fmt.Sprintf("recovery node %q not found", nodeID))
	}

	var rec DAGRecoveryRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return ErrorResult(fmt.Sprintf("invalid recovery record for node %q: %v", nodeID, err))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] recovery reference for omitted oversized message\n\n", rec.NodeID)
	fmt.Fprintf(&sb, "session: %s\n", rec.SessionKey)
	fmt.Fprintf(&sb, "role: %s\n", rec.Role)
	fmt.Fprintf(&sb, "original_index: %d\n", rec.OriginalIndex)
	fmt.Fprintf(&sb, "tokens_est: %d\n", rec.TokenEstimate)
	fmt.Fprintf(&sb, "reason: %s\n", rec.Reason)
	if !rec.CreatedAt.IsZero() {
		fmt.Fprintf(&sb, "created_at: %s\n", rec.CreatedAt.Format(time.RFC3339))
	}
	sb.WriteString("\n")
	sb.WriteString(rec.Content)

	return SilentResult(sb.String())
}

// DagDescribeTool returns node metadata and lineage (parents, children).
type DagDescribeTool struct {
	deps DAGToolDeps
}

// NewDagDescribeTool creates a dag_describe tool.
func NewDagDescribeTool(deps DAGToolDeps) *DagDescribeTool {
	return &DagDescribeTool{deps: deps}
}

func (t *DagDescribeTool) Name() string { return "dag_describe" }

func (t *DagDescribeTool) Description() string {
	return "Describe a DAG node: metadata, lineage (parents/children), and span. Use to inspect the compression structure before dag_expand."
}

func (t *DagDescribeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"node_id": map[string]interface{}{
				"type":        "string",
				"description": "DAG node ID (e.g. chunk-1, section-1)",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Session to query (default: current session)",
			},
		},
		"required": []string{"node_id"},
	}
}

func (t *DagDescribeTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	nodeID, _ := args["node_id"].(string)
	if nodeID == "" {
		return ErrorResult("node_id is required")
	}
	if t.deps.Queries == nil {
		return ErrorResult("dag query store is not configured")
	}

	sessionKey, _ := args["session_key"].(string)
	if sessionKey == "" {
		sessionKey = ResolveSessionKey(ctx, t.deps.SessionFn)
	}
	if sessionKey == "" {
		sessionKey = "default"
	}

	snap, err := t.deps.Queries.GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
		AgentID:    t.deps.AgentID,
		SessionKey: sessionKey,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrorResult(fmt.Sprintf("no DAG snapshot for session %q (conversation not yet compressed)", sessionKey))
		}
		return ErrorResult(fmt.Sprintf("no DAG snapshot for session %q: %v", sessionKey, err))
	}

	node, err := t.deps.Queries.GetDAGNodeBySnapshotAndNodeID(ctx, memsqlc.GetDAGNodeBySnapshotAndNodeIDParams{
		SnapshotID: snap.ID,
		NodeID:     nodeID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrorResult(fmt.Sprintf("node %q not found in DAG", nodeID))
		}
		return ErrorResult(fmt.Sprintf("node %q not found: %v", nodeID, err))
	}

	edges, err := t.deps.Queries.ListDAGEdgesBySnapshotID(ctx, memsqlc.ListDAGEdgesBySnapshotIDParams{
		SnapshotID: snap.ID,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("list edges: %v", err))
	}

	nodes, err := t.deps.Queries.ListDAGNodesBySnapshotID(ctx, memsqlc.ListDAGNodesBySnapshotIDParams{
		SnapshotID: snap.ID,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("list nodes: %v", err))
	}

	idToNodeID := make(map[ids.UUID]string)
	for _, n := range nodes {
		idToNodeID[n.ID] = n.NodeID
	}

	var parents, children []string
	for _, e := range edges {
		if idToNodeID[e.ChildNodeID] == nodeID {
			parents = append(parents, idToNodeID[e.ParentNodeID])
		}
		if idToNodeID[e.ParentNodeID] == nodeID {
			children = append(children, idToNodeID[e.ChildNodeID])
		}
	}

	levelNames := map[int64]string{1: "chunk", 2: "section", 3: "session"}
	levelName := levelNames[node.Level]
	if levelName == "" {
		levelName = fmt.Sprintf("level-%d", node.Level)
	}

	meta := map[string]interface{}{
		"node_id":  node.NodeID,
		"level":    levelName,
		"summary":  node.Summary,
		"tokens":   node.Tokens,
		"span":     fmt.Sprintf("%d-%d", node.StartIdx, node.EndIdx),
		"parents":  parents,
		"children": children,
		"snapshot": snap.ID.String(),
	}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	return SilentResult(string(metaJSON))
}

// DagGrepTool searches session history, optionally scoped by DAG node/range.
type DagGrepTool struct {
	deps DAGToolDeps
}

// NewDagGrepTool creates a dag_grep tool.
func NewDagGrepTool(deps DAGToolDeps) *DagGrepTool {
	return &DagGrepTool{deps: deps}
}

func (t *DagGrepTool) Name() string { return "dag_grep" }

func (t *DagGrepTool) Description() string {
	return "Search (grep) in immutable session history. Optionally scope by DAG node or message range. Returns matching messages with context."
}

func (t *DagGrepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search pattern (literal substring or regex)",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Session to search (default: current session)",
			},
			"node_id": map[string]interface{}{
				"type":        "string",
				"description": "Scope to this DAG node's message range (optional)",
			},
			"regex": map[string]interface{}{
				"type":        "boolean",
				"description": "Treat query as regex (default: false = literal)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Max matches to return (default 20)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *DagGrepTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required")
	}

	sessionKey, _ := args["session_key"].(string)
	if sessionKey == "" {
		sessionKey = ResolveSessionKey(ctx, t.deps.SessionFn)
	}
	if sessionKey == "" {
		sessionKey = "default"
	}

	useRegex, _ := args["regex"].(bool)
	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 100 {
			limit = 100
		}
	}

	var startIdx, endIdx int = 0, -1
	if nodeID, ok := args["node_id"].(string); ok && nodeID != "" {
		if t.deps.Queries == nil {
			return ErrorResult("dag query store is not configured")
		}
		snap, err := t.deps.Queries.GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
			AgentID:    t.deps.AgentID,
			SessionKey: sessionKey,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrorResult(fmt.Sprintf("no DAG snapshot for session %q", sessionKey))
			}
			return ErrorResult(fmt.Sprintf("no DAG snapshot: %v", err))
		}
		node, err := t.deps.Queries.GetDAGNodeBySnapshotAndNodeID(ctx, memsqlc.GetDAGNodeBySnapshotAndNodeIDParams{
			SnapshotID: snap.ID,
			NodeID:     nodeID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrorResult(fmt.Sprintf("node %q not found in DAG", nodeID))
			}
			return ErrorResult(fmt.Sprintf("node %q not found: %v", nodeID, err))
		}
		startIdx, endIdx = int(node.StartIdx), int(node.EndIdx)
	}

	if t.deps.Queries == nil && t.deps.Lister == nil {
		return ErrorResult("session message reader is not configured")
	}

	var total int
	if t.deps.Queries != nil {
		cnt, err := t.deps.Queries.CountSessionMessages(ctx, memsqlc.CountSessionMessagesParams{
			AgentID:    t.deps.AgentID,
			SessionKey: sessionKey,
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf("count messages: %v", err))
		}
		total = int(cnt)
	} else {
		rows, err := t.deps.Lister.ListSessionMessages(ctx, t.deps.AgentID, sessionKey, "", 100000)
		if err != nil {
			return ErrorResult(fmt.Sprintf("list messages: %v", err))
		}
		total = len(rows)
	}

	if endIdx < 0 || endIdx > total {
		endIdx = total
	}
	if startIdx >= endIdx {
		return SilentResult("No messages in range.")
	}

	msgs, err := t.deps.loadSessionWindow(ctx, sessionKey, startIdx, endIdx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list messages: %v", err))
	}

	slice := msgs

	var re *regexp.Regexp
	if useRegex {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid regex: %v", err))
		}
	}

	var matches []string
	for i, m := range slice {
		idx := startIdx + i
		matched := false
		if useRegex && re != nil {
			matched = re.MatchString(m.Content)
		} else {
			matched = strings.Contains(strings.ToLower(m.Content), strings.ToLower(query))
		}
		if matched {
			matches = append(matches, fmt.Sprintf("[%d] %s: %s", idx, m.Role, m.Content))
			if len(matches) >= limit {
				break
			}
		}
	}

	if len(matches) == 0 {
		return SilentResult(fmt.Sprintf("No matches for %q in range [%d:%d].", query, startIdx, endIdx))
	}
	return SilentResult(strings.Join(matches, "\n\n"))
}
