-- Batch decay of recall item importance (Cortex decay task)
-- name: DecayRecallImportanceBatch :exec
UPDATE recall_items
SET importance = MAX(
        recall_items.importance * sqlc.arg(factor),
        sqlc.arg(floor_val)
    ),
    updated_at = datetime('now')
WHERE recall_items.id IN (
        SELECT ri.id
        FROM recall_items ri
        WHERE ri.importance > sqlc.arg(floor_val)
            AND ri.suppressed_at IS NULL
        ORDER BY ri.updated_at ASC
        LIMIT sqlc.arg(batch_size)
    );
-- name: CountArchivalChunksWithoutEmbedding :one
SELECT COUNT(*)
FROM archival_chunks
WHERE embedding IS NULL
    AND suppressed_at IS NULL;
-- name: ListArchivalChunksWithoutEmbedding :many
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at
FROM archival_chunks
WHERE embedding IS NULL
    AND suppressed_at IS NULL
LIMIT sqlc.arg(lim);
-- name: UpdateArchivalChunkEmbedding :exec
UPDATE archival_chunks
SET embedding = sqlc.arg(embedding)
WHERE id = sqlc.arg(id);