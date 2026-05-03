package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// memoryDocumentStore is a minimal in-memory implementation of DocumentStore for tests.
type memoryDocumentStore struct {
	mu   sync.Mutex
	kv   map[string]string
	docs map[string]*memory.AgentDocument // keyed by agentID+":"+name
}

func newMemoryDocumentStore() *memoryDocumentStore {
	return &memoryDocumentStore{
		kv:   make(map[string]string),
		docs: make(map[string]*memory.AgentDocument),
	}
}

func (m *memoryDocumentStore) GetKV(_ context.Context, agentID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[agentID+":"+key]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}

func (m *memoryDocumentStore) UpsertKV(_ context.Context, agentID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[agentID+":"+key] = value
	return nil
}

func (m *memoryDocumentStore) UpsertDocument(_ context.Context, doc *memory.AgentDocument) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[doc.AgentID+":"+doc.Name] = doc
	return nil
}

func (m *memoryDocumentStore) ListDocumentsByCategory(_ context.Context, agentID, category string) ([]*memory.AgentDocument, error) {
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

func (m *memoryDocumentStore) getDoc(agentID, name string) *memory.AgentDocument {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docs[agentID+":"+name]
}

func (m *memoryDocumentStore) getHash(agentID, name string) string {
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
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md":    "# Agent\nYou are helpful.",
		"SOUL.md":     "# Soul\nCurious and kind.",
		"USER.md":     "# User\nName: Alice",
		"IDENTITY.md": "# Identity\nDragonScale v1",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(t.Context()))

	for _, name := range IdentityFiles {
		doc := store.getDoc("agent-1", name)
		require.NotNil(t, doc, "expected document for %s", name)
		assert.Empty(t, cmp.Diff(syncCategory, doc.Category))
		assert.NotEmpty(t, doc.Content)

		hash := store.getHash("agent-1", name)
		assert.NotEmpty(t, hash, "expected hash for %s", name)
	}
}

func TestSyncAll_SkipsUnchangedFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent\nSame content.",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(t.Context()))
	firstDoc := store.getDoc("agent-1", "AGENT.md")
	require.NotNil(t, firstDoc)
	firstID := firstDoc.ID

	require.NoError(t, s.SyncAll(t.Context()))
	secondDoc := store.getDoc("agent-1", "AGENT.md")
	assert.Empty(t, cmp.Diff(firstID, secondDoc.ID), "unchanged file should not be re-upserted")
}

func TestSyncAll_ReturnsUpsertDocumentError(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent\nVersion 1",
	})
	upsertErr := errors.New("upsert failed")

	ctrl := gomock.NewController(t)
	store := NewMockDocumentStore(ctrl)
	s := New(dir, "agent-1", store)

	gomock.InOrder(
		store.EXPECT().GetKV(gomock.Any(), "agent-1", kvPrefix+"AGENT.md").Return("", os.ErrNotExist),
		store.EXPECT().UpsertDocument(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *memory.AgentDocument) error {
				assert.Empty(t, cmp.Diff("agent-1", doc.AgentID))
				assert.Empty(t, cmp.Diff("AGENT.md", doc.Name))
				assert.Empty(t, cmp.Diff(syncCategory, doc.Category))
				assert.Empty(t, cmp.Diff("# Agent\nVersion 1", doc.Content))
				return upsertErr
			},
		),
	)

	err := s.SyncAll(t.Context())
	require.ErrorIs(t, err, upsertErr)
	assert.ErrorContains(t, err, "sync AGENT.md")
	assert.ErrorContains(t, err, "upsert document AGENT.md")
}

func TestSyncAll_UpsertsModifiedFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent\nVersion 1",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(t.Context()))

	v1Hash := store.getHash("agent-1", "AGENT.md")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Agent\nVersion 2"), 0644))
	require.NoError(t, s.SyncAll(t.Context()))

	v2Hash := store.getHash("agent-1", "AGENT.md")
	assert.NotEqual(t, v1Hash, v2Hash, "hash should change after file modification")

	doc := store.getDoc("agent-1", "AGENT.md")
	assert.Contains(t, doc.Content, "Version 2")
}

