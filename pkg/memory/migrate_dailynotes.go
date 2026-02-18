package memory

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
)

// MigrateDailyNotes performs a one-time migration of daily note files
// (memory/YYYYMM/YYYYMMDD.md) into recall items. Uses a marker file for idempotency.
func MigrateDailyNotes(ctx context.Context, workspace string, delegate MemoryDelegate, agentID string) error {
	if delegate == nil {
		return nil
	}

	memDir := filepath.Join(workspace, "memory")
	markerFile := filepath.Join(memDir, ".dailynotes_migrated")
	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	migrated := 0
	err := filepath.WalkDir(memDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		base := strings.TrimSuffix(d.Name(), ".md")
		if len(base) != 8 {
			return nil
		}

		noteDate, parseErr := time.Parse("20060102", base)
		if parseErr != nil {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		content := strings.TrimSpace(string(data))
		if content == "" {
			return nil
		}

		dateTag := noteDate.Format("2006-01-02")
		item := &RecallItem{
			ID:         ids.New(),
			AgentID:    agentID,
			SessionKey: "",
			Role:       "system",
			Sector:     SectorEpisodic,
			Importance: 0.4,
			Salience:   0.4,
			DecayRate:  0.01,
			Content:    content,
			Tags:       "daily-note," + dateTag,
			CreatedAt:  noteDate,
			UpdatedAt:  noteDate,
		}

		if err := delegate.InsertRecallItem(ctx, item); err != nil {
			log.Printf("[WARN] migrate_dailynotes: failed to insert note %s: %v", base, err)
			return nil
		}
		migrated++
		return nil
	})

	if err != nil {
		return err
	}

	if migrated > 0 {
		marker := []byte(time.Now().Format(time.RFC3339))
		if err := os.WriteFile(markerFile, marker, 0644); err != nil {
			log.Printf("[WARN] migrate_dailynotes: failed to write marker: %v", err)
		}
		log.Printf("[INFO] migrate_dailynotes: migrated %d daily notes to recall items", migrated)
	}

	return nil
}
