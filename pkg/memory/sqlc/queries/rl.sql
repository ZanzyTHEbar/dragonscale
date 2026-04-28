-- RL (Reinforcement Learning) queries for Memelord integration
-- Task baseline queries for per-agent performance statistics
-- name: GetTaskBaseline :one
-- Get the baseline statistics for an agent
SELECT agent_id,
    count,
    mean_tokens,
    mean_errors,
    mean_user_corrections,
    m2_tokens,
    m2_errors,
    m2_user_corrections,
    updated_at
FROM task_baselines
WHERE agent_id = sqlc.arg(agent_id)
LIMIT 1;
-- name: UpdateTaskBaseline :exec
-- Insert or replace task baseline statistics for an agent
INSERT INTO task_baselines (
        agent_id,
        count,
        mean_tokens,
        mean_errors,
        mean_user_corrections,
        m2_tokens,
        m2_errors,
        m2_user_corrections,
        updated_at
    )
VALUES (
        sqlc.arg(agent_id),
        sqlc.arg(count),
        sqlc.arg(mean_tokens),
        sqlc.arg(mean_errors),
        sqlc.arg(mean_user_corrections),
        sqlc.arg(m2_tokens),
        sqlc.arg(m2_errors),
        sqlc.arg(m2_user_corrections),
        datetime('now')
    ) ON CONFLICT (agent_id) DO
UPDATE
SET count = excluded.count,
    mean_tokens = excluded.mean_tokens,
    mean_errors = excluded.mean_errors,
    mean_user_corrections = excluded.mean_user_corrections,
    m2_tokens = excluded.m2_tokens,
    m2_errors = excluded.m2_errors,
    m2_user_corrections = excluded.m2_user_corrections,
    updated_at = excluded.updated_at;
-- name: UpdateMemoryWeight :exec
-- Update the RL weight and credit for a specific memory item
UPDATE recall_items
SET rl_weight = sqlc.arg(rl_weight),
    rl_credit = sqlc.arg(rl_credit),
    updated_at = datetime('now')
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: UpdateMemorySelfReportScore :exec
-- Update the self-reported score for a memory item
UPDATE recall_items
SET self_report_score = sqlc.arg(self_report_score),
    updated_at = datetime('now')
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: IncrementTaskRetrievalCount :exec
-- Increment the task retrieval counter for a memory item
UPDATE recall_items
SET task_retrieval_count = task_retrieval_count + 1,
    updated_at = datetime('now')
WHERE id = sqlc.arg(id)
    AND agent_id = sqlc.arg(agent_id);
-- name: GetMemoriesByRetrievalCount :many
-- Get memories ordered by their task retrieval count (for RL analysis)
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
    rl_weight,
    rl_credit,
    self_report_score,
    task_retrieval_count,
    created_at,
    updated_at
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL
ORDER BY task_retrieval_count DESC
LIMIT sqlc.arg(lim);
-- name: ListHighValueMemories :many
-- List memories with high RL weights (credits) for priority retention
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
    rl_weight,
    rl_credit,
    self_report_score,
    task_retrieval_count,
    created_at,
    updated_at
FROM recall_items
WHERE agent_id = sqlc.arg(agent_id)
    AND suppressed_at IS NULL
    AND (
        rl_credit > sqlc.arg(min_credit)
        OR rl_weight > sqlc.arg(min_weight)
    )
ORDER BY rl_credit DESC NULLS LAST
LIMIT sqlc.arg(lim);
-- name: StoreTaskCompletion :one
-- Store a task completion record for RL analysis
INSERT INTO task_completions (
        id,
        agent_id,
        conversation_id,
        run_id,
        description,
        tokens_used,
        tool_calls,
        errors,
        user_corrections,
        completed
    )
VALUES (
        sqlc.arg(id),
        sqlc.arg(agent_id),
        sqlc.arg(conversation_id),
        sqlc.arg(run_id),
        sqlc.arg(description),
        sqlc.arg(tokens_used),
        sqlc.arg(tool_calls),
        sqlc.arg(errors),
        sqlc.arg(user_corrections),
        sqlc.arg(completed)
    )
RETURNING id,
    agent_id,
    conversation_id,
    run_id,
    description,
    tokens_used,
    tool_calls,
    errors,
    user_corrections,
    completed,
    created_at;
-- name: GetCompletedTasks :many
-- Get tasks completed since the given time for RL processing
SELECT id,
    agent_id,
    conversation_id,
    run_id,
    description,
    tokens_used,
    tool_calls,
    errors,
    user_corrections,
    completed,
    created_at
FROM task_completions
WHERE agent_id = sqlc.arg(agent_id)
	AND unixepoch(created_at) > unixepoch(sqlc.arg(since))
	AND completed = 1
ORDER BY created_at ASC;
-- name: StoreTaskRetrieval :exec
-- Store a memory retrieval record for a task
INSERT INTO task_retrievals (id, task_id, memory_id, similarity)
VALUES (
        sqlc.arg(id),
        sqlc.arg(task_id),
        sqlc.arg(memory_id),
        sqlc.arg(similarity)
    ) ON CONFLICT (task_id, memory_id) DO
UPDATE
SET similarity = excluded.similarity;
-- name: GetRetrievedMemories :many
-- Get memories retrieved during a specific task with their self-report scores
SELECT tr.memory_id,
    tr.similarity,
    ri.self_report_score
FROM task_retrievals tr
    JOIN recall_items ri ON tr.memory_id = ri.id
WHERE tr.task_id = sqlc.arg(task_id);
-- name: ListActiveAgents :many
-- Get all unique agent IDs that have completed tasks (for multi-agent processing)
SELECT DISTINCT agent_id
FROM task_completions
WHERE unixepoch(created_at) > unixepoch(sqlc.arg(since))
ORDER BY agent_id;
-- name: GetHighTokenSessions :many
-- Get sessions with high token usage grouped by conversation/agent
SELECT conversation_id as session_id,
    agent_id,
    SUM(COALESCE(tokens_used, 0)) as total_tokens,
    COUNT(*) as task_count
FROM task_completions
WHERE created_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-24 hours')
GROUP BY conversation_id,
    agent_id
HAVING SUM(COALESCE(tokens_used, 0)) > sqlc.arg(min_tokens)
ORDER BY total_tokens DESC
LIMIT sqlc.arg(lim);
