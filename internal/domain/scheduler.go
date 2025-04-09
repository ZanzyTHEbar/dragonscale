package domain

import (
	"container/heap"
	"sync"
	"time"

        // Added for potential future use in Scheduler logic or related components
        // "golang.org/x/sync/errgroup"
        // "github.com/sourcegraph/conc"
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
	wg        sync.WaitGroup  // To wait for the scheduler loop to finish
	running   bool            // Flag to indicate if the scheduler is running

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

	if !ts.running {
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

	ts.wg.Add(1)
	go ts.runLoop()
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
	ts.cond.Broadcast() // Wake up the loop if it's waiting
	ts.mutex.Unlock()

	ts.wg.Wait() // Wait for the runLoop goroutine to exit
}


// runLoop is the main scheduler loop.
func (ts *TaskScheduler) runLoop() {
	defer ts.wg.Done()
    var timer *time.Timer // Use a single timer to avoid garbage
    defer func() {
        if timer != nil {
            timer.Stop()
        }
    }()

	for {
		ts.mutex.Lock()

        // Inner loop to wait for the right condition (task ready or stop signal)
        for {
            if !ts.running {
                ts.mutex.Unlock()
                return // Exit if stopped
            }

            nextTask := ts.taskQueue.Peek()
            var waitDuration time.Duration

            if nextTask == nil {
                waitDuration = -1 // Wait indefinitely if queue is empty
            } else {
                waitDuration = time.Until(nextTask.ScheduledTime)
            }

            if waitDuration <= 0 {
                 // Task is ready or overdue (or queue was empty and now isn't)
                 if nextTask != nil { // Ensure there's a task to process
                    break // Exit wait loop to process the task
                 } else {
                    // Queue is still empty, continue waiting indefinitely
                    waitDuration = -1
                 }
            }

            // Stop existing timer if it's running
            if timer != nil {
                timer.Stop()
                timer = nil
            }

            if waitDuration > 0 {
                // Start a timer for the duration until the next task
                 timer = time.NewTimer(waitDuration)
            }

            // Unlock mutex while waiting
            ts.mutex.Unlock()

            select {
            case <-ts.stopChan:
                // Ensure timer is stopped if we exit due to stop signal
                if timer != nil {
                    timer.Stop()
                }
                return // Exit runLoop

            case <-ts.cond.Signal: // Wait for signal (new task added or explicit wake-up)
                 // Re-acquire lock and loop again to check conditions
                 if timer != nil {
                      timer.Stop()
                      timer = nil
                 }
                 ts.mutex.Lock() // Lock needed for next loop iteration
                 continue // Re-evaluate queue and wait duration

            case <-func() <-chan time.Time { // Handle nil timer case for select
                 if timer == nil {
                     return nil // Return nil channel if no timer (wait indefinitely)
                 }
                 return timer.C // Return timer channel
                }():
                 // Timer fired, task should be ready. Re-acquire lock and loop.
                 ts.mutex.Lock() // Lock needed for next loop iteration
                 timer = nil // Timer is drained
                 continue // Re-evaluate queue and wait duration
            }
        } // End of inner wait loop

        // --- Task Processing --- (Mutex is held here)

        if !ts.running {
            ts.mutex.Unlock()
            return // Check again after acquiring lock
        }

        // Pop the ready task
        taskToRun := heap.Pop(ts.taskQueue).(*Task)

        // Update status before dispatching (while holding lock)
        taskToRun.Status = Running
        taskToRun.UpdatedAt = time.Now()
        job := taskToRun.Job // Extract job

        ts.mutex.Unlock() // *** Unlock before dispatching to worker pool ***

        // Dispatch the job to the worker pool
        // This is a blocking call if the pool's queue is full
        // Consider making Add non-blocking or handle potential block
        ts.pool.Add(job) // TODO: Handle error from Run() later

        // --- Handle Recurring Task (Re-acquire lock) ---
        ts.mutex.Lock()
        if ts.running { // Check running status again before rescheduling
             handleRecurringTask(ts, taskToRun)
        }
        ts.mutex.Unlock() // Unlock after handling recurring task
	}
}


// handleRecurringTask checks if a completed task is recurring and reschedules it.
// Assumes lock is held by the caller.
// NOTE: Still relies on approximation for FixedDelay completion time.
func handleRecurringTask(ts *TaskScheduler, task *Task) {
	now := time.Now()
	task.UpdatedAt = now // Mark update time (completion conceptually)

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
