-- Agent Audit Log queries
-- name: InsertAuditEntry :one
INSERT INTO agent_audit_log (
        id,
        agent_id,
        session_key,
        action,
        target,
        tool_call_id,
        input,
        output,
        success,
        error_msg,
        duration_ms,
        created_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(action),
        sqlc.arg(target),
        sqlc.arg(tool_call_id),
        sqlc.arg(input),
        sqlc.arg(output),
        sqlc.arg(success),
        sqlc.arg(error_msg),
        sqlc.arg(duration_ms),
        strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    )
RETURNING id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at;
-- name: ListAuditEntries :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: ListAuditEntriesGlobal :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at
FROM agent_audit_log
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: ListAuditEntriesGlobalSincePaged :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at
FROM agent_audit_log
WHERE julianday(created_at) > julianday(sqlc.arg(since))
ORDER BY created_at ASC,
    id ASC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: ListAuditEntriesByAction :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
    AND action = sqlc.arg(action)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: CountAuditEntries :one
SELECT COUNT(*)
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id);
-- name: ListAuditEntriesBySession :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    tool_call_id,
    input,
    output,
    success,
    error_msg,
    duration_ms,
    created_at
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: PruneOldAuditEntries :exec
DELETE FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
    AND created_at < sqlc.arg(before);
-- name: CountAuditEntriesByAction :one
SELECT COUNT(*)
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
    AND action = sqlc.arg(action);