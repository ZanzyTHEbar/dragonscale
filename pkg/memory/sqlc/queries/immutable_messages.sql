-- Immutable Messages queries (LCM ADR-001: append-only verbatim message store)
-- name: InsertImmutableMessage :one
INSERT INTO immutable_messages (
        id,
        session_key,
        role,
        content,
        tool_call_id,
        tool_calls,
        token_estimate
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(session_key),
        sqlc.arg(role),
        sqlc.arg(content),
        sqlc.arg(tool_call_id),
        sqlc.arg(tool_calls),
        sqlc.arg(token_estimate)
    )
RETURNING id,
    session_key,
    role,
    content,
    tool_call_id,
    tool_calls,
    token_estimate,
    created_at;
-- name: GetImmutableMessage :one
SELECT id,
    session_key,
    role,
    content,
    tool_call_id,
    tool_calls,
    token_estimate,
    created_at
FROM immutable_messages
WHERE id = sqlc.arg(id)
LIMIT 1;
-- name: ListImmutableMessages :many
SELECT id,
    session_key,
    role,
    content,
    tool_call_id,
    tool_calls,
    token_estimate,
    created_at
FROM immutable_messages
WHERE session_key = sqlc.arg(session_key)
ORDER BY created_at ASC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: CountImmutableMessages :one
SELECT COUNT(*)
FROM immutable_messages
WHERE session_key = sqlc.arg(session_key);