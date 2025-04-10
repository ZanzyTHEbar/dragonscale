package domain

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/sourcegraph/conc"
	"github.com/sourcegraph/conc/pool"
	"golang.org/x/sync/errgroup"
)

// DAGExecutor is responsible for optimizing and executing tasks within a DAG.
type DAGExecutor struct {
	cache         map[string]*DAG // Cache for static DAG structures
	mu            sync.RWMutex    // Protects the cache
	taskScheduler *TaskScheduler  // Reference to the main scheduler to dispatch tasks
	eventBus      EventBus        // For receiving task completion events
}

// EventBus defines the interface for event bus interaction.
type EventBus interface {
	Publish(event Event)
	Subscribe(topic string, bufferSize int) (chan Event, error)
	Unsubscribe(topic string, ch chan Event) error
}

// NewDAGExecutor creates a new DAGExecutor.
func NewDAGExecutor(scheduler *TaskScheduler, eventBus EventBus) *DAGExecutor {
	if scheduler == nil {
		log.Println("Warning: Creating DAGExecutor with nil TaskScheduler.")
	}
	return &DAGExecutor{
		cache:         make(map[string]*DAG),
		taskScheduler: scheduler,
		eventBus:      eventBus,
	}
}

// ExecuteDAG processes a DAG, optimizing and scheduling its tasks.
// This is the main entry point for DAG execution with dependency tracking.
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
			log.Printf("Using cached structure for DAG ID: %s", dag.ID)
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

	// We'll use the complete optimized execution path
	// but with a timeout based on DAG complexity
	ctx, cancel := context.WithTimeout(context.Background(), calculateDAGTimeout(dag))
	defer cancel() // Ensure resources are cleaned up

	// Publish DAG started event
	if de.eventBus != nil {
		de.PublishDAGStartedEvent(dag)
	}

	// --- Professional-grade scheduling algorithm ---
	// 1. Validate DAG for execution
	if err := validateDAG(dag); err != nil {
		if de.eventBus != nil {
			de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
				"dag":   dag,
				"error": err.Error(),
				"stage": "validation",
			}))
		}
		return fmt.Errorf("DAG validation failed: %w", err)
	}

	// 2. Compute Critical Path
	criticalPathTasks := de.computeCriticalPath(dag)
	log.Printf("Critical Path for DAG %s: %d tasks", dag.ID, len(criticalPathTasks))
	
	// Publish critical path event if we have an event bus
	if de.eventBus != nil {
		de.PublishDAGEvent(dag, CriticalPathFound, map[string]interface{}{
			"dag":          dag,
			"criticalPath": criticalPathTasks,
		})
	}

	// 3. Apply List Scheduling - Generate prioritized task list
	sortedTasks := de.listSchedule(dag, criticalPathTasks)
	log.Printf("List Schedule for DAG %s: %d tasks prioritized", dag.ID, len(sortedTasks))
	
	// 4. Create execution context and subscribe to events
	execContext := &dagExecutionContext{
		dag:         dag,
		completed:   make(map[string]bool),
		scheduled:   make(map[string]bool),
		mu:          &sync.Mutex{},
		executor:    de,
		ctx:         ctx,
	}
	
	// If event bus is not available, fallback to simpler execution
	if de.eventBus == nil {
		log.Printf("Warning: EventBus not available for DAG %s, using simplified execution", dag.ID)
		return de.executeDAGSimple(dag, sortedTasks)
	}
	
	// 5. Set up event handling
	eg, ctx := errgroup.WithContext(ctx)
	
	// Subscribe to multiple event types
	completionChannel, err := de.eventBus.Subscribe(TaskCompleted, 100)
	if err != nil {
		log.Printf("Warning: Failed to subscribe to completion events, using simplified execution: %v", err)
		return de.executeDAGSimple(dag, sortedTasks)
	}
	
	failureChannel, err := de.eventBus.Subscribe(TaskFailed, 100)
	if err != nil {
		de.eventBus.Unsubscribe(TaskCompleted, completionChannel)
		log.Printf("Warning: Failed to subscribe to failure events, using simplified execution: %v", err)
		return de.executeDAGSimple(dag, sortedTasks)
	}
	
	// Start event listeners
	eg.Go(func() error {
		defer de.eventBus.Unsubscribe(TaskCompleted, completionChannel)
		return de.handleCompletionEvents(ctx, dag, execContext, completionChannel)
	})
	
	eg.Go(func() error {
		defer de.eventBus.Unsubscribe(TaskFailed, failureChannel)
		for {
			select {
			case event, ok := <-failureChannel:
				if (!ok) {
					return nil // Channel closed
				}
				
				task, ok := event.Data.(*Task)
				if !ok {
					continue // Not a task event
				}
				
				// Check if this task is part of our DAG
				if _, exists := dag.Tasks[task.ID]; !exists {
					continue // Not a task in this DAG
				}
				
				log.Printf("DAG %s: Task %s failed: %s", dag.ID, task.ID, task.Error)
				
				// If the task is critical, fail the entire DAG
				if task.Critical {
					log.Printf("DAG %s: Critical task %s failed, failing DAG", dag.ID, task.ID)
					
					// Publish DAG failed event
					de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
						"dag":       dag,
						"failedTask": task,
					}))
					
					// Return error to cancel the errgroup
					return fmt.Errorf("critical task %s failed: %s", task.ID, task.Error)
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	
	// 6. Schedule source tasks using our enhanced taskWithEventBus wrapper
	sourceTasks := de.identifySourceTasks(dag, sortedTasks)
	
	// Use a pool for scheduling source tasks in parallel
	p := pool.New().WithMaxGoroutines(10).WithContext(ctx)
	
	for _, task := range sourceTasks {
		// Capture task variable to avoid closure issues
		currentTask := task
		
		// Mark as scheduled
		execContext.mu.Lock()
		execContext.scheduled[currentTask.ID] = true
		execContext.mu.Unlock()
		
		p.Go(func(ctx context.Context) error {
			// Publish task scheduled event
			de.eventBus.Publish(NewEvent(TaskScheduled, currentTask))
			
			// Create the event-publishing wrapper task
			wrapper := &taskWithEventBus{
				originalTask: currentTask,
				eventBus:     de.eventBus,
				ctx:          ctx,
				dagID:        dag.ID,
			}
			
			// Schedule the task with our scheduler
			scheduledTask := &Task{
				ID:            currentTask.ID,
				Priority:      currentTask.Priority,
				ScheduledTime: time.Now(),
				ExecutionType: OneTime,
				Status:        Pending,
				Job:           wrapper, // Use our event-publishing wrapper
				Dependencies:  currentTask.Dependencies,
				CreatedAt:     currentTask.CreatedAt,
				UpdatedAt:     time.Now(),
			}
			
			log.Printf("DAG %s: Scheduling source task %s", dag.ID, currentTask.ID)
			de.taskScheduler.Schedule(scheduledTask)
			return nil
		})
	}
	
	// Wait for all source tasks to be scheduled
	if err := p.Wait(); err != nil {
		de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
			"dag":   dag,
			"error": err.Error(),
			"stage": "source_task_scheduling",
		}))
		return fmt.Errorf("error scheduling source tasks: %w", err)
	}
	
	// 7. Wait for all tasks to complete or context to be canceled
	if err := eg.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
				"dag":   dag,
				"error": "timeout",
				"stage": "execution",
			}))
			return fmt.Errorf("DAG execution timed out after %v", calculateDAGTimeout(dag))
		}
		
		// Some other error occurred
		if de.eventBus != nil {
			de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
				"dag":   dag,
				"error": err.Error(),
				"stage": "execution",
			}))
		}
		return fmt.Errorf("DAG execution failed: %w", err)
	}
	
	// Count completed tasks
	execContext.mu.Lock()
	completedCount := len(execContext.completed)
	execContext.mu.Unlock()
	
	// Verify all tasks completed
	if completedCount < len(dag.Tasks) {
		if de.eventBus != nil {
			de.eventBus.Publish(NewEvent(DAGFailed, map[string]interface{}{
				"dag":            dag,
				"error":          "incomplete execution",
				"stage":          "verification",
				"completedTasks": completedCount,
				"totalTasks":     len(dag.Tasks),
			}))
		}
		return fmt.Errorf("DAG execution incomplete: %d of %d tasks completed", 
		                 completedCount, len(dag.Tasks))
	}
	
	// Publish success event if not published in the event handler
	if de.eventBus != nil {
		de.eventBus.Publish(NewEvent(DAGCompleted, dag))
	}
	
	log.Printf("DAG %s execution completed successfully", dag.ID)
	return nil
}

