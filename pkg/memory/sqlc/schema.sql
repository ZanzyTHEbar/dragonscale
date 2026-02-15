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