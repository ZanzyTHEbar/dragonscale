-- Archival Chunk queries
-- name: InsertArchivalChunk :exec
INSERT INTO archival_chunks (
        id,
        recall_id,
        chunk_index,
        content,
        embedding,
        source,
        hash,
        created_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(recall_id),
        sqlc.arg(chunk_index),
        sqlc.arg(content),
        sqlc.arg(embedding),
        sqlc.arg(source),
        sqlc.arg(hash),
        datetime('now')
    );
-- name: GetArchivalChunk :one
SELECT ac.id,
    ac.recall_id,
    ac.chunk_index,
    ac.content,
    ac.embedding,
    ac.source,
    ac.hash,
    ac.created_at
FROM archival_chunks ac
    JOIN recall_items ri ON ac.recall_id = ri.id
WHERE ac.id = sqlc.arg(id)
    AND ri.agent_id = sqlc.arg(agent_id)
LIMIT 1;
-- name: ListArchivalChunks :many
SELECT ac.id,
    ac.recall_id,
    ac.chunk_index,
    ac.content,
    ac.embedding,
    ac.source,
    ac.hash,
    ac.created_at
FROM archival_chunks ac
    JOIN recall_items ri ON ac.recall_id = ri.id
WHERE ac.recall_id = sqlc.arg(recall_id)
    AND ri.agent_id = sqlc.arg(agent_id)
ORDER BY ac.chunk_index
LIMIT sqlc.arg(lim);
-- name: DeleteArchivalChunksByRecall :exec
DELETE FROM archival_chunks
WHERE recall_id = sqlc.arg(recall_id);
-- name: CountArchivalChunks :one
SELECT COUNT(*)
FROM archival_chunks ac
    JOIN recall_items ri ON ac.recall_id = ri.id
WHERE ri.agent_id = sqlc.arg(agent_id);
-- name: ListAllArchivalChunks :many
SELECT ac.id,
    ac.recall_id,
    ac.chunk_index,
    ac.content,
    ac.embedding,
    ac.source,
    ac.hash,
    ac.created_at
FROM archival_chunks ac
    JOIN recall_items ri ON ac.recall_id = ri.id
WHERE ri.agent_id = sqlc.arg(agent_id)
ORDER BY ac.created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: GetArchivalChunksByIDs :many
SELECT ac.id,
    ac.recall_id,
    ac.chunk_index,
    ac.content,
    ac.embedding,
    ac.source,
    ac.hash,
    ac.created_at
FROM archival_chunks ac
    JOIN recall_items ri ON ac.recall_id = ri.id
WHERE ac.id IN (sqlc.slice('ids'))
    AND ri.agent_id = sqlc.arg(agent_id);