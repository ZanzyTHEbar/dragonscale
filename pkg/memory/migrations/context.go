package migrations

import "context"

type ctxKey int

const embeddingDimsKey ctxKey = iota

// WithEmbeddingDims returns a context carrying the embedding vector dimension
// for use by Go-based migrations that create F32_BLOB columns.
func WithEmbeddingDims(ctx context.Context, dims int) context.Context {
	return context.WithValue(ctx, embeddingDimsKey, dims)
}

func embeddingDimsFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(embeddingDimsKey).(int); ok && v > 0 {
		return v
	}
	return 768 // default: sentence-transformers
}
