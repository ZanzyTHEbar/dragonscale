-- name: EnqueueJob :one
INSERT INTO jobs (
        id,
        kind,
        status,
        run_at,
        max_attempts,
        payload_json,
        dedupe_key
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(kind),
        'queued',
        coalesce(
            sqlc.arg(run_at),
            strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
        ),
        coalesce(sqlc.arg(max_attempts), 3),
        coalesce(sqlc.arg(payload_json), '{}'),
        sqlc.arg(dedupe_key)
    ) ON CONFLICT(kind, dedupe_key)
WHERE dedupe_key IS NOT NULL DO
UPDATE
SET status = 'queued',
    run_at = excluded.run_at,
    max_attempts = excluded.max_attempts,
    payload_json = excluded.payload_json,
    attempts = 0,
    last_error = NULL,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING *;
-- name: FindNextRunnableJob :one
SELECT id
FROM jobs
WHERE status = 'queued'
    AND run_at <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
ORDER BY run_at ASC,
    created_at ASC
LIMIT 1;
-- name: ClaimJobByID :one
UPDATE jobs
SET status = 'running',
    attempts = attempts + 1,
    locked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    locked_by = sqlc.arg(locked_by),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    last_error = NULL
WHERE id = sqlc.arg(id)
    AND status = 'queued'
RETURNING *;
-- name: MarkJobSucceeded :one
UPDATE jobs
SET status = 'succeeded',
    completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    last_error = NULL
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: RequeueJob :one
UPDATE jobs
SET status = 'queued',
    run_at = coalesce(
        sqlc.arg(run_at),
        strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    last_error = sqlc.arg(last_error),
    locked_at = NULL,
    locked_by = NULL,
    completed_at = NULL
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: MarkJobFailed :one
UPDATE jobs
SET status = 'failed',
    completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id)
RETURNING *;
-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = sqlc.arg(id)
LIMIT 1;
-- name: ListJobs :many
SELECT *
FROM jobs
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: CountJobsByStatus :many
SELECT status,
    count(*) AS count
FROM jobs
GROUP BY status;