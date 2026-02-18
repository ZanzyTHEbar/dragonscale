package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
)

type ctxKeyResources struct{}

// ResourcesFromContext extracts loaded resources injected by tool_call before dispatch.
func ResourcesFromContext(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(ctxKeyResources{}).(map[string]string); ok {
		return v
	}
	return nil
}

// ToolCallTool is a meta-tool that dispatches to any registered tool by name.
// This enables progressive disclosure: instead of exposing all tools to the LLM,
// only tool_search and tool_call are exposed. The agent discovers tools via
// tool_search, then invokes them via tool_call.
type ToolCallTool struct {
	registry *ToolRegistry
	channel  string
	chatID   string
}

var _ ContextualTool = (*ToolCallTool)(nil)

// NewToolCallTool creates a meta-tool that dispatches to registered tools.
func NewToolCallTool(registry *ToolRegistry) *ToolCallTool {
	return &ToolCallTool{registry: registry}
}

func (t *ToolCallTool) Name() string { return "tool_call" }

func (t *ToolCallTool) Description() string {
	return "Execute any registered tool by name. Use tool_search first to discover available tools and their parameters, then call them here."
}

func (t *ToolCallTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the tool to execute (from tool_search results).",
			},
			"arguments": map[string]interface{}{
				"type":        "object",
				"description": "Arguments to pass to the tool, as a JSON object matching the tool's parameter schema.",
			},
		},
		"required": []string{"tool_name"},
	}
}

func (t *ToolCallTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

func (t *ToolCallTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	toolName, _ := args["tool_name"].(string)
	if toolName == "" {
		return ErrorResult("tool_name is required")
	}

	// Prevent recursive calls to meta-tools
	if toolName == "tool_call" || toolName == "tool_search" {
		return ErrorResult(fmt.Sprintf("cannot recursively call meta-tool %q", toolName))
	}

	// Extract arguments — handle both direct object and JSON string
	var toolArgs map[string]interface{}
	switch v := args["arguments"].(type) {
	case map[string]interface{}:
		toolArgs = v
	case string:
		const maxArgsJSON = 64 * 1024
		if len(v) > maxArgsJSON {
			return ErrorResult(fmt.Sprintf("arguments JSON too large: %d bytes (max %d)", len(v), maxArgsJSON))
		}
		if err := json.Unmarshal([]byte(v), &toolArgs); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments JSON: %v", err))
		}
	case nil:
		toolArgs = map[string]interface{}{}
	default:
		return ErrorResult(fmt.Sprintf("arguments must be a JSON object, got %T", v))
	}

	if len(toolArgs) > 50 {
		return ErrorResult(fmt.Sprintf("too many arguments: %d (max 50)", len(toolArgs)))
	}

	// If the target tool declares resources, load them before dispatch.
	if tool, found := t.registry.Get(toolName); found {
		if rp, ok := tool.(ResourceProvider); ok {
			resources, err := rp.LoadResources(ctx)
			if err != nil {
				logger.WarnCF("tool_call", "Failed to load resources for tool",
					map[string]interface{}{"tool": toolName, "error": err.Error()})
			} else if len(resources) > 0 {
				ctx = context.WithValue(ctx, ctxKeyResources{}, resources)
			}
		}
	}

	return t.registry.ExecuteWithContext(ctx, toolName, toolArgs, t.channel, t.chatID, nil)
}
