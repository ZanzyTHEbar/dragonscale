// Package memory provides the 3-tier MemGPT memory system.
// This file handles one-time migration of file-based session data
// into the SQLite-backed recall memory tier.
package memory

import (
	"context"
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const migrationMarkerFile = ".sessions_migrated"

// SessionFile mirrors the on-disk session format from pkg/session.
// Defined here to avoid circular imports.
type SessionFile struct {
	Key      string       `json:"key"`
	Messages []SessionMsg `json:"messages"`
	Summary  string       `json:"summary,omitzero"`
	Created  time.Time    `json:"created"`
	Updated  time.Time    `json:"updated"`
}

type SessionMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MigrateSessionsResult holds counts from a session migration run.
type MigrateSessionsResult struct {
	SessionsFound    int
	SessionsMigrated int
	ItemsCreated     int
	Errors           int
}

// MigrateFileSessions reads all JSON session files from sessionsDir and inserts
// their messages as RecallItems into the delegate. It writes a marker file to
// prevent re-running. Safe to call repeatedly — no-ops after first migration.
func MigrateFileSessions(ctx context.Context, del MemoryDelegate, agentID, sessionsDir string) (*MigrateSessionsResult, error) {
	if sessionsDir == "" {
		return nil, nil
	}

	markerPath := filepath.Join(sessionsDir, migrationMarkerFile)
	if _, err := os.Stat(markerPath); err == nil {
		return nil, nil // already migrated
	}

	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	result := &MigrateSessionsResult{}

	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		result.SessionsFound++

		sessPath := filepath.Join(sessionsDir, f.Name())
		data, err := os.ReadFile(sessPath)
		if err != nil {
			logger.WarnCF("migrate", "Failed to read session file",
				map[string]interface{}{"path": sessPath, "error": err.Error()})
			result.Errors++
			continue
		}

		var sess SessionFile
		if err := jsonv2.Unmarshal(data, &sess); err != nil {
			logger.WarnCF("migrate", "Failed to parse session file",
				map[string]interface{}{"path": sessPath, "error": err.Error()})
			result.Errors++
			continue
		}

		sessionKey := sess.Key
		if sessionKey == "" {
			sessionKey = strings.TrimSuffix(f.Name(), ".json")
		}

		migrated, err := migrateOneSession(ctx, del, agentID, sessionKey, &sess)
		if err != nil {
			logger.WarnCF("migrate", "Failed to migrate session",
				map[string]interface{}{"session": sessionKey, "error": err.Error()})
			result.Errors++
			continue
		}
		result.ItemsCreated += migrated
		result.SessionsMigrated++
	}

	// Write marker to prevent re-running
	if result.SessionsMigrated > 0 || result.SessionsFound == 0 {
		markerContent := fmt.Sprintf("migrated_at=%s sessions=%d items=%d errors=%d\n",
			time.Now().UTC().Format(time.RFC3339),
			result.SessionsMigrated,
			result.ItemsCreated,
			result.Errors,
		)
		os.WriteFile(markerPath, []byte(markerContent), 0644)
	}

	if result.SessionsMigrated > 0 {
		logger.InfoCF("migrate", "Session migration complete",
			map[string]interface{}{
				"sessions_found":    result.SessionsFound,
				"sessions_migrated": result.SessionsMigrated,
				"items_created":     result.ItemsCreated,
				"errors":            result.Errors,
			})
	}

	return result, nil
}

func migrateOneSession(ctx context.Context, del MemoryDelegate, agentID, sessionKey string, sess *SessionFile) (int, error) {
	count := 0

	for _, msg := range sess.Messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}

		item := &RecallItem{
			ID:         ids.New(),
			AgentID:    agentID,
			SessionKey: sessionKey,
			Role:       msg.Role,
			Sector:     SectorEpisodic,
			Importance: 0.3,
			Salience:   0.3,
			DecayRate:  0.01,
			Content:    msg.Content,
			Tags:       "migrated",
		}

		if err := del.InsertRecallItem(ctx, item); err != nil {
			return count, fmt.Errorf("insert recall item: %w", err)
		}
		count++
	}

	// If the session had a summary, store it as working context
	if sess.Summary != "" {
		if err := del.UpsertWorkingContext(ctx, agentID, sessionKey, sess.Summary); err != nil {
			logger.WarnCF("migrate", "Failed to store session summary as working context",
				map[string]interface{}{"session": sessionKey, "error": err.Error()})
		}
	}

	return count, nil
}
