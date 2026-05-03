package tools

import "context"

// CapableTool is an optional interface for tools that require elevated
// capabilities: secrets, network endpoints, filesystem paths, or shell access.
//
// Tools that do not implement CapableTool receive zero-capability defaults:
// no secrets, no network, filesystem read-only to workspace, no shell.
// This is the correct default for safe, backward-compatible operation.
//
// The SecureBus reads Capabilities() before each execution, enforces the
// declared policy, injects secrets, and redacts output — all transparent to
// the tool's Execute method.
type CapableTool interface {
	Tool
	Capabilities() ToolCapabilities
}

// ToolCapabilities declares what a tool needs at runtime.
// All fields are zero-value safe (nil slice / zero enum = deny).
type ToolCapabilities struct {
	// Secrets lists named secrets the tool requires. The SecureBus resolves
	// each from the SecretStore and injects it per the InjectAs spec.
	Secrets []SecretRef

	// Network lists URL glob patterns the tool is permitted to contact.
	// When SecureBus capability enforcement is active, SSRF validation is still
	// applied within permitted patterns.
	Network []EndpointRule

	// Filesystem lists lexical path rules the tool may access, relative to the
	// workspace root. Tools remain responsible for safe open/read/write-time path
	// confinement (including symlink handling).
	Filesystem []PathRule

	// Shell declares the shell access level for tools that execute subprocesses.
	Shell ShellAccessLevel
}

// SecretRef identifies a named secret and how to deliver it to the tool.
type SecretRef struct {
	// Name is the logical secret identifier (e.g. "github_token").
	Name string

	// InjectAs describes how to deliver the secret:
	//   "env:VAR_NAME"          — set environment variable
	//   "arg:key"               — add to args map under key
	//   "header:Header-Name"    — add as HTTP header (web tools)
	InjectAs string

	// Required causes tool execution to fail if the secret is missing.
	// When false, the tool executes with the secret omitted.
	Required bool
}

// EndpointRule permits access to a URL matching the given glob pattern.
// Patterns follow filepath.Match syntax applied to the full URL string.
// Example: "https://api.github.com/**"
type EndpointRule struct {
	Pattern string
}

// PathRule grants lexical filesystem access to paths matching Pattern.
// Pattern is relative to the workspace root and uses filepath.Match syntax.
type PathRule struct {
	// Pattern is the path glob, e.g. "data/**" or "config/*.toml".
	Pattern string

	// Mode is one of "r" (read-only), "w" (write-only), "rw" (read-write).
	Mode string
}

// ShellAccessLevel controls subprocess execution for shell tools.
type ShellAccessLevel int

const (
	// ShellNone disables all subprocess execution.
	ShellNone ShellAccessLevel = iota

	// ShellDenylist allows subprocess execution but blocks known-dangerous patterns
	// (e.g. rm -rf /, /dev/mem access, raw socket creation). This is the default
	// for tools that declare Shell access.
	ShellDenylist

	// ShellAllowlist restricts execution to an explicit permit list of command
	// patterns. Any command not matching the list is rejected.
	ShellAllowlist
)

// ZeroCapabilities returns the default deny-all capabilities applied to any
// tool that does not implement CapableTool.
func ZeroCapabilities() ToolCapabilities {
	return ToolCapabilities{Shell: ShellNone}
}

// ExtractCapabilities returns a tool's declared capabilities, or ZeroCapabilities
// for tools that do not implement CapableTool.
func ExtractCapabilities(t Tool) ToolCapabilities {
	if ct, ok := t.(CapableTool); ok {
		return ct.Capabilities()
	}
	return ZeroCapabilities()
}

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

// ExampleProvider is an optional interface for tools that want to include
// input examples in their schema definition. This follows Anthropic's Tool Use
// Examples pattern — concrete usage samples that teach the LLM when to include
// optional parameters, which combinations make sense, and API conventions.
type ExampleProvider interface {
	InputExamples() []map[string]interface{}
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
	fn := map[string]interface{}{
		"name":        tool.Name(),
		"description": tool.Description(),
		"parameters":  tool.Parameters(),
	}
	if ep, ok := tool.(ExampleProvider); ok {
		if examples := ep.InputExamples(); len(examples) > 0 {
			fn["input_examples"] = examples
		}
	}
	return map[string]interface{}{
		"type":     "function",
		"function": fn,
	}
}