// computeCriticalPath calculates the critical path of the DAG.
// The critical path is the longest path through the DAG, which determines
// the minimum time needed to complete all tasks.
func (de *DAGExecutor) computeCriticalPath(dag *DAG) []*Task {
	// Initialize earliest completion time (ECT) for each task
	ect := make(map[string]time.Duration)
	pathPredecessor := make(map[string]string) // Store predecessor on critical path
	
	// Topological sort to calculate ECT
	topoOrder := de.topologicalSort(dag)
	
	// Calculate earliest completion time for each task in topological order
	for _, taskID := range topoOrder {
		task := dag.Tasks[taskID]
		
		// Start with task's own duration
		maxECT := task.EstimatedDuration
		predecessor := ""
		
		// Check all dependencies to find the critical predecessor
		for _, depID := range dag.ReverseEdges[taskID] {
			if ect[depID]+task.EstimatedDuration > maxECT {
				maxECT = ect[depID] + task.EstimatedDuration
				predecessor = depID
			}
		}
		
		ect[taskID] = maxECT
		pathPredecessor[taskID] = predecessor
	}
	
	// Find the task with the maximum ECT (end of critical path)
	var endTask string
	var maxECT time.Duration
	for taskID, taskECT := range ect {
		if taskECT > maxECT {
			maxECT = taskECT
			endTask = taskID
		}
	}
	
	// Reconstruct the critical path by working backwards from the end task
	criticalPath := make([]*Task, 0)
	for current := endTask; current != ""; current = pathPredecessor[current] {
		criticalPath = append([]*Task{dag.Tasks[current]}, criticalPath...)
	}
	
	return criticalPath
}

// listSchedule implements the List Scheduling with Priority algorithm.
// This algorithm schedules tasks based on priority, dependencies, and the critical path.
func (de *DAGExecutor) listSchedule(dag *DAG, criticalPath []*Task) []*Task {
	// Create a map for O(1) lookup of tasks on the critical path
	onCriticalPath := make(map[string]bool)
	for _, task := range criticalPath {
		onCriticalPath[task.ID] = true
	}
	
	// Calculate the level (depth) of each task in the DAG
	levels := de.calculateTaskLevels(dag)
	
	// Calculate the total number of successors for each task (impact factor)
	successorCount := de.calculateSuccessorCounts(dag)
	
	// Calculate dynamic priority score for each task
	taskScores := make(map[string]float64)
	for taskID, task := range dag.Tasks {
		// Base priority from task definition
		basePriority := float64(task.Priority)
		
		// Critical path bonus (highest weight)
		criticalPathBonus := 0.0
		if onCriticalPath[taskID] {
			criticalPathBonus = 100.0
		}
		
		// Level bonus (higher levels should be scheduled earlier)
		levelBonus := float64(levels[taskID]) * 10.0
		
		// Successor bonus (tasks with more dependents have higher impact)
		successorBonus := float64(successorCount[taskID]) * 5.0
		
		// Estimated duration penalty (shorter tasks get priority when other factors are equal)
		durationPenalty := -float64(task.EstimatedDuration) * 0.1
		
		// Combine all factors into a final score
		taskScores[taskID] = basePriority + criticalPathBonus + levelBonus + successorBonus + durationPenalty
	}
	
	// Create a list of all tasks and sort by priority score
	allTasks := make([]*Task, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		allTasks = append(allTasks, task)
	}
	
	// Sort by calculated priority scores (descending)
	sort.Slice(allTasks, func(i, j int) bool {
		return taskScores[allTasks[i].ID] > taskScores[allTasks[j].ID]
	})
	
	return allTasks
}

