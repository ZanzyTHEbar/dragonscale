-- Agent KV Store queries
-- name: GetKV :one
SELECT agent_id,
    key,
    value,
    updated_at
FROM agent_kv
WHERE agent_id = sqlc.arg(agent_id)
    AND key = sqlc.arg(key)
LIMIT 1;
-- name: UpsertKV :one
INSERT INTO agent_kv (agent_id, key, value, updated_at)
VALUES (
        sqlc.arg(agent_id),
        sqlc.arg(key),
        sqlc.arg(value),
        datetime('now')
    ) ON CONFLICT (agent_id, key) DO
UPDATE
SET value = excluded.value,
    updated_at = excluded.updated_at
RETURNING agent_id, key, value, updated_at;
-- name: DeleteKV :exec
DELETE FROM agent_kv
WHERE agent_id = sqlc.arg(agent_id)
    AND key = sqlc.arg(key);
-- name: ListKVByPrefix :many
SELECT agent_id,
    key,
    value,
    updated_at
FROM agent_kv
WHERE agent_id = sqlc.arg(agent_id)
    AND key LIKE sqlc.arg(prefix) || '%'
ORDER BY key
LIMIT sqlc.arg(lim);