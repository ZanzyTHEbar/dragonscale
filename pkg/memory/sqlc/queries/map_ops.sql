-- name: InsertMapRun :one
INSERT INTO map_runs (
        id,
        agent_id,
        session_key,
        operator_kind,
        idempotency_key,
        status,
        total_items,
        queued_items,
        running_items,
        succeeded_items,
        failed_items,
        spec_fb,
        last_error
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(session_key),
        sqlc.arg(operator_kind),
        sqlc.arg(idempotency_key),
        sqlc.arg(status),
        sqlc.arg(total_items),
        sqlc.arg(queued_items),
        sqlc.arg(running_items),
        sqlc.arg(succeeded_items),
        sqlc.arg(failed_items),
        sqlc.arg(spec_fb),
        sqlc.arg(last_error)
    )
RETURNING *;
-- name: GetMapRunByIdempotencyKey :one
SELECT *
FROM map_runs
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
    AND operator_kind = sqlc.arg(operator_kind)
    AND idempotency_key = sqlc.arg(idempotency_key)
LIMIT 1;
-- name: GetMapRunByID :one
SELECT *
FROM map_runs
WHERE id = sqlc.arg(id)
LIMIT 1;
-- name: ListMapRunsBySession :many
SELECT *
FROM map_runs
WHERE agent_id = sqlc.arg(agent_id)
    AND session_key = sqlc.arg(session_key)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: UpdateMapRunProgress :one
UPDATE map_runs
SET status = sqlc.arg(status),
    queued_items = sqlc.arg(queued_items),
    running_items = sqlc.arg(running_items),
    succeeded_items = sqlc.arg(succeeded_items),
    failed_items = sqlc.arg(failed_items),
    last_error = sqlc.arg(last_error),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: InsertMapItem :one
INSERT INTO map_items (
        id,
        run_id,
        item_index,
        status,
        attempts,
        last_error,
        input_fb,
        output_fb,
        input_hash,
        output_hash
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(run_id),
        sqlc.arg(item_index),
        sqlc.arg(status),
        sqlc.arg(attempts),
        sqlc.arg(last_error),
        sqlc.arg(input_fb),
        sqlc.arg(output_fb),
        sqlc.arg(input_hash),
        sqlc.arg(output_hash)
    )
RETURNING *;
-- name: GetMapItemByID :one
SELECT *
FROM map_items
WHERE id = sqlc.arg(id)
LIMIT 1;
-- name: GetMapItemByRunAndIndex :one
SELECT *
FROM map_items
WHERE run_id = sqlc.arg(run_id)
    AND item_index = sqlc.arg(item_index)
LIMIT 1;
-- name: ListMapItemsByRunPaged :many
SELECT *
FROM map_items
WHERE run_id = sqlc.arg(run_id)
ORDER BY item_index ASC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: MarkMapItemRunning :one
UPDATE map_items
SET status = 'running',
    attempts = attempts + 1,
    last_error = NULL,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: MarkMapItemSucceeded :one
UPDATE map_items
SET status = 'succeeded',
    output_fb = sqlc.arg(output_fb),
    output_hash = sqlc.arg(output_hash),
    last_error = NULL,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: MarkMapItemFailed :one
UPDATE map_items
SET status = 'failed',
    last_error = sqlc.arg(last_error),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: CountMapItemsByRun :one
SELECT count(*) AS count
FROM map_items
WHERE run_id = sqlc.arg(run_id);
-- name: CountMapItemsByRunAndStatus :many
SELECT status,
    count(*) AS count
FROM map_items
WHERE run_id = sqlc.arg(run_id)
GROUP BY status;