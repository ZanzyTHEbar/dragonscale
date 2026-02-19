// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"context"
	"fmt"

	jsonv2 "github.com/go-json-experiment/json"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ToolLoopMode controls whether the agent uses the sequential ReAct loop,
// the parallel DAG executor, or lets the router decide automatically.
type ToolLoopMode int

const (
	ModeReAct ToolLoopMode = iota
	ModeDAG
	ModeAuto
)

// DAGRunResult holds the output from a DAG execution.
type DAGRunResult struct {
	Answer     string
	Tokens     uint32
	Iterations int
}

// DAGRunFunc executes a query through the DAG planner/executor pipeline.
// This function type breaks the import cycle between tools → dag → securebus → tools.
// The concrete implementation is wired in the application entry point.
type DAGRunFunc func(ctx context.Context, sessionKey, query string, availableTools []string) (*DAGRunResult, error)

// RouteFunc classifies a query and returns the preferred execution mode.
// When nil, all queries use ModeReAct.
type RouteFunc func(mode ToolLoopMode, query string) ToolLoopMode

// ToolLoopConfig configures the tool execution loop.
type ToolLoopConfig struct {
	Model         fantasy.LanguageModel
	ModelID       string
	Tools         *ToolRegistry
	Bus           *bus.MessageBus
	MaxIterations int

	// DAGRunner executes queries through the DAG planner/executor pipeline.
	// When nil, all queries use the sequential ReAct loop.
	DAGRunner DAGRunFunc

	// Router classifies queries into ModeReAct or ModeDAG. When nil,
	// ModeReAct is always used.
	Router RouteFunc

	// LoopMode controls execution routing. Default: ModeAuto.
	LoopMode ToolLoopMode
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
}

// RunToolLoop executes the agent loop with PicoClaw tools. It supports two
// execution modes:
//   - ReAct (sequential): Fantasy's step-by-step tool calling loop
//   - DAG (parallel): LLMCompiler-style DAG planning and execution
//
// When LoopMode is ModeAuto, the router classifies the query to pick the
// optimal mode. The SecureBus enforces capabilities in both modes.
func RunToolLoop(ctx context.Context, config ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*ToolLoopResult, error) {
	mode := config.LoopMode
	if config.Router != nil {
		mode = config.Router(mode, userPrompt)
	} else if mode == ModeAuto {
		mode = ModeReAct
	}

	if mode == ModeDAG && config.DAGRunner != nil {
		return runDAGLoop(ctx, config, userPrompt, channel)
	}

	return runReActLoop(ctx, config, systemPrompt, userPrompt, channel, chatID)
}

// runReActLoop is the original sequential Fantasy agent loop.
func runReActLoop(ctx context.Context, config ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*ToolLoopResult, error) {
	adaptedTools := BuildAdaptedToolsFromRegistry(config.Tools, config.Bus, channel, chatID)

	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(config.MaxIterations)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}
	agent := fantasy.NewAgent(config.Model, agentOpts...)

	logger.DebugCF("toolloop", "ReAct mode: Fantasy agent created",
		map[string]any{
			"tools_count":    len(adaptedTools),
			"max_iterations": config.MaxIterations,
		})

	result, err := agent.Generate(ctx, fantasy.AgentCall{
		Prompt: userPrompt,
	})
	if err != nil {
		logger.ErrorCF("toolloop", "Fantasy agent.Generate failed",
			map[string]any{"error": err.Error()})
		return nil, fmt.Errorf("agent Generate failed: %w", err)
	}

	finalContent := result.Response.Content.Text()
	stepCount := len(result.Steps)

	logger.InfoCF("toolloop", "ReAct loop completed",
		map[string]any{
			"steps":         stepCount,
			"content_chars": len(finalContent),
		})

	return &ToolLoopResult{
		Content:    finalContent,
		Iterations: stepCount,
	}, nil
}

// runDAGLoop uses the LLMCompiler-style DAG executor with replanning.
func runDAGLoop(ctx context.Context, config ToolLoopConfig, query, sessionKey string) (*ToolLoopResult, error) {
	logger.InfoCF("toolloop", "DAG mode: planning and executing",
		map[string]any{"query_len": len(query)})

	availableTools := config.Tools.List()

	result, err := config.DAGRunner(ctx, sessionKey, query, availableTools)
	if err != nil {
		logger.ErrorCF("toolloop", "DAG execution failed",
			map[string]any{"error": err.Error()})
		return nil, fmt.Errorf("DAG execution failed: %w", err)
	}

	logger.InfoCF("toolloop", "DAG loop completed",
		map[string]any{
			"iterations":   result.Iterations,
			"total_tokens": result.Tokens,
			"answer_chars": len(result.Answer),
		})

	return &ToolLoopResult{
		Content:    result.Answer,
		Iterations: result.Iterations,
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
	if err := jsonv2.Unmarshal([]byte(input), &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	return args, nil
}
