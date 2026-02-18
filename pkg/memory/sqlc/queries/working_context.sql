-- Working Context queries
-- name: GetWorkingContext :one
SELECT agent_id,
    session_key,
    content,
    updated_at
FROM working_context
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
LIMIT 1;
-- name: UpsertWorkingContext :one
INSERT INTO working_context (agent_id, session_key, content, updated_at)
VALUES (
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(content),
        datetime('now')
    ) ON CONFLICT (agent_id, session_key) DO
UPDATE
SET content = excluded.content,
    updated_at = excluded.updated_at
RETURNING agent_id, session_key, content, updated_at;