package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// SecureBusDispatchFn is an optional hook that lets tool_call re-enter the
// SecureBus for each target tool, so the nested call gets the same
// policy/secret/leak/audit treatment as a direct call.
type SecureBusDispatchFn func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse

type ctxKeySecureBusDispatcher struct{}

// WithSecureBusDispatcher stores a bus dispatcher in ctx so tool_call can
// route target-tool executions through SecureBus.
func WithSecureBusDispatcher(ctx context.Context, fn SecureBusDispatchFn) context.Context {
	return context.WithValue(ctx, ctxKeySecureBusDispatcher{}, fn)
}

// secureBusDispatcherFromContext extracts a SecureBus dispatch hook from ctx.
func secureBusDispatcherFromContext(ctx context.Context) SecureBusDispatchFn {
	if fn, ok := ctx.Value(ctxKeySecureBusDispatcher{}).(SecureBusDispatchFn); ok {
		return fn
	}
	return nil
}

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
	return "Fallback tool executor: dispatch any registered tool by name. Prefer calling discovered tools directly — after tool_search, tools become native callables. Use tool_call only if a tool is not yet directly available."
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
				"description": "Arguments to pass to the tool. Provide either a JSON object or a JSON string object matching the tool's parameter schema.",
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
	toolName = t.normalizeToolName(toolName)

	// Prevent recursive calls to meta-tools
	if toolName == "tool_call" || toolName == "tool_search" {
		return ErrorResult(fmt.Sprintf("cannot recursively call meta-tool %q", toolName))
	}

	tool, found := t.registry.Get(toolName)
	if !found {
		return ErrorResult(fmt.Sprintf("tool %q not found — use tool_search to discover available tools", toolName))
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
		if err := jsonv2.Unmarshal([]byte(v), &toolArgs); err != nil {
			return t.schemaHintError(tool, fmt.Sprintf("invalid arguments JSON: %v", err))
		}
	case nil:
		toolArgs = map[string]interface{}{}
	default:
		return t.schemaHintError(tool, fmt.Sprintf("arguments must be a JSON object, got %T", v))
	}

	if len(toolArgs) > 50 {
		return ErrorResult(fmt.Sprintf("too many arguments: %d (max 50)", len(toolArgs)))
	}

	// If the target tool declares resources, load them before dispatch.
	if rp, ok := tool.(ResourceProvider); ok {
		resources, err := rp.LoadResources(ctx)
		if err != nil {
			logger.WarnCF("tool_call", "Failed to load resources for tool",
				map[string]interface{}{"tool": toolName, "error": err.Error()})
		} else if len(resources) > 0 {
			ctx = context.WithValue(ctx, ctxKeyResources{}, resources)
		}
	}

	// When running under SecureBus, re-enter the bus for the target tool so
	// it gets the full policy/secret/leak/audit treatment.
	if busDispatcher := secureBusDispatcherFromContext(ctx); busDispatcher != nil {
		argsJSON, err := jsonv2.Marshal(toolArgs)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to marshal tool arguments: %v", err))
		}
		req := itr.NewToolExecRequest(ids.New().String(), SessionKeyFromContext(ctx), "", toolName, string(argsJSON))
		resp := busDispatcher(ctx, req)
		if resp.IsError {
			return ErrorResult(resp.Result)
		}
		return &ToolResult{ForLLM: resp.Result}
	}

	channel, chatID := ResolveExecutionTarget(ctx, t.channel, t.chatID)
	result := t.registry.ExecuteWithContext(ctx, toolName, toolArgs, channel, chatID, AsyncCallbackFromContext(ctx))
	var validationErr *toolArgValidationError
	if result != nil && result.IsError && errors.As(result.Err, &validationErr) {
		return t.schemaHintError(tool, validationErr.cause.Error()).WithError(result.Err)
	}
	return result
}

// schemaHintError returns an error result that includes the tool's expected
// parameter schema, giving the LLM a clear correction path.
func (t *ToolCallTool) schemaHintError(tool Tool, msg string) *ToolResult {
	schema := tool.Parameters()
	hint := fmt.Sprintf("%s\n\nExpected schema for %q:\n", msg, tool.Name())

	if props, ok := schema["properties"].(map[string]interface{}); ok {
		schemaJSON, err := jsonv2.Marshal(props)
		if err == nil {
			hint += string(schemaJSON)
		}
	}
	if req := extractRequiredFields(schema); len(req) > 0 {
		hint += fmt.Sprintf("\nRequired: %s", strings.Join(req, ", "))
	}

	hint += "\n\nNote: discovered tools are directly callable — you do not need tool_call for tools returned by tool_search."
	return ErrorResult(hint)
}
func (t *ToolCallTool) normalizeToolName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return name
	}
	if _, found := t.registry.Get(name); found {
		return name
	}

	known := t.registry.List()
	normalized := strings.ToLower(name)

	// First pass: boundary-safe contains (prefers full tool names embedded in noisy strings).
	best := ""
	bestPos := -1
	for _, candidate := range known {
		pat := fmt.Sprintf(`(^|[^a-z0-9_])%s([^a-z0-9_]|$)`, regexp.QuoteMeta(strings.ToLower(candidate)))
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		loc := re.FindStringIndex(normalized)
		if loc == nil {
			continue
		}
		if bestPos == -1 || loc[0] < bestPos {
			best = candidate
			bestPos = loc[0]
		}
	}
	if best != "" {
		logger.WarnCF("tool_call", "Normalized malformed tool_name",
			map[string]interface{}{"raw": raw, "normalized": best})
		return best
	}

	// Third pass: raw substring fallback for heavily malformed names
	// like "exec_tool_search_query_exec_run_shell_command...".
	best = ""
	bestPos = -1
	for _, candidate := range known {
		pos := strings.Index(normalized, strings.ToLower(candidate))
		if pos == -1 {
			continue
		}
		if bestPos == -1 || pos < bestPos {
			best = candidate
			bestPos = pos
		}
	}
	if best != "" {
		logger.WarnCF("tool_call", "Normalized tool_name by substring fallback",
			map[string]interface{}{"raw": raw, "normalized": best})
		return best
	}

	// Second pass: comma/space separated fragments, choose first valid tool token.
	replacer := strings.NewReplacer(",", " ", ";", " ", "|", " ")
	for _, token := range strings.Fields(replacer.Replace(name)) {
		if _, found := t.registry.Get(token); found {
			logger.WarnCF("tool_call", "Normalized split tool_name token",
				map[string]interface{}{"raw": raw, "normalized": token})
			return token
		}
	}
	return name
}
