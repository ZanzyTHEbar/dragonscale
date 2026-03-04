package cortex

import (
	"context"
	"fmt"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// FadeTestState represents the current state of the fade test.
type FadeTestState string

const (
	// StateNormal means no fade test is active, reminders at normal intervals.
	StateNormal FadeTestState = "normal"
	// StateFading means fade test is active, intervals are being increased.
	StateFading FadeTestState = "fading"
	// StateFaded means fade test completed successfully, extended intervals maintained.
	StateFaded FadeTestState = "faded"
)

// ComplianceTracker is the minimal interface for tracking ADHD inventory compliance.
type ComplianceTracker interface {
	// GetComplianceRate returns the compliance rate (0.0 to 1.0) over the given window.
	GetComplianceRate(ctx context.Context, window time.Duration) (float64, error)
	// GetCurrentReminderInterval returns the current reminder interval.
	GetCurrentReminderInterval(ctx context.Context) (time.Duration, error)
	// UpdateReminderInterval sets a new reminder interval.
	UpdateReminderInterval(ctx context.Context, interval time.Duration) error
	// LogFadeTestEvent records a fade test state change for auditing.
	LogFadeTestEvent(ctx context.Context, fromState, toState FadeTestState, reason string) error
}

// FadeConfig configures the fade test behavior for ADHD inventory reminders.
type FadeConfig struct {
	// ComplianceThreshold triggers fade mode when exceeded (default 0.90 = 90%).
	ComplianceThreshold float64
	// ComplianceWindow is the time window to evaluate compliance (default 30 days).
	ComplianceWindow time.Duration
	// IntervalIncreasePercent is how much to increase intervals during fade (default 20%).
	IntervalIncreasePercent float64
	// MaxInterval caps the reminder interval to prevent excessive spacing.
	MaxInterval time.Duration
	// CheckInterval is how often to evaluate compliance (default 24 hours).
	CheckInterval time.Duration
	// Timeout is the max execution time for each check (default 1 minute).
	Timeout time.Duration
}

// DefaultFadeConfig returns sensible defaults for the fade test.
func DefaultFadeConfig() FadeConfig {
	return FadeConfig{
		ComplianceThreshold:     0.90,
		ComplianceWindow:        30 * 24 * time.Hour, // 30 days
		IntervalIncreasePercent: 0.20,                // 20%
		MaxInterval:             4 * time.Hour,       // Cap at 4 hours
		CheckInterval:           24 * time.Hour,
		Timeout:                 1 * time.Minute,
	}
}

// FadeTask implements the "fade test" pattern for ADHD inventory reminders.
// When compliance stays high (>90% for 30 days), the task gradually increases
// reminder intervals by 20% to test if the user can maintain compliance with
// less frequent prompts. If compliance drops, intervals reset to normal.
type FadeTask struct {
	cfg     FadeConfig
	tracker ComplianceTracker
	state   FadeTestState
}

// NewFadeTask creates a fade test task with the given config and tracker.
// If tracker is nil, the task becomes a no-op.
// Validates config values, resetting to defaults if invalid.
func NewFadeTask(cfg FadeConfig, tracker ComplianceTracker) *FadeTask {
	// Validate compliance threshold: must be between 0.5 and 1.0
	if cfg.ComplianceThreshold < 0.5 || cfg.ComplianceThreshold > 1.0 {
		cfg.ComplianceThreshold = 0.90
	}
	// Validate interval increase: must be between 0 and 1
	if cfg.IntervalIncreasePercent <= 0 || cfg.IntervalIncreasePercent > 1.0 {
		cfg.IntervalIncreasePercent = 0.20
	}
	// Validate max interval: must be at least 1 hour
	if cfg.MaxInterval < time.Hour {
		cfg.MaxInterval = 4 * time.Hour
	}
	return &FadeTask{
		cfg:     cfg,
		tracker: tracker,
		state:   StateNormal,
	}
}

func (t *FadeTask) Name() string            { return "fade-test" }
func (t *FadeTask) Interval() time.Duration { return t.cfg.CheckInterval }
func (t *FadeTask) Timeout() time.Duration  { return t.cfg.Timeout }

// Execute runs the fade test compliance check and adjusts reminder intervals.
func (t *FadeTask) Execute(ctx context.Context) error {
	if t.tracker == nil {
		logger.DebugCF("cortex", "Fade test task skipped: no tracker configured", nil)
		return nil
	}

	// Get current compliance rate
	compliance, err := t.tracker.GetComplianceRate(ctx, t.cfg.ComplianceWindow)
	if err != nil {
		return fmt.Errorf("fade test failed to get compliance rate: %w", err)
	}

	// Get current reminder interval
	currentInterval, err := t.tracker.GetCurrentReminderInterval(ctx)
	if err != nil {
		return fmt.Errorf("fade test failed to get current interval: %w", err)
	}

	// Determine state transition and new interval
	oldState := t.state
	newInterval := currentInterval
	var transitionReason string

	switch t.state {
	case StateNormal:
		// Check if we should enter fade mode
		if compliance >= t.cfg.ComplianceThreshold {
			t.state = StateFading
			newInterval = time.Duration(float64(currentInterval) * (1 + t.cfg.IntervalIncreasePercent))
			if newInterval > t.cfg.MaxInterval {
				newInterval = t.cfg.MaxInterval
			}
			transitionReason = fmt.Sprintf("compliance %.1f%% exceeded threshold %.1f%%, entering fade mode",
				compliance*100, t.cfg.ComplianceThreshold*100)
		}

	case StateFading, StateFaded:
		// Check if we need to reset to normal
		if compliance < t.cfg.ComplianceThreshold {
			// Reset to normal spacing
			t.state = StateNormal
			newInterval = t.calculateNormalInterval(currentInterval)
			transitionReason = fmt.Sprintf("compliance %.1f%% dropped below threshold %.1f%%, resetting to normal",
				compliance*100, t.cfg.ComplianceThreshold*100)
		} else if t.state == StateFading {
			// Continue fading - can we increase further?
			candidateInterval := time.Duration(float64(currentInterval) * (1 + t.cfg.IntervalIncreasePercent))
			if candidateInterval <= t.cfg.MaxInterval {
				newInterval = candidateInterval
				transitionReason = fmt.Sprintf("maintaining fade mode, increasing interval to %v", newInterval)
			} else {
				// Max interval reached, mark as fully faded
				t.state = StateFaded
				transitionReason = "reached maximum interval, fade test completed"
			}
		}
	}

	// Log state change if any
	if oldState != t.state || transitionReason != "" {
		if err := t.tracker.LogFadeTestEvent(ctx, oldState, t.state, transitionReason); err != nil {
			logger.WarnCF("cortex", "Failed to log fade test event", map[string]interface{}{
				"error": err,
			})
		}
	}

	// Apply new interval if changed
	if newInterval != currentInterval {
		if err := t.tracker.UpdateReminderInterval(ctx, newInterval); err != nil {
			return fmt.Errorf("fade test failed to update interval to %v: %w", newInterval, err)
		}
		logger.InfoCF("cortex", "Fade test adjusted reminder interval",
			map[string]interface{}{
				"old_interval": currentInterval,
				"new_interval": newInterval,
				"compliance":   fmt.Sprintf("%.1f%%", compliance*100),
				"state":        string(t.state),
				"old_state":    string(oldState),
				"reason":       transitionReason,
			})
	}

	logger.DebugCF("cortex", "Fade test check completed",
		map[string]interface{}{
			"compliance": fmt.Sprintf("%.1f%%", compliance*100),
			"threshold":  fmt.Sprintf("%.1f%%", t.cfg.ComplianceThreshold*100),
			"state":      string(t.state),
			"interval":   currentInterval,
		})

	return nil
}

// calculateNormalInterval reverses the fade increase to restore original spacing.
// This is a simplified calculation - in production, you might store the original interval.
func (t *FadeTask) calculateNormalInterval(currentInterval time.Duration) time.Duration {
	// Reverse the percentage increase: new = old * 1.2, so old = new / 1.2
	normalInterval := time.Duration(float64(currentInterval) / (1 + t.cfg.IntervalIncreasePercent))

	// Don't let it go below a reasonable minimum (e.g., 15 minutes)
	minInterval := 15 * time.Minute
	if normalInterval < minInterval {
		return minInterval
	}
	return normalInterval
}

// GetState returns the current fade test state (for testing/debugging).
func (t *FadeTask) GetState() FadeTestState {
	return t.state
}
