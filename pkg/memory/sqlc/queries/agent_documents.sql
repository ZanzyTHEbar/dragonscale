-- Agent Documents queries
-- name: GetDocument :one
SELECT id,
    agent_id,
    name,
    category,
    content,
    version,
    is_active,
    created_at,
    updated_at
FROM agent_documents
WHERE agent_id = sqlc.arg(agent_id)
    AND name = sqlc.arg(name);
-- name: UpsertDocument :exec
INSERT INTO agent_documents (
        id,
        agent_id,
        name,
        category,
        content,
        version,
        is_active,
        created_at,
        updated_at
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(name),
        sqlc.arg(category),
        sqlc.arg(content),
        1,
        1,
        datetime('now'),
        datetime('now')
    ) ON CONFLICT (agent_id, name) DO
UPDATE
SET content = excluded.content,
    category = excluded.category,
    version = agent_documents.version + 1,
    is_active = 1,
    updated_at = datetime('now');
-- name: ListDocumentsByCategory :many
SELECT id,
    agent_id,
    name,
    category,
    content,
    version,
    is_active,
    created_at,
    updated_at
FROM agent_documents
WHERE agent_id = sqlc.arg(agent_id)
    AND category = sqlc.arg(category)
    AND is_active = 1
ORDER BY name;
-- name: DeleteDocument :exec
DELETE FROM agent_documents
WHERE agent_id = sqlc.arg(agent_id)
    AND name = sqlc.arg(name);
-- name: ListAllDocuments :many
SELECT id,
    agent_id,
    name,
    category,
    content,
    version,
    is_active,
    created_at,
    updated_at
FROM agent_documents
WHERE agent_id = sqlc.arg(agent_id)
    AND is_active = 1
ORDER BY category,
    name;