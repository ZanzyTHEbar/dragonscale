package domain

import (
	"container/heap"
	"context"
	"log"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// TaskHeap implements heap.Interface and holds Tasks.
// It uses pointers to Tasks (*Task) to avoid copying large structs.
type TaskHeap []*Task

// Len is the number of elements in the collection.
func (th TaskHeap) Len() int { return len(th) }

// Less reports whether the element with index i
// must sort before the element with index j.
// We use the Less method defined on the Task struct itself.
func (th TaskHeap) Less(i, j int) bool {
	return th[i].Less(th[j])
}

// Swap swaps the elements with indexes i and j.
func (th TaskHeap) Swap(i, j int) {
	th[i], th[j] = th[j], th[i]
	// If tasks had an index field for heap management, update it here.
	// th[i].index = i
	// th[j].index = j
}

// Push adds x as element Len().
// x is expected to be of type *Task.
func (th *TaskHeap) Push(x interface{}) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*th = append(*th, x.(*Task))
}

// Pop removes and returns element Len() - 1.
func (th *TaskHeap) Pop() interface{} {
	old := *th
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*th = old[0 : n-1]
	return item
}

// Peek returns the next task to be executed without removing it from the heap.
// Returns nil if the heap is empty.
func (th TaskHeap) Peek() *Task {
    if len(th) == 0 {
        return nil
    }
    return th[0]
}


// --- TaskScheduler Implementation (Initial) ---

// TaskScheduler manages the scheduling and dispatching of tasks.
type TaskScheduler struct {
	mutex     sync.Mutex
	cond      *sync.Cond      // Condition variable to signal new tasks or time changes
	taskQueue *TaskHeap       // Priority queue for tasks
	stopChan  chan struct{}   // Channel to signal the scheduler loop to stop
	running   bool            // Flag to indicate if the scheduler is running

    // For improved concurrency management using errgroup
    cancelFunc context.CancelFunc // Function to cancel the context
    eg         *errgroup.Group    // errgroup for managing goroutines

    // WorkerPool integration - needs a reference to the pool
    pool      interface { // Define Add method for dispatching runnable jobs
        Add(job Runnable)
    }
}

// NewTaskScheduler creates a new TaskScheduler.
func NewTaskScheduler(pool interface{ Add(Runnable) }) *TaskScheduler {
	th := make(TaskHeap, 0)
	ts := &TaskScheduler{
		taskQueue: &th,
		stopChan:  make(chan struct{}),
		pool:      pool,
	}
	ts.cond = sync.NewCond(&ts.mutex)
	heap.Init(ts.taskQueue)
	return ts
}

// Schedule adds a new task to the scheduler.
func (ts *TaskScheduler) Schedule(task *Task) {
	ts.mutex.Lock()
	defer ts.mutex.Unlock()

	if (!ts.running) {
		// Optionally return an error or log if scheduler is stopped
        // log.Println("Scheduler stopped, not scheduling task:", task.ID)
		return
	}

	task.Status = Pending
	task.UpdatedAt = time.Now()
	heap.Push(ts.taskQueue, task)
	ts.cond.Signal() // Signal the scheduler loop
}

// Start begins the scheduler's main loop in a separate goroutine.
func (ts *TaskScheduler) Start() {
	ts.mutex.Lock()
	if ts.running {
		ts.mutex.Unlock()
		return // Already running
	}
	ts.running = true
	ts.stopChan = make(chan struct{}) // Recreate stopChan if previously stopped
	ts.mutex.Unlock()

	// Create a context that can be canceled when stopping the scheduler
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancelFunc = cancel

	// Use errgroup to manage the loop goroutine with error handling
	ts.eg, ctx = errgroup.WithContext(ctx)
	ts.eg.Go(func() error {
		return ts.runLoop(ctx)
	})
}

// Stop signals the scheduler to stop processing tasks and waits for it to finish.
func (ts *TaskScheduler) Stop() {
	ts.mutex.Lock()
	if !ts.running {
		ts.mutex.Unlock()
		return // Already stopped
	}
	ts.running = false
	close(ts.stopChan) // Close the stop channel
	
	// Cancel the context to signal the runLoop to stop
	if ts.cancelFunc != nil {
		ts.cancelFunc()
	}
	
	ts.cond.Broadcast() // Wake up the loop if it's waiting
	ts.mutex.Unlock()

	// Wait for the runLoop goroutine to exit
	if ts.eg != nil {
		if err := ts.eg.Wait(); err != nil {
			log.Printf("Error in scheduler loop: %v", err)
		}
	}
}


// runLoop is the main scheduler loop.
func (ts *TaskScheduler) runLoop(ctx context.Context) error {
	var timer *time.Timer // Use a single timer to avoid garbage
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		ts.mutex.Lock()
		
		// Check if we should exit the loop
		if !ts.running {
			ts.mutex.Unlock()
			return nil
		}
		
		// Get next task if available
		nextTask := ts.peekTask()
		
		if nextTask == nil {
			// No tasks in the queue, wait for signal or stop
			// Use cond.Wait with a condition to handle context cancellation
			waitCh := make(chan struct{})
			go func() {
				ts.cond.Wait()
				close(waitCh)
			}()
			
			ts.mutex.Unlock()
			
			// Wait for either condition signal or context cancellation
			select {
			case <-waitCh:
				// Condition was signaled, continue the loop
				continue
			case <-ctx.Done():
				// Context was canceled, exit the loop
				return ctx.Err()
			}
		}
		
		// Calculate wait time until next task
		now := time.Now()
		waitTime := nextTask.ScheduledTime.Sub(now)
		
		if waitTime <= 0 {
			// Task is due - pop it from queue and dispatch
			task := heap.Pop(ts.taskQueue).(*Task)
			task.Status = Running
			task.UpdatedAt = now
			
			// Release the lock before dispatching to worker pool
			ts.mutex.Unlock()
			
			// Dispatch to worker pool
			ts.dispatchTask(task)
			
			// For FixedRate tasks, reschedule immediately
			// Note: FixedDelay tasks are rescheduled in the taskWrapper after completion
			if task.ExecutionType == FixedRate {
				ts.rescheduleTask(task)
			}
			
			continue
		}
		
		// Set timer for next task
		if timer == nil {
			timer = time.NewTimer(waitTime)
		} else {
			timer.Reset(waitTime)
		}
		ts.mutex.Unlock()
		
		// Wait for timer, new task signal, or stop signal
		select {
		case <-timer.C:
			// Timer expired, loop back to process the task
			continue
		case <-ts.stopChan:
			// Stop signal received
			return nil
		case <-ctx.Done():
			// Context canceled
			return ctx.Err()
		}
	}
}

