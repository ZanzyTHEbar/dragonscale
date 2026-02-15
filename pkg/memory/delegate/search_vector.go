package delegate

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

const vectorSearchANN = `
WITH vt AS (
    SELECT id FROM vector_top_k('idx_chunks_embedding', vector32(?), ?)
)
SELECT c.id, c.recall_id, c.content, c.source,
       vector_distance_cos(c.embedding, vector32(?)) AS distance
FROM vt JOIN archival_chunks c ON c.rowid = vt.id
WHERE c.embedding IS NOT NULL
ORDER BY distance ASC
LIMIT ? OFFSET ?
`

const vectorSearchBruteForce = `
SELECT c.id, c.recall_id, c.content, c.source,
       vector_distance_cos(c.embedding, vector32(?)) AS distance
FROM archival_chunks c
WHERE c.embedding IS NOT NULL
ORDER BY distance ASC
LIMIT ? OFFSET ?
`

// SearchArchivalByVector performs vector similarity search on archival chunks.
// Uses vector_top_k() ANN when available, falling back to brute-force
// vector_distance_cos() scan, and finally returning nil if neither works
// (caller should use Go-side VectorSearch as last resort).
func (d *LibSQLDelegate) SearchArchivalByVector(ctx context.Context, queryVec memory.Embedding, limit, offset int) ([]memory.SearchResult, error) {
	if len(queryVec) == 0 || limit <= 0 {
		return nil, nil
	}

	vecStr := vectorToString(queryVec)

	// Try ANN path first
	if d.caps.vectorTopK {
		results, err := d.vectorSearchANN(ctx, vecStr, limit, offset)
		if err == nil {
			return results, nil
		}
		// Fall through to brute-force on ANN failure
	}

	// Brute-force path using vector_distance_cos
	results, err := d.vectorSearchBrute(ctx, vecStr, limit, offset)
	if err != nil {
		return nil, nil // Caller should fall back to Go-side
	}
	return results, nil
}

func (d *LibSQLDelegate) vectorSearchANN(ctx context.Context, vecStr string, limit, offset int) ([]memory.SearchResult, error) {
	stmt, err := d.stmts.get(ctx, "vec_ann", vectorSearchANN)
	if err != nil {
		return nil, err
	}

	// vector_top_k needs extra k to account for offset
	topK := limit + offset
	rows, err := stmt.QueryContext(ctx, vecStr, topK, vecStr, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanVectorResults(rows)
}

func (d *LibSQLDelegate) vectorSearchBrute(ctx context.Context, vecStr string, limit, offset int) ([]memory.SearchResult, error) {
	stmt, err := d.stmts.get(ctx, "vec_brute", vectorSearchBruteForce)
	if err != nil {
		return nil, err
	}

	rows, err := stmt.QueryContext(ctx, vecStr, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanVectorResults(rows)
}

func scanVectorResults(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]memory.SearchResult, error) {
	var results []memory.SearchResult
	for rows.Next() {
		var (
			id       ids.UUID
			recallID []byte // scanned but unused
			content  string
			source   string
			distance float64
		)
		if err := rows.Scan(&id, &recallID, &content, &source, &distance); err != nil {
			return nil, err
		}
		// Convert distance to similarity score (1 - cosine_distance)
		score := 1.0 - distance
		results = append(results, memory.SearchResult{
			ID:      id,
			Content: content,
			Source:  source,
			Score:   score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// vectorToString formats an Embedding as a vector string for libSQL's vector32() function.
// Output format: "[0.123, 0.456, ...]"
func vectorToString(vec memory.Embedding) string {
	if len(vec) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%g", v)
	}
	b.WriteByte(']')
	return b.String()
}

// extractVector decodes an F32_BLOB binary blob into a float32 slice.
// This is the inverse of the F32_BLOB wire format: little-endian IEEE 754 float32.
func extractVector(blob []byte, dims int) ([]float32, error) {
	expected := dims * 4
	if len(blob) != expected {
		return nil, fmt.Errorf("vector blob size %d, expected %d for %d dims", len(blob), expected, dims)
	}
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec, nil
}
