package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

// MemoryToolAction is the action to perform on the memory system.
type MemoryToolAction string

const (
	MemoryToolSearch    MemoryToolAction = "search" // Hybrid search across all tiers
	MemoryToolRead      MemoryToolAction = "read"   // Read a specific memory by ID
	MemoryToolWrite     MemoryToolAction = "write"  // Write a new memory to recall or archival
	MemoryToolUpdate    MemoryToolAction = "update" // Update an existing memory
	MemoryToolDelete    MemoryToolAction = "delete" // Delete a memory by ID
	MemoryToolGetStatus MemoryToolAction = "status" // Get memory system status / context pressure
)

// MemoryToolRequest is the input to the memory tool.
type MemoryToolRequest struct {
	Action  MemoryToolAction `json:"action"`
	Query   string           `json:"query,omitempty"`   // For search
	ID      string           `json:"id,omitempty"`      // For read/update/delete
	Content string           `json:"content,omitempty"` // For write/update
	Source  string           `json:"source,omitempty"`  // For write
	Sector  string           `json:"sector,omitempty"`  // For write: episodic/semantic/procedural/reflective
	Tags    string           `json:"tags,omitempty"`    // For write: comma-separated
	Tier    string           `json:"tier,omitempty"`    // "recall" or "archival" — defaults to "recall"
	Limit   int              `json:"limit,omitempty"`   // For search — defaults to 5
}

// MemoryToolResponse is the output of the memory tool.
type MemoryToolResponse struct {
	Success bool              `json:"success"`
	Message string            `json:"message,omitempty"`
	Results []MemoryToolEntry `json:"results,omitempty"`
	Status  *MemoryToolStatus `json:"status,omitempty"`
}

// MemoryToolEntry is a single memory entry in tool results.
type MemoryToolEntry struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Source  string  `json:"source,omitempty"`
	Sector  string  `json:"sector,omitempty"`
	Score   float64 `json:"score,omitempty"`
}

// MemoryToolStatus summarizes the memory system state.
type MemoryToolStatus struct {
	WorkingContextTokens int     `json:"working_context_tokens"`
	RecallItemCount      int     `json:"recall_item_count"`
	ArchivalChunkCount   int     `json:"archival_chunk_count"`
	UsageRatio           float64 `json:"usage_ratio"`
	PressureLevel        string  `json:"pressure_level"`
}

// MemoryTool provides the agent with a unified interface to the memory system.
// It is designed to be registered as a tool in the agent's tool registry.
type MemoryTool struct {
	store   *MemoryStore
	agentID string
	session string
}

// NewMemoryTool creates a MemoryTool bound to a specific agent and session.
func NewMemoryTool(store *MemoryStore, agentID, session string) *MemoryTool {
	return &MemoryTool{
		store:   store,
		agentID: agentID,
		session: session,
	}
}

// Execute processes a memory tool request and returns a JSON response.
func (t *MemoryTool) Execute(ctx context.Context, input string) (string, error) {
	var req MemoryToolRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return t.errorResponse("invalid input: " + err.Error()), nil
	}

	var resp *MemoryToolResponse
	var err error

	switch req.Action {
	case MemoryToolSearch:
		resp, err = t.search(ctx, &req)
	case MemoryToolRead:
		resp, err = t.read(ctx, &req)
	case MemoryToolWrite:
		resp, err = t.write(ctx, &req)
	case MemoryToolUpdate:
		resp, err = t.update(ctx, &req)
	case MemoryToolDelete:
		resp, err = t.deleteMem(ctx, &req)
	case MemoryToolGetStatus:
		resp, err = t.status(ctx)
	default:
		resp = &MemoryToolResponse{
			Success: false,
			Message: fmt.Sprintf("unknown action: %s. Valid: search, read, write, update, delete, status", req.Action),
		}
	}

	if err != nil {
		return t.errorResponse(err.Error()), nil
	}
	return t.jsonResponse(resp), nil
}