func TestSyncAll_SkipsMissingFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent only",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(t.Context()))

	assert.NotNil(t, store.getDoc("agent-1", "AGENT.md"))
	assert.Nil(t, store.getDoc("agent-1", "SOUL.md"), "missing file should not create a doc")
	assert.Nil(t, store.getDoc("agent-1", "USER.md"))
	assert.Nil(t, store.getDoc("agent-1", "IDENTITY.md"))
}

func TestSyncAll_SkipsEmptyFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "   \n\t\n  ",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)

	require.NoError(t, s.SyncAll(t.Context()))
	assert.Nil(t, store.getDoc("agent-1", "AGENT.md"), "empty/whitespace-only file should be skipped")
}

func TestSyncAll_IsolatesAgents(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Shared agent file",
	})

	store := newMemoryDocumentStore()
	s1 := New(dir, "agent-a", store)
	s2 := New(dir, "agent-b", store)

	require.NoError(t, s1.SyncAll(t.Context()))
	require.NoError(t, s2.SyncAll(t.Context()))

	docA := store.getDoc("agent-a", "AGENT.md")
	docB := store.getDoc("agent-b", "AGENT.md")
	require.NotNil(t, docA)
	require.NotNil(t, docB)
	assert.NotEqual(t, docA.ID, docB.ID, "different agents should get separate doc records")
}

func TestCheckAndSync_DetectsModifiedFile(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Original",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(t.Context()))

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Modified"), 0644))

	require.NoError(t, s.CheckAndSync(t.Context()))

	doc := store.getDoc("agent-1", "AGENT.md")
	assert.Contains(t, doc.Content, "Modified")
}

func TestCheckAndSync_SkipsUntouchedFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Stable",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(t.Context()))

	hash1 := store.getHash("agent-1", "AGENT.md")

	s.lastSync.Store(time.Now().Add(time.Second).UnixNano())
	require.NoError(t, s.CheckAndSync(t.Context()))

	hash2 := store.getHash("agent-1", "AGENT.md")
	assert.Empty(t, cmp.Diff(hash1, hash2), "untouched file should not trigger re-sync")
}

func TestWatch_DetectsFileChange(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Initial",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(t.Context()))

	ctx := t.Context()
	require.NoError(t, s.Watch(ctx))
	defer s.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# Updated via watch"), 0644))

	assert.Eventually(t, func() bool {
		doc := store.getDoc("agent-1", "AGENT.md")
		return doc != nil && doc.Content == "# Updated via watch"
	}, 3*time.Second, 100*time.Millisecond, "watcher should detect and sync the change")
}

func TestWatch_IgnoresNonIdentityFiles(t *testing.T) {
	t.Parallel()
	dir := setupIdentityDir(t, map[string]string{
		"AGENT.md": "# Agent",
	})

	store := newMemoryDocumentStore()
	s := New(dir, "agent-1", store)
	require.NoError(t, s.SyncAll(t.Context()))

	ctx := t.Context()
	require.NoError(t, s.Watch(ctx))
	defer s.Close()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "random.txt"), []byte("not an identity file"), 0644))
	time.Sleep(500 * time.Millisecond)

	assert.Nil(t, store.getDoc("agent-1", "random.txt"), "non-identity file should be ignored")
}

func TestContentHash_Deterministic(t *testing.T) {
	t.Parallel()
	data := []byte("hello world")
	h1 := contentHash(data)
	h2 := contentHash(data)
	assert.Empty(t, cmp.Diff(h1, h2))
	assert.Len(t, h1, 64)
}

func TestContentHash_DifferentForDifferentContent(t *testing.T) {
	t.Parallel()
	h1 := contentHash([]byte("version 1"))
	h2 := contentHash([]byte("version 2"))
	assert.NotEqual(t, h1, h2)
}

func TestIsIdentityFile(t *testing.T) {
	t.Parallel()
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
			assert.Empty(t, cmp.Diff(tt.want, isIdentityFile(tt.name)))
		})
	}
}

func TestNew_SetsFields(t *testing.T) {
	t.Parallel()
	store := newMemoryDocumentStore()
	s := New("/tmp/identity", "test-agent", store)
	assert.Empty(t, cmp.Diff("/tmp/identity", s.identityDir))
	assert.Empty(t, cmp.Diff("test-agent", s.agentID))
	assert.NotNil(t, s.store)
}
