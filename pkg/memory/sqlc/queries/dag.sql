-- DAG persistence queries
-- name: InsertDAGSnapshot :one
INSERT INTO dag_snapshots (
        id,
        agent_id,
        session_key,
        from_msg_idx,
        to_msg_idx,
        msg_count,
        roots_json,
        content_hash
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(from_msg_idx),
        sqlc.arg(to_msg_idx),
        sqlc.arg(msg_count),
        sqlc.arg(roots_json),
        sqlc.arg(content_hash)
    )
RETURNING id,
    agent_id,
    session_key,
    from_msg_idx,
    to_msg_idx,
    msg_count,
    roots_json,
    content_hash,
    created_at,
    updated_at;
-- name: InsertDAGNode :one
INSERT INTO dag_nodes (
        id,
        snapshot_id,
        node_id,
        level,
        summary,
        tokens,
        start_idx,
        end_idx,
        span,
        content_hash,
        metrics_json,
        metadata_json
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(snapshot_id),
        sqlc.arg(node_id),
        sqlc.arg(level),
        sqlc.arg(summary),
        sqlc.arg(tokens),
        sqlc.arg(start_idx),
        sqlc.arg(end_idx),
        sqlc.arg(span),
        sqlc.arg(content_hash),
        sqlc.arg(metrics_json),
        sqlc.arg(metadata_json)
    )
RETURNING id,
    snapshot_id,
    node_id,
    level,
    summary,
    tokens,
    start_idx,
    end_idx,
    span,
    content_hash,
    metrics_json,
    metadata_json,
    created_at,
    updated_at;
-- name: InsertDAGEdge :exec
INSERT INTO dag_edges (
        id,
        snapshot_id,
        parent_node_id,
        child_node_id,
        edge_index,
        metadata_json
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(snapshot_id),
        sqlc.arg(parent_node_id),
        sqlc.arg(child_node_id),
        sqlc.arg(edge_index),
        sqlc.arg(metadata_json)
    );
-- name: GetLatestDAGSnapshotBySession :one
SELECT id,
    agent_id,
    session_key,
    from_msg_idx,
    to_msg_idx,
    msg_count,
    roots_json,
    content_hash,
    created_at,
    updated_at
FROM dag_snapshots
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
ORDER BY created_at DESC
LIMIT 1;
-- name: GetDAGSnapshotByID :one
SELECT id,
    agent_id,
    session_key,
    from_msg_idx,
    to_msg_idx,
    msg_count,
    roots_json,
    content_hash,
    created_at,
    updated_at
FROM dag_snapshots
WHERE id = sqlc.arg(id)
LIMIT 1;
-- name: ListDAGNodesBySnapshotID :many
SELECT id,
    snapshot_id,
    node_id,
    level,
    summary,
    tokens,
    start_idx,
    end_idx,
    span,
    content_hash,
    metrics_json,
    metadata_json,
    created_at,
    updated_at
FROM dag_nodes
WHERE snapshot_id = sqlc.arg(snapshot_id)
ORDER BY start_idx ASC;
-- name: GetDAGNodeBySnapshotAndNodeID :one
SELECT id,
    snapshot_id,
    node_id,
    level,
    summary,
    tokens,
    start_idx,
    end_idx,
    span,
    content_hash,
    metrics_json,
    metadata_json,
    created_at,
    updated_at
FROM dag_nodes
WHERE snapshot_id = sqlc.arg(snapshot_id)
    AND node_id = sqlc.arg(node_id)
LIMIT 1;
-- name: ListDAGEdgesBySnapshotID :many
SELECT id,
    snapshot_id,
    parent_node_id,
    child_node_id,
    edge_index,
    metadata_json,
    created_at,
    updated_at
FROM dag_edges
WHERE snapshot_id = sqlc.arg(snapshot_id)
ORDER BY edge_index ASC,
    created_at ASC;