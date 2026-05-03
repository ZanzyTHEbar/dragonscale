-- Recall Item queries
-- name: InsertRecallItem :one
INSERT INTO recall_items (
        id,
        agent_id,
        session_key,
        role,
        sector,
        importance,
        salience,
        decay_rate,
        content,
        tags,
        rl_weight,
        created_at,
        updated_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(role),
        sqlc.arg(sector),
        sqlc.arg(importance),
        sqlc.arg(salience),
        sqlc.arg(decay_rate),
        sqlc.arg(content),
        sqlc.arg(tags),
        sqlc.arg(rl_weight),
        datetime('now'),
        datetime('now')
    )
RETURNING id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    rl_weight,
    created_at,
    updated_at;
-- name: GetRecallItem :one
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL
LIMIT 1;
-- name: GetRecallItemByID :one
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE id = sqlc.arg(id)
    AND suppressed_at IS NULL
LIMIT 1;
-- name: UpdateRecallItem :exec
UPDATE recall_items
SET role = sqlc.arg(role),
    sector = sqlc.arg(sector),
    importance = sqlc.arg(importance),
    salience = sqlc.arg(salience),
    decay_rate = sqlc.arg(decay_rate),
    content = sqlc.arg(content),
    tags = sqlc.arg(tags),
    updated_at = datetime('now')
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: DeleteRecallItem :exec
DELETE FROM recall_items
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: ListRecallItems :many
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL
    AND (
        session_key = sqlc.arg(session_key)
        OR sqlc.arg(session_key) = ''
    )
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: SearchRecallByKeyword :many
SELECT ri.id,
    ri.agent_id,
    ri.session_key,
    ri.role,
    ri.sector,
    ri.importance,
    ri.salience,
    ri.decay_rate,
    ri.content,
    ri.tags,
    ri.created_at,
    ri.updated_at
FROM recall_items ri
WHERE ri.content LIKE '%' || sqlc.arg(keyword) || '%'
    AND ri.agent_id = sqlc.arg(agent_id)
    AND ri.suppressed_at IS NULL
ORDER BY ri.importance DESC
LIMIT sqlc.arg(lim);
-- name: CountRecallItems :one
SELECT COUNT(*)
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL
    AND (
        session_key = sqlc.arg(session_key)
        OR sqlc.arg(session_key) = ''
    );
-- name: GetRecallItemsByIDs :many
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE id IN (sqlc.slice('ids'))
    AND agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL;
-- name: InsertSessionMessage :one
INSERT INTO recall_items (
        id,
        agent_id,
        session_key,
        role,
        sector,
        importance,
        salience,
        decay_rate,
        content,
        tags,
        created_at,
        updated_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(role),
        'episodic',
        0.5,
        0.5,
        0.01,
        sqlc.arg(content),
        'session-message',
        datetime('now'),
        datetime('now')
    )
RETURNING id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at;
-- name: ListSessionMessages :many
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
    AND tags = 'session-message'
    AND suppressed_at IS NULL
    AND (
        role = sqlc.arg(role)
        OR sqlc.arg(role) = ''
    )
ORDER BY created_at ASC
LIMIT sqlc.arg(lim);
-- name: ListSessionMessagesPaged :many
SELECT id,
    agent_id,
    session_key,
    role,
    sector,
    importance,
    salience,
    decay_rate,
    content,
    tags,
    created_at,
    updated_at
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
    AND tags = 'session-message'
    AND suppressed_at IS NULL
    AND (
        role = sqlc.arg(role)
        OR sqlc.arg(role) = ''
    )
ORDER BY created_at ASC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: CountSessionMessages :one
SELECT COUNT(*)
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
    AND tags = 'session-message'
    AND suppressed_at IS NULL;