// calculateTaskLevels computes the level (depth) of each task in the DAG.
// A task's level is the length of the longest path from any source node to this task.
func (de *DAGExecutor) calculateTaskLevels(dag *DAG) map[string]int {
	levels := make(map[string]int)
	
	// Find source nodes (tasks with no dependencies)
	sourceNodes := make([]string, 0)
	for taskID := range dag.Tasks {
		if len(dag.ReverseEdges[taskID]) == 0 {
			sourceNodes = append(sourceNodes, taskID)
			levels[taskID] = 0 // Source nodes are at level 0
		}
	}
	
	// Process nodes in topological order to calculate levels
	queue := sourceNodes
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		
		// Process all tasks that depend on the current task
		for _, dependent := range dag.Edges[current] {
			// The level of a dependent is the max of its current level and the level of the current task + 1
			newLevel := levels[current] + 1
			if currentLevel, exists := levels[dependent]; !exists || newLevel > currentLevel {
				levels[dependent] = newLevel
			}
			
			// Check if all dependencies of the dependent have been processed
			allDepsProcessed := true
			for _, dep := range dag.ReverseEdges[dependent] {
				if _, exists := levels[dep]; !exists {
					allDepsProcessed = false
					break
				}
			}
			
			// If all dependencies are processed, add the dependent to the queue
			if allDepsProcessed && !contains(queue, dependent) {
				queue = append(queue, dependent)
			}
		}
	}
	
	return levels
}

// calculateSuccessorCounts computes the total number of successors for each task.
// This includes direct successors and indirect successors.
func (de *DAGExecutor) calculateSuccessorCounts(dag *DAG) map[string]int {
	counts := make(map[string]int)
	
	// Initialize with direct successor counts
	for taskID, successors := range dag.Edges {
		counts[taskID] = len(successors)
	}
	
	// Process in reverse topological order to account for indirect successors
	topoOrder := de.topologicalSort(dag)
	for i := len(topoOrder) - 1; i >= 0; i-- {
		taskID := topoOrder[i]
		
		// Add the current task's successor count to all its predecessors
		for _, predID := range dag.ReverseEdges[taskID] {
			counts[predID] += counts[taskID]
		}
	}
	
	return counts
}

// topologicalSort returns a topological ordering of the tasks in the DAG.
func (de *DAGExecutor) topologicalSort(dag *DAG) []string {
	// Count incoming edges for each node
	inDegree := make(map[string]int)
	for taskID := range dag.Tasks {
		inDegree[taskID] = len(dag.ReverseEdges[taskID])
	}
	
	// Collect nodes with no incoming edges
	queue := make([]string, 0)
	for taskID := range dag.Tasks {
		if inDegree[taskID] == 0 {
			queue = append(queue, taskID)
		}
	}
	
	// Process nodes in topological order
	result := make([]string, 0, len(dag.Tasks))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)
		
		// Reduce in-degree of all successors
		for _, successor := range dag.Edges[current] {
			inDegree[successor]--
			if inDegree[successor] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	
	// Check if we visited all nodes (if not, the graph has a cycle)
	if len(result) != len(dag.Tasks) {
		log.Printf("Warning: DAG contains a cycle, topological sort is incomplete")
	}
	
	return result
}

