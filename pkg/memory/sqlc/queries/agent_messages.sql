-- name: AddAgentMessage :one
INSERT INTO agent_messages (
        id,
        conversation_id,
        role,
        content,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentMessagesByConversationID :many
SELECT *
FROM agent_messages
WHERE conversation_id = ?
ORDER BY created_at ASC;
-- name: ListAgentMessagesByConversationIDLimit :many
SELECT *
FROM agent_messages
WHERE conversation_id = ?
ORDER BY created_at ASC
LIMIT ?;