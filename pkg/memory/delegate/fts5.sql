-- FTS5 virtual table for keyword search on recall items.
-- Standalone FTS5 table (NOT external-content mode) — more reliable with go-libsql.
-- Uses unicode61 tokenizer with extended tokenchars for domain-specific identifiers
-- and prefix indexes for efficient prefix matching.
-- NOTE: tokenchars uses equals-sign syntax (not space+quotes) per go-libsql compatibility.
CREATE VIRTUAL TABLE IF NOT EXISTS recall_items_fts USING fts5(
    content,
    tags,
    tokenize = 'unicode61 tokenchars=:-_@./',
    prefix = '2 3 4 5 6 7'
);
-- Triggers to keep standalone FTS5 table in sync with recall_items.
-- Uses DELETE+INSERT pattern for UPDATE (FTS5 standard approach).
CREATE TRIGGER IF NOT EXISTS recall_items_ai
AFTER
INSERT ON recall_items BEGIN
INSERT INTO recall_items_fts(rowid, content, tags)
VALUES (new.rowid, new.content, new.tags);
END;
CREATE TRIGGER IF NOT EXISTS recall_items_ad
AFTER DELETE ON recall_items BEGIN
DELETE FROM recall_items_fts
WHERE rowid = old.rowid;
END;
CREATE TRIGGER IF NOT EXISTS recall_items_au
AFTER
UPDATE ON recall_items BEGIN
DELETE FROM recall_items_fts
WHERE rowid = old.rowid;
INSERT INTO recall_items_fts(rowid, content, tags)
VALUES (new.rowid, new.content, new.tags);
END;