// ExecuteOptimizedDAG performs more advanced optimizations and executes the DAG.
// It implements proper dependency tracking through the event bus and uses conc for parallel processing.
func (de *DAGExecutor) ExecuteOptimizedDAG(dag *DAG) error {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel() // Ensure resources are cleaned up

    if dag == nil {
        return fmt.Errorf("cannot execute nil DAG")
    }
    log.Printf("Executing optimized DAG ID: %s with %d tasks", dag.ID, len(dag.Tasks))
    
    if de.taskScheduler == nil {
        return fmt.Errorf("taskscheduler not configured for dagexecutor")
    }
    
    if de.eventBus == nil {
        return fmt.Errorf("eventbus not configured for dagexecutor")
    }

    // Perform topological sort to detect cycles and get execution order
    sortedTasks, err := de.topologicalSortWithCycleDetection(dag)
    if (err != nil) {
        return fmt.Errorf("DAG execution failed: %w", err)
    }
    
    // Create a DAG execution context for tracking task state with proper context support
    execContext := &dagExecutionContext{
        dag:         dag,
        completed:   make(map[string]bool),
        scheduled:   make(map[string]bool),
        mu:          &sync.Mutex{},
        executor:    de,
        ctx:         ctx,
    }
    
    // Use errgroup for tracking and managing goroutines
    eg, ctx := errgroup.WithContext(ctx)
    
    // Subscribe to task completion events
    completionChannel, err := de.eventBus.Subscribe("task.completed", 100)
    if err != nil {
        return fmt.Errorf("failed to subscribe to task completion events: %w", err)
    }
    
    // Start a goroutine to handle task completion events
    eg.Go(func() error {
        defer de.eventBus.Unsubscribe("task.completed", completionChannel)
        
        for {
            select {
            case event, ok := <-completionChannel:
                if !ok {
                    // Channel closed
                    return nil
                }
                
                task, ok := event.Data.(*Task)
                if !ok {
                    continue // Not a task event
                }
                
                // Check if this task is part of our DAG
                if _, exists := dag.Tasks[task.ID]; !exists {
                    continue // Not a task in this DAG
                }
                
                // Mark the task as completed
                execContext.markCompleted(task.ID)
                
                // Check and schedule dependent tasks if possible
                if err := execContext.scheduleDependentTasks(task.ID); err != nil {
                    log.Printf("Error scheduling dependent tasks: %v", err)
                }
            
            case <-ctx.Done():
                // Context canceled
                return ctx.Err()
            }
        }
    })
    
    // Use conc package's pool for efficient parallel processing of source tasks
    p := pool.New().WithMaxGoroutines(10)
    
    // Schedule all source tasks (tasks with no dependencies)
    for _, task := range sortedTasks {
        if len(dag.ReverseEdges[task.ID]) == 0 {
            // Use copy of the task variable to avoid closure issues
            currentTask := task
            
            // Mark as scheduled to prevent rescheduling
            execContext.mu.Lock()
            execContext.scheduled[currentTask.ID] = true
            execContext.mu.Unlock()
            
            // Use conc's pool to schedule source tasks in parallel
            p.Go(func() {
                log.Printf("DAG %s: Scheduling source task %s", dag.ID, currentTask.ID)
                
                // Create a wrapper task that publishes completion events
                wrapperTask := &taskWithPublication{
                    originalTask: currentTask,
                    eventBus:     de.eventBus,
                    ctx:          ctx,
                }
                
                // Schedule the task for execution
                scheduledTask := &Task{
                    ID:            currentTask.ID,
                    Priority:      currentTask.Priority,
                    ScheduledTime: time.Now(),
                    ExecutionType: OneTime,
                    Status:        Pending,
                    Job:           wrapperTask,
                    Dependencies:  currentTask.Dependencies,
                    CreatedAt:     currentTask.CreatedAt,
                    UpdatedAt:     time.Now(),
                }
                
                de.taskScheduler.Schedule(scheduledTask)
            })
        }
    }
    
    // Wait for all source tasks to be scheduled
    p.Wait()
    
    // Wait for all event processing to complete or context to be canceled
    return eg.Wait()
}

// topologicalSortWithCycleDetection performs a topological sort of the DAG and detects cycles.
func (de *DAGExecutor) topologicalSortWithCycleDetection(dag *DAG) ([]*Task, error) {
    // Result will contain the sorted tasks
    var result []*Task
    
    // Track visited and in-progress nodes for cycle detection
    visited := make(map[string]bool)
    inProgress := make(map[string]bool)
    
    // Define recursive DFS function
    var dfs func(taskID string) error
    dfs = func(taskID string) error {
        // If we've already fully processed this node, skip
        if visited[taskID] {
            return nil
        }
        
        // Check for cycle
        if inProgress[taskID] {
            return fmt.Errorf("cycle detected in DAG involving task %s", taskID)
        }
        
        // Mark as in-progress for cycle detection
        inProgress[taskID] = true
        
        // Visit all children first (depth-first)
        for _, dependentID := range dag.Edges[taskID] {
            if err := dfs(dependentID); err != nil {
                return err
            }
        }
        
        // Mark as visited and add to result
        visited[taskID] = true
        inProgress[taskID] = false
        
        // Add to sorted result
        task, exists := dag.Tasks[taskID]
        if !exists {
            return fmt.Errorf("invalid task ID in DAG: %s", taskID)
        }
        result = append(result, task)
        
        return nil
    }
    
    // Start DFS from each unvisited node
    for taskID := range dag.Tasks {
        if !visited[taskID] {
            if err := dfs(taskID); err != nil {
                return nil, err
            }
        }
    }
    
    // Reverse the result to get correct topological order
    // (DFS puts dependencies after the tasks, we want dependencies first)
    for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
        result[i], result[j] = result[j], result[i]
    }
    
    return result, nil
}

// dagExecutionContext holds the state for a running DAG execution
type dagExecutionContext struct {
    dag         *DAG
    completed   map[string]bool // Track which tasks have completed
    scheduled   map[string]bool // Track which tasks have been scheduled
    mu          *sync.Mutex     // Mutex for concurrent access
    executor    *DAGExecutor    // Reference back to the executor
    ctx         context.Context // Context for cancellation support
}

// markCompleted marks a task as completed
func (dc *dagExecutionContext) markCompleted(taskID string) {
    dc.mu.Lock()
    defer dc.mu.Unlock()
    dc.completed[taskID] = true
    log.Printf("DAG %s: Task %s marked completed", dc.dag.ID, taskID)
}

