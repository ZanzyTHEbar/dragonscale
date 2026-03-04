-- Soft delete queries (T2.4: quarantine before permanent deletion)
-- name: SoftDeleteRecallItem :exec
-- Sets suppressed_at for a recall item instead of permanently deleting it.
UPDATE recall_items
SET suppressed_at = datetime('now')
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: SoftDeleteArchivalChunks :exec
-- Sets suppressed_at for all chunks belonging to a recall item.
UPDATE archival_chunks
SET suppressed_at = datetime('now')
WHERE recall_id = sqlc.arg(recall_id);
-- name: ListQuarantinedRecallItems :many
-- Returns recall items that have been soft-deleted (suppressed) and are
-- eligible for permanent deletion (older than quarantine period).
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at,
    suppressed_at
FROM recall_items
WHERE suppressed_at IS NOT NULL
    AND suppressed_at < sqlc.arg(before_date)
ORDER BY suppressed_at ASC
LIMIT sqlc.arg(lim);
-- name: ListQuarantinedArchivalChunks :many
-- Returns archival chunks that have been soft-deleted and are
-- eligible for permanent deletion.
SELECT id,
    recall_id,
    chunk_index,
    content,
    embedding,
    source,
    hash,
    created_at,
    suppressed_at
FROM archival_chunks
WHERE suppressed_at IS NOT NULL
    AND suppressed_at < sqlc.arg(before_date)
ORDER BY suppressed_at ASC
LIMIT sqlc.arg(lim);
-- name: HardDeleteRecallItem :exec
-- Permanently deletes a recall item (after quarantine period).
DELETE FROM recall_items
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: HardDeleteArchivalChunks :exec
-- Permanently deletes archival chunks for a recall item.
DELETE FROM archival_chunks
WHERE recall_id = sqlc.arg(recall_id);
-- name: HardDeleteChunk :exec
-- Permanently deletes a single archival chunk by ID.
DELETE FROM archival_chunks
WHERE id = sqlc.arg(id);
-- name: RestoreRecallItem :exec
-- Restores a soft-deleted recall item by clearing suppressed_at.
UPDATE recall_items
SET suppressed_at = NULL
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);