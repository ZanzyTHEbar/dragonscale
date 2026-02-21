package dag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

// PersistSnapshot is the serializable shape for persisting a DAG snapshot.
type PersistSnapshot struct {
	FromMsgIdx int
	ToMsgIdx   int
	MsgCount   int
	DAG        *DAG
}

// contentHash returns a deterministic hash of the DAG structure for deduplication.
func contentHash(d *DAG) string {
	if d == nil || len(d.Nodes) == 0 {
		return ""
	}
	h := sha256.New()
	roots, _ := json.Marshal(d.Roots)
	h.Write(roots)

	nodeIDs := make([]string, 0, len(d.Nodes))
	for id := range d.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	for _, id := range nodeIDs {
		n := d.Nodes[id]
		h.Write([]byte(id))
		h.Write([]byte(n.Summary))
		h.Write([]byte(fmt.Sprintf("%d:%d:%d", n.StartIdx, n.EndIdx, n.Tokens)))
		if len(n.Children) > 0 {
			children := append([]string(nil), n.Children...)
			sort.Strings(children)
			for _, child := range children {
				h.Write([]byte(child))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DAGPersister persists DAG snapshots to storage.
// Implemented by delegates that support DAG persistence.
type DAGPersister interface {
	PersistDAG(ctx context.Context, agentID, sessionKey string, snap *PersistSnapshot) error
}

// PersistDAG persists a DAG snapshot via sqlc in a transaction.
func PersistDAG(ctx context.Context, db *sql.DB, q *memsqlc.Queries, agentID, sessionKey string, snap *PersistSnapshot) error {
	if snap == nil || snap.DAG == nil || len(snap.DAG.Nodes) == 0 {
		return nil
	}

	hash := contentHash(snap.DAG)
	snapshotID := ids.New()

	rootsJSON, err := json.Marshal(snap.DAG.Roots)
	if err != nil {
		return fmt.Errorf("marshal roots: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qTx := q.WithTx(tx)

	_, err = qTx.InsertDAGSnapshot(ctx, memsqlc.InsertDAGSnapshotParams{
		ID:          snapshotID,
		AgentID:     agentID,
		SessionKey:  sessionKey,
		FromMsgIdx:  int64(snap.FromMsgIdx),
		ToMsgIdx:    int64(snap.ToMsgIdx),
		MsgCount:    int64(snap.MsgCount),
		RootsJson:   string(rootsJSON),
		ContentHash: hash,
	})
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	nodeIDsOrdered := make([]string, 0, len(snap.DAG.Nodes))
	for nodeID := range snap.DAG.Nodes {
		nodeIDsOrdered = append(nodeIDsOrdered, nodeID)
	}
	sort.Strings(nodeIDsOrdered)

	nodeIDs := make(map[string]ids.UUID)
	for _, nodeID := range nodeIDsOrdered {
		node := snap.DAG.Nodes[nodeID]
		nodeUUID := ids.New()
		nodeIDs[nodeID] = nodeUUID

		nodeHash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%d", node.ID, node.Summary, node.StartIdx, node.EndIdx, node.Tokens)))

		metricsJSONBytes, _ := json.Marshal(map[string]any{
			"tokens":         node.Tokens,
			"span":           node.Span(),
			"children_count": len(node.Children),
		})
		metadataJSONBytes, _ := json.Marshal(map[string]any{
			"level":    node.Level.String(),
			"children": node.Children,
		})
		_, err = qTx.InsertDAGNode(ctx, memsqlc.InsertDAGNodeParams{
			ID:           nodeUUID,
			SnapshotID:   snapshotID,
			NodeID:       node.ID,
			Level:        int64(node.Level),
			Summary:      node.Summary,
			Tokens:       int64(node.Tokens),
			StartIdx:     int64(node.StartIdx),
			EndIdx:       int64(node.EndIdx),
			Span:         int64(node.Span()),
			ContentHash:  hex.EncodeToString(nodeHash[:]),
			MetricsJson:  metricsJSONBytes,
			MetadataJson: metadataJSONBytes,
		})
		if err != nil {
			return fmt.Errorf("insert node %s: %w", node.ID, err)
		}
	}

	for _, nodeID := range nodeIDsOrdered {
		node := snap.DAG.Nodes[nodeID]
		for edgeIdx, childID := range node.Children {
			childUUID, ok := nodeIDs[childID]
			if !ok {
				continue
			}
			parentUUID := nodeIDs[node.ID]
			edgeMetadata, _ := json.Marshal(map[string]any{
				"parent_node_id": node.ID,
				"child_node_id":  childID,
			})
			err = qTx.InsertDAGEdge(ctx, memsqlc.InsertDAGEdgeParams{
				ID:           ids.New(),
				SnapshotID:   snapshotID,
				ParentNodeID: parentUUID,
				ChildNodeID:  childUUID,
				EdgeIndex:    int64(edgeIdx),
				MetadataJson: edgeMetadata,
			})
			if err != nil {
				return fmt.Errorf("insert edge %s->%s: %w", node.ID, childID, err)
			}
		}
	}

	return tx.Commit()
}