// scheduleDependentTasks checks and schedules any dependent tasks that are ready
// Returns an error if scheduling fails
func (dc *dagExecutionContext) scheduleDependentTasks(completedTaskID string) error {
    dc.mu.Lock()
    defer dc.mu.Unlock()
    
    // Create a waitgroup to track task scheduling
    var wg conc.WaitGroup
    
    // Get all tasks that depend on the completed task
    for _, dependentID := range dc.dag.Edges[completedTaskID] {
        // Skip if already scheduled
        if dc.scheduled[dependentID] {
            continue
        }
        
        // Check if all dependencies are completed
        allDependenciesMet := true
        for _, depID := range dc.dag.ReverseEdges[dependentID] {
            if !dc.completed[depID] {
                allDependenciesMet = false
                break
            }
        }
        
        // If all dependencies met, schedule the task
        if allDependenciesMet {
            dc.scheduled[dependentID] = true
            
            // Use copy of dependentID to avoid closure issues
            taskID := dependentID
            dependentTask := dc.dag.Tasks[taskID]
            
            // Use conc's WaitGroup to run scheduling operations concurrently
            wg.Go(func() {
                // Check for context cancellation
                if dc.ctx.Err() != nil {
                    log.Printf("DAG %s: Context canceled, not scheduling task %s", dc.dag.ID, taskID)
                    return
                }
                
                log.Printf("DAG %s: Scheduling dependent task %s", dc.dag.ID, taskID)
                
                // Create a wrapper task that publishes completion events
                wrapperTask := &taskWithPublication{
                    originalTask: dependentTask,
                    eventBus:     dc.executor.eventBus,
                    ctx:          dc.ctx,
                }
                
                // Schedule the task for execution
                scheduledTask := &Task{
                    ID:            dependentTask.ID,
                    Priority:      dependentTask.Priority,
                    ScheduledTime: time.Now(),
                    ExecutionType: OneTime,
                    Status:        Pending,
                    Job:           wrapperTask,
                    Dependencies:  dependentTask.Dependencies,
                    CreatedAt:     dependentTask.CreatedAt,
                    UpdatedAt:     time.Now(),
                }
                
                dc.executor.taskScheduler.Schedule(scheduledTask)
            })
        }
    }
    
    // Wait for all scheduling operations to complete
    wg.Wait()
    
    return nil
}

// taskWithPublication is a task wrapper that publishes events when the task completes
type taskWithPublication struct {
    originalTask *Task
    eventBus     EventBus
    ctx          context.Context // Context for cancellation support
}

// Run executes the task and publishes a completion event
func (tp *taskWithPublication) Run() error {
    // Check if context is cancelled before starting execution
    if tp.ctx != nil && tp.ctx.Err() != nil {
        log.Printf("Task %s: Execution cancelled due to context cancellation", tp.originalTask.ID)
        tp.originalTask.Status = Failed
        tp.originalTask.UpdatedAt = time.Now()
        return tp.ctx.Err()
    }

    // Execute the original task
    err := tp.originalTask.Job.Run()
    
    // Update task status based on execution result
    if err != nil {
        tp.originalTask.Status = Failed
    } else {
        tp.originalTask.Status = Completed
    }
    tp.originalTask.UpdatedAt = time.Now()
    
    // Publish task completion event
    tp.eventBus.Publish(NewEvent("task.completed", tp.originalTask))
    
    return err
}

// dagTaskWrapper is a special Runnable that manages DAG execution dependencies.
// It provides a context-aware task execution that respects dependency relationships.
type dagTaskWrapper struct {
    originalTask    *Task
    dag             *DAG
    completedTasks  map[string]bool
    completionMutex *sync.Mutex
    taskScheduler   *TaskScheduler
    ctx             context.Context
}

// Run implements the Runnable interface for DAG task execution.
func (dw *dagTaskWrapper) Run() error {
    // Check for context cancellation
    if dw.ctx != nil && dw.ctx.Err() != nil {
        return dw.ctx.Err()
    }
    
    // Check if all dependencies are met
    dw.completionMutex.Lock()
    allDependenciesMet := true
    for _, depID := range dw.dag.ReverseEdges[dw.originalTask.ID] {
        if !dw.completedTasks[depID] {
            allDependenciesMet = false
            break
        }
    }
    dw.completionMutex.Unlock()
    
    if !allDependenciesMet {
        // Reschedule with a delay to check again later
        if dw.taskScheduler != nil {
            log.Printf("Task %s: Dependencies not met, rescheduling with delay", dw.originalTask.ID)
            go func() {
                time.Sleep(500 * time.Millisecond)
                if dw.ctx == nil || dw.ctx.Err() == nil { // Check if context is still valid
                    dw.taskScheduler.Schedule(&Task{
                        ID:            dw.originalTask.ID,
                        Priority:      dw.originalTask.Priority,
                        ScheduledTime: time.Now().Add(500 * time.Millisecond),
                        ExecutionType: OneTime,
                        Status:        Pending,
                        Job:           dw,
                        Dependencies:  dw.originalTask.Dependencies,
                        CreatedAt:     dw.originalTask.CreatedAt,
                        UpdatedAt:     time.Now(),
                    })
                }
            }()
        }
        return fmt.Errorf("dependencies not met for task %s", dw.originalTask.ID)
    }
    
    // Execute the original task
    log.Printf("Task %s: All dependencies met, executing", dw.originalTask.ID)
    err := dw.originalTask.Job.Run()
    
    // Update task status
    if err != nil {
        dw.originalTask.Status = Failed
        log.Printf("Task %s failed: %v", dw.originalTask.ID, err)
    } else {
        dw.originalTask.Status = Completed
        log.Printf("Task %s completed successfully", dw.originalTask.ID)
    }
    dw.originalTask.UpdatedAt = time.Now()
    
    // Mark task as completed and schedule dependent tasks
    dw.completionMutex.Lock()
    dw.completedTasks[dw.originalTask.ID] = true
    
    // Check if any dependent tasks can now be scheduled
    scheduledTasks := 0
    for _, dependentID := range dw.dag.Edges[dw.originalTask.ID] {
        dependentTask := dw.dag.Tasks[dependentID]
        
        // Check if all dependencies of the dependent task are met
        allMet := true
        for _, depID := range dw.dag.ReverseEdges[dependentID] {
            if !dw.completedTasks[depID] {
                allMet = false
                break
            }
        }
        
        if allMet && dw.taskScheduler != nil {
            // All dependencies are met, schedule this task
            log.Printf("DAG: Task %s completed; Scheduling dependent task %s", 
                       dw.originalTask.ID, dependentID)
            
            // Create a new wrapper for this dependent task
            wrapper := &dagTaskWrapper{
                originalTask:    dependentTask,
                dag:             dw.dag,
                completedTasks:  dw.completedTasks,
                completionMutex: dw.completionMutex,
                taskScheduler:   dw.taskScheduler,
                ctx:             dw.ctx,
            }
            
            // Schedule it with the task scheduler
            dw.taskScheduler.Schedule(&Task{
                ID:            dependentTask.ID,
                Priority:      dependentTask.Priority,
                ScheduledTime: time.Now(),
                ExecutionType: OneTime,
                Status:        Pending,
                Job:           wrapper,
                Dependencies:  dependentTask.Dependencies,
                CreatedAt:     dependentTask.CreatedAt,
                UpdatedAt:     time.Now(),
            })
            
            scheduledTasks++
        }
    }
    
    if scheduledTasks > 0 {
        log.Printf("Task %s: Scheduled %d dependent tasks", dw.originalTask.ID, scheduledTasks)
    } else if len(dw.dag.Edges[dw.originalTask.ID]) == 0 {
        log.Printf("Task %s: No dependent tasks to schedule (leaf node)", dw.originalTask.ID)
    }
    
    dw.completionMutex.Unlock()
    
    return err
}

