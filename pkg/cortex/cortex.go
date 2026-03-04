package cortex

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// Cortex is an autonomous background scheduler that runs periodic
// maintenance tasks for the agent's memory and state systems.
// Each task self-locks via TryLock so slow runs don't pile up.
// Per-task Interval() is respected: a task only fires when its
// individual interval has elapsed since its last execution.
type Cortex struct {
	tasks        []Task
	locks        map[string]*sync.Mutex
	lastRun      map[string]time.Time
	running      atomic.Bool
	tickInterval time.Duration
}

// New creates a Cortex scheduler with the given tasks.
// Tasks are evaluated every tickInterval (default 60s).
func New(tasks []Task, tickInterval time.Duration) *Cortex {
	if tickInterval <= 0 {
		tickInterval = 60 * time.Second
	}
	locks := make(map[string]*sync.Mutex, len(tasks))
	lastRun := make(map[string]time.Time, len(tasks))
	for _, t := range tasks {
		locks[t.Name()] = &sync.Mutex{}
	}
	return &Cortex{
		tasks:        tasks,
		locks:        locks,
		lastRun:      lastRun,
		tickInterval: tickInterval,
	}
}

// Start begins the scheduler loop. It blocks until ctx is cancelled.
// Call this in a goroutine.
func (c *Cortex) Start(ctx context.Context) {
	c.running.Store(true)
	defer c.running.Store(false)

	ticker := time.NewTicker(c.tickInterval)
	defer ticker.Stop()

	logger.InfoCF("cortex", "Cortex scheduler started",
		map[string]interface{}{"tasks": len(c.tasks), "tick_interval": c.tickInterval.String()})

	for {
		select {
		case <-ctx.Done():
			logger.InfoCF("cortex", "Cortex scheduler stopping", nil)
			return
		case <-ticker.C:
			c.tick(ctx)
		}
	}
}

// Running returns true if the scheduler loop is active.
func (c *Cortex) Running() bool {
	return c.running.Load()
}

func (c *Cortex) tick(ctx context.Context) {
	now := time.Now()
	for _, task := range c.tasks {
		task := task
		mu := c.locks[task.Name()]

		if last, ok := c.lastRun[task.Name()]; ok {
			if now.Sub(last) < task.Interval() {
				continue
			}
		}

		if !mu.TryLock() {
			continue
		}

		c.lastRun[task.Name()] = now

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("cortex", "Task panicked", map[string]interface{}{
						"task":  task.Name(),
						"panic": r,
					})
				}
				mu.Unlock()
			}()
			tCtx, cancel := context.WithTimeout(ctx, task.Timeout())
			defer cancel()

			if err := task.Execute(tCtx); err != nil {
				logger.WarnCF("cortex", "Task failed",
					map[string]interface{}{
						"task":    task.Name(),
						"error":   err.Error(),
						"elapsed": time.Since(now).String(),
					})
			}
		}()
	}
}
