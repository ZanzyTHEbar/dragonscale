// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ToolLoopConfig configures the tool execution loop.
type ToolLoopConfig struct {
	Model         fantasy.LanguageModel
	ModelID       string
	Tools         *ToolRegistry
	Bus           *bus.MessageBus
	MaxIterations int
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
}

// RunToolLoop executes the Fantasy agent loop with PicoClaw tools.
// This is the core agent logic reused by both main agent and subagents.
func RunToolLoop(ctx context.Context, config ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*ToolLoopResult, error) {
	// Build adapted tools
	adaptedTools := BuildAdaptedToolsFromRegistry(config.Tools, config.Bus, channel, chatID)

	// Create Fantasy agent
	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(config.MaxIterations)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}
	agent := fantasy.NewAgent(config.Model, agentOpts...)

	logger.DebugCF("toolloop", "Fantasy agent created for tool loop",
		map[string]any{
			"tools_count":    len(adaptedTools),
			"max_iterations": config.MaxIterations,
		})

	// Run Fantasy agent
	result, err := agent.Generate(ctx, fantasy.AgentCall{
		Prompt: userPrompt,
	})
	if err != nil {
		logger.ErrorCF("toolloop", "Fantasy agent.Generate failed",
			map[string]any{
				"error": err.Error(),
			})
		return nil, fmt.Errorf("agent Generate failed: %w", err)
	}

	finalContent := result.Response.Content.Text()
	stepCount := len(result.Steps)

	logger.InfoCF("toolloop", "Tool loop completed",
		map[string]any{
			"steps":         stepCount,
			"content_chars": len(finalContent),
		})

	return &ToolLoopResult{
		Content:    finalContent,
		Iterations: stepCount,
	}, nil
}

// BuildAdaptedToolsFromRegistry wraps all tools in a ToolRegistry as Fantasy AgentTools.
// This is a local wrapper that avoids circular imports by duplicating the adapter logic.
func BuildAdaptedToolsFromRegistry(registry *ToolRegistry, msgBus *bus.MessageBus, channel, chatID string) []fantasy.AgentTool {
	if registry == nil {
		return nil
	}

	names := registry.List()
	adapted := make([]fantasy.AgentTool, 0, len(names))

	for _, name := range names {
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}
		adapted = append(adapted, &picoToolAdapter{
			inner:   tool,
			bus:     msgBus,
			channel: channel,
			chatID:  chatID,
		})
	}

	return adapted
}

// picoToolAdapter wraps a PicoClaw tool as a Fantasy AgentTool.
// This is a local copy to avoid circular imports with pkg/fantasy.
type picoToolAdapter struct {
	inner   Tool
	bus     *bus.MessageBus
	channel string
	chatID  string
}

func (a *picoToolAdapter) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        a.inner.Name(),
		Description: a.inner.Description(),
		Parameters:  a.inner.Parameters(),
	}
}

func (a *picoToolAdapter) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	args, err := parseToolCallArgs(call.Input)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if ct, ok := a.inner.(ContextualTool); ok {
		ct.SetContext(a.channel, a.chatID)
	}

	if at, ok := a.inner.(AsyncTool); ok {
		at.SetCallback(func(_ context.Context, result *ToolResult) {
			if result != nil && result.ForUser != "" && !result.Silent && a.bus != nil {
				a.bus.PublishOutbound(bus.OutboundMessage{
					Channel: a.channel,
					ChatID:  a.chatID,
					Content: result.ForUser,
				})
			}
		})
	}

	result := a.inner.Execute(ctx, args)
	if result == nil {
		return fantasy.NewTextErrorResponse("tool returned nil result"), nil
	}

	if result.ForUser != "" && !result.Silent && a.bus != nil {
		a.bus.PublishOutbound(bus.OutboundMessage{
			Channel: a.channel,
			ChatID:  a.chatID,
			Content: result.ForUser,
		})
	}

	if result.IsError {
		return fantasy.NewTextErrorResponse(result.ForLLM), nil
	}
	return fantasy.NewTextResponse(result.ForLLM), nil
}

func (a *picoToolAdapter) ProviderOptions() fantasy.ProviderOptions {
	return fantasy.ProviderOptions{}
}

func (a *picoToolAdapter) SetProviderOptions(_ fantasy.ProviderOptions) {}

// parseToolCallArgs deserializes JSON input string into args map.
func parseToolCallArgs(input string) (map[string]interface{}, error) {
	if input == "" || input == "{}" {
		return map[string]interface{}{}, nil
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	return args, nil
}