// ClearCache clears the DAG structure cache.
func (de *DAGExecutor) ClearCache() {
    de.mu.Lock()
    defer de.mu.Unlock()
    de.cache = make(map[string]*DAG)
    log.Println("DAG Executor cache cleared")
}

// RemoveFromCache removes a specific DAG from the cache.
func (de *DAGExecutor) RemoveFromCache(dagID string) {
    de.mu.Lock()
    defer de.mu.Unlock()
    delete(de.cache, dagID)
    log.Printf("DAG %s removed from cache", dagID)
}

// validateDAG performs validation checks on a DAG before execution.
// Returns an error if the DAG has issues that would prevent proper execution.
func validateDAG(dag *DAG) error {
    // Check for empty DAG
    if len(dag.Tasks) == 0 {
        return fmt.Errorf("cannot execute empty DAG with ID %s", dag.ID)
    }
    
    // Check for tasks with invalid or missing dependencies
    for taskID, task := range dag.Tasks {
        // Check if this task refers to dependencies that don't exist
        for _, depID := range task.Dependencies {
            if _, exists := dag.Tasks[depID]; !exists {
                return fmt.Errorf("task %s depends on non-existent task %s", taskID, depID)
            }
        }
    }
    
    // Check for isolated tasks (no edges connecting them to the rest of the DAG)
    connectedTasks := make(map[string]bool)
    
    // Helper function for DFS traversal
    var visit func(taskID string)
    visit = func(taskID string) {
        if connectedTasks[taskID] {
            return // Already visited
        }
        connectedTasks[taskID] = true
        
        // Visit all neighbors (both incoming and outgoing edges)
        for _, depID := range dag.ReverseEdges[taskID] {
            visit(depID)
        }
        for _, succID := range dag.Edges[taskID] {
            visit(succID)
        }
    }
    
    // Start DFS from the first task we find
    if len(dag.Tasks) > 0 {
        for id := range dag.Tasks {
            visit(id)
            break
        }
    }
    
    // Check if all tasks are connected
    if len(connectedTasks) != len(dag.Tasks) {
        return fmt.Errorf("DAG contains isolated tasks that are not connected to the main graph")
    }
    
    return nil
}

// calculateDAGTimeout calculates an appropriate timeout for DAG execution
// based on the complexity and number of tasks.
func calculateDAGTimeout(dag *DAG) time.Duration {
    // Base timeout of 30 seconds
    baseTimeout := 30 * time.Second
    
    // Add 5 seconds per task
    taskTimeout := time.Duration(len(dag.Tasks)) * 5 * time.Second
    
    // Calculate average estimated duration of tasks
    var totalEstimatedDuration time.Duration
    tasksWithEstimates := 0
    for _, task := range dag.Tasks {
        if task.EstimatedDuration > 0 {
            totalEstimatedDuration += task.EstimatedDuration
            tasksWithEstimates++
        }
    }
    
    // If we have estimated durations, use them to inform the timeout
    var estimateBasedTimeout time.Duration
    if tasksWithEstimates > 0 {
        avgDuration := totalEstimatedDuration / time.Duration(tasksWithEstimates)
        // Use 2x the average duration times the number of tasks, plus some buffer
        estimateBasedTimeout = time.Duration(len(dag.Tasks)) * avgDuration * 2
    }
    
    // Take the maximum of our calculated timeouts
    timeout := baseTimeout
    if taskTimeout > timeout {
        timeout = taskTimeout
    }
    if estimateBasedTimeout > timeout {
        timeout = estimateBasedTimeout
    }
    
    // Cap at a reasonable maximum (5 minutes)
    maxTimeout := 5 * time.Minute
    if timeout > maxTimeout {
        timeout = maxTimeout
    }
    
    return timeout
}

// handleCompletionEvents processes task completion events for a DAG.
func (de *DAGExecutor) handleCompletionEvents(ctx context.Context, dag *DAG, execContext *dagExecutionContext, completionChannel <-chan Event) error {
    for {
        select {
        case event, ok := <-completionChannel:
            if !ok {
                // Channel closed
                return nil
            }
            
            task, ok := event.Data.(*Task)
            if !ok {
                continue // Not a task event
            }
            
            // Check if this task is part of our DAG
            if _, exists := dag.Tasks[task.ID]; !exists {
                continue // Not a task in this DAG
            }
            
            // Mark the task as completed
            execContext.markCompleted(task.ID)
            
            // Check and schedule dependent tasks if possible
            if err := execContext.scheduleDependentTasks(task.ID); err != nil {
                log.Printf("Error scheduling dependent tasks: %v", err)
            }
            
            // Check if all tasks are completed
            execContext.mu.Lock()
            completedCount := len(execContext.completed)
            execContext.mu.Unlock()
            
            if completedCount == len(dag.Tasks) {
                log.Printf("DAG %s: All %d tasks completed", dag.ID, completedCount)
                de.eventBus.Publish(NewEvent("dag.completed", dag))
                return nil
            }
        
        case <-ctx.Done():
            // Context canceled
            return ctx.Err()
        }
    }
}

