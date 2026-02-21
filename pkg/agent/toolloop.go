// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package agent

import (
	"context"
	"fmt"
	"strings"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	picofantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// MakeUnifiedRunLoopFunc wires subagent execution through the same unified
// runtime stack used by the main loop: SecureBus + offloading + run state.
func MakeUnifiedRunLoopFunc(al *AgentLoop) tools.RunLoopFunc {
	return func(ctx context.Context, config tools.ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*tools.ToolLoopResult, error) {
		if al == nil {
			return nil, fmt.Errorf("agent loop is nil")
		}
		if al.secureBus == nil || al.queries == nil || al.kvDelegate == nil || al.stateStore == nil {
			return nil, fmt.Errorf("unified runtime dependencies are not initialized")
		}

		baseSession := fmt.Sprintf("%s:%s", channel, chatID)
		if v := al.activeSessionKey.Load(); v != nil {
			if active, ok := v.(string); ok && strings.TrimSpace(active) != "" {
				baseSession = active
			}
		}
		if strings.TrimSpace(baseSession) == "" {
			baseSession = "subagent:default"
		}
		sessionKey := baseSession + "::subagent"

		conversationID, runID, err := al.prepareRuntimeState(ctx, sessionKey)
		if err != nil {
			return nil, err
		}

		baseRuntime := OffloadingToolRuntime{
			Base:           fantasy.DAGToolRuntime{MaxConcurrency: defaultToolMaxConcurrency},
			KV:             al.kvDelegate,
			Queries:        al.queries,
			ConversationID: conversationID,
			RunID:          runID,
		}
		toolRuntime := SecureBusToolRuntime{
			Base:       baseRuntime,
			Bus:        al.secureBus,
			SessionKey: sessionKey,
			StateStore: al.stateStore,
			RunID:      runID,
		}

		extraTools := make([]fantasy.AgentTool, 0, 1)
		if al.toolResultSearch != nil {
			extraTools = append(extraTools, al.toolResultSearch)
		}

		al.sessions.AddMessage(sessionKey, "user", userPrompt)
		result, err := runToolLoopWithRuntime(ctx, config, systemPrompt, userPrompt, channel, chatID, al.memoryStore, sessionKey, toolRuntime, extraTools)
		if err != nil {
			return nil, err
		}
		al.sessions.AddMessage(sessionKey, "assistant", result.Content)
		al.sessions.Save(sessionKey)
		al.maybeSummarize(ctx, sessionKey, channel, chatID)
		return result, nil
	}
}

func runToolLoopWithRuntime(
	ctx context.Context,
	config tools.ToolLoopConfig,
	systemPrompt, userPrompt, channel, chatID string,
	ms *memstore.MemoryStore,
	sessionKey string,
	toolRuntime fantasy.ToolRuntime,
	extraTools []fantasy.AgentTool,
) (*tools.ToolLoopResult, error) {
	if toolRuntime == nil {
		return nil, fmt.Errorf("tool runtime is required")
	}

	adaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   ms,
		AgentID:    pkg.NAME,
		SessionKey: sessionKey,
	}
	adaptedTools := picofantasy.BuildAdaptedTools(config.Tools, config.Bus, channel, chatID, adaptCfg)
	if len(extraTools) > 0 {
		adaptedTools = append(adaptedTools, extraTools...)
	}
	promotedSet := make(map[string]bool, len(adaptedTools))
	for _, at := range adaptedTools {
		promotedSet[at.Info().Name] = true
	}

	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(config.MaxIterations)),
		fantasy.WithToolRuntime(toolRuntime),
	}
	if config.Tools != nil {
		prepareStep := func(ctx context.Context, _ fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
			discovered := config.Tools.DrainDiscovered()
			if len(discovered) == 0 {
				return ctx, fantasy.PrepareStepResult{}, nil
			}
			var newTools []tools.Tool
			for _, t := range discovered {
				if promotedSet[t.Name()] {
					continue
				}
				newTools = append(newTools, t)
				promotedSet[t.Name()] = true
			}
			if len(newTools) == 0 {
				return ctx, fantasy.PrepareStepResult{}, nil
			}
			newAdapted := picofantasy.AdaptTools(newTools, config.Bus, channel, chatID, adaptCfg)
			adaptedTools = append(adaptedTools, newAdapted...)
			return ctx, fantasy.PrepareStepResult{Tools: adaptedTools}, nil
		}
		agentOpts = append(agentOpts, fantasy.WithPrepareStep(prepareStep))
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
