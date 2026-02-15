package delegate

import (
	"context"
	"database/sql"
	"sync"
)

// stmtCache provides a thread-safe prepared statement cache for hand-written SQL
// queries (FTS5, vector search) that aren't managed by sqlc.
type stmtCache struct {
	mu    sync.RWMutex
	db    *sql.DB
	stmts map[string]*sql.Stmt
}

func newStmtCache(db *sql.DB) *stmtCache {
	return &stmtCache{
		db:    db,
		stmts: make(map[string]*sql.Stmt),
	}
}

// get returns a cached prepared statement, creating it on first access.
func (c *stmtCache) get(ctx context.Context, key, query string) (*sql.Stmt, error) {
	c.mu.RLock()
	stmt, ok := c.stmts[key]
	c.mu.RUnlock()
	if ok {
		return stmt, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if stmt, ok = c.stmts[key]; ok {
		return stmt, nil
	}

	stmt, err := c.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	c.stmts[key] = stmt
	return stmt, nil
}

// close releases all cached statements.
func (c *stmtCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stmt := range c.stmts {
		stmt.Close()
	}
	c.stmts = make(map[string]*sql.Stmt)
}
