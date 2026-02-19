package sync

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore is a minimal in-memory implementation of DocumentStore for tests.
type mockStore struct {
	mu   sync.Mutex
	kv   map[string]string
	docs map[string]*memory.AgentDocument // keyed by agentID+":"+name
}

func newMockStore() *mockStore {
	return &mockStore{
		kv:   make(map[string]string),
		docs: make(map[string]*memory.AgentDocument),
	}
}

func (m *mockStore) GetKV(_ context.Context, agentID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[agentID+":"+key]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}

func (m *mockStore) UpsertKV(_ context.Context, agentID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[agentID+":"+key] = value
	return nil
}

func (m *mockStore) UpsertDocument(_ context.Context, doc *memory.AgentDocument) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[doc.AgentID+":"+doc.Name] = doc
	return nil
}

func (m *mockStore) ListDocumentsByCategory(_ context.Context, agentID, category string) ([]*memory.AgentDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*memory.AgentDocument
	for _, doc := range m.docs {
		if doc.AgentID == agentID && doc.Category == category {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (m *mockStore) getDoc(agentID, name string) *memory.AgentDocument {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docs[agentID+":"+name]
}

func (m *mockStore) getHash(agentID, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.kv[agentID+":"+kvPrefix+name]
}

func setupIdentityDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	return dir
}

func TestSyncAll_InsertsNewFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md":    "# Agent\nYou are helpful.",
		"SOUL.md":     "# Soul\nCurious and kind.",
		"USER.md":     "# User\nName: Alice",
		"IDENTITY.md": "# Identity\nPicoClaw v1",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(context.Background()))

	for _, name := range IdentityFiles {
		doc := store.getDoc("agent-1", name)
		require.NotNil(t, doc, "expected document for %s", name)
		assert.Equal(t, syncCategory, doc.Category)
		assert.NotEmpty(t, doc.Content)

		hash := store.getHash("agent-1", name)
		assert.NotEmpty(t, hash, "expected hash for %s", name)
	}
}

func TestSyncAll_SkipsUnchangedFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent\nSame content.",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(context.Background()))
	firstDoc := store.getDoc("agent-1", "AGENT.md")
	require.NotNil(t, firstDoc)
	firstID := firstDoc.ID

	require.NoError(t, s.SyncAll(context.Background()))
	secondDoc := store.getDoc("agent-1", "AGENT.md")
	assert.Equal(t, firstID, secondDoc.ID, "unchanged file should not be re-upserted")
}

func TestSyncAll_UpsertsModifiedFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent\nVersion 1",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(context.Background()))

	v1Hash := store.getHash("agent-1", "AGENT.md")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Agent\nVersion 2"), 0644))
	require.NoError(t, s.SyncAll(context.Background()))

	v2Hash := store.getHash("agent-1", "AGENT.md")
	assert.NotEqual(t, v1Hash, v2Hash, "hash should change after file modification")

	doc := store.getDoc("agent-1", "AGENT.md")
	assert.Contains(t, doc.Content, "Version 2")
}

func TestSyncAll_SkipsMissingFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent only",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(context.Background()))

	assert.NotNil(t, store.getDoc("agent-1", "AGENT.md"))
	assert.Nil(t, store.getDoc("agent-1", "SOUL.md"), "missing file should not create a doc")
	assert.Nil(t, store.getDoc("agent-1", "USER.md"))
	assert.Nil(t, store.getDoc("agent-1", "IDENTITY.md"))
}

func TestSyncAll_SkipsEmptyFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "   \n\t\n  ",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(context.Background()))
	assert.Nil(t, store.getDoc("agent-1", "AGENT.md"), "empty/whitespace-only file should be skipped")
}

func TestSyncAll_IsolatesAgents(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Shared agent file",
	})

	store := newMockStore()
	s1 := New(dir, "agent-a", store)
	s2 := New(dir, "agent-b", store)

	require.NoError(t, s1.SyncAll(context.Background()))
	require.NoError(t, s2.SyncAll(context.Background()))

	docA := store.getDoc("agent-a", "AGENT.md")
	docB := store.getDoc("agent-b", "AGENT.md")
	require.NotNil(t, docA)
	require.NotNil(t, docB)
	assert.NotEqual(t, docA.ID, docB.ID, "different agents should get separate doc records")
}

func TestCheckAndSync_DetectsModifiedFile(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Original",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(context.Background()))

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Modified"), 0644))

	require.NoError(t, s.CheckAndSync(context.Background()))

	doc := store.getDoc("agent-1", "AGENT.md")
	assert.Contains(t, doc.Content, "Modified")
}

func TestCheckAndSync_SkipsUntouchedFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Stable",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(context.Background()))

	hash1 := store.getHash("agent-1", "AGENT.md")

	s.lastSync.Store(time.Now().Add(time.Second).UnixNano())
	require.NoError(t, s.CheckAndSync(context.Background()))

	hash2 := store.getHash("agent-1", "AGENT.md")
	assert.Equal(t, hash1, hash2, "untouched file should not trigger re-sync")
}

func TestWatch_DetectsFileChange(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Initial",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(context.Background()))

	ctx := context.Background()
	require.NoError(t, s.Watch(ctx))
	defer s.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Updated via watch"), 0644))

	assert.Eventually(t, func() bool {
		doc := store.getDoc("agent-1", "AGENT.md")
		return doc != nil && doc.Content == "# Updated via watch"
	}, 3*time.Second, 100*time.Millisecond, "watcher should detect and sync the change")
}

func TestWatch_IgnoresNonIdentityFiles(t *testing.T) {
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent",
	})

	store := newMockStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(context.Background()))

	ctx := context.Background()
	require.NoError(t, s.Watch(ctx))
	defer s.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "random.txt"), []byte("not an identity file"), 0644))
	time.Sleep(500 * time.Millisecond)

	assert.Nil(t, store.getDoc("agent-1", "random.txt"), "non-identity file should be ignored")
}

func TestContentHash_Deterministic(t *testing.T) {
	data := []byte("hello world")
	h1 := contentHash(data)
	h2 := contentHash(data)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64)
}

func TestContentHash_DifferentForDifferentContent(t *testing.T) {
	h1 := contentHash([]byte("version 1"))
	h2 := contentHash([]byte("version 2"))
	assert.NotEqual(t, h1, h2)
}

func TestIsIdentityFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"AGENT.md", true},
		{"IDENTITY.md", true},
		{"SOUL.md", true},
		{"USER.md", true},
		{"README.md", false},
		{"agent.md", false},
		{"AGENT.txt", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isIdentityFile(tt.name))
		})
	}
}

func TestNew_SetsFields(t *testing.T) {
	store := newMockStore()
	s := New("/tmp/identity", "test-agent", store)
	assert.Equal(t, "/tmp/identity", s.identityDir)
	assert.Equal(t, "test-agent", s.agentID)
	assert.NotNil(t, s.store)
}
