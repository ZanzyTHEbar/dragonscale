package memory

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
)

// mockDelegate captures all calls for testing migration without a real DB.
type mockDelegate struct {
	recallItems     []*RecallItem
	workingContexts map[string]string
}

func newMockDelegate() *mockDelegate {
	return &mockDelegate{workingContexts: make(map[string]string)}
}

func (m *mockDelegate) Init(_ context.Context) error { return nil }
func (m *mockDelegate) Close() error                 { return nil }
func (m *mockDelegate) GetWorkingContext(_ context.Context, agentID, sk string) (*WorkingContext, error) {
	if c, ok := m.workingContexts[agentID+"/"+sk]; ok {
		return &WorkingContext{AgentID: agentID, SessionKey: sk, Content: c}, nil
	}
	return nil, nil
}
func (m *mockDelegate) UpsertWorkingContext(_ context.Context, agentID, sk, content string) error {
	m.workingContexts[agentID+"/"+sk] = content
	return nil
}
func (m *mockDelegate) InsertRecallItem(_ context.Context, item *RecallItem) error {
	m.recallItems = append(m.recallItems, item)
	return nil
}
func (m *mockDelegate) GetRecallItem(_ context.Context, _ string, _ ids.UUID) (*RecallItem, error) {
	return nil, nil
}
func (m *mockDelegate) UpdateRecallItem(_ context.Context, _ *RecallItem) error        { return nil }
func (m *mockDelegate) DeleteRecallItem(_ context.Context, _ string, _ ids.UUID) error { return nil }
func (m *mockDelegate) ListRecallItems(_ context.Context, _, _ string, _, _ int) ([]*RecallItem, error) {
	return nil, nil
}
func (m *mockDelegate) SearchRecallByKeyword(_ context.Context, _, _ string, _ int) ([]*RecallItem, error) {
	return nil, nil
}
func (m *mockDelegate) SearchRecallByFTS(_ context.Context, _, _ string, _ int) ([]*RecallItem, error) {
	return nil, nil
}
func (m *mockDelegate) SearchArchivalByVector(_ context.Context, _ Embedding, _, _ int) ([]SearchResult, error) {
	return nil, nil
}
func (m *mockDelegate) InsertArchivalChunk(_ context.Context, _ *ArchivalChunk) error { return nil }
func (m *mockDelegate) GetArchivalChunk(_ context.Context, _ string, _ ids.UUID) (*ArchivalChunk, error) {
	return nil, nil
}
func (m *mockDelegate) ListArchivalChunks(_ context.Context, _ string, _ ids.UUID) ([]*ArchivalChunk, error) {
	return nil, nil
}
func (m *mockDelegate) ListAllArchivalChunks(_ context.Context, _ string, _, _ int) ([]*ArchivalChunk, error) {
	return nil, nil
}
func (m *mockDelegate) DeleteArchivalChunks(_ context.Context, _ ids.UUID) error { return nil }
func (m *mockDelegate) InsertSummary(_ context.Context, _ *MemorySummary) error  { return nil }
func (m *mockDelegate) ListSummaries(_ context.Context, _, _ string, _ int) ([]*MemorySummary, error) {
	return nil, nil
}
func (m *mockDelegate) CountRecallItems(_ context.Context, _, _ string) (int, error) {
	return len(m.recallItems), nil
}
func (m *mockDelegate) CountArchivalChunks(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *mockDelegate) HasVectorSearch() bool                                        { return false }
func (m *mockDelegate) HasFTS() bool                                                 { return false }
func (m *mockDelegate) GetKV(_ context.Context, _, _ string) (string, error)         { return "", nil }
func (m *mockDelegate) UpsertKV(_ context.Context, _, _, _ string) error             { return nil }
func (m *mockDelegate) DeleteKV(_ context.Context, _, _ string) error                { return nil }
func (m *mockDelegate) ListKVByPrefix(_ context.Context, _, _ string, _ int) (map[string]string, error) {
	return nil, nil
}
func (m *mockDelegate) GetDocument(_ context.Context, _, _ string) (*AgentDocument, error) {
	return nil, nil
}
func (m *mockDelegate) UpsertDocument(_ context.Context, _ *AgentDocument) error { return nil }
func (m *mockDelegate) DeleteDocument(_ context.Context, _, _ string) error      { return nil }
func (m *mockDelegate) ListDocumentsByCategory(_ context.Context, _, _ string) ([]*AgentDocument, error) {
	return nil, nil
}
func (m *mockDelegate) ListAllDocuments(_ context.Context, _ string) ([]*AgentDocument, error) {
	return nil, nil
}
func (m *mockDelegate) InsertAuditEntry(_ context.Context, _ *AuditEntry) error { return nil }
func (m *mockDelegate) ListAuditEntries(_ context.Context, _ string, _ int) ([]*AuditEntry, error) {
	return nil, nil
}
func (m *mockDelegate) ListAuditEntriesByAction(_ context.Context, _, _ string, _ int) ([]*AuditEntry, error) {
	return nil, nil
}
func (m *mockDelegate) CountAuditEntries(_ context.Context, _ string) (int, error) { return 0, nil }

