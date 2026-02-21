// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package tools

import (
	"context"
	"errors"
	"fmt"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
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

// ErrRunLoopNotConfigured indicates that the unified loop runner was not
// injected into tools runtime wiring.
var ErrRunLoopNotConfigured = errors.New("subagent run loop is not configured")

// RunToolLoop is the package-level fallback used when no injected loop runner
// is available. It returns an explicit contract error because the concrete
// implementation lives in pkg/agent and must be injected via
// SubagentManager.SetRunLoop.
func RunToolLoop(_ context.Context, _ ToolLoopConfig, _, _, _, _ string) (*ToolLoopResult, error) {
	return nil, fmt.Errorf("%w: wire agent.MakeUnifiedRunLoopFunc via SubagentManager.SetRunLoop", ErrRunLoopNotConfigured)
}
