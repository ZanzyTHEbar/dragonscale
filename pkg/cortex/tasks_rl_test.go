package cortex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// mockRLStore implements RLStore for testing without a real database.
type mockRLStore struct {
	baseline       *TaskBaseline
	tasks          []TaskRecord
	memories       []RetrievedMemoryRecord
	updatedWeights []struct {
		id     ids.UUID
		weight float64
		credit float64
	}
	updatedSelfReports []struct {
		id    ids.UUID
		score int
	}
	getTaskBaselineErr        error
	updateTaskBaselineErr     error
	getCompletedTasksErr      error
	getRetrievedMemoriesErr   error
	updateMemoryWeightErr     error
	updateMemorySelfReportErr error
}

func (m *mockRLStore) GetTaskBaseline(ctx context.Context, agentID string) (*TaskBaseline, error) {
	if m.getTaskBaselineErr != nil {
		return nil, m.getTaskBaselineErr
	}
	return m.baseline, nil
}

func (m *mockRLStore) UpdateTaskBaseline(ctx context.Context, agentID string, baseline *TaskBaseline) error {
	if m.updateTaskBaselineErr != nil {
		return m.updateTaskBaselineErr
	}
	m.baseline = baseline
	return nil
}

func (m *mockRLStore) GetCompletedTasks(ctx context.Context, since time.Time) ([]TaskRecord, error) {
	if m.getCompletedTasksErr != nil {
		return nil, m.getCompletedTasksErr
	}
	return m.tasks, nil
}

func (m *mockRLStore) GetRetrievedMemories(ctx context.Context, taskID string) ([]RetrievedMemoryRecord, error) {
	if m.getRetrievedMemoriesErr != nil {
		return nil, m.getRetrievedMemoriesErr
	}
	return m.memories, nil
}

func (m *mockRLStore) UpdateMemoryWeight(ctx context.Context, memoryID ids.UUID, weight, credit float64) error {
	if m.updateMemoryWeightErr != nil {
		return m.updateMemoryWeightErr
	}
	m.updatedWeights = append(m.updatedWeights, struct {
		id     ids.UUID
		weight float64
		credit float64
	}{memoryID, weight, credit})
	return nil
}

func (m *mockRLStore) UpdateMemorySelfReport(ctx context.Context, memoryID ids.UUID, score int) error {
	if m.updateMemorySelfReportErr != nil {
		return m.updateMemorySelfReportErr
	}
	m.updatedSelfReports = append(m.updatedSelfReports, struct {
		id    ids.UUID
		score int
	}{memoryID, score})
	return nil
}

func TestRLTask_Name(t *testing.T) {
	store := &mockRLStore{}
	task := NewRLTask(store, "test-agent")

	if got := task.Name(); got != "rl" {
		t.Errorf("Name() = %q, want %q", got, "rl")
	}
}

func TestRLTask_Interval(t *testing.T) {
	store := &mockRLStore{}
	task := NewRLTask(store, "test-agent")

	want := 5 * time.Minute
	if got := task.Interval(); got != want {
		t.Errorf("Interval() = %v, want %v", got, want)
	}
}

func TestRLTask_Timeout(t *testing.T) {
	store := &mockRLStore{}
	task := NewRLTask(store, "test-agent")

	want := 30 * time.Second
	if got := task.Timeout(); got != want {
		t.Errorf("Timeout() = %v, want %v", got, want)
	}
}

func TestRLTask_Execute_NoStore(t *testing.T) {
	task := NewRLTask(nil, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() with nil store should not error, got: %v", err)
	}
}

func TestRLTask_Execute_NoTasks(t *testing.T) {
	store := &mockRLStore{
		baseline: &TaskBaseline{Count: 10},
		tasks:    []TaskRecord{}, // No tasks
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() with no tasks should not error, got: %v", err)
	}

	// Verify lastRun was updated (task should complete successfully)
	if task.lastRun.IsZero() {
		t.Error("lastRun should have been updated after Execute")
	}
}

