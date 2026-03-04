-- Consolidation queries for memory graph maintenance (ADR-004)
-- name: ListRecallItemsForConsolidation :many
-- Returns recall items created after the cutoff time, joined with their first
-- archival chunk's embedding for similarity comparison.
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
    ri.updated_at,
    ac.embedding as embedding
FROM recall_items ri
    LEFT JOIN archival_chunks ac ON ac.recall_id = ri.id
    AND ac.chunk_index = 0
WHERE ri.created_at > sqlc.arg(cutoff)
    AND ri.suppressed_at IS NULL
    AND (
        sqlc.arg(agent_id) = ''
        OR ri.agent_id = sqlc.arg(agent_id)
    )
ORDER BY ri.created_at DESC
LIMIT sqlc.arg(lim);
-- name: FindSimilarRecallItems :many
-- Finds recall items with high cosine similarity to the given embedding.
-- Uses libSQL's built-in vector similarity if available, otherwise returns
-- candidates for manual comparison.
SELECT ri.id,
    ri.agent_id,
    ri.content,
    ac.embedding as embedding,
    vector_distance_cosine(ac.embedding, sqlc.arg(query_embedding)) as distance
FROM recall_items ri
    JOIN archival_chunks ac ON ac.recall_id = ri.id
    AND ac.chunk_index = 0
WHERE ri.agent_id = sqlc.arg(agent_id)
    AND ri.id != sqlc.arg(exclude_id)
    AND ac.embedding IS NOT NULL
ORDER BY distance ASC
LIMIT sqlc.arg(lim);