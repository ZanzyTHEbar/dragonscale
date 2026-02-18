package delegate

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

func BenchmarkListRecallItems(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = d.UpsertKV(ctx, "bench-agent", "bench-key", "bench-value")
	}
}

func BenchmarkGetKV(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := context.Background()
	_ = d.UpsertKV(ctx, "bench-agent", "bench-key", "bench-value")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = d.GetKV(ctx, "bench-agent", "bench-key")
	}
}

func BenchmarkInsertAuditEntry(b *testing.B) {
	d := newBenchDelegate(b)
	ctx := context.Background()

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

func newBenchDelegate(b *testing.B) *LibSQLDelegate {
	b.Helper()
	d, err := NewLibSQLInMemory()
	if err != nil {
		b.Fatal(err)
	}
	if err := d.Init(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { d.Close() })
	return d
}
