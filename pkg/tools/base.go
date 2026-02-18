package tools

import "context"

// Tool is the interface that all tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) *ToolResult
}

// ContextualTool is an optional interface that tools can implement
// to receive the current message context (channel, chatID)
type ContextualTool interface {
	Tool
	SetContext(channel, chatID string)
}

// AsyncCallback is a function type that async tools use to notify completion.
// When an async tool finishes its work, it calls this callback with the result.
//
// The ctx parameter allows the callback to be canceled if the agent is shutting down.
// The result parameter contains the tool's execution result.
//
// Example usage in an async tool:
//
//	func (t *MyAsyncTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
//	    // Start async work in background
//	    go func() {
//	        result := doAsyncWork()
//	        if t.callback != nil {
//	            t.callback(ctx, result)
//	        }
//	    }()
//	    return AsyncResult("Async task started")
//	}
type AsyncCallback func(ctx context.Context, result *ToolResult)

// AsyncTool is an optional interface that tools can implement to support
// asynchronous execution with completion callbacks.
//
// Async tools return immediately with an AsyncResult, then notify completion
// via the callback set by SetCallback.
//
// This is useful for:
// - Long-running operations that shouldn't block the agent loop
// - Subagent spawns that complete independently
// - Background tasks that need to report results later
//
// Example:
//
//	type SpawnTool struct {
//	    callback AsyncCallback
//	}
//
//	func (t *SpawnTool) SetCallback(cb AsyncCallback) {
//	    t.callback = cb
//	}
//
//	func (t *SpawnTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
//	    go t.runSubagent(ctx, args)
//	    return AsyncResult("Subagent spawned, will report back")
//	}
type AsyncTool interface {
	Tool
	// SetCallback registers a callback function to be invoked when the async operation completes.
	// The callback will be called from a goroutine and should handle thread-safety if needed.
	SetCallback(cb AsyncCallback)
}

// ResourceProvider is an optional interface that tools can implement to declare
// resources they need loaded before execution (schemas, examples, docs, configs).
//
// When a tool implements this interface, the tool_call meta-tool will call
// LoadResources before dispatching the tool execution, injecting the loaded
// resources into the execution context.
//
// This enables lazy loading: resources are only fetched when the tool is
// actually invoked, not when it's registered, saving memory and startup time.
type ResourceProvider interface {
	// ResourceKeys returns identifiers for resources this tool needs.
	// Keys are opaque strings meaningful to the tool (e.g., "schema:users",
	// "example:basic", "config:limits").
	ResourceKeys() []string

	// LoadResources fetches the declared resources and returns them as a map
	// of key -> content. Called once per tool_call dispatch. The tool can
	// cache internally if needed.
	LoadResources(ctx context.Context) (map[string]string, error)
}

func ToolToSchema(tool Tool) map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		},
	}
}