// identifySourceTasks returns a list of source tasks (tasks with no dependencies).
func (de *DAGExecutor) identifySourceTasks(dag *DAG, sortedTasks []*Task) []*Task {
    sourceTasks := make([]*Task, 0)
    
    for _, task := range sortedTasks {
        if len(dag.ReverseEdges[task.ID]) == 0 {
            sourceTasks = append(sourceTasks, task)
        }
    }
    
    log.Printf("DAG %s: Identified %d source tasks", dag.ID, len(sourceTasks))
    return sourceTasks
}

// scheduleSourceTasks schedules all source tasks of a DAG.
func (de *DAGExecutor) scheduleSourceTasks(ctx context.Context, dag *DAG, sourceTasks []*Task, execContext *dagExecutionContext) error {
    // Use conc package's pool for efficient parallel processing of source tasks
    p := pool.New().WithMaxGoroutines(10).WithContext(ctx)
    
    for _, task := range sourceTasks {
        // Use copy of the task variable to avoid closure issues
        currentTask := task
        
        // Mark as scheduled to prevent rescheduling
        execContext.mu.Lock()
        execContext.scheduled[currentTask.ID] = true
        execContext.mu.Unlock()
        
        // Use conc's pool to schedule source tasks in parallel
        p.Go(func(ctx context.Context) error {
            log.Printf("DAG %s: Scheduling source task %s", dag.ID, currentTask.ID)
            
            // Create a wrapper task that publishes completion events
            wrapperTask := &taskWithPublication{
                originalTask: currentTask,
                eventBus:     de.eventBus,
                ctx:          ctx,
            }
            
            // Schedule the task for execution
            scheduledTask := &Task{
                ID:            currentTask.ID,
                Priority:      currentTask.Priority,
                ScheduledTime: time.Now(),
                ExecutionType: OneTime,
                Status:        Pending,
                Job:           wrapperTask,
                Dependencies:  currentTask.Dependencies,
                CreatedAt:     currentTask.CreatedAt,
                UpdatedAt:     time.Now(),
            }
            
            de.taskScheduler.Schedule(scheduledTask)
            return nil
        })
    }
    
    // Wait for all source tasks to be scheduled
    if err := p.Wait(); err != nil {
        return fmt.Errorf("error scheduling source tasks: %w", err)
    }
    
    return nil
}

// executeDAGSimple executes a DAG in a simplified manner when event bus is not available.
// This is a fallback execution method that doesn't use event-based dependency tracking.
func (de *DAGExecutor) executeDAGSimple(dag *DAG, sortedTasks []*Task) error {
    if de.taskScheduler == nil {
        return fmt.Errorf("taskscheduler not configured for dagexecutor")
    }

    log.Printf("Using simplified execution for DAG %s with %d tasks", dag.ID, len(sortedTasks))
    
    // Create a shared state for tracking task completion
    completedTasks := make(map[string]bool)
    var completionMutex sync.Mutex
    
    // Schedule all tasks with the dagTaskWrapper to manage dependencies
    for _, task := range sortedTasks {
        if task == nil {
            continue
        }
        
        // Create a wrapper that manages dependencies
        wrapper := &dagTaskWrapper{
            originalTask:    task,
            dag:             dag,
            completedTasks:  completedTasks,
            completionMutex: &completionMutex,
            taskScheduler:   de.taskScheduler,
        }
        
        // Schedule task with the dependency-aware wrapper
        scheduledTask := &Task{
            ID:            task.ID,
            Priority:      task.Priority,
            ScheduledTime: calculateTaskStartTime(task, dag, completedTasks),
            ExecutionType: OneTime,
            Status:        Pending,
            Job:           wrapper,
            Dependencies:  task.Dependencies,
            CreatedAt:     task.CreatedAt,
            UpdatedAt:     time.Now(),
        }
        
        de.taskScheduler.Schedule(scheduledTask)
        log.Printf("Scheduled task %s with priority %d", task.ID, task.Priority)
    }
    
    return nil
}

// calculateTaskStartTime estimates when a task should start execution.
// Tasks with no dependencies start immediately, others are delayed based on their dependencies.
func calculateTaskStartTime(task *Task, dag *DAG, completedTasks map[string]bool) time.Time {
    // Start immediately if no dependencies or all dependencies are already done
    if len(dag.ReverseEdges[task.ID]) == 0 {
        return time.Now()
    }
    
    // Delay based on number of incomplete dependencies
    incompleteCount := 0
    for _, depID := range dag.ReverseEdges[task.ID] {
        if !completedTasks[depID] {
            incompleteCount++
        }
    }
    
    // Each incomplete dependency adds a delay
    delay := time.Duration(incompleteCount) * 500 * time.Millisecond
    return time.Now().Add(delay)
}

// DAGEvent types used for EventBus communication
const (
	DAGStarted        = "dag.started"
	DAGCompleted      = "dag.completed"
	DAGFailed         = "dag.failed"
	TaskScheduled     = "task.scheduled"
	TaskStarted       = "task.started"
	TaskCompleted     = "task.completed"
	TaskFailed        = "task.failed"
	CriticalPathFound = "dag.critical_path.found"
)

