package workerpool

import (
	"context"

	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"log"

	"github.com/ZanzyTHEbar/dragonscale/internal/domain"
	"github.com/sourcegraph/conc/panics"
	"github.com/sourcegraph/conc/pool"
)

const (
	defaultQueueSize       = 100
	defaultMonitorInterval = 10 * time.Second
	defaultCooldownPeriod  = 1 * time.Minute // Cooldown after scaling down
	minWorkers             = 1
	cpuLowThreshold        = 0.5 // Threshold to consider scaling down
)

// WorkerPool manages a pool of goroutines to execute Runnable tasks.
// This implementation uses the conc package for improved concurrency management.
type WorkerPool struct {
	minWorkers      int
	maxWorkers      int
	currentWorkers  int32                // Using atomic for thread-safe counter
	taskQueue       chan domain.Runnable // Channel for task queue
	workerPool      *pool.ContextPool    // Conc package's Pool for worker management
	ctx             context.Context      // Context for cancellation
	cancel          context.CancelFunc   // Function to cancel the context
	monitor         *LoadMonitor         // System load monitor
	monitorInterval time.Duration        // How often to check load
	cooldownUntil   time.Time            // Time until scaling down is allowed again
	monitorRunning  bool                 // Flag to track if monitor is active
	panicHandler    *panics.Catcher      // Conc's panic handler

	mu sync.Mutex // Protects cooldownUntil, monitorRunning
}

// NewWorkerPool creates a new WorkerPool with adaptive sizing.
func NewWorkerPool(initialWorkers, minPoolWorkers, maxPoolWorkers int, queueSize int, monitor *LoadMonitor) (*WorkerPool, error) {
	// Parameter validation and default setting
	if minPoolWorkers <= 0 {
		minPoolWorkers = minWorkers
	}
	if maxPoolWorkers <= 0 {
		maxPoolWorkers = runtime.NumCPU() * 4
	}
	if initialWorkers <= 0 {
		initialWorkers = runtime.NumCPU()
	}
	if initialWorkers < minPoolWorkers {
		initialWorkers = minPoolWorkers
	}
	if initialWorkers > maxPoolWorkers {
		initialWorkers = maxPoolWorkers
	}
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	if monitor == nil {
		monitor = NewLoadMonitor(0.8, 0.9)
	}

	// Create context with cancellation for pool management
	ctx, cancel := context.WithCancel(context.Background())

	// Create the worker pool
	wp := &WorkerPool{
		minWorkers:      minPoolWorkers,
		maxWorkers:      maxPoolWorkers,
		currentWorkers:  0,
		taskQueue:       make(chan domain.Runnable, queueSize),
		ctx:             ctx,
		cancel:          cancel,
		monitor:         monitor,
		monitorInterval: defaultMonitorInterval,
		monitorRunning:  false,
		panicHandler:    &panics.Catcher{},
	}

	log.Printf("Initializing worker pool: Min=%d, Max=%d, Initial=%d, QueueSize=%d",
		minPoolWorkers, maxPoolWorkers, initialWorkers, queueSize)

	// Initialize the conc pool with the initial worker count
	wp.workerPool = pool.New().
		WithContext(ctx).
		WithMaxGoroutines(initialWorkers)

	// Set the current worker count
	atomic.StoreInt32(&wp.currentWorkers, int32(initialWorkers))

	// Start the worker controller goroutine to process tasks
	go wp.processTaskQueue()

	return wp, nil
}

// processTaskQueue continuously processes tasks from the queue
func (wp *WorkerPool) processTaskQueue() {
	// Process tasks until the context is canceled
	for {
		select {
		case <-wp.ctx.Done():
			// Context canceled, exit the processing loop
			log.Println("Worker pool shutting down, task queue processor exiting")
			return
		case task, ok := <-wp.taskQueue:
			if !ok {
				// Channel closed
				log.Println("Task queue channel closed, processor exiting")
				return
			}

			// TODO: Add context support into runnable
			// Execute the task with the conc pool
			wp.workerPool.Go(func(ctx context.Context) error {
				// Check if the task is nil
				if task == nil {
					log.Println("Received nil task, skipping")
					return nil
				}
				// Use the panic handler to recover from panics in user tasks
				wp.panicHandler.Try(func() {
					if err := task.Run(); err != nil {
						log.Printf("Task execution error: %v", err)
					}
				})
				return nil
			})
		}
	}
}

