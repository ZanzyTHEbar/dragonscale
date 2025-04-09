package ports

import "github.com/ZanzyTHEbar/dragonscale/internal/domain"

// TaskExecutor defines the port for submitting tasks to an execution engine (like a worker pool).
// This decouples the core scheduling logic from the specific implementation of task execution.
type TaskExecutor interface {
	// Add submits a runnable job for execution.
	// Implementations may block if internal capacity is reached.
	Add(job domain.Runnable)

	// TryAdd attempts to submit a runnable job for execution without blocking.
	// Returns true if the job was accepted, false otherwise (e.g., queue full, pool stopped).
	// Optional: Useful for scenarios where blocking is undesirable.
	TryAdd(job domain.Runnable) bool

	// Start initializes the executor (e.g., starts worker pool monitor).
	// Optional: Depending on whether the executor needs an explicit start signal.
	Start() error // Or just Start() if no error possible on start

	// Stop gracefully shuts down the executor, waiting for active jobs to complete (optional).
	Stop()
}
