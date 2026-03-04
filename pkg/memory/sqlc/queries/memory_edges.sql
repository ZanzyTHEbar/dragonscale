-- Memory Graph Edges queries (ADR-004: typed relational memory)
-- name: InsertMemoryEdge :one
INSERT INTO memory_edges (from_id, to_id, edge_type, weight)
VALUES (
        sqlc.arg(from_id),
        sqlc.arg(to_id),
        sqlc.arg(edge_type),
        sqlc.arg(weight)
    )
RETURNING id,
    from_id,
    to_id,
    edge_type,
    weight,
    created_at;
-- name: ListMemoryEdgesForItem :many
SELECT id,
    from_id,
    to_id,
    edge_type,
    weight,
    created_at
FROM memory_edges
WHERE from_id = sqlc.arg(memory_id)
    OR to_id = sqlc.arg(memory_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: CountMemoryEdgesForItem :one
SELECT COUNT(*)
FROM memory_edges
WHERE from_id = sqlc.arg(memory_id)
    OR to_id = sqlc.arg(memory_id);
-- name: ListMemoryEdgesByType :many
SELECT id,
    from_id,
    to_id,
    edge_type,
    weight,
    created_at
FROM memory_edges
WHERE edge_type = sqlc.arg(edge_type)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: DeleteMemoryEdgesForItem :exec
DELETE FROM memory_edges
WHERE from_id = sqlc.arg(memory_id)
    OR to_id = sqlc.arg(memory_id);