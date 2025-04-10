# Dragonscale Usage Guide

## 1. Getting Started

### 1.1 Installation

```bash
go get github.com/ZanzyTHEbar/dragonscale
```

### 1.2 Basic Setup

To set up Dragonscale in your project:

```go
import (
    "github.com/ZanzyTHEbar/dragonscale/internal/adapters/eventbus"
    "github.com/ZanzyTHEbar/dragonscale/internal/adapters/workerpool"
    "github.com/ZanzyTHEbar/dragonscale/internal/config"
    "github.com/ZanzyTHEbar/dragonscale/internal/domain"
)

func main() {
    // Load configuration
    cfg, err := config.LoadFromFile("config.json")
    if err != nil {
        log.Fatalf("Failed to load configuration: %v", err)
    }

    // Create event bus
    bus := eventbus.NewSimpleEventBus()

    // Create worker pool
    monitor := workerpool.NewLoadMonitor(cfg.WorkerPool.CPUThreshold, cfg.WorkerPool.MemThreshold)
    pool, err := workerpool.NewWorkerPool(
        cfg.WorkerPool.InitialWorkers,
        cfg.WorkerPool.MinWorkers,
        cfg.WorkerPool.MaxWorkers,
        cfg.WorkerPool.QueueSize,
        monitor,
    )
    if err != nil {
        log.Fatalf("Failed to create worker pool: %v", err)
    }

    // Create task scheduler
    scheduler := domain.NewTaskScheduler(pool)

    // Create DAG executor
    dagExecutor := domain.NewDAGExecutor(scheduler, bus)

    // Start components
    scheduler.Start()
}
```

## 2. Configuration

Dragonscale uses a JSON configuration file for customization. A sample `config.json` is included in the root directory.

### 2.1 Configuration Options

```json
{
  "system": {
    "logLevel": "info",         // debug, info, warn, error
    "metricsEnabled": false,    // Enable metrics collection
    "metricsEndpoint": "http://localhost:9091/metrics"
  },
  "workerPool": {
    "initialWorkers": 4,        // Initial number of workers
    "minWorkers": 2,            // Minimum number of workers
    "maxWorkers": 8,            // Maximum number of workers
    "queueSize": 100,           // Size of the task queue
    "cpuThreshold": 0.8,        // CPU usage threshold for scaling (0.0-1.0)
    "memThreshold": 0.9         // Memory usage threshold for scaling (0.0-1.0)
  },
  "scheduler": {
    "enablePriorityBoost": true, // Boost priority of long-waiting tasks
    "maxTaskRetries": 3,         // Maximum retries for failed tasks
    "defaultInterval": "10s"     // Default interval for recurring tasks
  },
  "tools": {
    "defaultTimeout": 10,        // Default timeout in seconds
    "allowedTools": ["*"],       // List of allowed tools
    "sandboxEnabled": true       // Enable tool sandboxing
  },
  "eventBus": {
    "defaultBufferSize": 20      // Default buffer size for subscribers
  }
}
```

## 3. Task Management

### 3.1 Creating Tasks

```go
// One-time task
task := domain.NewTask(
    "task-1",                  // Task ID
    1,                         // Priority (lower is higher)
    time.Now().Add(5*time.Second), // Scheduled time
    &MyRunnable{},             // Task implementation (implements Runnable)
)

// Recurring task (Fixed Rate)
recurringTask := &domain.Task{
    ID:            "recurring-task",
    Priority:      5,
    ScheduledTime: time.Now(),
    ExecutionType: domain.FixedRate,
    Interval:      30 * time.Second,
    Job:           &MyRunnable{},
}
```

### 3.2 Scheduling Tasks

```go
// Schedule a single task
scheduler.Schedule(task)

// Schedule multiple tasks
for _, task := range tasks {
    scheduler.Schedule(task)
}
```

## 4. DAG (Directed Acyclic Graph) Execution

### 4.1 Creating a DAG

```go
// Create a new DAG
dag := domain.NewDAG("my-processing-pipeline")

// Create tasks
taskA := domain.NewTask("A", 1, time.Now(), &MyRunnable{})
taskB := domain.NewTask("B", 2, time.Now(), &MyRunnable{})
taskC := domain.NewTask("C", 2, time.Now(), &MyRunnable{})

// Add tasks to DAG
dag.AddTask(taskA)
dag.AddTask(taskB)
dag.AddTask(taskC)

// Add dependencies
dag.AddEdge("A", "B") // A must complete before B
dag.AddEdge("A", "C") // A must complete before C
```

### 4.2 Executing a DAG

```go
// Execute with basic scheduling (ignoring dependencies)
dagExecutor.ExecuteDAG(dag)

// Execute with optimized scheduling (respecting dependencies)
dagExecutor.ExecuteOptimizedDAG(dag)
```

## 5. Event Bus Integration

### 5.1 Publishing Events

```go
// Create an event
event := domain.NewEvent("task.completed", myTask)

// Publish the event
bus.Publish(event)
```

### 5.2 Subscribing to Events

```go
// Subscribe to events with a buffer size
subscription, err := bus.Subscribe("task.completed", 10)
if err != nil {
    log.Fatalf("Failed to subscribe: %v", err)
}

// Process events in a goroutine
go func() {
    for event := range subscription {
        if task, ok := event.Data.(*domain.Task); ok {
            log.Printf("Task %s completed at %v", task.ID, event.Timestamp)
        }
    }
}()
```

### 5.3 Common Event Topics

- `task.completed`: Published when a task completes successfully
- `task.failed`: Published when a task fails
- `dag.completed`: Published when a DAG completes
- `system.load.high`: Published when system load exceeds thresholds

## 6. Monitoring and Statistics

### 6.1 Using the Task Stats Collector

```go
// Create a stats collector
statsCollector := utils.NewTaskStatsCollector()

// Subscribe to task events
taskStatusSub, _ := bus.Subscribe("task.completed", 10)
statsCollector.EventHandler(taskStatusSub)

// Start periodic stats monitoring
stopChan := make(chan struct{})
statsCollector.StartStatsMonitor(5*time.Second, stopChan)

// Print stats on demand
statsCollector.PrintStats()
```

### 6.2 Using the Task Progress Bar

```go
// Create a progress bar for DAG execution
progressBar := utils.NewTaskProgressBar(len(dag.Tasks))

// Update progress
progressBar.Update(completedTasks, failedTasks)

// Print progress
progressBar.Print()
```

## 7. Tool Integration

### 7.1 Creating Custom Tools

```go
// Implement the ExecutableTool interface
type MyTool struct {
    name string
    description string
}

func (t *MyTool) Execute(input interface{}) (interface{}, error) {
    // Tool implementation
    return "Processed: " + input.(string), nil
}

func (t *MyTool) Name() string {
    return t.name
}

func (t *MyTool) Description() string {
    return t.description
}
```

### 7.2 Registering and Using Tools

```go
// Create tool manager
toolManager := tools.NewSimpleToolManager()

// Register tool
toolManager.RegisterTool(&MyTool{
    name: "my-tool",
    description: "My custom processing tool",
})

// Create safe executor with timeout
safeExecutor := tools.NewSafeToolExecutor(toolManager, 5)

// Execute tool with timeout
result, err := safeExecutor.ExecuteWithTimeout("my-tool", "input data")
```

## 8. Graceful Shutdown

```go
// Handle OS signals
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

// Wait for shutdown signal
<-sigChan

// Stop components in reverse order
bus.Stop()
scheduler.Stop()
pool.Stop()
```
