package domain

import (
    "time"
    "fmt" // Added fmt import
)

// Edge represents a dependency between two tasks in the DAG.
// FromTaskID must complete before ToTaskID can start.
// type Edge struct { // Simplified: Using adjacency lists directly
// 	FromTaskID string
// 	ToTaskID   string
// }

// DAG represents a Directed Acyclic Graph of Tasks.
type DAG struct {
	ID             string           // Unique identifier for this DAG instance
	Tasks          map[string]*Task // Map of Task ID to Task object, representing the nodes
	Edges          map[string][]string // Adjacency list: map Task ID to IDs of tasks that depend on it (outgoing edges)
	ReverseEdges   map[string][]string // Adjacency list: map Task ID to IDs of tasks it depends on (incoming edges) - useful for scheduling
	Cacheable      bool             // Hint for the executor whether this DAG structure can be cached
	LastCompaction time.Time        // Timestamp for dynamic DAG compaction strategy
	// Add other metadata as needed, e.g., source nodes, sink nodes
}

// NewDAG creates a new DAG instance.
func NewDAG(id string) *DAG {
	return &DAG{
		ID:           id,
		Tasks:        make(map[string]*Task),
		Edges:        make(map[string][]string),
		ReverseEdges: make(map[string][]string),
		Cacheable:    true, // Default to cacheable unless specified otherwise
	}
}

// AddTask adds a task (node) to the DAG.
func (d *DAG) AddTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("cannot add nil task to DAG")
	}
    if task.ID == "" {
        return fmt.Errorf("cannot add task with empty ID to DAG")
    }
	if _, exists := d.Tasks[task.ID]; exists {
		return fmt.Errorf("task with ID '%s' already exists in DAG '%s'", task.ID, d.ID)
	}
	d.Tasks[task.ID] = task
	// Initialize edge lists for the new task
	d.Edges[task.ID] = make([]string, 0)
	d.ReverseEdges[task.ID] = make([]string, 0)
	return nil
}

// AddEdge adds a dependency (directed edge) between two tasks in the DAG.
func (d *DAG) AddEdge(fromTaskID, toTaskID string) error {
	// Check if tasks exist
	if _, exists := d.Tasks[fromTaskID]; !exists {
		return fmt.Errorf("source task '%s' not found in DAG '%s'", fromTaskID, d.ID)
	}
	if _, exists := d.Tasks[toTaskID]; !exists {
		return fmt.Errorf("destination task '%s' not found in DAG '%s'", toTaskID, d.ID)
	}

	// Add edge to forward adjacency list (avoid duplicates)
    for _, existingTo := range d.Edges[fromTaskID] {
        if existingTo == toTaskID {
            return nil // Edge already exists
        }
    }
	d.Edges[fromTaskID] = append(d.Edges[fromTaskID], toTaskID)

    // Add edge to reverse adjacency list (avoid duplicates)
    for _, existingFrom := range d.ReverseEdges[toTaskID] {
        if existingFrom == fromTaskID {
            return nil // Edge already exists (should be consistent with forward edge)
        }
    }
	d.ReverseEdges[toTaskID] = append(d.ReverseEdges[toTaskID], fromTaskID)

    // Optional: Add cycle detection logic here
    // if d.hasCycle() { ... rollback and return error ... }

	return nil
}

// TODO: Implement methods for dynamic DAG updates (ApplyDelta, Compact) if needed later.
// TODO: Implement cycle detection (e.g., using DFS)
