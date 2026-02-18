-- name: CreateAgentConversationLink :one
INSERT INTO agent_conversation_links (
        id,
        conversation_id,
        linked_conversation_id,
        kind,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentConversationLinksByConversationID :many
SELECT *
FROM agent_conversation_links
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: DeleteAgentConversationLink :exec
DELETE FROM agent_conversation_links
WHERE conversation_id = ?
    AND linked_conversation_id = ?
    AND kind = ?;