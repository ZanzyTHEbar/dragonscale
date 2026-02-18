package memory

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MigrateLongTermMemory performs a one-time migration of MEMORY.md content
// into the working context tier. Uses a marker file for idempotency.
func MigrateLongTermMemory(ctx context.Context, workspace string, delegate MemoryDelegate, agentID string) error {
	if delegate == nil {
		return nil
	}

	memDir := filepath.Join(workspace, "memory")
	markerFile := filepath.Join(memDir, ".longterm_migrated")
	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	memFile := filepath.Join(memDir, "MEMORY.md")
	data, err := os.ReadFile(memFile)
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

	if err := delegate.UpsertWorkingContext(ctx, agentID, "default", content); err != nil {
		return err
	}

	marker := []byte(time.Now().Format(time.RFC3339))
	if err := os.WriteFile(markerFile, marker, 0644); err != nil {
		log.Printf("[WARN] migrate_longterm: failed to write marker: %v", err)
	}

	log.Printf("[INFO] migrate_longterm: migrated MEMORY.md to working context")
	return nil
}
