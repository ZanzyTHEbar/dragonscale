// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package fantasy

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	jsonv2 "github.com/go-json-experiment/json"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	memstore "github.com/sipeed/picoclaw/pkg/memory/store"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// PicoToolAdapter wraps a PicoClaw tool as a Fantasy AgentTool.
// It bridges PicoClaw's dual-channel ToolResult semantics with Fantasy's
// simple ToolResponse by publishing ForUser content to the bus as a side effect
// and returning only ForLLM content to Fantasy.
type PicoToolAdapter struct {
	inner      tools.Tool
	bus        *bus.MessageBus
	channel    string
	chatID     string
	memStore   *memstore.MemoryStore // may be nil if memory system disabled
	agentID    string
	sessionKey string
}

// Compile-time check that PicoToolAdapter implements fantasy.AgentTool.
var _ fantasy.AgentTool = (*PicoToolAdapter)(nil)

// Info returns Fantasy-compatible tool metadata from the PicoClaw tool.
// Fantasy's ToolInfo expects Parameters to be just the properties map and
// Required to be a separate []string. PicoClaw tools return a full JSON
// Schema object from Parameters() (with "type", "properties", "required"
// keys), so we must unwrap it here to avoid double-wrapping in
// agent.prepareTools() and agent.validateToolCall().
func (a *PicoToolAdapter) Info() fantasy.ToolInfo {
	params := a.inner.Parameters()
	properties, required := unwrapSchema(params)
	return fantasy.ToolInfo{
		Name:        a.inner.Name(),
		Description: a.inner.Description(),
		Parameters:  properties,
		Required:    required,
	}
}

// unwrapSchema extracts the properties map and required slice from a full
// JSON Schema object. If params already contains "type"+"properties" keys
// (i.e. it's a complete schema), extract the inner fields. Otherwise treat
// the whole map as a flat properties map (backward-compatible).
func unwrapSchema(params map[string]interface{}) (map[string]interface{}, []string) {
	props, hasProps := params["properties"].(map[string]interface{})
	_, hasType := params["type"]
	if !hasType || !hasProps {
		return params, nil
	}

	var required []string
	switch r := params["required"].(type) {
	case []string:
		required = r
	case []interface{}:
		for _, v := range r {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	return props, required
}

// Run executes the PicoClaw tool and bridges the result to Fantasy.
//
// Side effects:
//   - If the tool result has ForUser content and is not Silent, publishes to the bus.
//   - If the tool is a ContextualTool, sets channel/chatID context before execution.
//   - If the tool is an AsyncTool, wires a callback that publishes results to the bus.
func (a *PicoToolAdapter) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	// 1. Deserialize Fantasy's JSON string input into PicoClaw's map format.
	args, err := parseToolArgs(call.Input)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	// 2. Set context for ContextualTool implementations.
	if ct, ok := a.inner.(tools.ContextualTool); ok {
		ct.SetContext(a.channel, a.chatID)
	}

	// 3. Wire async callback for AsyncTool implementations.
	if at, ok := a.inner.(tools.AsyncTool); ok {
		at.SetCallback(func(_ context.Context, result *tools.ToolResult) {
			if result != nil && result.ForUser != "" && !result.Silent && a.bus != nil {
				a.bus.PublishOutbound(bus.OutboundMessage{
					Channel: a.channel,
					ChatID:  a.chatID,
					Content: result.ForUser,
				})
			}
		})
	}

	// 4. Execute the PicoClaw tool.
	result := a.inner.Execute(ctx, args)
	if result == nil {
		return fantasy.NewTextErrorResponse("tool returned nil result"), nil
	}

	// 5. Dual-channel: publish ForUser content to bus (side effect).
	// Fantasy never sees this — only the LLM-facing content is returned.
	if result.ForUser != "" && !result.Silent && a.bus != nil {
		a.bus.PublishOutbound(bus.OutboundMessage{
			Channel: a.channel,
			ChatID:  a.chatID,
			Content: result.ForUser,
		})
	}

	// 6. Offload large tool results to archival memory if configured.
	if a.memStore != nil && !result.IsError && a.memStore.ShouldOffload(result.ForLLM) {
		refID, summary, err := a.memStore.OffloadToolResult(ctx, a.inner.Name(), result.ForLLM, a.agentID, a.sessionKey)
		if err == nil {
			logger.DebugCF("adapter", "Offloaded large tool result to archival",
				map[string]interface{}{
					"tool":   a.inner.Name(),
					"ref_id": refID,
					"bytes":  len(result.ForLLM),
				})
			result.ForLLM = summary
		}
		// On error, fall through with original result
	}

	// 7. Return only the LLM-facing content to Fantasy.
	if result.IsError {
		return fantasy.NewTextErrorResponse(result.ForLLM), nil
	}
	return fantasy.NewTextResponse(result.ForLLM), nil
}

// ProviderOptions returns nil — PicoClaw tools have no provider-specific options.
func (a *PicoToolAdapter) ProviderOptions() fantasy.ProviderOptions {
	return fantasy.ProviderOptions{}
}

// SetProviderOptions is a no-op for PicoClaw tools.
func (a *PicoToolAdapter) SetProviderOptions(_ fantasy.ProviderOptions) {}

// AdaptedToolsConfig configures how tools are adapted for the Fantasy agent.
type AdaptedToolsConfig struct {
	MemStore   *memstore.MemoryStore // optional: enables tool result offloading
	AgentID    string
	SessionKey string
}

// BuildAdaptedTools wraps visible tools in a ToolRegistry as Fantasy AgentTools.
// In progressive disclosure mode, only gateway tools (tool_search, tool_call, memory, etc.)
// are exposed to Fantasy. The agent discovers and invokes other tools via tool_search + tool_call.
func BuildAdaptedTools(registry *tools.ToolRegistry, msgBus *bus.MessageBus, channel, chatID string, opts ...AdaptedToolsConfig) []fantasy.AgentTool {
	if registry == nil {
		return nil
	}

	var cfg AdaptedToolsConfig
	if len(opts) > 0 {
		cfg = opts[0]
	}

	names := registry.ListVisible()
	adapted := make([]fantasy.AgentTool, 0, len(names))

	for _, name := range names {
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}
		adapted = append(adapted, &PicoToolAdapter{
			inner:      tool,
			bus:        msgBus,
			channel:    channel,
			chatID:     chatID,
			memStore:   cfg.MemStore,
			agentID:    cfg.AgentID,
			sessionKey: cfg.SessionKey,
		})
	}

	return adapted
}

// parseToolArgs deserializes a JSON string into a map.
// Handles both JSON objects and empty inputs gracefully.
func parseToolArgs(input string) (map[string]interface{}, error) {
	if input == "" || input == "{}" {
		return map[string]interface{}{}, nil
	}

	var args map[string]interface{}
	if err := jsonv2.Unmarshal([]byte(input), &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	return args, nil
}
