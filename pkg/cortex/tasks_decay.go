package cortex

import (
	"context"
	"fmt"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// DecayStore is the minimal interface for batch importance decay.
// Implemented by LibSQLDelegate via hand-written SQL.
type DecayStore interface {
	DecayRecallImportance(ctx context.Context, factor, floor float64, batchSize int) (int64, error)
}

// DecayConfig configures the memory decay task.
type DecayConfig struct {
	Factor    float64       // multiplicative decay factor (default 0.95)
	Floor     float64       // minimum importance value (default 0.1)
	BatchSize int           // max items per run (default 30)
	Interval  time.Duration // how often to run (default 5 minutes)
	Timeout   time.Duration // max execution time (default 30 seconds)
}

// DefaultDecayConfig returns sensible defaults for memory decay.
func DefaultDecayConfig() DecayConfig {
	return DecayConfig{
		Factor:    0.95,
		Floor:     0.1,
		BatchSize: 30,
		Interval:  5 * time.Minute,
		Timeout:   30 * time.Second,
	}
}

// DecayTask applies multiplicative importance decay to old recall items.
// importance_new = max(importance_old * factor, floor)
type DecayTask struct {
	cfg   DecayConfig
	store DecayStore
}

// NewDecayTask creates a decay task with the given config and store.
// If store is nil, the task becomes a no-op (logs a warning).
// Validates Factor and Floor are between 0 and 1, resetting to defaults if invalid.
func NewDecayTask(cfg DecayConfig, store DecayStore) *DecayTask {
	// Validate Factor: must be between 0 and 1
	if cfg.Factor < 0 || cfg.Factor > 1 {
		cfg.Factor = 0.95 // default
	}
	// Validate Floor: must be between 0 and 1
	if cfg.Floor < 0 || cfg.Floor > 1 {
		cfg.Floor = 0.1 // default
	}
	return &DecayTask{cfg: cfg, store: store}
}

func (t *DecayTask) Name() string            { return "decay" }
func (t *DecayTask) Interval() time.Duration { return t.cfg.Interval }
func (t *DecayTask) Timeout() time.Duration  { return t.cfg.Timeout }

func (t *DecayTask) Execute(ctx context.Context) error {
	if t.store == nil {
		logger.DebugCF("cortex", "Decay task skipped: no store configured", nil)
		return nil
	}

	affected, err := t.store.DecayRecallImportance(ctx, t.cfg.Factor, t.cfg.Floor, t.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("decay task failed for factor=%f floor=%f: %w", t.cfg.Factor, t.cfg.Floor, err)
	}

	if affected > 0 {
		logger.DebugCF("cortex", "Decay task applied",
			map[string]interface{}{
				"affected": affected,
				"factor":   t.cfg.Factor,
				"floor":    t.cfg.Floor,
			})
	}
	return nil
}
