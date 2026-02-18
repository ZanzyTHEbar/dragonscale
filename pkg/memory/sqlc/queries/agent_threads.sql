-- name: CreateAgentThread :one
INSERT INTO agent_threads (id, conversation_id, title, metadata_json)
VALUES (?, ?, ?, ?)
RETURNING *;
-- name: ListAgentThreadsByConversationID :many
SELECT *
FROM agent_threads
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: AddAgentThreadMessage :one
INSERT INTO agent_thread_messages (id, thread_id, role, content, metadata_json)
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentThreadMessagesByThreadID :many
SELECT *
FROM agent_thread_messages
WHERE thread_id = ?
ORDER BY created_at ASC;
-- name: ListAgentThreadMessagesByThreadIDDescLimit :many
SELECT *
FROM agent_thread_messages
WHERE thread_id = ?
ORDER BY created_at DESC
LIMIT ?;