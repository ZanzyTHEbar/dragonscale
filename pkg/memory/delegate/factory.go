package delegate

import (
	"fmt"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// NewFromConfig creates a LibSQLDelegate from the application config.
// It selects local-only or embedded-replica mode based on the sync configuration.
//
// Falls back to local-only mode if replica setup fails (logs a warning).
func NewFromConfig(cfg config.MemoryConfig, defaultDBPath string) (*LibSQLDelegate, error) {
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	dims := cfg.EmbeddingDims
	if dims <= 0 {
		dims = DefaultEmbeddingDims
	}

	var d *LibSQLDelegate
	var err error

	if cfg.Sync.SyncURL != "" {
		d, err = newReplicaDelegate(dbPath, dims, cfg.Sync)
		if err != nil {
			logger.WarnCF("memory", "Embedded replica setup failed, falling back to local-only mode",
				map[string]interface{}{
					"error":    err.Error(),
					"sync_url": cfg.Sync.SyncURL,
				})
			d, err = newLocalDelegate(dbPath, dims)
		}
	} else {
		d, err = newLocalDelegate(dbPath, dims)
	}

	if err != nil {
		return nil, fmt.Errorf("create memory delegate: %w", err)
	}

	if d.IsReplica() {
		logger.InfoCF("memory", "Memory delegate initialized in embedded replica mode",
			map[string]interface{}{
				"db_path":       dbPath,
				"sync_url":      cfg.Sync.SyncURL,
				"sync_interval": cfg.Sync.SyncIntervalSeconds,
			})
	} else {
		logger.InfoCF("memory", "Memory delegate initialized in local-only mode",
			map[string]interface{}{"db_path": dbPath})
	}

	return d, nil
}

func newLocalDelegate(dbPath string, dims int) (*LibSQLDelegate, error) {
	if dims == DefaultEmbeddingDims {
		return NewLibSQLDelegate(dbPath)
	}
	return NewLibSQLDelegateWithDims(dbPath, dims)
}

func newReplicaDelegate(dbPath string, dims int, syncCfg config.MemorySyncConfig) (*LibSQLDelegate, error) {
	var interval time.Duration
	if syncCfg.SyncIntervalSeconds > 0 {
		interval = time.Duration(syncCfg.SyncIntervalSeconds) * time.Second
	}

	d, err := NewLibSQLDelegateWithSync(dbPath, SyncConfig{
		SyncURL:       syncCfg.SyncURL,
		AuthToken:     syncCfg.AuthToken,
		SyncInterval:  interval,
		EncryptionKey: syncCfg.EncryptionKey,
	})
	if err != nil {
		return nil, err
	}

	if dims > 0 && dims != DefaultEmbeddingDims {
		d.embeddingDims = dims
	}
	return d, nil
}
