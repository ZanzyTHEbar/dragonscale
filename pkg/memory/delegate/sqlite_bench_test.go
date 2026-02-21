package delegate

import (
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

func BenchmarkListRecallItems(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	agent := "bench-agent"
	session := "bench-sess"

	for i := 0; i < 50; i++ {
		_ = d.InsertRecallItem(ctx, &memory.RecallItem{
			ID:         ids.New(),
			AgentID:    agent,
			SessionKey: session,
			Role:       "user",
			Sector:     memory.SectorEpisodic,
			Importance: 0.5,
			Content:    "Benchmark recall content for testing delegate read performance.",
			Tags:       "bench",
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = d.ListRecallItems(ctx, agent, session, 20, 0)
	}
}

func BenchmarkGetWorkingContext(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	agent := "bench-agent"
	session := "bench-sess"

	_ = d.UpsertWorkingContext(ctx, agent, session, "Working context content for benchmarking.")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = d.GetWorkingContext(ctx, agent, session)
	}
}

func BenchmarkUpsertKV(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = d.UpsertKV(ctx, "bench-agent", "bench-key", "bench-value")
	}
}

func BenchmarkGetKV(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	_ = d.UpsertKV(ctx, "bench-agent", "bench-key", "bench-value")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = d.GetKV(ctx, "bench-agent", "bench-key")
	}
}

func BenchmarkInsertAuditEntry(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = d.InsertAuditEntry(ctx, &memory.AuditEntry{
			ID:         ids.New(),
			AgentID:    "bench-agent",
			SessionKey: "bench-sess",
			Action:     "tool_call",
			Target:     "read_file",
			Input:      `{"path": "/tmp/test"}`,
		})
	}
}

// BenchmarkInsertRecallItems_Sequential measures sequential single-insert performance.
func BenchmarkInsertRecallItems_Sequential(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for i := 0; i < 10; i++ {
			_ = d.InsertRecallItem(ctx, &memory.RecallItem{
				ID:         ids.New(),
				AgentID:    "bench-agent",
				SessionKey: "bench-sess",
				Role:       "user",
				Sector:     memory.SectorEpisodic,
				Importance: 0.5,
				Content:    "Batch benchmark recall content item for performance comparison.",
				Tags:       "bench",
			})
		}
	}
}

// BenchmarkInsertRecallItems_Batch measures batch-tx insert performance for 10 items.
// Compare with BenchmarkInsertRecallItems_Sequential to quantify WAL savings.
func BenchmarkInsertRecallItems_Batch(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		items := make([]*memory.RecallItem, 10)
		for i := range items {
			items[i] = &memory.RecallItem{
				ID:         ids.New(),
				AgentID:    "bench-agent",
				SessionKey: "bench-sess",
				Role:       "user",
				Sector:     memory.SectorEpisodic,
				Importance: 0.5,
				Content:    "Batch benchmark recall content item for performance comparison.",
				Tags:       "bench",
			}
		}
		_ = d.InsertRecallItemsBatch(ctx, items)
	}
}

// BenchmarkInsertArchivalChunks_Sequential measures sequential chunk inserts.
func BenchmarkInsertArchivalChunks_Sequential(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	recallID := ids.New()
	_ = d.InsertRecallItem(ctx, &memory.RecallItem{
		ID: recallID, AgentID: "bench-agent", SessionKey: "s",
		Role: "user", Sector: memory.SectorEpisodic, Content: "parent",
	})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for i := 0; i < 5; i++ {
			_ = d.InsertArchivalChunk(ctx, &memory.ArchivalChunk{
				ID: ids.New(), RecallID: recallID, ChunkIndex: i,
				Content: "chunk content for archival benchmark",
				Source:  "bench", Hash: "abc",
			})
		}
	}
}

// BenchmarkInsertArchivalChunks_Batch measures batch-tx chunk inserts for 5 chunks.
func BenchmarkInsertArchivalChunks_Batch(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := b.Context()
	recallID := ids.New()
	_ = d.InsertRecallItem(ctx, &memory.RecallItem{
		ID: recallID, AgentID: "bench-agent", SessionKey: "s",
		Role: "user", Sector: memory.SectorEpisodic, Content: "parent",
	})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		chunks := make([]*memory.ArchivalChunk, 5)
		for i := range chunks {
			chunks[i] = &memory.ArchivalChunk{
				ID: ids.New(), RecallID: recallID, ChunkIndex: i,
				Content: "chunk content for archival benchmark",
				Source:  "bench", Hash: "abc",
			}
		}
		_ = d.InsertArchivalChunksBatch(ctx, chunks)
	}
}

func newBenchDelegate(b *testing.B) *LibSQLDelegate {
	b.Helper()
	ctx := b.Context()
	d, err := NewLibSQLInMemory()
	if err != nil {
		b.Fatal(err)
	}
	if err := d.Init(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { d.Close() })
	return d
}
