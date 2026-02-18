-- name: AddAgentToolResult :one
INSERT INTO agent_tool_results (
        id,
        conversation_id,
        run_id,
        step_index,
        tool_call_id,
        tool_name,
        full_key,
        preview,
        chunk_count,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;
-- name: GetAgentToolResultByRunIDAndToolCallID :one
SELECT *
FROM agent_tool_results
WHERE run_id = ?
    AND tool_call_id = ?
LIMIT 1;
-- name: ListAgentToolResultsByConversationID :many
SELECT *
FROM agent_tool_results
WHERE conversation_id = ?
ORDER BY created_at DESC;
-- name: ListAgentToolResultsByConversationIDLimit :many
SELECT *
FROM agent_tool_results
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT ?;
-- name: ListAgentToolResultsByRunID :many
SELECT *
FROM agent_tool_results
WHERE run_id = ?
ORDER BY step_index ASC,
    created_at ASC
LIMIT sqlc.arg(lim);