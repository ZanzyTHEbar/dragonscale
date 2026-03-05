package delegate

import (
	"context"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

// setupRLTest creates an in-memory delegate with initialized schema for RL tests.
func setupRLTest(t *testing.T) *LibSQLDelegate {
	t.Helper()
	d, err := NewLibSQLInMemory()
	if err != nil {
		t.Fatalf("NewLibSQLInMemory: %v", err)
	}
	if err := d.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// insertTestRecallItem creates a recall item for testing RL operations.
func insertTestRecallItem(ctx context.Context, t *testing.T, d *LibSQLDelegate, agentID string) ids.UUID {
	t.Helper()
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: "test-session",
		Role:       "assistant",
		Sector:     memory.SectorEpisodic,
		Importance: 0.8,
		Salience:   0.6,
		DecayRate:  0.01,
		Content:    "Test content for RL weight updates",
		Tags:       "test,rl",
	}
	if err := d.InsertRecallItem(ctx, item); err != nil {
		t.Fatalf("InsertRecallItem: %v", err)
	}
	return item.ID
}

func TestSQLiteDelegate_GetTaskBaseline_NoBaseline(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Get baseline for agent without one - should return nil
	baseline, err := d.GetTaskBaseline(ctx, "new-agent")
	if err != nil {
		t.Fatalf("GetTaskBaseline: %v", err)
	}
	if baseline != nil {
		t.Error("expected nil baseline for new agent")
	}
}

func TestSQLiteDelegate_GetTaskBaseline_AfterUpdate(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	agentID := "test-agent"

	// Initially no baseline
	baseline, err := d.GetTaskBaseline(ctx, agentID)
	if err != nil {
		t.Fatalf("GetTaskBaseline: %v", err)
	}
	if baseline != nil {
		t.Error("expected nil baseline initially")
	}

	// Update baseline
	newBaseline := &TaskBaseline{
		Count:               10,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            5000,
		M2Errors:            50,
		M2UserCorrections:   20,
	}
	if err := d.UpdateTaskBaseline(ctx, agentID, newBaseline); err != nil {
		t.Fatalf("UpdateTaskBaseline: %v", err)
	}

	// Get baseline again
	baseline, err = d.GetTaskBaseline(ctx, agentID)
	if err != nil {
		t.Fatalf("GetTaskBaseline after update: %v", err)
	}
	if baseline == nil {
		t.Fatal("expected non-nil baseline after update")
	}

	// Verify values
	if baseline.Count != 10 {
		t.Errorf("Count = %d, want 10", baseline.Count)
	}
	if baseline.MeanTokens != 1000 {
		t.Errorf("MeanTokens = %f, want 1000", baseline.MeanTokens)
	}
	if baseline.MeanErrors != 5 {
		t.Errorf("MeanErrors = %f, want 5", baseline.MeanErrors)
	}
	if baseline.MeanUserCorrections != 2 {
		t.Errorf("MeanUserCorrections = %f, want 2", baseline.MeanUserCorrections)
	}
	if baseline.M2Tokens != 5000 {
		t.Errorf("M2Tokens = %f, want 5000", baseline.M2Tokens)
	}
	if baseline.M2Errors != 50 {
		t.Errorf("M2Errors = %f, want 50", baseline.M2Errors)
	}
	if baseline.M2UserCorrections != 20 {
		t.Errorf("M2UserCorrections = %f, want 20", baseline.M2UserCorrections)
	}
}

func TestSQLiteDelegate_UpdateTaskBaseline_MultipleUpdates(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	agentID := "test-agent"

	// First update
	baseline1 := &TaskBaseline{
		Count:      5,
		MeanTokens: 500,
	}
	if err := d.UpdateTaskBaseline(ctx, agentID, baseline1); err != nil {
		t.Fatalf("UpdateTaskBaseline (1): %v", err)
	}

	// Second update (should overwrite)
	baseline2 := &TaskBaseline{
		Count:               15,
		MeanTokens:          1500,
		MeanErrors:          10,
		MeanUserCorrections: 3,
		M2Tokens:            10000,
		M2Errors:            100,
		M2UserCorrections:   30,
	}
	if err := d.UpdateTaskBaseline(ctx, agentID, baseline2); err != nil {
		t.Fatalf("UpdateTaskBaseline (2): %v", err)
	}

	// Verify second values
	baseline, err := d.GetTaskBaseline(ctx, agentID)
	if err != nil {
		t.Fatalf("GetTaskBaseline: %v", err)
	}
	if baseline == nil {
		t.Fatal("expected non-nil baseline")
	}

	if baseline.Count != 15 {
		t.Errorf("Count = %d, want 15", baseline.Count)
	}
	if baseline.MeanTokens != 1500 {
		t.Errorf("MeanTokens = %f, want 1500", baseline.MeanTokens)
	}
	if baseline.MeanErrors != 10 {
		t.Errorf("MeanErrors = %f, want 10", baseline.MeanErrors)
	}
}

func TestSQLiteDelegate_UpdateMemoryWeight(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	agentID := "test-agent"

	// Insert a recall item first
	memoryID := insertTestRecallItem(ctx, t, d, agentID)

	// Update the memory weight via delegate API
	newWeight := 2.5
	credit := 3.0
	err := d.UpdateMemoryWeight(ctx, memoryID, newWeight, credit)
	if err != nil {
		t.Fatalf("UpdateMemoryWeight: %v", err)
	}

	// Verify the item still exists (GetRecallItem doesn't return RL fields)
	item, err := d.GetRecallItem(ctx, agentID, memoryID)
	if err != nil {
		t.Fatalf("GetRecallItem: %v", err)
	}
	if item == nil {
		t.Fatal("expected non-nil recall item")
	}
}

func TestSQLiteDelegate_UpdateMemoryWeight_NonExistent(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Try to update weight for non-existent memory.
	nonExistentID := ids.New()
	err := d.UpdateMemoryWeight(ctx, nonExistentID, 2.0, 1.0)
	if err == nil {
		t.Fatal("expected error for non-existent memory update")
	}
}

func TestSQLiteDelegate_UpdateMemorySelfReport(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	agentID := "test-agent"

	// Insert a recall item first
	memoryID := insertTestRecallItem(ctx, t, d, agentID)

	// Update self-report score via delegate API.
	err := d.UpdateMemorySelfReport(ctx, memoryID, 2)
	if err != nil {
		t.Fatalf("UpdateMemorySelfReportScore: %v", err)
	}

	// Verify the item still exists (GetRecallItem doesn't return self_report_score)
	item, err := d.GetRecallItem(ctx, agentID, memoryID)
	if err != nil {
		t.Fatalf("GetRecallItem: %v", err)
	}
	if item == nil {
		t.Fatal("expected non-nil recall item")
	}
}

func TestSQLiteDelegate_UpdateMemorySelfReport_NonExistent(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Try to update self-report for non-existent memory.
	nonExistentID := ids.New()
	err := d.UpdateMemorySelfReport(ctx, nonExistentID, 3)
	if err == nil {
		t.Fatal("expected error for non-existent memory self-report update")
	}
}

func TestSQLiteDelegate_StoreDetectedPattern(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Store a detected pattern
	pattern := DetectedPattern{
		Type:        "correction",
		Description: "Tool read_file was corrected from wrong path to correct path",
		Weight:      1.0,
		Category:    "correction",
		SessionID:   "session-123",
		AgentID:     "agent-456",
	}

	if err := d.StoreDetectedPattern(ctx, pattern); err != nil {
		t.Fatalf("StoreDetectedPattern: %v", err)
	}

	// Verify the pattern was stored as a recall item
	items, err := d.ListRecallItems(ctx, pattern.AgentID, pattern.SessionID, 10, 0)
	if err != nil {
		t.Fatalf("ListRecallItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 recall item, got %d", len(items))
	}

	item := items[0]
	if item.Content != pattern.Description {
		t.Errorf("Content = %q, want %q", item.Content, pattern.Description)
	}
	if item.Importance != pattern.Weight {
		t.Errorf("Importance = %f, want %f", item.Importance, pattern.Weight)
	}
	if item.Salience != pattern.Weight {
		t.Errorf("Salience = %f, want %f", item.Salience, pattern.Weight)
	}
	if item.Sector != memory.SectorReflective {
		t.Errorf("Sector = %v, want %v", item.Sector, memory.SectorReflective)
	}
	if item.Role != "system" {
		t.Errorf("Role = %q, want %q", item.Role, "system")
	}
}

func TestSQLiteDelegate_StoreDetectedPattern_Multiple(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	patterns := []DetectedPattern{
		{Type: "correction", Description: "Pattern 1", Weight: 1.0, Category: "correction", SessionID: "s1", AgentID: "a1"},
		{Type: "discovery", Description: "Pattern 2", Weight: 1.2, Category: "discovery", SessionID: "s1", AgentID: "a1"},
		{Type: "failure_pattern", Description: "Pattern 3", Weight: 1.5, Category: "correction", SessionID: "s2", AgentID: "a2"},
	}

	for _, pattern := range patterns {
		if err := d.StoreDetectedPattern(ctx, pattern); err != nil {
			t.Fatalf("StoreDetectedPattern: %v", err)
		}
	}

	// Check items in first session
	items1, err := d.ListRecallItems(ctx, "a1", "s1", 10, 0)
	if err != nil {
		t.Fatalf("ListRecallItems (a1/s1): %v", err)
	}
	if len(items1) != 2 {
		t.Errorf("expected 2 items in a1/s1, got %d", len(items1))
	}

	// Check items in second session
	items2, err := d.ListRecallItems(ctx, "a2", "s2", 10, 0)
	if err != nil {
		t.Fatalf("ListRecallItems (a2/s2): %v", err)
	}
	if len(items2) != 1 {
		t.Errorf("expected 1 item in a2/s2, got %d", len(items2))
	}
}

func TestSQLiteDelegate_GetCompletedTasks(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	tasks, err := d.GetCompletedTasks(ctx, "test-agent", time.Time{})
	if err != nil {
		t.Fatalf("GetCompletedTasks: %v", err)
	}
	// Should return empty list when no tasks exist
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestSQLiteDelegate_GetRetrievedMemories(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Drop and recreate tables with relaxed constraints for testing
	// (the real schema from Init() has FK constraints requiring valid conversations/runs)
	_, _ = d.db.ExecContext(ctx, `DROP TABLE IF EXISTS task_retrievals`)
	_, _ = d.db.ExecContext(ctx, `DROP TABLE IF EXISTS task_completions`)

	// Create test tables with relaxed constraints
	// Use WITHOUT ROWID for BLOB PRIMARY KEY to match schema.sql
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE task_completions (
			id BLOB PRIMARY KEY,
			agent_id TEXT NOT NULL,
			conversation_id BLOB NOT NULL DEFAULT (x'00000000000000000000000000000000'),
			run_id BLOB NOT NULL DEFAULT (x'00000000000000000000000000000000'),
			description TEXT,
			tokens_used INTEGER DEFAULT 0,
			tool_calls INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			user_corrections INTEGER DEFAULT 0,
			completed BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		) WITHOUT ROWID
	`)
	if err != nil {
		t.Fatalf("create task_completions table: %v", err)
	}

	_, err = d.db.ExecContext(ctx, `
		CREATE TABLE task_retrievals (
			id BLOB PRIMARY KEY,
			task_id BLOB NOT NULL,
			memory_id BLOB NOT NULL,
			similarity REAL NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			UNIQUE(task_id, memory_id)
		) WITHOUT ROWID
	`)
	if err != nil {
		t.Fatalf("create task_retrievals table: %v", err)
	}

	// Create a valid task ID
	taskID := ids.New()

	// Insert a task completion first (required for FK)
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO task_completions (id, agent_id, description, completed)
		VALUES (?, ?, ?, ?)
	`, taskID, "test-agent", "test task", true)
	if err != nil {
		t.Fatalf("insert task completion: %v", err)
	}

	// Insert a recall item to retrieve
	memoryID := insertTestRecallItem(ctx, t, d, "test-agent")

	// Store a task retrieval record
	err = d.queries.StoreTaskRetrieval(ctx, memsqlc.StoreTaskRetrievalParams{
		ID:         ids.New(),
		TaskID:     taskID,
		MemoryID:   memoryID,
		Similarity: 0.95,
	})
	if err != nil {
		t.Fatalf("StoreTaskRetrieval: %v", err)
	}

	// Update self-report score on the recall item
	selfReportScore := int64(3)
	err = d.queries.UpdateMemorySelfReportScore(ctx, memsqlc.UpdateMemorySelfReportScoreParams{
		SelfReportScore: &selfReportScore,
		ID:              memoryID,
		AgentID:         "test-agent",
	})
	if err != nil {
		t.Fatalf("UpdateMemorySelfReportScore: %v", err)
	}

	// Retrieve memories for the task
	memories, err := d.GetRetrievedMemories(ctx, taskID.String())
	if err != nil {
		t.Fatalf("GetRetrievedMemories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}

	// Verify the retrieved memory
	if memories[0].MemoryID != memoryID {
		t.Errorf("expected memory ID %s, got %s", memoryID, memories[0].MemoryID)
	}
	if memories[0].Similarity != 0.95 {
		t.Errorf("expected similarity 0.95, got %f", memories[0].Similarity)
	}
	if memories[0].SelfReportScore == nil || *memories[0].SelfReportScore != 3 {
		t.Errorf("expected self-report score 3, got %v", memories[0].SelfReportScore)
	}
}

func TestSQLiteDelegate_GetRetrievedMemories_InvalidTaskID(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Test with invalid task ID format
	_, err := d.GetRetrievedMemories(ctx, "invalid-task-id")
	if err == nil {
		t.Error("expected error for invalid task ID, got nil")
	}
}

func TestSQLiteDelegate_GetRetrievedMemories_Empty(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Drop and recreate tables with relaxed constraints for testing
	_, _ = d.db.ExecContext(ctx, `DROP TABLE IF EXISTS task_retrievals`)
	_, _ = d.db.ExecContext(ctx, `DROP TABLE IF EXISTS task_completions`)

	// Create test tables with relaxed constraints
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE task_completions (
			id BLOB PRIMARY KEY,
			agent_id TEXT NOT NULL,
			conversation_id BLOB NOT NULL DEFAULT (x'00000000000000000000000000000000'),
			run_id BLOB NOT NULL DEFAULT (x'00000000000000000000000000000000'),
			description TEXT,
			tokens_used INTEGER DEFAULT 0,
			tool_calls INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			user_corrections INTEGER DEFAULT 0,
			completed BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		) WITHOUT ROWID
	`)
	if err != nil {
		t.Fatalf("create task_completions table: %v", err)
	}

	_, err = d.db.ExecContext(ctx, `
		CREATE TABLE task_retrievals (
			id BLOB PRIMARY KEY,
			task_id BLOB NOT NULL,
			memory_id BLOB NOT NULL,
			similarity REAL NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			UNIQUE(task_id, memory_id)
		) WITHOUT ROWID
	`)
	if err != nil {
		t.Fatalf("create task_retrievals table: %v", err)
	}

	// Test with valid UUID but no retrievals
	taskID := ids.New()
	memories, err := d.GetRetrievedMemories(ctx, taskID.String())
	if err != nil {
		t.Fatalf("GetRetrievedMemories: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("expected 0 memories for task with no retrievals, got %d", len(memories))
	}
}

func TestSQLiteDelegate_GetRecentAuditEntries(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	since := time.Now().Add(-1 * time.Minute)

	// Insert some audit entries
	entries := []*memory.AuditEntry{
		{
			ID:         ids.New(),
			AgentID:    "audit-agent",
			SessionKey: "session-1",
			Action:     "read_file",
			Target:     "/path/to/file",
			Input:      `{"path": "/test"}`,
			Output:     "content",
		},
		{
			ID:         ids.New(),
			AgentID:    "audit-agent-2",
			SessionKey: "session-2",
			Action:     "write_file",
			Target:     "/path/to/output",
			Input:      `{"path": "/output"}`,
			Output:     "success",
		},
	}

	for _, entry := range entries {
		if err := d.InsertAuditEntry(ctx, entry); err != nil {
			t.Fatalf("InsertAuditEntry: %v", err)
		}
	}

	// Get recent audit entries across all agents.
	auditEntries, err := d.GetRecentAuditEntries(ctx, since)
	if err != nil {
		t.Fatalf("GetRecentAuditEntries: %v", err)
	}
	if len(auditEntries) != 2 {
		t.Fatalf("expected 2 recent audit entries, got %d", len(auditEntries))
	}

	foundAgents := map[string]bool{}
	foundTools := map[string]bool{}
	for _, entry := range auditEntries {
		foundAgents[entry.AgentID] = true
		foundTools[entry.ToolName] = true
	}
	if !foundAgents["audit-agent"] || !foundAgents["audit-agent-2"] {
		t.Fatalf("expected entries from both agents, got %+v", foundAgents)
	}
	if !foundTools["read_file"] || !foundTools["write_file"] {
		t.Fatalf("expected tool names mapped from action, got %+v", foundTools)
	}
}

func TestSQLiteDelegate_GetHighTokenSessions(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()

	// Recreate task_completions with relaxed constraints for focused aggregation testing.
	_, _ = d.db.ExecContext(ctx, `DROP TABLE IF EXISTS task_completions`)
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE task_completions (
			id BLOB PRIMARY KEY,
			agent_id TEXT NOT NULL,
			conversation_id BLOB NOT NULL,
			run_id BLOB NOT NULL,
			description TEXT,
			tokens_used INTEGER DEFAULT 0,
			tool_calls INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			user_corrections INTEGER DEFAULT 0,
			completed BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		) WITHOUT ROWID
	`)
	if err != nil {
		t.Fatalf("create task_completions table: %v", err)
	}

	session1 := ids.New()
	session2 := ids.New()
	session3 := ids.New()
	runID := ids.New()

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO task_completions (id, agent_id, conversation_id, run_id, description, tokens_used, completed)
		VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)
	`,
		ids.New(), "agent-a", session1, runID, "task-1", 800, true,
		ids.New(), "agent-a", session1, runID, "task-2", 900, true,
		ids.New(), "agent-b", session2, runID, "task-3", 400, true,
		ids.New(), "agent-c", session3, runID, "task-4", 300, true,
		ids.New(), "agent-c", session3, runID, "task-5", 450, true,
	)
	if err != nil {
		t.Fatalf("insert task completions: %v", err)
	}

	sessions, err := d.GetHighTokenSessions(ctx, 700)
	if err != nil {
		t.Fatalf("GetHighTokenSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 high-token sessions, got %d", len(sessions))
	}

	gotByAgent := map[string]SessionSummary{}
	for _, s := range sessions {
		gotByAgent[s.AgentID] = s
	}

	a, ok := gotByAgent["agent-a"]
	if !ok {
		t.Fatalf("expected aggregated session for agent-a, got %+v", gotByAgent)
	}
	if a.SessionID != session1.String() {
		t.Fatalf("expected session %s for agent-a, got %s", session1.String(), a.SessionID)
	}
	if a.TotalTokens < 1700 {
		t.Fatalf("expected aggregated tokens >= 1700 for agent-a, got %d", a.TotalTokens)
	}

	c, ok := gotByAgent["agent-c"]
	if !ok {
		t.Fatalf("expected aggregated session for agent-c, got %+v", gotByAgent)
	}
	if c.SessionID != session3.String() {
		t.Fatalf("expected session %s for agent-c, got %s", session3.String(), c.SessionID)
	}
	if c.TotalTokens < 750 {
		t.Fatalf("expected aggregated tokens >= 750 for agent-c, got %d", c.TotalTokens)
	}
}

func TestSQLiteDelegate_RLStore_Integration(t *testing.T) {
	t.Parallel()
	d := setupRLTest(t)
	ctx := t.Context()
	agentID := "integration-agent"

	t.Run("BaselineFlow", func(t *testing.T) {
		// Initially no baseline
		baseline, err := d.GetTaskBaseline(ctx, agentID)
		if err != nil {
			t.Fatalf("GetTaskBaseline: %v", err)
		}
		if baseline != nil {
			t.Error("expected nil baseline initially")
		}

		// Update baseline multiple times (simulating task processing)
		for i := 1; i <= 5; i++ {
			baseline := &TaskBaseline{
				Count:               i,
				MeanTokens:          float64(1000 + i*100),
				MeanErrors:          float64(i),
				MeanUserCorrections: float64(i % 2),
				M2Tokens:            float64(i * 1000),
				M2Errors:            float64(i * 10),
				M2UserCorrections:   float64(i * 5),
			}
			if err := d.UpdateTaskBaseline(ctx, agentID, baseline); err != nil {
				t.Fatalf("UpdateTaskBaseline iteration %d: %v", i, err)
			}
		}

		// Verify final baseline
		baseline, err = d.GetTaskBaseline(ctx, agentID)
		if err != nil {
			t.Fatalf("GetTaskBaseline final: %v", err)
		}
		if baseline == nil {
			t.Fatal("expected non-nil baseline")
		}
		if baseline.Count != 5 {
			t.Errorf("Count = %d, want 5", baseline.Count)
		}
	})

	t.Run("MemoryWeightUpdates", func(t *testing.T) {
		// Create multiple recall items
		memoryIDs := make([]ids.UUID, 3)
		for i := 0; i < 3; i++ {
			memoryIDs[i] = insertTestRecallItem(ctx, t, d, agentID)
		}

		// Update weights for each memory via delegate API.
		weights := []float64{1.5, 2.0, 2.5}
		credits := []float64{1.0, 2.0, 3.0}
		for i, memoryID := range memoryIDs {
			err := d.UpdateMemoryWeight(ctx, memoryID, weights[i], credits[i])
			if err != nil {
				t.Fatalf("UpdateMemoryWeight %d: %v", i, err)
			}
		}

		// Verify items still exist after update
		for i, memoryID := range memoryIDs {
			item, err := d.GetRecallItem(ctx, agentID, memoryID)
			if err != nil {
				t.Fatalf("GetRecallItem %d: %v", i, err)
			}
			if item == nil {
				t.Fatalf("item %d is nil after weight update", i)
			}
		}
	})

	t.Run("SelfReportUpdates", func(t *testing.T) {
		memoryID := insertTestRecallItem(ctx, t, d, agentID)

		// Update self-report scores via delegate API.
		scores := []int64{0, 1, 2, 3}
		for _, score := range scores {
			err := d.UpdateMemorySelfReport(ctx, memoryID, int(score))
			if err != nil {
				t.Fatalf("UpdateMemorySelfReportScore %d: %v", score, err)
			}
		}

		// Verify item still exists after updates
		item, err := d.GetRecallItem(ctx, agentID, memoryID)
		if err != nil {
			t.Fatalf("GetRecallItem: %v", err)
		}
		if item == nil {
			t.Fatal("item is nil after self-report updates")
		}
	})

	t.Run("PatternStorage", func(t *testing.T) {
		patterns := []DetectedPattern{
			{Type: "correction", Description: "Correction pattern", Weight: 1.0, Category: "correction", SessionID: "sess-1", AgentID: agentID},
			{Type: "discovery", Description: "Discovery pattern", Weight: 1.2, Category: "discovery", SessionID: "sess-2", AgentID: agentID},
			{Type: "failure_pattern", Description: "Failure pattern", Weight: 1.5, Category: "correction", SessionID: "sess-3", AgentID: agentID},
		}

		for _, pattern := range patterns {
			if err := d.StoreDetectedPattern(ctx, pattern); err != nil {
				t.Fatalf("StoreDetectedPattern: %v", err)
			}
		}

		// Count all patterns stored for this agent
		count, err := d.CountRecallItems(ctx, agentID, "")
		if err != nil {
			t.Fatalf("CountRecallItems: %v", err)
		}
		// Should have 3 patterns + previous test items
		if count < 3 {
			t.Errorf("expected at least 3 recall items for patterns, got %d", count)
		}
	})
}

func TestSQLiteDelegate_TaskRecordTypes(t *testing.T) {
	t.Parallel()

	// Test that TaskRecord type is properly defined
	record := TaskRecord{
		ID:              "task-1",
		Description:     "Test task",
		TokensUsed:      100,
		ToolCalls:       5,
		Errors:          1,
		UserCorrections: 0,
		Completed:       true,
	}

	if record.ID != "task-1" {
		t.Error("TaskRecord ID mismatch")
	}
	if record.TokensUsed != 100 {
		t.Error("TaskRecord TokensUsed mismatch")
	}
	if !record.Completed {
		t.Error("TaskRecord Completed should be true")
	}
}

func TestSQLiteDelegate_RetrievedMemoryRecordTypes(t *testing.T) {
	t.Parallel()

	score := 2
	record := RetrievedMemoryRecord{
		MemoryID:        ids.New(),
		Similarity:      0.85,
		SelfReportScore: &score,
	}

	if record.Similarity != 0.85 {
		t.Error("RetrievedMemoryRecord Similarity mismatch")
	}
	if record.SelfReportScore == nil || *record.SelfReportScore != 2 {
		t.Error("RetrievedMemoryRecord SelfReportScore mismatch")
	}

	// Test with nil score
	record2 := RetrievedMemoryRecord{
		MemoryID:        ids.New(),
		Similarity:      0.75,
		SelfReportScore: nil,
	}
	if record2.SelfReportScore != nil {
		t.Error("RetrievedMemoryRecord SelfReportScore should be nil")
	}
}

func TestSQLiteDelegate_DetectedPatternTypes(t *testing.T) {
	t.Parallel()

	pattern := DetectedPattern{
		Type:        "correction",
		Description: "Tool corrected",
		Weight:      1.0,
		Category:    "correction",
		SessionID:   "session-123",
		AgentID:     "agent-456",
	}

	if pattern.Type != "correction" {
		t.Error("DetectedPattern Type mismatch")
	}
	if pattern.Weight != 1.0 {
		t.Error("DetectedPattern Weight mismatch")
	}
	if pattern.SessionID != "session-123" {
		t.Error("DetectedPattern SessionID mismatch")
	}
}

func TestSQLiteDelegate_AuditEntryTypes(t *testing.T) {
	t.Parallel()

	entry := AuditEntry{
		ID:        "entry-1",
		Timestamp: time.Now(),
		ToolName:  "read_file",
		ToolInput: `{"path": "/test"}`,
		Success:   true,
		ErrorMsg:  "",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}

	if entry.ID != "entry-1" {
		t.Error("AuditEntry ID mismatch")
	}
	if entry.ToolName != "read_file" {
		t.Error("AuditEntry ToolName mismatch")
	}
	if !entry.Success {
		t.Error("AuditEntry Success should be true")
	}
}

func TestSQLiteDelegate_SessionSummaryTypes(t *testing.T) {
	t.Parallel()

	summary := SessionSummary{
		SessionID:   "session-1",
		AgentID:     "agent-1",
		TotalTokens: 10000,
		ToolCounts: map[string]int{
			"read":   10,
			"write":  5,
			"search": 15,
		},
	}

	if summary.SessionID != "session-1" {
		t.Error("SessionSummary SessionID mismatch")
	}
	if summary.TotalTokens != 10000 {
		t.Error("SessionSummary TotalTokens mismatch")
	}
	if summary.ToolCounts["read"] != 10 {
		t.Error("SessionSummary ToolCounts[read] mismatch")
	}
}
