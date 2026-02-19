package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

// IdentityFiles are the canonical set of bootstrap identity documents.
var IdentityFiles = []string{
	"AGENT.md",
	"IDENTITY.md",
	"SOUL.md",
	"USER.md",
}

const (
	kvPrefix      = "sync:hash:"
	kvLastSyncKey = "sync:last_sync_ts"
	syncCategory  = "bootstrap"
	debounceDelay = 250 * time.Millisecond
	pollInterval  = 30 * time.Second
)

// DocumentStore is the minimal interface the sync engine needs from the memory
// delegate. This avoids importing the full MemoryDelegate interface.
type DocumentStore interface {
	GetKV(ctx context.Context, agentID, key string) (string, error)
	UpsertKV(ctx context.Context, agentID, key, value string) error
	UpsertDocument(ctx context.Context, doc *memory.AgentDocument) error
	ListDocumentsByCategory(ctx context.Context, agentID, category string) ([]*memory.AgentDocument, error)
}

// IdentitySync watches identity files on disk and mirrors their content into
// the database. Disk is the source of truth; the DB is a derived runtime cache.
type IdentitySync struct {
	identityDir string
	agentID     string
	store       DocumentStore

	watcher  *fsnotify.Watcher
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	lastSync atomic.Int64 // unix nanos of the last successful sync
}

// New creates a new IdentitySync. identityDir is the path to the directory
// containing the identity markdown files ($XDG_CONFIG_HOME/picoclaw/identity).
func New(identityDir, agentID string, store DocumentStore) *IdentitySync {
	s := &IdentitySync{
		identityDir: identityDir,
		agentID:     agentID,
		store:       store,
	}
	s.lastSync.Store(time.Now().UnixNano())
	return s
}

// SyncAll performs a full reconciliation: reads every identity file from disk,
// compares its content hash against the stored hash in agent_kv, and upserts
// any that differ. This is the startup path and catches all changes made while
// picoclaw was not running.
func (s *IdentitySync) SyncAll(ctx context.Context) error {
	for _, name := range IdentityFiles {
		if err := s.syncFile(ctx, name); err != nil {
			return fmt.Errorf("sync %s: %w", name, err)
		}
	}
	now := time.Now()
	s.lastSync.Store(now.UnixNano())
	_ = s.store.UpsertKV(ctx, s.agentID, kvLastSyncKey, now.Format(time.RFC3339Nano))
	return nil
}

// Watch starts an fsnotify watcher on the identity directory. File change
// events are debounced and trigger a re-sync of the affected file. Call Close
// to stop watching. This is the daemon-mode path for real-time sync.
func (s *IdentitySync) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fsnotify watcher: %w", err)
	}
	if err := w.Add(s.identityDir); err != nil {
		w.Close()
		return fmt.Errorf("watch %s: %w", s.identityDir, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	s.watcher = w
	s.cancel = cancel

	s.wg.Add(1)
	go s.watchLoop(ctx, w)
	return nil
}

// CheckAndSync is a lightweight pre-prompt safety net. It stats every identity
// file and re-syncs any whose mtime is newer than the last sync timestamp.
// Cost: O(n) stat calls where n = len(IdentityFiles).
func (s *IdentitySync) CheckAndSync(ctx context.Context) error {
	threshold := time.Unix(0, s.lastSync.Load())
	changed := false
	for _, name := range IdentityFiles {
		path := filepath.Join(s.identityDir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(threshold) {
			if err := s.syncFile(ctx, name); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		now := time.Now()
		s.lastSync.Store(now.UnixNano())
		_ = s.store.UpsertKV(ctx, s.agentID, kvLastSyncKey, now.Format(time.RFC3339Nano))
	}
	return nil
}

// Close stops the fsnotify watcher and waits for the watch goroutine to exit.
func (s *IdentitySync) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.watcher != nil {
		s.watcher.Close()
	}
	s.wg.Wait()
}

// syncFile reads a single identity file from disk, computes its SHA-256 hash,
// compares with the stored hash, and upserts to agent_documents if different.
func (s *IdentitySync) syncFile(ctx context.Context, name string) error {
	path := filepath.Join(s.identityDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}

	hash := contentHash(data)
	kvKey := kvPrefix + name

	stored, err := s.store.GetKV(ctx, s.agentID, kvKey)
	if err == nil && stored == hash {
		return nil
	}

	doc := &memory.AgentDocument{
		ID:       ids.New(),
		AgentID:  s.agentID,
		Name:     name,
		Category: syncCategory,
		Content:  content,
	}
	if err := s.store.UpsertDocument(ctx, doc); err != nil {
		return fmt.Errorf("upsert document %s: %w", name, err)
	}

	if err := s.store.UpsertKV(ctx, s.agentID, kvKey, hash); err != nil {
		return fmt.Errorf("upsert hash for %s: %w", name, err)
	}

	return nil
}

// watchLoop processes fsnotify events with debouncing.
func (s *IdentitySync) watchLoop(ctx context.Context, w *fsnotify.Watcher) {
	defer s.wg.Done()

	pending := make(map[string]time.Time)
	ticker := time.NewTicker(debounceDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			name := filepath.Base(event.Name)
			if !isIdentityFile(name) {
				continue
			}
			pending[name] = time.Now()

		case _, ok := <-w.Errors:
			if !ok {
				return
			}

		case now := <-ticker.C:
			for name, queued := range pending {
				if now.Sub(queued) < debounceDelay {
					continue
				}
				syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = s.syncFile(syncCtx, name)
				cancel()
				delete(pending, name)
				s.lastSync.Store(time.Now().UnixNano())
			}
		}
	}
}

func isIdentityFile(name string) bool {
	for _, f := range IdentityFiles {
		if f == name {
			return true
		}
	}
	return false
}

func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
