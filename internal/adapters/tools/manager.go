package tools

import (
	"fmt"
	"log"
	"sync"
)

// ExecutableTool defines the interface for any tool that can be executed by the manager.
// TODO: This is a placeholder; real tools might need more complex context, input/output types.
type ExecutableTool interface {
	Execute(input interface{}) (output interface{}, err error)
	Name() string
	Description() string
}

// ToolManager defines the interface for managing and executing tools.
// This aligns with the hexagonal architecture port for tool interactions.
type ToolManager interface {
	RegisterTool(tool ExecutableTool) error
	ExecuteTool(toolName string, input interface{}) (output interface{}, err error)
	ListTools() []string
}

// SimpleToolManager is a basic in-memory implementation of ToolManager.
// It holds registered tools in a map.
type SimpleToolManager struct {
	tools map[string]ExecutableTool
	mu    sync.RWMutex // Protects the tools map
}

// NewSimpleToolManager creates a new SimpleToolManager.
func NewSimpleToolManager() *SimpleToolManager {
	return &SimpleToolManager{
		tools: make(map[string]ExecutableTool),
	}
}

// RegisterTool adds a tool to the manager.
// TODO: In a real implementation, this might load from config or WASM.
func (tm *SimpleToolManager) RegisterTool(tool ExecutableTool) error {
	if tool == nil || tool.Name() == "" {
		return fmt.Errorf("cannot register nil tool or tool with empty name")
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	name := tool.Name()
	if _, exists := tm.tools[name]; exists {
		return fmt.Errorf("tool '%s' already registered", name)
	}

	tm.tools[name] = tool
	log.Printf("Tool registered: %s", name)
	return nil
}

// ExecuteTool runs a registered tool by its name.
func (tm *SimpleToolManager) ExecuteTool(toolName string, input interface{}) (output interface{}, err error) {
	tm.mu.RLock()
	tool, exists := tm.tools[toolName]
	tm.mu.RUnlock() // Unlock before executing the tool

	if !exists {
		return nil, fmt.Errorf("tool '%s' not found", toolName)
	}

	// log.Printf("Executing tool '%s'...", toolName) // Reduce noise
	// TODO: Execution happens outside the lock to allow concurrent tool executions
	// assuming the tool's Execute method itself is safe or handles its own locking if needed.
	output, err = tool.Execute(input)
	if err != nil {
		log.Printf("Error executing tool '%s': %v", toolName, err)
		return nil, fmt.Errorf("tool '%s' execution failed: %w", toolName, err)
	}

	// log.Printf("Tool '%s' executed successfully.", toolName) // Reduce noise
	return output, nil
}

// ListTools returns the names of all registered tools.
func (tm *SimpleToolManager) ListTools() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	names := make([]string, 0, len(tm.tools))
	for name := range tm.tools {
		names = append(names, name)
	}
	return names
}