// SubscribeToEvents sets up subscriptions for all DAG-related events.
// Returns a cleanup function that unsubscribes from all channels.
func (de *DAGExecutor) SubscribeToEvents() (func(), error) {
	if de.eventBus == nil {
		return nil, fmt.Errorf("event bus not configured for DAG executor")
	}

	// Subscribe to task completion events
	taskCompletedCh, err := de.eventBus.Subscribe(TaskCompleted, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to task completion events: %w", err)
	}

	// Subscribe to task failure events
	taskFailedCh, err := de.eventBus.Subscribe(TaskFailed, 100)
	if err != nil {
		de.eventBus.Unsubscribe(TaskCompleted, taskCompletedCh)
		return nil, fmt.Errorf("failed to subscribe to task failure events: %w", err)
	}

	// Set up background processing of events
	go de.processTaskCompletionEvents(taskCompletedCh)
	go de.processTaskFailureEvents(taskFailedCh)
	
	// Return cleanup function
	cleanup := func() {
		de.eventBus.Unsubscribe(TaskCompleted, taskCompletedCh)
		de.eventBus.Unsubscribe(TaskFailed, taskFailedCh)
		log.Println("Unsubscribed from DAG executor events")
	}
	
	return cleanup, nil
}

// processTaskCompletionEvents handles task completion events in the background
func (de *DAGExecutor) processTaskCompletionEvents(ch <-chan Event) {
	for event := range ch {
		task, ok := event.Data.(*Task)
		if !ok {
			log.Printf("Warning: Expected Task in completion event but got %T", event.Data)
			continue
		}
		
		log.Printf("Task %s completed at %v", task.ID, event.Timestamp)
		
		// Find which DAGs contain this task
		de.mu.RLock()
		for dagID, dag := range de.cache {
			if _, exists := dag.Tasks[task.ID]; exists {
				// This task belongs to this DAG
				// Check if all tasks in the DAG are completed
				allCompleted := true
				for taskID, dagTask := range dag.Tasks {
					if dagTask.Status != Completed && taskID != task.ID {
						allCompleted = false
						break
					}
				}
				
				if allCompleted {
					// Publish DAG completion event
					de.eventBus.Publish(NewEvent(DAGCompleted, dag))
					log.Printf("DAG %s execution completed", dagID)
				}
			}
		}
		de.mu.RUnlock()
	}
}

// processTaskFailureEvents handles task failure events in the background
func (de *DAGExecutor) processTaskFailureEvents(ch <-chan Event) {
	for event := range ch {
		task, ok := event.Data.(*Task)
		if !ok {
			log.Printf("Warning: Expected Task in failure event but got %T", event.Data)
			continue
		}
		
		log.Printf("Task %s failed at %v: %v", task.ID, event.Timestamp, task.Error)
		
		// Find which DAGs contain this task
		de.mu.RLock()
		for dagID, dag := range de.cache {
			if _, exists := dag.Tasks[task.ID]; exists {
				// This task belongs to this DAG
				// If the task is critical, fail the entire DAG
				if task.Critical {
					de.eventBus.Publish(NewEvent(DAGFailed, dag))
					log.Printf("DAG %s execution failed due to critical task %s failure", dagID, task.ID)
				}
			}
		}
		de.mu.RUnlock()
	}
}

// taskWithEventBus is an enhanced task wrapper that publishes events for all 
// task lifecycle stages including scheduling, starting, completion, and failure
type taskWithEventBus struct {
    originalTask *Task
    eventBus     EventBus
    ctx          context.Context
    dagID        string  // Tracks which DAG this task belongs to
}

// Run implements the Runnable interface with comprehensive event publishing
func (te *taskWithEventBus) Run() error {
    taskID := te.originalTask.ID
    
    // 1. Publish task started event
    te.eventBus.Publish(NewEvent(TaskStarted, te.originalTask))
    
    // Check for context cancellation before executing
    if te.ctx != nil && te.ctx.Err() != nil {
        log.Printf("Task %s: Execution cancelled", taskID)
        te.originalTask.Status = Failed
        te.originalTask.Error = te.ctx.Err().Error()
        te.originalTask.UpdatedAt = time.Now()
        
        // Publish task failed event
        te.eventBus.Publish(NewEvent(TaskFailed, te.originalTask))
        return te.ctx.Err()
    }
    
    // 2. Execute the task
    startTime := time.Now()
    err := te.originalTask.Job.Run()
    executionTime := time.Since(startTime)
    
    // Update task status and metadata
    te.originalTask.ExecutionTime = executionTime
    te.originalTask.UpdatedAt = time.Now()
    
    // 3. Publish appropriate completion/failure event
    if err != nil {
        te.originalTask.Status = Failed
        te.originalTask.Error = err.Error()
        te.eventBus.Publish(NewEvent(TaskFailed, te.originalTask))
        log.Printf("Task %s failed after %v: %v", taskID, executionTime, err)
    } else {
        te.originalTask.Status = Completed
        te.eventBus.Publish(NewEvent(TaskCompleted, te.originalTask))
        log.Printf("Task %s completed successfully in %v", taskID, executionTime)
    }
    
    return err
}

// PublishDAGStartedEvent publishes an event indicating that a DAG has started execution.
func (de *DAGExecutor) PublishDAGStartedEvent(dag *DAG) {
	if de.eventBus == nil {
		return
	}
	
	de.eventBus.Publish(NewEvent(DAGStarted, map[string]interface{}{
		"dag":       dag,
		"startTime": time.Now(),
	}))
	log.Printf("DAG %s: Execution started", dag.ID)
}

// PublishDAGEvent publishes a general DAG-related event with additional data.
func (de *DAGExecutor) PublishDAGEvent(dag *DAG, eventType string, data interface{}) {
	if de.eventBus == nil {
		return
	}
	
	de.eventBus.Publish(NewEvent(eventType, data))
	log.Printf("DAG %s: Event %s published", dag.ID, eventType)
}

// contains checks if a string is present in a slice.
func contains(slice []string, str string) bool {
	for _, v := range slice {
		if v == str {
			return true
		}
	}
	return false
}
