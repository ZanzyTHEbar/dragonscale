package tools

import (
	"fmt"
	"time"
)

// --- Safe Tool Executor ---

// SafeToolExecutor provides sandboxed execution for tools.
// TODO: This is a placeholder for a more sophisticated implementation that might
// use WASM, containers, or other isolation mechanisms.
type SafeToolExecutor struct {
	manager     ToolManager
	timeoutSecs int
}

// NewSafeToolExecutor creates a new SafeToolExecutor.
func NewSafeToolExecutor(manager ToolManager, timeoutSecs int) *SafeToolExecutor {
	if timeoutSecs <= 0 {
		timeoutSecs = 10 // Default timeout
	}

	return &SafeToolExecutor{
		manager:     manager,
		timeoutSecs: timeoutSecs,
	}
}

// ExecuteWithTimeout runs a tool with a timeout.
func (se *SafeToolExecutor) ExecuteWithTimeout(toolName string, input interface{}) (interface{}, error) {
	resultChan := make(chan struct {
		result interface{}
		err    error
	}, 1)

	// Run the tool in a goroutine
	go func() {
		result, err := se.manager.ExecuteTool(toolName, input)
		resultChan <- struct {
			result interface{}
			err    error
		}{result, err}
	}()

	// Wait for result or timeout
	select {
	case res := <-resultChan:
		return res.result, res.err
	case <-time.After(time.Duration(se.timeoutSecs) * time.Second):
		return nil, fmt.Errorf("tool execution timed out after %d seconds", se.timeoutSecs)
	}
}
