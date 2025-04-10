package main

import (
	"log"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/adapters/tools"
	"github.com/ZanzyTHEbar/dragonscale/internal/config"
	"github.com/ZanzyTHEbar/dragonscale/internal/domain"
)

// runDemoExamples demonstrates various features of the system with example tasks and DAGs.
func runDemoExamples(scheduler *domain.TaskScheduler, dagExecutor *domain.DAGExecutor, safeExecutor *tools.SafeToolExecutor, cfg *config.Config) {
	log.Println("Running demo examples...")

	// Example 1: Schedule a simple recurring task
	log.Println("Example 1: Scheduling a recurring task")
	createRecurringTask(scheduler, 5*time.Second)

	// Example 2: Create and execute a DAG
	log.Println("Example 2: Creating and executing a DAG")
	exampleDAG := createExampleDAG()
	err := dagExecutor.ExecuteDAG(exampleDAG)
	if err != nil {
		log.Printf("Error executing example DAG: %v", err)
	} else {
		log.Printf("DAG %s submitted for execution", exampleDAG.ID)
	}

	// Example 3: Execute a simple tool safely
	log.Println("Example 3: Executing a tool with safety wrappers")
	result, err := safeExecutor.ExecuteWithTimeout("echo", map[string]interface{}{
		"text": "Hello from the safe tool executor!",
	})
	if err != nil {
		log.Printf("Error executing tool: %v", err)
	} else {
		log.Printf("Tool execution result: %v", result)
	}

	log.Println("Demo examples have been scheduled.")
}
