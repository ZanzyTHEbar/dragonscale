package memory

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
)

var bootstrapDocNames = []string{
	"AGENTS.md",
	"SOUL.md",
	"USER.md",
	"IDENTITY.md",
}

// MigrateDocuments performs a one-time migration of workspace bootstrap files
// into the agent_documents table. Uses a marker file for idempotency.
func MigrateDocuments(ctx context.Context, workspace string, delegate MemoryDelegate, agentID string) error {
	if delegate == nil {
		return nil
	}

	markerFile := filepath.Join(workspace, ".documents_migrated")
	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	migrated := 0
	for _, name := range bootstrapDocNames {
		filePath := filepath.Join(workspace, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		doc := &AgentDocument{
			ID:       ids.New(),
			AgentID:  agentID,
			Name:     name,
			Category: "bootstrap",
			Content:  string(data),
		}
		if err := delegate.UpsertDocument(ctx, doc); err != nil {
			return err
		}
		migrated++
	}

	if migrated > 0 {
		marker := []byte(time.Now().Format(time.RFC3339))
		if err := os.WriteFile(markerFile, marker, 0644); err != nil {
			log.Printf("[WARN] migrate_documents: failed to write marker: %v", err)
		}
		log.Printf("[INFO] migrate_documents: migrated %d bootstrap files to agent_documents", migrated)
	}

	return nil
}