func TestRLTask_Execute_UpdatesWeights(t *testing.T) {
	memoryID1 := ids.New()
	memoryID2 := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               20,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
			M2Tokens:            10000,
			M2Errors:            100,
			M2UserCorrections:   40,
		},
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "Test task",
				TokensUsed:      800, // Better than baseline (fewer tokens)
				ToolCalls:       5,
				Errors:          1, // Better than baseline (fewer errors)
				UserCorrections: 0,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
			{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(2)},
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Verify weights were updated
	if len(store.updatedWeights) != 2 {
		t.Errorf("expected 2 weight updates, got %d", len(store.updatedWeights))
	}

	// Verify baseline was updated
	if store.baseline == nil {
		t.Fatal("baseline should have been updated")
	}
	if store.baseline.Count != 21 {
		t.Errorf("baseline count = %d, want 21", store.baseline.Count)
	}
}

func TestRLTask_Execute_ColdStart(t *testing.T) {
	memoryID := ids.New()

	store := &mockRLStore{
		baseline: nil, // Cold start - no baseline
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "First task",
				TokensUsed:      500,
				ToolCalls:       3,
				Errors:          0,
				UserCorrections: 0,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID, Similarity: 0.95, SelfReportScore: intPtr(3)},
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Verify baseline was created
	if store.baseline == nil {
		t.Fatal("baseline should have been created")
	}
	if store.baseline.Count != 1 {
		t.Errorf("baseline count = %d, want 1", store.baseline.Count)
	}
	if store.baseline.MeanTokens != 500 {
		t.Errorf("baseline mean tokens = %f, want 500", store.baseline.MeanTokens)
	}

	// Verify weights were updated
	if len(store.updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(store.updatedWeights))
	}
}

func TestRLTask_Execute_MultipleMemories(t *testing.T) {
	// Test that credit is distributed across multiple memories
	memoryID1 := ids.New()
	memoryID2 := ids.New()
	memoryID3 := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               15,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
			M2Tokens:            5000,
			M2Errors:            50,
			M2UserCorrections:   20,
		},
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "Multi-memory task",
				TokensUsed:      900,
				ToolCalls:       10,
				Errors:          2,
				UserCorrections: 1,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
			{MemoryID: memoryID2, Similarity: 0.85, SelfReportScore: intPtr(3)},
			{MemoryID: memoryID3, Similarity: 0.8, SelfReportScore: intPtr(3)},
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// All 3 memories should be updated
	if len(store.updatedWeights) != 3 {
		t.Errorf("expected 3 weight updates, got %d", len(store.updatedWeights))
	}

	// Verify that all memory IDs were updated
	updatedIDs := make(map[ids.UUID]bool)
	for _, uw := range store.updatedWeights {
		updatedIDs[uw.id] = true
	}
	if !updatedIDs[memoryID1] {
		t.Error("memoryID1 was not updated")
	}
	if !updatedIDs[memoryID2] {
		t.Error("memoryID2 was not updated")
	}
	if !updatedIDs[memoryID3] {
		t.Error("memoryID3 was not updated")
	}
}

func TestRLTask_Execute_IncompleteTask(t *testing.T) {
	memoryID := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               20,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
			M2Tokens:            10000,
			M2Errors:            100,
			M2UserCorrections:   40,
		},
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "Incomplete task",
				TokensUsed:      800,
				ToolCalls:       5,
				Errors:          1,
				UserCorrections: 0,
				Completed:       false, // Not completed
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: intPtr(3)},
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Even incomplete tasks should update memories (with negative credit)
	if len(store.updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(store.updatedWeights))
	}

	// The credit should be negative for incomplete task
	credit := store.updatedWeights[0].credit
	if credit > 0 {
		t.Errorf("expected negative credit for incomplete task, got %f", credit)
	}
}

