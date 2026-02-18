-- Agent Audit Log queries
-- name: InsertAuditEntry :exec
INSERT INTO agent_audit_log (
        id,
        agent_id,
        session_key,
        action,
        target,
        input,
        output,
        duration_ms,
        created_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(action),
        sqlc.arg(target),
        sqlc.arg(input),
        sqlc.arg(output),
        sqlc.arg(duration_ms),
        datetime('now')
    );
-- name: ListAuditEntries :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    input,
    output,
    duration_ms,
    created_at
FROM agent_audit_log
WHERE agent_id = sqlc.arg(agent_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);
-- name: ListAuditEntriesByAction :many
SELECT id,
    agent_id,
    session_key,
    action,
    target,
    input,
    output,
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
    input,
    output,
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