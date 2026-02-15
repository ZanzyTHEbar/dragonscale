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
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at
FROM archival_chunks
WHERE id = sqlc.arg(id);
-- name: ListArchivalChunks :many
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at
FROM archival_chunks
WHERE recall_id = sqlc.arg(recall_id)
ORDER BY chunk_index;
-- name: DeleteArchivalChunksByRecall :exec
DELETE FROM archival_chunks
WHERE recall_id = sqlc.arg(recall_id);
-- name: CountArchivalChunks :one
SELECT COUNT(*)
FROM archival_chunks;
-- name: ListAllArchivalChunks :many
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at
FROM archival_chunks
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: GetArchivalChunksByIDs :many
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at
FROM archival_chunks
WHERE id IN (sqlc.slice('ids'));