// Add submits a task to the worker pool queue.
func (wp *WorkerPool) Add(job domain.Runnable) {
	if job == nil {
		return
	}

	select {
	case wp.taskQueue <- job:
		// Task added to queue, start the load monitor if not already running
		wp.startMonitorIfNeeded()
	case <-wp.ctx.Done():
		// Worker pool is shutting down, don't accept new tasks
		log.Println("Worker pool is shutting down, rejecting task")
	}
}

// TryAdd attempts to submit a task without blocking.
func (wp *WorkerPool) TryAdd(job domain.Runnable) bool {
	if job == nil {
		return false
	}

	select {
	case wp.taskQueue <- job:
		wp.startMonitorIfNeeded()
		return true
	case <-wp.ctx.Done():
		log.Println("Worker pool stopped, task not added.")
		return false
	default:
		return false // Queue full
	}
}

// startMonitorIfNeeded starts the load monitor if it's not already running
func (wp *WorkerPool) startMonitorIfNeeded() {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if !wp.monitorRunning {
		wp.monitorRunning = true
		go wp.monitorLoad()
	}
}

// monitorLoad periodically checks system load and adjusts pool size
func (wp *WorkerPool) monitorLoad() {
	ticker := time.NewTicker(wp.monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wp.adjustPoolSize()
		case <-wp.ctx.Done():
			wp.mu.Lock()
			wp.monitorRunning = false
			wp.mu.Unlock()
			return
		}
	}
}

// adjustPoolSize changes the number of workers based on system load
func (wp *WorkerPool) adjustPoolSize() {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	// Check if we're in a cooldown period after scaling down
	if time.Now().Before(wp.cooldownUntil) {
		return
	}

	currentWorkers := int(atomic.LoadInt32(&wp.currentWorkers))

	// Get current system load metrics
	cpuLoad := wp.monitor.GetCPUUsage()
	queueLoad := float64(len(wp.taskQueue)) / float64(cap(wp.taskQueue))

	// Determine if we should scale up or down
	if cpuLoad > wp.monitor.GetCPUThreshold() || queueLoad > 0.8 {
		// High load, scale up if below max
		if currentWorkers < wp.maxWorkers {
			newWorkers := min(currentWorkers+2, wp.maxWorkers)

			// Use the conc pool's WithMaxGoroutines to update the pool size
			wp.workerPool.WithMaxGoroutines(newWorkers)

			// Update the worker count
			atomic.StoreInt32(&wp.currentWorkers, int32(newWorkers))

			log.Printf("Scaling up: %d -> %d workers (CPU: %.2f, Queue: %.2f)",
				currentWorkers, newWorkers, cpuLoad, queueLoad)
		}
	} else if (cpuLoad < cpuLowThreshold && queueLoad < 0.2) && currentWorkers > wp.minWorkers {
		// Low load, scale down if above min
		newWorkers := max(currentWorkers-1, wp.minWorkers)

		// Update the pool size
		wp.workerPool.WithMaxGoroutines(newWorkers)

		// Update the worker count
		atomic.StoreInt32(&wp.currentWorkers, int32(newWorkers))

		// Set cooldown period after scaling down
		wp.cooldownUntil = time.Now().Add(defaultCooldownPeriod)

		log.Printf("Scaling down: %d -> %d workers (CPU: %.2f, Queue: %.2f)",
			currentWorkers, newWorkers, cpuLoad, queueLoad)
	}
}

// Stop gracefully shuts down the worker pool and waits for all workers to finish.
func (wp *WorkerPool) Stop() {
	// Cancel the context to signal all workers to stop
	wp.cancel()

	// Wait for all tasks to complete
	wp.workerPool.Wait()

	// Close the task queue
	close(wp.taskQueue)

	log.Println("Worker pool stopped")
}

// Wait blocks until all worker pool goroutines exit.
func (wp *WorkerPool) Wait() {
	wp.workerPool.Wait()
}

// GetWorkerCount returns the current number of workers.
func (wp *WorkerPool) GetWorkerCount() int {
	return int(atomic.LoadInt32(&wp.currentWorkers))
}

// GetQueueDepth returns the number of tasks currently in the queue.
func (wp *WorkerPool) GetQueueDepth() int {
	return len(wp.taskQueue)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
