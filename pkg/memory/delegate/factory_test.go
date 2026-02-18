package delegate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

func TestNewFromConfig_LocalOnly_DefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	defaultPath := filepath.Join(tmpDir, "test.db")

	cfg := config.MemoryConfig{
		Enabled: true,
	}

	d, err := NewFromConfig(cfg, defaultPath)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer d.Close()

	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if d.IsReplica() {
		t.Error("expected local-only mode, got replica")
	}

	// Verify it's functional
	err = d.UpsertWorkingContext(context.Background(), "agent", "sess", "test content")
	if err != nil {
		t.Fatalf("UpsertWorkingContext: %v", err)
	}
}

func TestNewFromConfig_LocalOnly_CustomPath(t *testing.T) {
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom.db")

	cfg := config.MemoryConfig{
		Enabled: true,
		DBPath:  customPath,
	}

	d, err := NewFromConfig(cfg, filepath.Join(tmpDir, "default.db"))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer d.Close()

	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// The custom path should have been used — check it exists
	if _, err := os.Stat(customPath); os.IsNotExist(err) {
		t.Error("expected custom DB path to exist")
	}
}

func TestNewFromConfig_CustomDims(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.MemoryConfig{
		Enabled:       true,
		EmbeddingDims: 384,
	}

	d, err := NewFromConfig(cfg, dbPath)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer d.Close()

	if d.EmbeddingDims() != 384 {
		t.Errorf("expected 384 dims, got %d", d.EmbeddingDims())
	}
}

func TestNewFromConfig_DefaultDims(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.MemoryConfig{
		Enabled: true,
	}

	d, err := NewFromConfig(cfg, dbPath)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer d.Close()

	if d.EmbeddingDims() != DefaultEmbeddingDims {
		t.Errorf("expected %d default dims, got %d", DefaultEmbeddingDims, d.EmbeddingDims())
	}
}

func TestNewFromConfig_ReplicaFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.MemoryConfig{
		Enabled: true,
		Sync: config.MemorySyncConfig{
			SyncURL:   "libsql://nonexistent-db.turso.io",
			AuthToken: "invalid-token",
		},
	}

	// Should fall back to local-only mode when replica setup fails
	d, err := NewFromConfig(cfg, dbPath)
	if err != nil {
		t.Fatalf("NewFromConfig should fall back, got error: %v", err)
	}
	defer d.Close()

	if d.IsReplica() {
		t.Error("expected fallback to local-only mode")
	}

	// Should still be functional in local mode
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

func TestNewFromConfig_LocalFullRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "roundtrip.db")

	cfg := config.MemoryConfig{
		Enabled:       true,
		EmbeddingDims: 768,
	}

	d, err := NewFromConfig(cfg, dbPath)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer d.Close()

	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx := context.Background()

	// Working context round-trip
	if err := d.UpsertWorkingContext(ctx, "a1", "s1", "hello"); err != nil {
		t.Fatalf("UpsertWorkingContext: %v", err)
	}
	wc, err := d.GetWorkingContext(ctx, "a1", "s1")
	if err != nil {
		t.Fatalf("GetWorkingContext: %v", err)
	}
	if wc.Content != "hello" {
		t.Errorf("expected 'hello', got %q", wc.Content)
	}

	// Recall item round-trip
	item := &memory.RecallItem{
		ID:      ids.New(),
		AgentID: "a1",
		Role:    "user",
		Sector:  memory.SectorEpisodic,
		Content: "test recall",
	}
	if err := d.InsertRecallItem(ctx, item); err != nil {
		t.Fatalf("InsertRecallItem: %v", err)
	}

	got, err := d.GetRecallItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetRecallItem: %v", err)
	}
	if got.Content != "test recall" {
		t.Errorf("expected 'test recall', got %q", got.Content)
	}
}

func TestSyncConfig_Defaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Memory.Enabled {
		t.Error("memory should be enabled by default")
	}
	if cfg.Memory.EmbeddingDims != 768 {
		t.Errorf("expected 768 default dims, got %d", cfg.Memory.EmbeddingDims)
	}
	if cfg.Memory.OffloadThresholdTokens != 4000 {
		t.Errorf("expected 4000 offload threshold, got %d", cfg.Memory.OffloadThresholdTokens)
	}
	if cfg.Memory.Sync.SyncIntervalSeconds != 60 {
		t.Errorf("expected 60s sync interval, got %d", cfg.Memory.Sync.SyncIntervalSeconds)
	}
	if cfg.Memory.Sync.SyncURL != "" {
		t.Error("sync URL should be empty by default")
	}
}
