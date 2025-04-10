package domain

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// TaskStatsCollector collects statistics about task execution
type TaskStatsCollector struct {
	completed    int
	failed       int
	pending      int
	running      int
	totalLatency time.Duration
	mu           sync.RWMutex
	startTime    time.Time
}

// NewTaskStatsCollector creates a new TaskStatsCollector
func NewTaskStatsCollector() *TaskStatsCollector {
	return &TaskStatsCollector{
		startTime: time.Now(),
	}
}

// RecordTaskStatus updates statistics based on task status
func (tsc *TaskStatsCollector) RecordTaskStatus(status TaskStatus, latency time.Duration) {
	tsc.mu.Lock()
	defer tsc.mu.Unlock()

	switch status {
	case Completed:
		tsc.completed++
		tsc.totalLatency += latency
	case Failed:
		tsc.failed++
	case Pending:
		tsc.pending++
	case Running:
		tsc.running++
	}
}

// GetStats returns the current statistics
func (tsc *TaskStatsCollector) GetStats() map[string]interface{} {
	tsc.mu.RLock()
	defer tsc.mu.RUnlock()

	avgLatency := time.Duration(0)
	if tsc.completed > 0 {
		avgLatency = tsc.totalLatency / time.Duration(tsc.completed)
	}

	return map[string]interface{}{
		"completed":     tsc.completed,
		"failed":        tsc.failed,
		"pending":       tsc.pending,
		"running":       tsc.running,
		"avg_latency":   avgLatency,
		"total_latency": tsc.totalLatency,
		"uptime":        time.Since(tsc.startTime),
	}
}

// PrintStats prints the current statistics to the log
func (tsc *TaskStatsCollector) PrintStats() {
	stats := tsc.GetStats()
	log.Printf("Task Stats - Completed: %d, Failed: %d, Pending: %d, Running: %d, Avg Latency: %v",
		stats["completed"], stats["failed"], stats["pending"], stats["running"], stats["avg_latency"])
}

// StartStatsMonitor starts a background goroutine that periodically prints stats
func (tsc *TaskStatsCollector) StartStatsMonitor(interval time.Duration, stopCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				tsc.PrintStats()
			case <-stopCh:
				return
			}
		}
	}()
}

// EventHandler processes domain events and updates statistics
func (tsc *TaskStatsCollector) EventHandler(events <-chan Event) {
	go func() {
		for event := range events {
			if task, ok := event.Data.(*Task); ok {
				latency := time.Since(task.CreatedAt)
				tsc.RecordTaskStatus(task.Status, latency)
			}
		}
	}()
}

// TaskProgressBar provides a simple way to track DAG execution progress
type TaskProgressBar struct {
	total     int
	completed int
	failed    int
	mu        sync.RWMutex
}

// NewTaskProgressBar creates a new TaskProgressBar
func NewTaskProgressBar(totalTasks int) *TaskProgressBar {
	return &TaskProgressBar{
		total: totalTasks,
	}
}

// Update updates the progress bar state
func (tpb *TaskProgressBar) Update(completed, failed int) {
	tpb.mu.Lock()
	defer tpb.mu.Unlock()
	tpb.completed = completed
	tpb.failed = failed
}

// Print prints the current progress to stdout
func (tpb *TaskProgressBar) Print() {
	tpb.mu.RLock()
	defer tpb.mu.RUnlock()

	// Calculate progress percentage
	progress := float64(tpb.completed+tpb.failed) / float64(tpb.total) * 100

	// Create a visual bar
	width := 50
	completed := int(float64(width) * float64(tpb.completed) / float64(tpb.total))
	failed := int(float64(width) * float64(tpb.failed) / float64(tpb.total))
	remaining := width - completed - failed

	bar := strings.Repeat("█", completed) + strings.Repeat("▒", failed) + strings.Repeat("░", remaining)

	fmt.Printf("\rProgress: [%s] %.1f%% (%d/%d completed, %d failed)",
		bar, progress, tpb.completed, tpb.total, tpb.failed)

	// If complete, add a newline
	if tpb.completed+tpb.failed >= tpb.total {
		fmt.Println()
	} else {
		// Flush stdout to ensure partial line is displayed
		os.Stdout.Sync()
	}
}
