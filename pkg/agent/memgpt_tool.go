// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package agent

import (
	"context"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"

	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// MemGPTTool wraps store.MemoryTool as a DragonScale tools.Tool so it can be
// registered in the ToolRegistry and executed by the Fantasy agent loop.
type MemGPTTool struct {
	store        *memstore.MemoryStore
	agentID      string
	session      string
	sessionKeyFn func() string
}

var _ tools.Tool = (*MemGPTTool)(nil)

// NewMemGPTTool creates a DragonScale tool wrapper around a MemoryTool.
func NewMemGPTTool(store *memstore.MemoryStore, agentID, session string) *MemGPTTool {
	return &MemGPTTool{
		store:   store,
		agentID: agentID,
		session: session,
	}
}

func (t *MemGPTTool) Name() string { return "memory" }

func (t *MemGPTTool) Description() string {
	return "Manage the agent's 3-tier memory system. Actions: search (hybrid keyword+vector), read (by ID), write (to recall or archival), update, delete, status (context pressure). All memory persists across sessions."
}

func (t *MemGPTTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "The memory operation to perform.",
				"enum":        []string{"search", "read", "write", "update", "delete", "status"},
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (for action=search).",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Memory ID (for action=read/update/delete).",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to store or update (for action=write/update).",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Source label for archival writes.",
			},
			"sector": map[string]interface{}{
				"type":        "string",
				"description": "Memory sector: episodic, semantic, procedural, reflective.",
				"enum":        []string{"episodic", "semantic", "procedural", "reflective"},
			},
			"tags": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated tags for the memory entry.",
			},
			"tier": map[string]interface{}{
				"type":        "string",
				"description": "Storage tier: recall (warm, default) or archival (cold, chunked+embedded).",
				"enum":        []string{"recall", "archival"},
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Max results for search. Default: 5.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MemGPTTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	// Marshal the args back to JSON for the inner MemoryTool.Execute()
	input, err := jsonv2.Marshal(args)
	if err != nil {
		return tools.ErrorResult("invalid arguments: " + err.Error())
	}

	result, err := memstore.NewMemoryTool(t.store, t.agentID, t.currentSession()).Execute(ctx, string(input))
	if err != nil {
		return tools.ErrorResult("memory tool error: " + err.Error())
	}

	return &tools.ToolResult{
		ForLLM: result,
	}
}

// UpdateSession rebinds the inner MemoryTool to a new session.
// Called when the agent switches sessions.
func (t *MemGPTTool) UpdateSession(store *memstore.MemoryStore, agentID, session string) {
	t.store = store
	t.agentID = agentID
	t.session = session
}

func (t *MemGPTTool) SetSessionResolver(sessionKeyFn func() string) {
	t.sessionKeyFn = sessionKeyFn
}

func (t *MemGPTTool) currentSession() string {
	if t.sessionKeyFn != nil {
		if sessionKey := strings.TrimSpace(t.sessionKeyFn()); sessionKey != "" {
			return sessionKey
		}
	}
	if strings.TrimSpace(t.session) != "" {
		return t.session
	}
	return "default"
}
