-- PicoClaw Memory System Schema (SQLite / libSQL)
-- 3-tier MemGPT-style: working_context (hot), recall_items (warm), archival_chunks (cold)
-- NOTE: This schema is parsed by sqlc. The actual runtime DDL (with F32_BLOB, etc.)
-- is in delegate/schemaDDL(). Keep column names and types in sync.
--
-- Entity IDs: BLOB PRIMARY KEY storing 16-byte UUIDv7 (RFC 9562).
-- External identifiers (agent_id, session_key): remain TEXT.
-- Working context: hot tier, single mutable buffer per agent/session
CREATE TABLE IF NOT EXISTS working_context (
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, session_key)
);
-- Recall items: warm tier, scored and classified memory entries
CREATE TABLE IF NOT EXISTS recall_items (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'system',
    sector TEXT NOT NULL DEFAULT 'episodic',
    importance REAL NOT NULL DEFAULT 0.5,
    salience REAL NOT NULL DEFAULT 0.5,
    decay_rate REAL NOT NULL DEFAULT 0.01,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_recall_agent_session ON recall_items(agent_id, session_key);
CREATE INDEX IF NOT EXISTS idx_recall_sector ON recall_items(sector);
CREATE INDEX IF NOT EXISTS idx_recall_importance ON recall_items(importance DESC);
CREATE INDEX IF NOT EXISTS idx_recall_created ON recall_items(created_at DESC);
-- NOTE: FTS5 virtual table and sync triggers are created in the
-- delegate's Init() method since sqlc cannot parse virtual table DDL.
-- Archival chunks: cold tier, chunked + embedded content
-- NOTE: sqlc sees embedding as BLOB. The real DDL uses F32_BLOB(N).
CREATE TABLE IF NOT EXISTS archival_chunks (
    id BLOB PRIMARY KEY,
    recall_id BLOB NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding BLOB,
    source TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunks_recall ON archival_chunks(recall_id);
CREATE INDEX IF NOT EXISTS idx_chunks_source ON archival_chunks(source);
-- Memory summaries: compacted conversation summaries
CREATE TABLE IF NOT EXISTS memory_summaries (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    from_msg_idx INTEGER NOT NULL DEFAULT 0,
    to_msg_idx INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_summaries_agent_session ON memory_summaries(agent_id, session_key);
-- Agent KV store: generic key-value pairs for agent state, preferences, config
CREATE TABLE IF NOT EXISTS agent_kv (
    agent_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, key)
);
-- Agent documents: versioned named documents (bootstrap files, identity, etc.)
CREATE TABLE IF NOT EXISTS agent_documents (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'bootstrap',
    content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_id, name)
);
CREATE INDEX IF NOT EXISTS idx_docs_agent_cat ON agent_documents(agent_id, category);
CREATE INDEX IF NOT EXISTS idx_docs_name ON agent_documents(name);
-- Agent audit log: append-only record of tool calls, state changes, etc.
CREATE TABLE IF NOT EXISTS agent_audit_log (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    input TEXT,
    output TEXT,
    duration_ms INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_agent_time ON agent_audit_log(agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action ON agent_audit_log(action);
-- ============================================================================
-- Agent Runtime State Tables
-- ============================================================================
-- Conversations: minimal parent entity for agent runs and messages.
CREATE TABLE IF NOT EXISTS agent_conversations (
    id BLOB PRIMARY KEY,
    title TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- Messages: conversation turns.
CREATE TABLE IF NOT EXISTS agent_messages (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_messages_conversation_created_at ON agent_messages(conversation_id, created_at);
-- Runs: a single invocation of the agent runtime (one RunTurn call).
CREATE TABLE IF NOT EXISTS agent_runs (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'running',
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_runs_conversation_created_at ON agent_runs(conversation_id, created_at);
-- Run states: snapshots captured per step.
CREATE TABLE IF NOT EXISTS agent_run_states (
    id BLOB PRIMARY KEY,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    state TEXT NOT NULL,
    snapshot_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(run_id, step_index)
);
CREATE INDEX IF NOT EXISTS idx_agent_run_states_run_step ON agent_run_states(run_id, step_index);
-- Transition log: debugging and resumability.
CREATE TABLE IF NOT EXISTS agent_state_transitions (
    id BLOB PRIMARY KEY,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    trigger TEXT NOT NULL,
    at DATETIME NOT NULL,
    meta_json JSON NOT NULL DEFAULT '{}',
    error TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_state_transitions_run_step_at ON agent_state_transitions(run_id, step_index, at);
-- Checkpoints: named snapshots for later restore.
CREATE TABLE IF NOT EXISTS agent_checkpoints (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    run_state_id BLOB NOT NULL REFERENCES agent_run_states(id) ON DELETE RESTRICT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(conversation_id, name)
);
CREATE INDEX IF NOT EXISTS idx_agent_checkpoints_conversation_created_at ON agent_checkpoints(conversation_id, created_at);
-- Tool results: offloaded tool outputs.
CREATE TABLE IF NOT EXISTS agent_tool_results (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    full_key TEXT NOT NULL,
    preview TEXT,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(run_id, tool_call_id)
);
CREATE INDEX IF NOT EXISTS idx_agent_tool_results_conversation_created_at ON agent_tool_results(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_agent_tool_results_run_step ON agent_tool_results(run_id, step_index);
CREATE INDEX IF NOT EXISTS idx_agent_tool_results_tool_name ON agent_tool_results(tool_name);
-- ============================================================================
-- Job Queue
-- ============================================================================
CREATE TABLE IF NOT EXISTS jobs (
    id BLOB PRIMARY KEY,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    run_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    locked_at DATETIME,
    locked_by TEXT,
    payload_json JSON NOT NULL DEFAULT '{}',
    dedupe_key TEXT,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    completed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_jobs_status_run_at ON jobs(status, run_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_kind_dedupe ON jobs(kind, dedupe_key)
WHERE dedupe_key IS NOT NULL;
-- ============================================================================
-- Conversation Graph (forks, links, threads, mentions, edits)
-- ============================================================================
-- Forks: parent→child conversation via checkpoint.
CREATE TABLE IF NOT EXISTS agent_conversation_forks (
    id BLOB PRIMARY KEY,
    parent_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    child_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    checkpoint_id BLOB NOT NULL REFERENCES agent_checkpoints(id) ON DELETE RESTRICT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(child_conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_agent_conversation_forks_parent_created_at ON agent_conversation_forks(parent_conversation_id, created_at DESC);
-- Links: user-created relationships between conversations (merge, reference, etc.).
CREATE TABLE IF NOT EXISTS agent_conversation_links (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    linked_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'merge',
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(conversation_id, linked_conversation_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_agent_conversation_links_conversation_id_created_at ON agent_conversation_links(conversation_id, created_at DESC);
-- Threads: sub-conversations within a main conversation.
CREATE TABLE IF NOT EXISTS agent_threads (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    title TEXT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_threads_conversation_id_created_at ON agent_threads(conversation_id, created_at DESC);
-- Thread messages.
CREATE TABLE IF NOT EXISTS agent_thread_messages (
    id BLOB PRIMARY KEY,
    thread_id BLOB NOT NULL REFERENCES agent_threads(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_thread_messages_thread_id_created_at ON agent_thread_messages(thread_id, created_at ASC);
-- Mentions: captured from messages (conversation, thread, file references).
CREATE TABLE IF NOT EXISTS agent_mentions (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    message_id BLOB REFERENCES agent_messages(id) ON DELETE
    SET NULL,
        kind TEXT NOT NULL,
        target_id BLOB NOT NULL,
        raw TEXT NOT NULL,
        metadata_json JSON NOT NULL DEFAULT '{}',
        created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
        updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_mentions_conversation_id_created_at ON agent_mentions(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_mentions_kind_target_id ON agent_mentions(kind, target_id);
-- Message revisions: edit history.
CREATE TABLE IF NOT EXISTS agent_message_revisions (
    id BLOB PRIMARY KEY,
    message_id BLOB NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
    editor TEXT NOT NULL,
    old_content TEXT NOT NULL,
    new_content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_message_revisions_message_id_created_at ON agent_message_revisions(message_id, created_at DESC);