func TestRLTask_Execute_GetBaselineError(t *testing.T) {
	store := &mockRLStore{
		getTaskBaselineErr: errors.New("database error"),
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	err := task.Execute(ctx)
	if err == nil {
		t.Error("expected error when GetTaskBaseline fails")
	}
	if err.Error() != "failed to get task baseline: database error" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRLTask_Execute_UpdateBaselineError(t *testing.T) {
	store := &mockRLStore{
		baseline: &TaskBaseline{Count: 10},
		tasks: []TaskRecord{
			{
				ID:        "task-1",
				Completed: true,
				CreatedAt: time.Now(),
			},
		},
		updateTaskBaselineErr: errors.New("update failed"),
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	err := task.Execute(ctx)
	if err == nil {
		t.Error("expected error when UpdateTaskBaseline fails")
	}
}

func TestRLTask_Execute_GetRetrievedMemoriesError(t *testing.T) {
	store := &mockRLStore{
		baseline: &TaskBaseline{Count: 10},
		tasks: []TaskRecord{
			{
				ID:        "task-1",
				Completed: true,
				CreatedAt: time.Now(),
			},
		},
		getRetrievedMemoriesErr: errors.New("memory lookup failed"),
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	// Should not error - the task continues even if memory retrieval fails
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() should not error on memory retrieval failure: %v", err)
	}
}

func TestRLTask_Execute_UpdateMemoryWeightError(t *testing.T) {
	memoryID1 := ids.New()
	memoryID2 := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{Count: 10},
		tasks: []TaskRecord{
			{
				ID:        "task-1",
				Completed: true,
				CreatedAt: time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
			{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(3)},
		},
		updateMemoryWeightErr: errors.New("weight update failed"),
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	// Should not error - the task continues even if weight update fails
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() should not error on weight update failure: %v", err)
	}

	// No weights should be recorded (all updates failed)
	if len(store.updatedWeights) != 0 {
		t.Errorf("expected 0 weight updates (all failed), got %d", len(store.updatedWeights))
	}
}

func TestRLTask_Execute_NoMemoriesForTask(t *testing.T) {
	store := &mockRLStore{
		baseline: &TaskBaseline{Count: 10},
		tasks: []TaskRecord{
			{
				ID:        "task-1",
				Completed: true,
				CreatedAt: time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{}, // No memories retrieved
	}
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// No weights should be updated
	if len(store.updatedWeights) != 0 {
		t.Errorf("expected 0 weight updates (no memories), got %d", len(store.updatedWeights))
	}
}

func TestRLTask_Execute_MultipleTasks(t *testing.T) {
	memoryID1 := ids.New()
	memoryID2 := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               10,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
		},
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "First task",
				TokensUsed:      900,
				ToolCalls:       5,
				Errors:          1,
				UserCorrections: 0,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
			{
				ID:              "task-2",
				Description:     "Second task",
				TokensUsed:      950,
				ToolCalls:       6,
				Errors:          2,
				UserCorrections: 1,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
			{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(2)},
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Both tasks should process the same memories (4 updates total)
	if len(store.updatedWeights) != 4 {
		t.Errorf("expected 4 weight updates (2 tasks x 2 memories), got %d", len(store.updatedWeights))
	}

	// Baseline should be updated twice (count = 12)
	if store.baseline.Count != 12 {
		t.Errorf("baseline count = %d, want 12", store.baseline.Count)
	}
}

func TestRLTask_Execute_NoSelfReportScore(t *testing.T) {
	memoryID := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               15,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
		},
		tasks: []TaskRecord{
			{
				ID:              "task-1",
				Description:     "Task with no self-report",
				TokensUsed:      900,
				ToolCalls:       5,
				Errors:          1,
				UserCorrections: 0,
				Completed:       true,
				CreatedAt:       time.Now(),
			},
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: nil}, // No self-report
		},
	}

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Memory should still be updated with default self-report of 0
	if len(store.updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(store.updatedWeights))
	}

	// Credit should be 0 when no self-report (selfReportNorm = 0/3 = 0)
	credit := store.updatedWeights[0].credit
	if credit != 0 {
		t.Errorf("expected 0 credit when no self-report, got %f", credit)
	}
}

func TestRLTask_processTask_SingleMemory(t *testing.T) {
	memoryID := ids.New()

	store := &mockRLStore{
		baseline: &TaskBaseline{
			Count:               15,
			MeanTokens:          1000,
			MeanErrors:          5,
			MeanUserCorrections: 2,
		},
		memories: []RetrievedMemoryRecord{
			{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: intPtr(3)},
		},
	}

	task := NewRLTask(store, "test-agent")

	taskRecord := TaskRecord{
		ID:              "task-1",
		Description:     "Single memory task",
		TokensUsed:      800,
		ToolCalls:       5,
		Errors:          1,
		UserCorrections: 0,
		Completed:       true,
		CreatedAt:       time.Now(),
	}

	ctx := context.Background()
	if err := task.processTask(ctx, taskRecord, store.baseline); err != nil {
		t.Fatalf("processTask() error: %v", err)
	}

	// Single memory should get full distribution (no split)
	if len(store.updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(store.updatedWeights))
	}
}

// intPtr returns a pointer to an int
func intPtr(i int) *int {
	return &i
}
