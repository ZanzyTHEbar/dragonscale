-- name: CreateAgentConversation :one
INSERT INTO agent_conversations (id, title)
VALUES (?, ?)
RETURNING *;
-- name: GetAgentConversation :one
SELECT *
FROM agent_conversations
WHERE id = ?
LIMIT 1;
-- name: GetLatestAgentConversationByTitle :one
SELECT *
FROM agent_conversations
WHERE title = ?
ORDER BY created_at DESC
LIMIT 1;
-- name: ListAgentConversations :many
SELECT *
FROM agent_conversations
ORDER BY created_at DESC
LIMIT ?;
-- name: UpdateAgentConversationTitle :one
UPDATE agent_conversations
SET title = ?,
    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE id = ?
RETURNING *;