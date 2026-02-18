-- name: CreateAgentRun :one
INSERT INTO agent_runs (id, conversation_id, status, metadata_json)
VALUES (?, ?, ?, ?)
RETURNING *;
-- name: UpdateAgentRunStatus :one
UPDATE agent_runs
SET status = ?,
    metadata_json = ?,
    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE id = ?
RETURNING *;
-- name: GetLatestAgentRunByConversationID :one
SELECT *
FROM agent_runs
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT 1;
-- name: AddAgentRunState :one
INSERT INTO agent_run_states (id, run_id, step_index, state, snapshot_json)
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentRunStatesByRunID :many
SELECT *
FROM agent_run_states
WHERE run_id = ?
ORDER BY step_index ASC
LIMIT sqlc.arg(lim);
-- name: GetLatestAgentRunStateByRunID :one
SELECT *
FROM agent_run_states
WHERE run_id = ?
ORDER BY step_index DESC
LIMIT 1;
-- name: GetAgentRunStateByID :one
SELECT *
FROM agent_run_states
WHERE id = ?
LIMIT 1;
-- name: AddAgentStateTransition :one
INSERT INTO agent_state_transitions (
        id,
        run_id,
        step_index,
        from_state,
        to_state,
        trigger,
        at,
        meta_json,
        error
    )
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentStateTransitionsByRunID :many
SELECT *
FROM agent_state_transitions
WHERE run_id = ?
ORDER BY at ASC
LIMIT sqlc.arg(lim);
-- name: CreateAgentCheckpoint :one
INSERT INTO agent_checkpoints (
        id,
        conversation_id,
        name,
        run_state_id,
        metadata_json
    )
VALUES (?, ?, ?, ?, ?)
RETURNING *;
-- name: ListAgentCheckpointsByConversationID :many
SELECT *
FROM agent_checkpoints
WHERE conversation_id = ?
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: GetAgentCheckpointByConversationIDAndName :one
SELECT *
FROM agent_checkpoints
WHERE conversation_id = ?
    AND name = ?
LIMIT 1;