func writeSessionFile(t *testing.T, dir, name string, sess SessionFile) {
	t.Helper()
	data, err := jsonv2.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}

func TestMigrateFileSessions_Basic(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	writeSessionFile(t, sessDir, "sess1.json", SessionFile{
		Key: "session-1",
		Messages: []SessionMsg{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
		Created: time.Now().Add(-time.Hour),
		Updated: time.Now(),
	})

	writeSessionFile(t, sessDir, "sess2.json", SessionFile{
		Key:     "session-2",
		Summary: "Talked about Go programming",
		Messages: []SessionMsg{
			{Role: "user", Content: "Tell me about Go"},
		},
	})

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}

	if result.SessionsFound != 2 {
		t.Errorf("expected 2 sessions found, got %d", result.SessionsFound)
	}
	if result.SessionsMigrated != 2 {
		t.Errorf("expected 2 sessions migrated, got %d", result.SessionsMigrated)
	}
	if result.ItemsCreated != 3 {
		t.Errorf("expected 3 items, got %d", result.ItemsCreated)
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}

	// Check recall items were created with correct data
	if len(del.recallItems) != 3 {
		t.Fatalf("expected 3 recall items, got %d", len(del.recallItems))
	}
	if del.recallItems[0].Role != "user" {
		t.Errorf("expected 'user' role, got %q", del.recallItems[0].Role)
	}
	if del.recallItems[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", del.recallItems[0].Content)
	}
	if del.recallItems[0].SessionKey != "session-1" {
		t.Errorf("expected 'session-1', got %q", del.recallItems[0].SessionKey)
	}
	if del.recallItems[0].Tags != "migrated" {
		t.Errorf("expected 'migrated' tag, got %q", del.recallItems[0].Tags)
	}

	// Check summary was stored as working context
	wc, err := del.GetWorkingContext(context.Background(), "picoclaw", "session-2")
	if err != nil {
		t.Fatalf("GetWorkingContext: %v", err)
	}
	if wc == nil || wc.Content != "Talked about Go programming" {
		t.Errorf("expected summary as working context, got %v", wc)
	}
}

func TestMigrateFileSessions_Idempotent(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	writeSessionFile(t, sessDir, "sess.json", SessionFile{
		Key: "s1",
		Messages: []SessionMsg{
			{Role: "user", Content: "test"},
		},
	})

	// First run
	result1, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if result1.SessionsMigrated != 1 {
		t.Fatalf("expected 1, got %d", result1.SessionsMigrated)
	}

	// Second run should be a no-op (marker file exists)
	result2, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if result2 != nil {
		t.Error("expected nil result for already-migrated directory")
	}

	// Still only 1 item
	if len(del.recallItems) != 1 {
		t.Errorf("expected 1 recall item (no duplicates), got %d", len(del.recallItems))
	}
}

func TestMigrateFileSessions_EmptyDir(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}
	if result.SessionsFound != 0 {
		t.Errorf("expected 0 sessions, got %d", result.SessionsFound)
	}
}

func TestMigrateFileSessions_NonexistentDir(t *testing.T) {
	del := newMockDelegate()

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", "/nonexistent/path")
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nonexistent dir")
	}
}

func TestMigrateFileSessions_SkipsEmptyMessages(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	writeSessionFile(t, sessDir, "sess.json", SessionFile{
		Key: "s1",
		Messages: []SessionMsg{
			{Role: "user", Content: "real content"},
			{Role: "assistant", Content: ""},
			{Role: "user", Content: "   "},
		},
	})

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}
	if result.ItemsCreated != 1 {
		t.Errorf("expected 1 item (empty msgs skipped), got %d", result.ItemsCreated)
	}
}

func TestMigrateFileSessions_FallbackKey(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	// Session with empty key — should use filename
	writeSessionFile(t, sessDir, "custom-key.json", SessionFile{
		Key: "",
		Messages: []SessionMsg{
			{Role: "user", Content: "test"},
		},
	})

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}
	if result.ItemsCreated != 1 {
		t.Fatalf("expected 1 item, got %d", result.ItemsCreated)
	}
	if del.recallItems[0].SessionKey != "custom-key" {
		t.Errorf("expected 'custom-key' from filename, got %q", del.recallItems[0].SessionKey)
	}
}

func TestMigrateFileSessions_MalformedJSON(t *testing.T) {
	sessDir := t.TempDir()
	del := newMockDelegate()

	// Write a malformed JSON file
	os.WriteFile(filepath.Join(sessDir, "bad.json"), []byte("{broken"), 0644)

	// Also a valid one
	writeSessionFile(t, sessDir, "good.json", SessionFile{
		Key: "g1",
		Messages: []SessionMsg{
			{Role: "user", Content: "ok"},
		},
	})

	result, err := MigrateFileSessions(context.Background(), del, "picoclaw", sessDir)
	if err != nil {
		t.Fatalf("MigrateFileSessions: %v", err)
	}
	if result.SessionsFound != 2 {
		t.Errorf("expected 2 found, got %d", result.SessionsFound)
	}
	if result.SessionsMigrated != 1 {
		t.Errorf("expected 1 migrated, got %d", result.SessionsMigrated)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}
