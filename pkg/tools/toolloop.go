// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"context"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
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

// RunToolLoop is the package-level fallback used when no injected loop runner
// is available. It panics because the concrete implementation lives in
// pkg/agent and must be injected via SubagentManager.SetRunLoop.
func RunToolLoop(_ context.Context, _ ToolLoopConfig, _, _, _, _ string) (*ToolLoopResult, error) {
	panic("tools.RunToolLoop called without injected loop runner; wire agent.MakeRunLoopFunc via SubagentManager.SetRunLoop")
}
