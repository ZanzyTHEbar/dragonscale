-- name: AddAgentMessageRevision :one
INSERT INTO agent_message_revisions (
        id,
        message_id,
        editor,
        old_content,
        new_content,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentMessageRevisionsByMessageID :many
SELECT *
FROM agent_message_revisions
WHERE message_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);