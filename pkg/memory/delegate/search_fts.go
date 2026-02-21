package delegate

import (
	"context"
	"database/sql"
	"strings"
	"unicode"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

const ftsSearchQuery = `
SELECT ri.id, ri.agent_id, ri.session_key, ri.role, ri.sector,
       ri.importance, ri.salience, ri.decay_rate,
       ri.content, ri.tags, ri.created_at, ri.updated_at,
       bm25(recall_items_fts) AS rank
FROM recall_items_fts
JOIN recall_items ri ON ri.rowid = recall_items_fts.rowid
WHERE recall_items_fts MATCH ?
  AND ri.agent_id = ?
ORDER BY rank ASC
LIMIT ?
`

// SearchRecallByFTS performs full-text search using FTS5 MATCH with BM25 ranking.
// The query is normalized via buildFTSMatchExpr before execution.
// Falls back to nil results (not an error) if FTS is unavailable.
func (d *LibSQLDelegate) SearchRecallByFTS(ctx context.Context, query, agentID string, limit int) ([]*memory.RecallItem, error) {
	if !d.caps.fts5 {
		return nil, nil
	}

	matchExpr := buildFTSMatchExpr(query)
	if matchExpr == "" {
		return nil, nil
	}

	stmt, err := d.stmts.get(ctx, "fts_search", ftsSearchQuery)
	if err != nil {
		return nil, nil // FTS not available
	}

	rows, err := stmt.QueryContext(ctx, matchExpr, agentID, limit)
	if err != nil {
		return nil, nil // FTS query failed, caller should fall back to LIKE
	}
	defer rows.Close()

	var items []*memory.RecallItem
	for rows.Next() {
		var (
			item   memory.RecallItem
			sector string
			rank   float64
		)
		if err := rows.Scan(
			&item.ID, &item.AgentID, &item.SessionKey, &item.Role, &sector,
			&item.Importance, &item.Salience, &item.DecayRate,
			&item.Content, &item.Tags, &item.CreatedAt, &item.UpdatedAt, &rank,
		); err != nil {
			return nil, err
		}
		item.Sector = memory.Sector(sector)
		items = append(items, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// buildFTSMatchExpr normalizes a raw search query into an FTS5 MATCH expression.
// It handles:
//   - Multiple words: joined with implicit AND
//   - Quoted phrases: passed through
//   - Special characters: cleaned for FTS5 safety
//   - Empty/invalid input: returns empty string (caller should skip search)
func buildFTSMatchExpr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// If already quoted, use as-is (phrase search)
	if strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		return raw
	}

	// Split into words, filter out FTS5-unsafe tokens
	words := strings.Fields(raw)
	var clean []string
	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != ':' && r != '.' && r != '@' && r != '/'
		})
		if w == "" {
			continue
		}
		// Escape double quotes inside tokens
		w = strings.ReplaceAll(w, `"`, `""`)
		clean = append(clean, `"`+w+`"`)
	}

	if len(clean) == 0 {
		return ""
	}

	return strings.Join(clean, " ")
}

// ensure SearchRecallByFTS is valid at compile-time
var _ = (*LibSQLDelegate)(nil).SearchRecallByFTS
var _ = (*sql.DB)(nil)
