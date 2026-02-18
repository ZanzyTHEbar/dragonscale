-- name: AddAgentMention :one
INSERT INTO agent_mentions (
        id,
        conversation_id,
        message_id,
        kind,
        target_id,
        raw,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentMentionsByConversationID :many
SELECT *
FROM agent_mentions
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);