func (t *MemoryTool) search(ctx context.Context, req *MemoryToolRequest) (*MemoryToolResponse, error) {
	if req.Query == "" {
		return &MemoryToolResponse{Success: false, Message: "query is required for search"}, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}

	var sectors []memory.Sector
	if req.Sector != "" {
		sectors = []memory.Sector{memory.Sector(req.Sector)}
	}

	results, err := t.store.Search(ctx, req.Query, memory.SearchOptions{
		AgentID: t.agentID,
		Sectors: sectors,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}

	entries := make([]MemoryToolEntry, len(results))
	for i, r := range results {
		entries[i] = MemoryToolEntry{
			ID:      r.ID.String(),
			Content: r.Content,
			Source:  r.Source,
			Sector:  string(r.Sector),
			Score:   r.Score,
		}
	}

	return &MemoryToolResponse{
		Success: true,
		Message: fmt.Sprintf("Found %d results for: %s", len(entries), req.Query),
		Results: entries,
	}, nil
}

func (t *MemoryTool) read(ctx context.Context, req *MemoryToolRequest) (*MemoryToolResponse, error) {
	if req.ID == "" {
		return &MemoryToolResponse{Success: false, Message: "id is required for read"}, nil
	}

	id, err := ids.Parse(req.ID)
	if err != nil {
		return &MemoryToolResponse{Success: false, Message: "invalid id: " + req.ID}, nil
	}

	// Try recall first
	item, err := t.store.GetRecall(ctx, id)
	if err != nil {
		return nil, err
	}
	if item != nil {
		return &MemoryToolResponse{
			Success: true,
			Results: []MemoryToolEntry{{
				ID:      item.ID.String(),
				Content: item.Content,
				Sector:  string(item.Sector),
			}},
		}, nil
	}

	// Try archival
	content, err := t.store.RetrieveArchival(ctx, id)
	if err != nil {
		return &MemoryToolResponse{Success: false, Message: "memory not found: " + req.ID}, nil
	}

	return &MemoryToolResponse{
		Success: true,
		Results: []MemoryToolEntry{{
			ID:      req.ID,
			Content: content,
		}},
	}, nil
}

func (t *MemoryTool) write(ctx context.Context, req *MemoryToolRequest) (*MemoryToolResponse, error) {
	if req.Content == "" {
		return &MemoryToolResponse{Success: false, Message: "content is required for write"}, nil
	}

	tier := strings.ToLower(req.Tier)
	sector := memory.Sector(req.Sector)
	if sector == "" {
		sector = memory.SectorSemantic
	}

	if tier == "archival" {
		refID, err := t.store.StoreArchival(ctx, req.Content, req.Source, map[string]string{
			"agent_id":    t.agentID,
			"session_key": t.session,
			"tags":        req.Tags,
			"sector":      string(sector),
		})
		if err != nil {
			return nil, err
		}
		return &MemoryToolResponse{
			Success: true,
			Message: fmt.Sprintf("Stored in archival tier with ID: %s", refID.String()),
		}, nil
	}

	// Default: recall tier
	item := &memory.RecallItem{
		AgentID:    t.agentID,
		SessionKey: t.session,
		Role:       "system",
		Sector:     sector,
		Importance: 0.7, // default; scorer will refine later
		Content:    req.Content,
		Tags:       req.Tags,
	}

	if err := t.store.StoreRecall(ctx, item); err != nil {
		return nil, err
	}

	return &MemoryToolResponse{
		Success: true,
		Message: fmt.Sprintf("Stored in recall tier with ID: %s", item.ID.String()),
	}, nil
}

func (t *MemoryTool) update(ctx context.Context, req *MemoryToolRequest) (*MemoryToolResponse, error) {
	if req.ID == "" || req.Content == "" {
		return &MemoryToolResponse{Success: false, Message: "id and content are required for update"}, nil
	}

	id, err := ids.Parse(req.ID)
	if err != nil {
		return &MemoryToolResponse{Success: false, Message: "invalid id: " + req.ID}, nil
	}

	item, err := t.store.GetRecall(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return &MemoryToolResponse{Success: false, Message: "recall item not found: " + req.ID}, nil
	}

	item.Content = req.Content
	if req.Tags != "" {
		item.Tags = req.Tags
	}
	if req.Sector != "" {
		item.Sector = memory.Sector(req.Sector)
	}

	if err := t.store.UpdateRecall(ctx, item); err != nil {
		return nil, err
	}

	return &MemoryToolResponse{
		Success: true,
		Message: fmt.Sprintf("Updated recall item: %s", req.ID),
	}, nil
}

func (t *MemoryTool) deleteMem(ctx context.Context, req *MemoryToolRequest) (*MemoryToolResponse, error) {
	if req.ID == "" {
		return &MemoryToolResponse{Success: false, Message: "id is required for delete"}, nil
	}

	id, err := ids.Parse(req.ID)
	if err != nil {
		return &MemoryToolResponse{Success: false, Message: "invalid id: " + req.ID}, nil
	}

	if err := t.store.DeleteRecall(ctx, id); err != nil {
		return nil, err
	}

	return &MemoryToolResponse{
		Success: true,
		Message: fmt.Sprintf("Deleted memory: %s", req.ID),
	}, nil
}

func (t *MemoryTool) status(ctx context.Context) (*MemoryToolResponse, error) {
	pressure, err := t.store.ContextUsage(ctx, t.agentID, t.session)
	if err != nil {
		return nil, err
	}

	return &MemoryToolResponse{
		Success: true,
		Status: &MemoryToolStatus{
			WorkingContextTokens: pressure.WorkingContextTokens,
			RecallItemCount:      pressure.RecallItemCount,
			ArchivalChunkCount:   pressure.ArchivalChunkCount,
			UsageRatio:           pressure.UsageRatio,
			PressureLevel:        string(pressure.PressureLevel),
		},
	}, nil
}

func (t *MemoryTool) errorResponse(msg string) string {
	return t.jsonResponse(&MemoryToolResponse{Success: false, Message: msg})
}

func (t *MemoryTool) jsonResponse(resp *MemoryToolResponse) string {
	b, _ := json.Marshal(resp)
	return string(b)
}
