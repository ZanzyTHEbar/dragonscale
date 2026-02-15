-- Vector index for ANN search on archival chunk embeddings.
-- Uses libSQL's native vector indexing. Gracefully skipped if not supported.
CREATE INDEX IF NOT EXISTS idx_chunks_embedding ON archival_chunks(libsql_vector_idx(embedding));