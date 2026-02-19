package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

type ToolRegistry struct {
	tools map[string]Tool
	mu    sync.RWMutex
	// gatewayTools are always visible to the LLM; all other tools are
	// discovered via tool_search + tool_call (progressive disclosure).
	gatewayTools map[string]bool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:        make(map[string]Tool),
		gatewayTools: make(map[string]bool),
	}
}

// SetProgressiveDisclosure is a no-op retained for backward compatibility.
// Progressive disclosure is always enabled.
func (r *ToolRegistry) SetProgressiveDisclosure(_ bool) {}

// MarkGateway marks a tool name as always visible in progressive disclosure mode.
func (r *ToolRegistry) MarkGateway(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gatewayTools[name] = true
}

// RegisterMetaTools creates and registers tool_search and tool_call, then marks them
// as gateway tools. Call this once after creating the registry.
func (r *ToolRegistry) RegisterMetaTools() {
	search := NewToolSearchTool(r)
	call := NewToolCallTool(r)
	r.Register(search)
	r.Register(call)
	r.MarkGateway("tool_search")
	r.MarkGateway("tool_call")
}

// GetVisibleDefinitions returns tool definitions visible to the LLM.
// Only gateway tools are returned; the agent discovers others via tool_search.
func (r *ToolRegistry) GetVisibleDefinitions() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	definitions := make([]map[string]interface{}, 0)
	for _, tool := range r.tools {
		if r.gatewayTools[tool.Name()] {
			definitions = append(definitions, ToolToSchema(tool))
		}
	}
	return definitions
}

func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) *ToolResult {
	return r.ExecuteWithContext(ctx, name, args, "", "", nil)
}

// ExecuteWithContext executes a tool with channel/chatID context and optional async callback.
// If the tool implements AsyncTool and a non-nil callback is provided,
// the callback will be set on the tool before execution.
func (r *ToolRegistry) ExecuteWithContext(ctx context.Context, name string, args map[string]interface{}, channel, chatID string, asyncCallback AsyncCallback) *ToolResult {
	logger.InfoCF("tool", "Tool execution started",
		map[string]interface{}{
			"tool": name,
			"args": args,
		})

	tool, ok := r.Get(name)
	if !ok {
		logger.ErrorCF("tool", "Tool not found",
			map[string]interface{}{
				"tool": name,
			})
		return ErrorResult(fmt.Sprintf("tool %q not found", name)).WithError(fmt.Errorf("tool not found"))
	}

	// If tool implements ContextualTool, set context
	if contextualTool, ok := tool.(ContextualTool); ok && channel != "" && chatID != "" {
		contextualTool.SetContext(channel, chatID)
	}

	// If tool implements AsyncTool and callback is provided, set callback
	if asyncTool, ok := tool.(AsyncTool); ok && asyncCallback != nil {
		asyncTool.SetCallback(asyncCallback)
		logger.DebugCF("tool", "Async callback injected",
			map[string]interface{}{
				"tool": name,
			})
	}

	start := time.Now()
	result := tool.Execute(ctx, args)
	duration := time.Since(start)

	// Log based on result type
	if result.IsError {
		logger.ErrorCF("tool", "Tool execution failed",
			map[string]interface{}{
				"tool":     name,
				"duration": duration.Milliseconds(),
				"error":    result.ForLLM,
			})
	} else if result.Async {
		logger.InfoCF("tool", "Tool started (async)",
			map[string]interface{}{
				"tool":     name,
				"duration": duration.Milliseconds(),
			})
	} else {
		logger.InfoCF("tool", "Tool execution completed",
			map[string]interface{}{
				"tool":          name,
				"duration_ms":   duration.Milliseconds(),
				"result_length": len(result.ForLLM),
			})
	}

	return result
}

func (r *ToolRegistry) GetDefinitions() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	definitions := make([]map[string]interface{}, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, ToolToSchema(tool))
	}
	return definitions
}

// List returns a list of all registered tool names.
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// ListVisible returns tool names visible to the LLM (gateway tools only).
func (r *ToolRegistry) ListVisible() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.gatewayTools))
	for name := range r.gatewayTools {
		if _, ok := r.tools[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// GetSummaries returns human-readable summaries of all registered tools.
// Returns a slice of "name - description" strings.
func (r *ToolRegistry) GetSummaries() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	summaries := make([]string, 0, len(r.tools))
	for _, tool := range r.tools {
		summaries = append(summaries, fmt.Sprintf("- `%s` - %s", tool.Name(), tool.Description()))
	}
	return summaries
}
