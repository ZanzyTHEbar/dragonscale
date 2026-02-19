// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"

	fantasy "charm.land/fantasy"
	picofantasy "github.com/sipeed/picoclaw/pkg/fantasy"
	"github.com/sipeed/picoclaw/pkg/logger"
	memstore "github.com/sipeed/picoclaw/pkg/memory/store"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// RunToolLoop executes an agent tool loop using Fantasy with the canonical
// PicoToolAdapter (schema unwrapping + offloading). This is the single
// implementation used by both main agent and subagents.
func RunToolLoop(ctx context.Context, config tools.ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*tools.ToolLoopResult, error) {
	return runToolLoopWithMem(ctx, config, systemPrompt, userPrompt, channel, chatID, nil)
}

// MakeRunLoopFunc returns a RunLoopFunc that uses the given MemoryStore for
// tool result offloading. This is wired into SubagentManager so subagent tool
// results get offloaded to archival memory.
func MakeRunLoopFunc(ms *memstore.MemoryStore) tools.RunLoopFunc {
	return func(ctx context.Context, config tools.ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*tools.ToolLoopResult, error) {
		return runToolLoopWithMem(ctx, config, systemPrompt, userPrompt, channel, chatID, ms)
	}
}

func runToolLoopWithMem(ctx context.Context, config tools.ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string, ms *memstore.MemoryStore) (*tools.ToolLoopResult, error) {
	adaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   ms,
		AgentID:    "picoclaw",
		SessionKey: "",
	}
	adaptedTools := picofantasy.BuildAdaptedTools(config.Tools, config.Bus, channel, chatID, adaptCfg)

	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(config.MaxIterations)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}
	agent := fantasy.NewAgent(config.Model, agentOpts...)

	logger.DebugCF("toolloop", "Agent created",
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

	logger.InfoCF("toolloop", "Tool loop completed",
		map[string]any{
			"steps":         stepCount,
			"content_chars": len(finalContent),
		})

	return &tools.ToolLoopResult{
		Content:    finalContent,
		Iterations: stepCount,
	}, nil
}
