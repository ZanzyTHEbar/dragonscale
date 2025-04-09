package domain

import (
	"fmt" // Added fmt import
	"log"
	"sync"
	"time"
	// "golang.org/x/sync/errgroup" // Consider using for parallel execution within OptimizeAndExecute
)

// DAGExecutor is responsible for optimizing and executing tasks within a DAG.
type DAGExecutor struct {
	cache         map[string]*DAG // Cache for static DAG structures
	mu            sync.RWMutex    // Protects the cache
	taskScheduler *TaskScheduler  // Reference to the main scheduler to dispatch tasks
	// TODO: Add EventBus reference if needed for task completion events
	// eventBus EventBus
}

// NewDAGExecutor creates a new DAGExecutor.
func NewDAGExecutor(scheduler *TaskScheduler) *DAGExecutor {
	if scheduler == nil {
		log.Println("Warning: Creating DAGExecutor with nil TaskScheduler.")
	}
	return &DAGExecutor{
		cache:         make(map[string]*DAG),
		taskScheduler: scheduler,
	}
}

// ExecuteDAG processes a DAG, potentially optimizing it and scheduling its tasks.
// This is the main entry point for DAG execution.
func (de *DAGExecutor) ExecuteDAG(dag *DAG) error {
	if dag == nil {
		return fmt.Errorf("cannot execute nil DAG")
	}
	log.Printf("Executing DAG ID: %s", dag.ID)

	// Check cache for static DAGs
	if dag.Cacheable {
		de.mu.RLock()
		cachedDAG, found := de.cache[dag.ID]
		de.mu.RUnlock()
		if found {
			// log.Printf("Using cached structure for DAG ID: %s", dag.ID)
			dag = cachedDAG // Use cached version for optimization steps
		} else {
			// Add to cache if not found
			de.mu.Lock()
			// Only cache if not already present (double-check)
			if _, exists := de.cache[dag.ID]; !exists {
				de.cache[dag.ID] = dag
			}
			de.mu.Unlock()
		}
	}

	// --- Placeholder Optimization & Scheduling ---
	// In a real system, this needs proper dependency management.
	// The current approach schedules all tasks at once, relying on the
	// TaskScheduler's time-based scheduling, but ignoring DAG dependencies.

	// 1. Compute Critical Path (Placeholder)
	criticalPathTasks := de.computeCriticalPath(dag)
	// log.Printf("Placeholder Critical Path for DAG %s: %d tasks", dag.ID, len(criticalPathTasks))

	// 2. List Scheduling (Placeholder) - Generate prioritized task list
	sortedTasks := de.listSchedule(dag, criticalPathTasks)
	// log.Printf("Placeholder List Schedule for DAG %s: %d tasks", dag.ID, len(sortedTasks))

	// 3. Dispatch Tasks (Simplified - Ignores Dependencies)
	if de.taskScheduler == nil {
		log.Printf("Error: DAGExecutor has no TaskScheduler, cannot schedule tasks for DAG %s", dag.ID)
		return fmt.Errorf("taskscheduler not configured for dagexecutor")
	}

	log.Printf("Dispatching %d tasks from DAG %s to TaskScheduler (dependency ignored by placeholder)", len(sortedTasks), dag.ID)
	for _, task := range sortedTasks {
		if task != nil {
			// Ensure task status is Pending before scheduling
			task.Status = Pending
			task.UpdatedAt = time.Now() // Reset update time
			de.taskScheduler.Schedule(task)
		} else {
			log.Printf("Warning: Nil task found in sorted list for DAG %s", dag.ID)
		}
	}

	// TODO: Implement proper DAG execution logic:
	//   - Identify source nodes (tasks with no dependencies).
	//   - Schedule source nodes.
	//   - Listen for task completion events (e.g., via EventBus).
	//   - When a task completes, check its dependents.
	//   - If a dependent task has all its prerequisites met, schedule it.
	//   - Use errgroup or similar for concurrent checks/scheduling.

	return nil
}

// computeCriticalPath identifies the critical path (longest execution path) in the DAG.
// Placeholder implementation: Returns all tasks for now.
func (de *DAGExecutor) computeCriticalPath(dag *DAG) []*Task {
	// Placeholder: just return all tasks
	allTasks := make([]*Task, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		allTasks = append(allTasks, task)
	}
	return allTasks
}

// listSchedule generates an ordered list of tasks for execution based on priority and dependencies.
// Placeholder implementation: Simulates prioritizing critical path tasks.
func (de *DAGExecutor) listSchedule(dag *DAG, criticalPath []*Task) []*Task {
	allTasks := make([]*Task, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		allTasks = append(allTasks, task)
	}

	// Basic prioritization: Put alleged critical path tasks first
	criticalMap := make(map[string]bool)
	for _, t := range criticalPath {
		if t != nil {
			criticalMap[t.ID] = true
		}
	}
	sorted := make([]*Task, 0, len(allTasks))
	nonCritical := make([]*Task, 0)
	for _, t := range allTasks {
		if _, isCritical := criticalMap[t.ID]; isCritical {
			sorted = append(sorted, t)
		} else {
			nonCritical = append(nonCritical, t)
		}
	}
	sorted = append(sorted, nonCritical...)

	return sorted
}

// TODO: Implement UpdateDynamicDAG and delta/compaction logic later if needed.
// func (de *DAGExecutor) UpdateDynamicDAG(dag *DAG, delta *Delta) { ... }
