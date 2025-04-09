package workerpool

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"log" // For logging errors or pool adjustments

	"github.com/ZanzyTHEbar/dragonscale/internal/domain" // Import domain for Runnable
)

const (
	defaultQueueSize       = 100
	defaultMonitorInterval = 10 * time.Second
	defaultCooldownPeriod  = 1 * time.Minute // Cooldown after scaling down
	minWorkers             = 1
	cpuLowThreshold        = 0.5 // Threshold to consider scaling down
)

// WorkerPool manages a pool of goroutines to execute Runnable tasks.
type WorkerPool struct {
	minWorkers      int
	maxWorkers      int
	currentWorkers  int                  // Current number of active workers
	workerQueue     chan domain.Runnable // Channel to send tasks to workers
	stopChan        chan struct{}        // Channel to signal workers and monitor to stop
	wg              sync.WaitGroup       // To wait for workers to finish
	monitor         *LoadMonitor         // System load monitor
	monitorInterval time.Duration        // How often to check load
	cooldownUntil   time.Time            // Time until scaling down is allowed again
	monitorRunning  bool                 // Flag to track if monitor is active

	mu sync.Mutex // Protects currentWorkers, cooldownUntil, monitorRunning
}

// NewWorkerPool creates a new WorkerPool with adaptive sizing.
func NewWorkerPool(initialWorkers, minPoolWorkers, maxPoolWorkers int, queueSize int, monitor *LoadMonitor) (*WorkerPool, error) {
	// Parameter validation and default setting (as before)
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

	pool := &WorkerPool{
		minWorkers:      minPoolWorkers,
		maxWorkers:      maxPoolWorkers,
		currentWorkers:  0,
		workerQueue:     make(chan domain.Runnable, queueSize),
		stopChan:        make(chan struct{}),
		monitor:         monitor,
		monitorInterval: defaultMonitorInterval,
		monitorRunning:  false,
	}

	log.Printf("Initializing worker pool: Min=%d, Max=%d, Initial=%d, QueueSize=%d", minPoolWorkers, maxPoolWorkers, initialWorkers, queueSize)

	pool.mu.Lock()
	pool.stopChan = make(chan struct{}) // Ensure stopChan is fresh
	for i := 0; i < initialWorkers; i++ {
		pool.startWorker() // Starts workers and increments wg
	}
	pool.mu.Unlock()

	return pool, nil
}

// startWorker launches a new worker goroutine.
// Assumes mu lock is held by the caller.
func (wp *WorkerPool) startWorker() {
	wp.currentWorkers++
	wp.wg.Add(1)
	go wp.worker(wp.currentWorkers)
}

// Add submits a task to the worker pool queue.
func (wp *WorkerPool) Add(job domain.Runnable) {
	if job == nil {
		return
	}
	select {
	case wp.workerQueue <- job:
	case <-wp.stopChan:
		log.Println("Worker pool stopped, task not added.")
	}
}

// TryAdd attempts to submit a task without blocking.
func (wp *WorkerPool) TryAdd(job domain.Runnable) bool {
	if job == nil {
		return false
	}
	select {
	case wp.workerQueue <- job:
		return true
	case <-wp.stopChan:
		log.Println("Worker pool stopped, task not added.")
		return false
	default:
		return false // Queue full
	}
}

// worker is the execution loop for a single worker goroutine.
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		select {
		case job, ok := <-wp.workerQueue:
			if !ok {
				// Channel closed, likely during Stop(). Exit gracefully.
				return
			}
			if err := job.Run(); err != nil {
				log.Printf("Worker %d error running job: %v", id, err)
				// TODO: Error reporting / Task status update
			}
			// TODO: Signal task completion for FixedDelay

		case <-wp.stopChan:
			return // Stop signal received
		}
	}
}

// Start implements the ports.TaskExecutor interface.
// It starts the pool's monitor if it's not already running.
func (wp *WorkerPool) Start() error {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.monitorRunning {
		return fmt.Errorf("worker pool monitor already started")
	}

	// Ensure stopChan is valid if pool was stopped and restarted (though typical use is new pool)
	select {
	case <-wp.stopChan:
		// If channel is closed, recreate it
		wp.stopChan = make(chan struct{})
	default:
		// Channel is open or nil (first start), proceed
	}

	go wp.adjustSizeLoop() // Run monitor in background
	wp.monitorRunning = true
	log.Println("Worker pool monitor started.")
	return nil
}

// adjustSizeLoop periodically checks system load and adjusts the worker count.
func (wp *WorkerPool) adjustSizeLoop() {
	wp.wg.Add(1)       // Add monitor to WaitGroup
	defer wp.wg.Done() // Ensure monitor Done is called on exit

	ticker := time.NewTicker(wp.monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wp.adjustSize()
		case <-wp.stopChan:
			log.Println("Worker pool monitor stopping.")
			wp.mu.Lock()
			wp.monitorRunning = false // Mark monitor as stopped
			wp.mu.Unlock()
			return
		}
	}
}

// adjustSize performs the logic for scaling the worker pool up or down.
func (wp *WorkerPool) adjustSize() {
	// Logic remains the same as before
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if !time.Now().After(wp.cooldownUntil) {
		return
	}

	cpuUsage := wp.monitor.GetCPUUsage()
	memUsage := wp.monitor.GetMemUsage()
	queueLength := len(wp.workerQueue)
	capacity := cap(wp.workerQueue)
	queueUsage := 0.0
	if capacity > 0 {
		queueUsage = float64(queueLength) / float64(capacity)
	}

	// Scale Up
	scaleUpThresholdMet := cpuUsage > wp.monitor.GetCPUThreshold() || memUsage > wp.monitor.GetMemThreshold()
	scaleUpNeeded := scaleUpThresholdMet || (queueUsage > 0.75)
	if scaleUpNeeded && wp.currentWorkers < wp.maxWorkers {
		log.Printf("Scaling up: Load(CPU:%.2f,Mem:%.2f,Queue:%.2f), Workers:%d->%d", cpuUsage, memUsage, queueUsage, wp.currentWorkers, wp.currentWorkers+1)
		wp.startWorker()
		return
	}

	// Scale Down
	scaleDownThresholdMet := cpuUsage < cpuLowThreshold && queueUsage < 0.1
	if scaleDownThresholdMet && wp.currentWorkers > wp.minWorkers {
		log.Printf("Scaling down check: Conditions met(CPU:%.2f,Queue:%.2f). Workers:%d. Cooldown activated.", cpuUsage, queueUsage, wp.currentWorkers)
		wp.cooldownUntil = time.Now().Add(defaultCooldownPeriod)
	}
}

// Stop implements the ports.TaskExecutor interface.
// Signals shutdown and waits for workers and monitor.
func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	select {
	case <-wp.stopChan:
		wp.mu.Unlock()
		return // Already stopping/stopped
	default:
		log.Printf("Worker pool stopping... Signaling %d workers and monitor.", wp.currentWorkers)
		close(wp.stopChan)
	}
	currentWorkerCount := wp.currentWorkers
	monitorWasRunning := wp.monitorRunning
	wp.mu.Unlock()

	wp.wg.Wait() // Wait for all workers + monitor (if running)

	wp.mu.Lock()              // Lock to update final state
	wp.monitorRunning = false // Ensure monitor state is false after stop
	wp.mu.Unlock()

	log.Printf("Worker pool stopped. %d workers exited. Monitor active at stop: %t", currentWorkerCount, monitorWasRunning)
}

// GetCurrentWorkers returns the current number of active worker goroutines.
func (wp *WorkerPool) GetCurrentWorkers() int {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	return wp.currentWorkers
}
