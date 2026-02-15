-- Memory Summary queries
-- name: InsertSummary :exec
INSERT INTO memory_summaries (
        id,
        agent_id,
        session_key,
        content,
        from_msg_idx,
        to_msg_idx,
        created_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(content),
        sqlc.arg(from_msg_idx),
        sqlc.arg(to_msg_idx),
        datetime('now')
    );
-- name: ListSummaries :many
SELECT id,
    agent_id,
    session_key,
    content,
    from_msg_idx,
    to_msg_idx,
    created_at
FROM memory_summaries
WHERE agent_id = sqlc.arg(agent_id)
    AND (
        session_key = sqlc.arg(session_key)
        OR sqlc.arg(session_key) = ''
    )
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);