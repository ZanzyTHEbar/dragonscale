-- name: CreateAgentConversationFork :one
INSERT INTO agent_conversation_forks (
        id,
        parent_conversation_id,
        child_conversation_id,
        checkpoint_id,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentConversationForksByParentConversationID :many
SELECT *
FROM agent_conversation_forks
WHERE parent_conversation_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: GetAgentConversationForkByChildConversationID :one
SELECT *
FROM agent_conversation_forks
WHERE child_conversation_id = ?
LIMIT 1;