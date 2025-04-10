package domain

import "time"

// ExecutionType defines the scheduling type of a task.
type ExecutionType int

const (
	// OneTime tasks run only once.
	OneTime ExecutionType = iota
	// FixedRate tasks run at a fixed interval after the start of the previous run.
	FixedRate
	// FixedDelay tasks run at a fixed interval after the completion of the previous run.
	FixedDelay // TODO: Not directly handled by basic scheduler loop, requires rescheduling logic later.
)

// TaskStatus defines the current state of a task.
type TaskStatus int

const (
	// Pending tasks are waiting to be scheduled or executed.
	Pending TaskStatus = iota
	// Running tasks are currently being executed by a worker.
	Running
	// Completed tasks have finished execution successfully.
	Completed
	// Failed tasks encountered an error during execution.
	Failed
)

// Runnable defines the interface for jobs that can be executed by the worker pool.
type Runnable interface {
	Run() error
}

// Task represents a unit of work to be scheduled and executed.
type Task struct {
	ID                string          // Unique identifier for the task
	Priority          int             // Lower values indicate higher priority
	ScheduledTime     time.Time       // The time the task is scheduled to run
	ExecutionType     ExecutionType   // Type of scheduling (OneTime, FixedRate, etc.)
	Status            TaskStatus      // Current status of the task
	Job               Runnable        // The actual work to be done, must implement Runnable
	Dependencies      []string        // IDs of tasks that must complete before this one starts (for DAGs)
	Interval          time.Duration   // Interval for recurring tasks (FixedRate, FixedDelay)
	EstimatedDuration time.Duration   // Estimated time required to run the task (for DAG optimization)
	CreatedAt         time.Time       // Timestamp when the task was created
	UpdatedAt         time.Time       // Timestamp when the task was last updated
	ExecutionTime     time.Duration   // Actual time it took to execute the task
	Error             string          // Error message if the task failed
	Critical          bool            // Whether the task is on the critical path or otherwise essential
	// TODO: executionCount int       // Could be added for tracking runs
}

// NewTask creates a new task with default values.
func NewTask(id string, priority int, scheduledTime time.Time, job Runnable) *Task {
	return &Task{
		ID:            id,
		Priority:      priority,
		ScheduledTime: scheduledTime,
		ExecutionType: OneTime, // Default to OneTime
		Status:        Pending,
		Job:           job,
		Dependencies:  make([]string, 0),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

// Implement methods needed for heap interface if Task itself is put in heap
// We will use a wrapper or a pointer slice, but Less is useful anyway.
// Less reports whether the task with index i should sort before the task with index j.
// Primary sort by ScheduledTime, secondary by Priority (lower is higher).
func (t *Task) Less(other *Task) bool {
	if t.ScheduledTime.Before(other.ScheduledTime) {
		return true
	}
	if t.ScheduledTime.After(other.ScheduledTime) {
		return false
	}
	// If scheduled times are equal, higher priority (lower number) comes first.
	return t.Priority < other.Priority
}