// peekTask returns the next task without removing it from the queue (already locked)
func (ts *TaskScheduler) peekTask() *Task {
    if ts.taskQueue.Len() == 0 {
        return nil
    }
    return (*ts.taskQueue)[0]
}

// dispatchTask sends the task to the worker pool for execution
func (ts *TaskScheduler) dispatchTask(task *Task) {
    // Create a wrapper that will handle task completion and rescheduling
    wrapper := &taskWrapper{
        task:      task,
        scheduler: ts,
    }
    
    // Send to worker pool
    ts.pool.Add(wrapper)
}

// rescheduleTask reschedules a recurring task
func (ts *TaskScheduler) rescheduleTask(task *Task) {
    // Only reschedule if it's a recurring task
    if task.ExecutionType != FixedRate && task.ExecutionType != FixedDelay {
        return
    }
    
    // Clone the task with updated schedule time
    newTask := &Task{
        ID:             task.ID,
        Priority:       task.Priority,
        ExecutionType:  task.ExecutionType,
        Job:            task.Job,
        Dependencies:   task.Dependencies,
        Interval:       task.Interval,
        CreatedAt:      task.CreatedAt,
        UpdatedAt:      time.Now(),
    }
    
    // FixedRate tasks run on a fixed schedule regardless of execution time
    if task.ExecutionType == FixedRate {
        newTask.ScheduledTime = task.ScheduledTime.Add(task.Interval)
    } else {
        // FixedDelay tasks run after a delay from completion
        // Will be rescheduled by the wrapper when the task completes
        return
    }
    
    // Schedule the task
    ts.Schedule(newTask)
}

// taskWrapper wraps a task with completion handling
type taskWrapper struct {
    task      *Task
    scheduler *TaskScheduler
}

// Run executes the task and handles completion
func (tw *taskWrapper) Run() error {
    // Execute the task
    err := tw.task.Job.Run()
    
    // Update task status based on execution result
    tw.scheduler.mutex.Lock()
    if err != nil {
        tw.task.Status = Failed
    } else {
        tw.task.Status = Completed
    }
    tw.task.UpdatedAt = time.Now()
    tw.scheduler.mutex.Unlock()
    
    // For FixedDelay tasks, reschedule after completion
    if tw.task.ExecutionType == FixedDelay {
        newTask := &Task{
            ID:             tw.task.ID,
            Priority:       tw.task.Priority,
            ScheduledTime:  time.Now().Add(tw.task.Interval),
            ExecutionType:  tw.task.ExecutionType,
            Job:            tw.task.Job,
            Dependencies:   tw.task.Dependencies,
            Interval:       tw.task.Interval,
            CreatedAt:      tw.task.CreatedAt,
            UpdatedAt:      time.Now(),
        }
        tw.scheduler.Schedule(newTask)
    }
    
    return err
}

//! Deprecated
// handleRecurringTask checks if a completed task is recurring and reschedules it.
// This function is no longer needed as rescheduling is handled by the taskWrapper.
// It's kept here for reference but should be removed in a future cleanup.
func handleRecurringTask(ts *TaskScheduler, task *Task) {
	// Deprecated - Rescheduling is now handled by taskWrapper.Run()
	// This improves accuracy for FixedDelay tasks by ensuring they 
	// are rescheduled based on actual completion time.
	now := time.Now()
	reschedule := false
	switch task.ExecutionType {
	case FixedRate:
		// Schedule based on the original scheduled time + interval
        nextScheduledTime := task.ScheduledTime.Add(task.Interval)
        // Prevent drifting if execution took longer than interval
        if nextScheduledTime.Before(now) {
             // Option 1: Schedule immediately for next cycle relative to 'now'
             // nextScheduledTime = now.Add(task.Interval)
             // Option 2: Schedule immediately for the 'missed' slot
             nextScheduledTime = now
             // Option 3 (current): Schedule for the calculated time, even if past
             // This maintains the rate cadence but might run immediately if overdue.
        }
        task.ScheduledTime = nextScheduledTime
		reschedule = true
	case FixedDelay:
		// Schedule based on approximate completion time ('now') + interval
		task.ScheduledTime = now.Add(task.Interval)
		reschedule = true
	case OneTime:
        // TODO: Need feedback from Runnable/Worker for actual status (Completed/Failed)
		task.Status = Completed // Placeholder status
	}

	if reschedule {
		task.Status = Pending
		heap.Push(ts.taskQueue, task)
		// Signal needed ONLY if the loop might be waiting indefinitely (empty queue). 
        // If we just pushed a task, the queue is not empty, so the loop
        // will either process it next or set a timer. A signal here might be redundant
        // but ensures the loop wakes up if it was in the indefinite wait state before.
        // Let's keep it for safety.
        ts.cond.Signal()
	}
}
