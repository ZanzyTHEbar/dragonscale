package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
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
func (m *mockDelegate) GetRecallItemsByIDs(_ context.Context, _ string, _ []ids.UUID) (map[ids.UUID]*RecallItem, error) {
	return make(map[ids.UUID]*RecallItem), nil
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
func (m *mockDelegate) InsertArchivalChunkBatch(_ context.Context, _ []*ArchivalChunk) error {
	return nil
}
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
func (m *mockDelegate) InsertAuditEntryBatch(_ context.Context, _ []*AuditEntry) error {
	return nil
}
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

func TestMigrateFileSessions(t *testing.T) {
	t.Parallel()

	type tc struct {
		name        string
		setup       func(t *testing.T, dir string, del *mockDelegate)
		sessionsDir string
		want        *MigrateSessionsResult
		extra       func(t *testing.T, dir string, result *MigrateSessionsResult, del *mockDelegate)
	}

	tests := []tc{
		{
			name: "basic",
			setup: func(t *testing.T, dir string, _ *mockDelegate) {
				writeSessionFile(t, dir, "sess1.json", SessionFile{
					Key: "session-1",
					Messages: []SessionMsg{
						{Role: "user", Content: "Hello"},
						{Role: "assistant", Content: "Hi there!"},
					},
					Created: time.Now().Add(-time.Hour),
					Updated: time.Now(),
				})
				writeSessionFile(t, dir, "sess2.json", SessionFile{
					Key:     "session-2",
					Summary: "Talked about Go programming",
					Messages: []SessionMsg{
						{Role: "user", Content: "Tell me about Go"},
					},
				})
			},
			want: &MigrateSessionsResult{SessionsFound: 2, SessionsMigrated: 2, ItemsCreated: 3, Errors: 0},
			extra: func(t *testing.T, _ string, _ *MigrateSessionsResult, del *mockDelegate) {
				if len(del.recallItems) != 3 {
					t.Fatalf("expected 3 recall items, got %d", len(del.recallItems))
				}
				first := del.recallItems[0]
				if first.Role != "user" || first.Content != "Hello" || first.SessionKey != "session-1" || first.Tags != "migrated" {
					t.Errorf("unexpected first recall item: %+v", first)
				}

				wc, err := del.GetWorkingContext(t.Context(), pkg.NAME, "session-2")
				if err != nil {
					t.Fatalf("GetWorkingContext: %v", err)
				}
				if wc == nil || wc.Content != "Talked about Go programming" {
					t.Errorf("expected summary as working context, got %v", wc)
				}
			},
		},
		{
			name:  "empty dir",
			setup: func(_ *testing.T, _ string, _ *mockDelegate) {},
			want: &MigrateSessionsResult{
				SessionsFound:    0,
				SessionsMigrated: 0,
				ItemsCreated:     0,
				Errors:           0,
			},
		},
		{
			name: "skip empty messages",
			setup: func(t *testing.T, dir string, _ *mockDelegate) {
				writeSessionFile(t, dir, "sess.json", SessionFile{
					Key: "s1",
					Messages: []SessionMsg{
						{Role: "user", Content: "real content"},
						{Role: "assistant", Content: ""},
						{Role: "user", Content: "   "},
					},
				})
			},
			want: &MigrateSessionsResult{SessionsFound: 1, SessionsMigrated: 1, ItemsCreated: 1, Errors: 0},
		},
		{
			name: "fallback key from filename",
			setup: func(t *testing.T, dir string, _ *mockDelegate) {
				writeSessionFile(t, dir, "custom-key.json", SessionFile{
					Key: "",
					Messages: []SessionMsg{
						{Role: "user", Content: "test"},
					},
				})
			},
			want: &MigrateSessionsResult{SessionsFound: 1, SessionsMigrated: 1, ItemsCreated: 1, Errors: 0},
			extra: func(t *testing.T, _ string, result *MigrateSessionsResult, del *mockDelegate) {
				if got := del.recallItems[0].SessionKey; got != "custom-key" {
					t.Errorf("expected 'custom-key' from filename, got %q", got)
				}
				if result.ItemsCreated != 1 {
					t.Fatalf("expected 1 item, got %d", result.ItemsCreated)
				}
			},
		},
		{
			name: "malformed json",
			setup: func(t *testing.T, dir string, _ *mockDelegate) {
				if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{broken"), 0644); err != nil {
					t.Fatalf("write malformed json: %v", err)
				}
				writeSessionFile(t, dir, "good.json", SessionFile{
					Key: "g1",
					Messages: []SessionMsg{
						{Role: "user", Content: "ok"},
					},
				})
			},
			want: &MigrateSessionsResult{SessionsFound: 2, SessionsMigrated: 1, ItemsCreated: 1, Errors: 1},
		},
		{
			name:        "nonexistent dir",
			sessionsDir: "/nonexistent/path",
			want:        nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			del := newMockDelegate()
			sessDir := t.TempDir()
			if tt.sessionsDir != "" {
				sessDir = tt.sessionsDir
			}
			if tt.setup != nil {
				tt.setup(t, sessDir, del)
			}

			got, err := MigrateFileSessions(t.Context(), del, pkg.NAME, sessDir)
			if err != nil {
				t.Fatalf("MigrateFileSessions: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil result, got %#v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected migration result, got nil")
			}
			if diff := cmp.Diff(*tt.want, *got); diff != "" {
				t.Errorf("migration result mismatch (-want +got):\n%s", diff)
			}
			if tt.extra != nil {
				tt.extra(t, sessDir, got, del)
			}
		})
	}
}

func TestMigrateFileSessions_Idempotent(t *testing.T) {
	t.Parallel()
	sessDir := t.TempDir()
	del := newMockDelegate()

	writeSessionFile(t, sessDir, "sess.json", SessionFile{
		Key: "s1",
		Messages: []SessionMsg{
			{Role: "user", Content: "test"},
		},
	})

	result1, err := MigrateFileSessions(t.Context(), del, pkg.NAME, sessDir)
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if result1.SessionsMigrated != 1 {
		t.Fatalf("expected 1, got %d", result1.SessionsMigrated)
	}

	result2, err := MigrateFileSessions(t.Context(), del, pkg.NAME, sessDir)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if result2 != nil {
		t.Error("expected nil result for already-migrated directory")
	}

	if len(del.recallItems) != 1 {
		t.Errorf("expected 1 recall item (no duplicates), got %d", len(del.recallItems))
	}
}
