package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/adapters/eventbus"
	"github.com/ZanzyTHEbar/dragonscale/internal/adapters/tools"
	"github.com/ZanzyTHEbar/dragonscale/internal/adapters/workerpool"
	"github.com/ZanzyTHEbar/dragonscale/internal/config"
	"github.com/ZanzyTHEbar/dragonscale/internal/domain"
)

// SimpleTask implements the Runnable interface for demonstration.
type SimpleTask struct {
	id      string
	payload string
}

// Run executes the task.
func (t *SimpleTask) Run() error {
	log.Printf("Executing task %s with payload: %s", t.id, t.payload)
	// Simulate work
	time.Sleep(100 * time.Millisecond)
	return nil
}

func main() {
	// Parse command-line flags
	configFile := flag.String("config", "config.json", "Path to configuration file")
	demoMode := flag.Bool("demo", true, "Run in demo mode with examples")
	demoTimeout := flag.Int("timeout", 30, "Demo timeout in seconds (if demo mode enabled)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	workerCount := flag.Int("workers", 0, "Override initial worker count (0 = use config value)")
	flag.Parse()

	if *verbose {
		log.Println("Verbose logging enabled")
	}

	log.Println("Starting Dragonscale...")

	// Load configuration
	log.Printf("Loading configuration from %s...", *configFile)
	cfg, err := config.LoadFromFile(*configFile)
	if err != nil {
		log.Printf("Warning: Failed to load configuration: %v", err)
		log.Println("Using default configuration")
		cfg = config.DefaultConfig()
	}

	// Override config with command-line flags if specified
	if *workerCount > 0 {
		log.Printf("Overriding worker count with command-line value: %d", *workerCount)
		cfg.WorkerPool.InitialWorkers = *workerCount
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Create event bus
	log.Println("Initializing event bus...")
	bus := eventbus.NewSimpleEventBus()

	// Create worker pool with configuration
	log.Println("Initializing worker pool...")
	mon := workerpool.NewLoadMonitor(cfg.WorkerPool.CPUThreshold, cfg.WorkerPool.MemThreshold)
	pool, err := workerpool.NewWorkerPool(
		cfg.WorkerPool.InitialWorkers,
		cfg.WorkerPool.MinWorkers,
		cfg.WorkerPool.MaxWorkers,
		cfg.WorkerPool.QueueSize,
		mon,
	)
	if err != nil {
		log.Fatalf("Failed to create worker pool: %v", err)
	}

	// Create task scheduler with worker pool
	log.Println("Initializing task scheduler...")
	scheduler := domain.NewTaskScheduler(pool)

	// Create DAG executor with scheduler and event bus
	log.Println("Initializing DAG executor...")
	dagExecutor := domain.NewDAGExecutor(scheduler, bus)

	// Create tool manager
	log.Println("Initializing tool manager...")
	toolManager := tools.NewSimpleToolManager()

	// Register some example tools
	log.Println("Registering tools...")
	toolManager.RegisterTool(tools.NewPlaceholderTool("echo", "Echo the input"))
	toolManager.RegisterTool(tools.NewPlaceholderTool("reverse", "Reverse the input"))
	toolManager.RegisterTool(tools.NewMCPTool("calculator", "Basic math operations", 
		"https://example.com/mcp/calculator", map[string]string{"apiKey": "demo-key"}))

	// Create safe tool executor with configuration
	safeExecutor := tools.NewSafeToolExecutor(toolManager, cfg.ToolsConfig.DefaultTimeout)

	// Start components
	log.Println("Starting the system...")
	scheduler.Start()

	// Subscribe to task events with configured buffer size
	taskCompletionSub, err := bus.Subscribe("task.completed", cfg.EventBus.DefaultBufferSize)
	if err != nil {
		log.Printf("Warning: Failed to subscribe to task events: %v", err)
	} else {
		// Process task completion events in background
		go func() {
			for event := range taskCompletionSub {
				if task, ok := event.Data.(*domain.Task); ok {
					log.Printf("Event received: Task %s completed at %v", task.ID, event.Timestamp)
				}
			}
		}()
	}

	// Setup OS signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Create a stats collector for monitoring task execution
	statsCollector := domain.NewTaskStatsCollector()
	statsStopChan := make(chan struct{})
	
	// Subscribe to task status events
	taskStatusSub, err := bus.Subscribe("task.completed", cfg.EventBus.DefaultBufferSize)
	if err != nil {
		log.Printf("Warning: Failed to subscribe to task status events: %v", err)
	} else {
		statsCollector.EventHandler(taskStatusSub)
	}
	
	// Start the stats monitor
	statsCollector.StartStatsMonitor(5*time.Second, statsStopChan)
	
	// Run demo examples if enabled
	if *demoMode {
		runDemoExamples(scheduler, dagExecutor, safeExecutor, cfg)
	} else {
		log.Println("Running in production mode (demo examples disabled)")
	}

	// Wait for shutdown signal or timeout for demo
	log.Println("\nSystem running. Press Ctrl+C to stop...")
	select {
	case <-sigChan:
		log.Println("Shutdown signal received")
	case <-time.After(time.Duration(*demoTimeout) * time.Second):
		if *demoMode {
			log.Printf("Demo timeout reached (%d seconds)", *demoTimeout)
		} else {
			// In non-demo mode, we should only exit on signal
			<-sigChan
			log.Println("Shutdown signal received")
		}
	}

	// Graceful shutdown
	log.Println("Shutting down...")
	
	// Stop stats monitoring
	close(statsStopChan)
	log.Println("Stats monitoring stopped")
	
	// Final stats display
	statsCollector.PrintStats()
	
	// Stop components
	bus.Stop()
	scheduler.Stop()
	pool.Stop()
	log.Println("Dragonscale shutdown complete")
}

// createExampleDAG creates a sample DAG for demonstration.
func createExampleDAG() *domain.DAG {
	dag := domain.NewDAG("example-dag")

	// Create tasks
	taskA := domain.NewTask("A", 1, time.Now(), &SimpleTask{id: "A", payload: "Task A - DAG source"})
	taskB := domain.NewTask("B", 2, time.Now(), &SimpleTask{id: "B", payload: "Task B - depends on A"})
	taskC := domain.NewTask("C", 2, time.Now(), &SimpleTask{id: "C", payload: "Task C - depends on A"})
	taskD := domain.NewTask("D", 3, time.Now(), &SimpleTask{id: "D", payload: "Task D - depends on B and C"})

	// Add tasks to DAG
	dag.AddTask(taskA)
	dag.AddTask(taskB)
	dag.AddTask(taskC)
	dag.AddTask(taskD)

	// Add dependencies
	dag.AddEdge("A", "B") // A -> B
	dag.AddEdge("A", "C") // A -> C
	dag.AddEdge("B", "D") // B -> D
	dag.AddEdge("C", "D") // C -> D

	return dag
}

// createRecurringTask creates a recurring task for demonstration.
func createRecurringTask(scheduler *domain.TaskScheduler, interval time.Duration) {
	// Recurring task that prints a message at the specified interval
	task := &domain.Task{
		ID:            "recurring-task",
		Priority:      5,
		ScheduledTime: time.Now(),
		ExecutionType: domain.FixedRate,
		Interval:      interval,
		Job: &SimpleTask{
			id:      "recurring-task",
			payload: fmt.Sprintf("I run every %v", interval),
		},
	}
	
	scheduler.Schedule(task)
	log.Printf("Scheduled recurring task with interval: %v", interval)
}