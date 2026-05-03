package cortex

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"go.uber.org/mock/gomock"
)

type updatedWeight struct {
	id     ids.UUID
	weight float64
	credit float64
}

func expectUpdateTaskBaseline(store *MockRLStore, updatedBaseline **TaskBaseline, err error) {
	store.EXPECT().UpdateTaskBaseline(gomock.Any(), "test-agent", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, baseline *TaskBaseline) error {
			if err != nil {
				return err
			}
			*updatedBaseline = baseline
			return nil
		},
	)
}

func expectUpdateMemoryWeights(store *MockRLStore, updatedWeights *[]updatedWeight, expectedMemoryIDs []ids.UUID, times int, err error) {
	// Track expected occurrences using a count map so duplicate IDs are handled correctly.
	remaining := make(map[ids.UUID]int)
	for _, id := range expectedMemoryIDs {
		remaining[id]++
	}
	store.EXPECT().UpdateMemoryWeight(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, memoryID ids.UUID, weight, credit float64) error {
			if err != nil {
				return err
			}
			if remaining[memoryID] <= 0 {
				panic(fmt.Sprintf("unexpected memory ID %s updated (not in expected set or already exhausted)", memoryID))
			}
			remaining[memoryID]--
			*updatedWeights = append(*updatedWeights, updatedWeight{memoryID, weight, credit})
			return nil
		},
	).Times(times)
}

func expectRetrievedMemories(store *MockRLStore, taskID string, memories []RetrievedMemoryRecord) {
	// Execute needs one read for processing and currently allows a second read for metrics/logging.
	store.EXPECT().GetRetrievedMemories(gomock.Any(), taskID).Return(memories, nil).MinTimes(1).MaxTimes(2)
}

func TestRLTask_Name(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	task := NewRLTask(store, "test-agent")

	if got := task.Name(); got != "rl" {
		t.Errorf("Name() = %q, want %q", got, "rl")
	}
}

func TestRLTask_Interval(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	task := NewRLTask(store, "test-agent")

	want := 5 * time.Minute
	if got := task.Interval(); got != want {
		t.Errorf("Interval() = %v, want %v", got, want)
	}
}

func TestRLTask_Timeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
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
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(&TaskBaseline{Count: 10}, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return([]TaskRecord{}, nil)
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

	baseline := &TaskBaseline{
		Count:               20,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            10000,
		M2Errors:            100,
		M2UserCorrections:   40,
	}
	tasks := []TaskRecord{
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
	}
	memories := []RetrievedMemoryRecord{
		{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
		{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(2)},
	}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID1, memoryID2}, 2, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Verify weights were updated
	if len(updatedWeights) != 2 {
		t.Errorf("expected 2 weight updates, got %d", len(updatedWeights))
	}

	// Verify baseline was updated
	if updatedBaseline == nil {
		t.Fatal("baseline should have been updated")
	}
	if updatedBaseline.Count != 21 {
		t.Errorf("baseline count = %d, want 21", updatedBaseline.Count)
	}
}

func TestRLTask_Execute_ColdStart(t *testing.T) {
	memoryID := ids.New()

	tasks := []TaskRecord{
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
	}
	memories := []RetrievedMemoryRecord{
		{MemoryID: memoryID, Similarity: 0.95, SelfReportScore: intPtr(3)},
	}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(nil, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID}, 1, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Verify baseline was created
	if updatedBaseline == nil {
		t.Fatal("baseline should have been created")
	}
	if updatedBaseline.Count != 1 {
		t.Errorf("baseline count = %d, want 1", updatedBaseline.Count)
	}
	if updatedBaseline.MeanTokens != 500 {
		t.Errorf("baseline mean tokens = %f, want 500", updatedBaseline.MeanTokens)
	}

	// Verify weights were updated
	if len(updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(updatedWeights))
	}
}

func TestRLTask_Execute_MultipleMemories(t *testing.T) {
	// Test that credit is distributed across multiple memories
	memoryID1 := ids.New()
	memoryID2 := ids.New()
	memoryID3 := ids.New()

	baseline := &TaskBaseline{
		Count:               15,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            5000,
		M2Errors:            50,
		M2UserCorrections:   20,
	}
	tasks := []TaskRecord{{ID: "task-1", Description: "Multi-memory task", TokensUsed: 900, ToolCalls: 10, Errors: 2, UserCorrections: 1, Completed: true, CreatedAt: time.Now()}}
	memories := []RetrievedMemoryRecord{
		{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
		{MemoryID: memoryID2, Similarity: 0.85, SelfReportScore: intPtr(3)},
		{MemoryID: memoryID3, Similarity: 0.8, SelfReportScore: intPtr(3)},
	}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID1, memoryID2, memoryID3}, 3, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// All 3 memories should be updated
	if len(updatedWeights) != 3 {
		t.Errorf("expected 3 weight updates, got %d", len(updatedWeights))
	}

	// Verify that all memory IDs were updated
	updatedIDs := make(map[ids.UUID]bool)
	for _, uw := range updatedWeights {
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

	baseline := &TaskBaseline{
		Count:               20,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            10000,
		M2Errors:            100,
		M2UserCorrections:   40,
	}
	tasks := []TaskRecord{{ID: "task-1", Description: "Incomplete task", TokensUsed: 800, ToolCalls: 5, Errors: 1, UserCorrections: 0, Completed: false, CreatedAt: time.Now()}}
	memories := []RetrievedMemoryRecord{{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: intPtr(3)}}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID}, 1, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Even incomplete tasks should update memories (with negative credit)
	if len(updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(updatedWeights))
	}

	// The credit should be negative for incomplete task
	credit := updatedWeights[0].credit
	if credit > 0 {
		t.Errorf("expected negative credit for incomplete task, got %f", credit)
	}
}

func TestRLTask_Execute_GetBaselineError(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(nil, errors.New("database error"))
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
	baseline := &TaskBaseline{Count: 10}
	tasks := []TaskRecord{{ID: "task-1", Completed: true, CreatedAt: time.Now()}}
	var updatedBaseline *TaskBaseline
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, errors.New("update failed"))
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	err := task.Execute(ctx)
	if err == nil {
		t.Error("expected error when UpdateTaskBaseline fails")
	}
}

func TestRLTask_Execute_GetRetrievedMemoriesError(t *testing.T) {
	baseline := &TaskBaseline{Count: 10}
	tasks := []TaskRecord{{ID: "task-1", Completed: true, CreatedAt: time.Now()}}
	var updatedBaseline *TaskBaseline
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	store.EXPECT().GetRetrievedMemories(gomock.Any(), "task-1").Return(nil, errors.New("memory lookup failed"))
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)
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

	baseline := &TaskBaseline{Count: 10}
	tasks := []TaskRecord{{ID: "task-1", Completed: true, CreatedAt: time.Now()}}
	memories := []RetrievedMemoryRecord{
		{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
		{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(3)},
	}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID1, memoryID2}, 2, errors.New("weight update failed"))
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	// Should not error - the task continues even if weight update fails
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() should not error on weight update failure: %v", err)
	}

	// No weights should be recorded (all updates failed)
	if len(updatedWeights) != 0 {
		t.Errorf("expected 0 weight updates (all failed), got %d", len(updatedWeights))
	}
}

func TestRLTask_Execute_NoMemoriesForTask(t *testing.T) {
	baseline := &TaskBaseline{Count: 10}
	tasks := []TaskRecord{{ID: "task-1", Completed: true, CreatedAt: time.Now()}}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", []RetrievedMemoryRecord{})
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)
	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// No weights should be updated
	if len(updatedWeights) != 0 {
		t.Errorf("expected 0 weight updates (no memories), got %d", len(updatedWeights))
	}
}

func TestRLTask_Execute_MultipleTasks(t *testing.T) {
	memoryID1 := ids.New()
	memoryID2 := ids.New()

	baseline := &TaskBaseline{
		Count:               10,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
	}
	tasks := []TaskRecord{
		{ID: "task-1", Description: "First task", TokensUsed: 900, ToolCalls: 5, Errors: 1, UserCorrections: 0, Completed: true, CreatedAt: time.Now()},
		{ID: "task-2", Description: "Second task", TokensUsed: 950, ToolCalls: 6, Errors: 2, UserCorrections: 1, Completed: true, CreatedAt: time.Now()},
	}
	memories := []RetrievedMemoryRecord{
		{MemoryID: memoryID1, Similarity: 0.9, SelfReportScore: intPtr(3)},
		{MemoryID: memoryID2, Similarity: 0.8, SelfReportScore: intPtr(2)},
	}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectRetrievedMemories(store, "task-2", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID1, memoryID2, memoryID1, memoryID2}, 4, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Both tasks should process the same memories (4 updates total)
	if len(updatedWeights) != 4 {
		t.Errorf("expected 4 weight updates (2 tasks x 2 memories), got %d", len(updatedWeights))
	}

	// Baseline should be updated twice (count = 12)
	if updatedBaseline.Count != 12 {
		t.Errorf("baseline count = %d, want 12", updatedBaseline.Count)
	}
}

func TestRLTask_Execute_NoSelfReportScore(t *testing.T) {
	memoryID := ids.New()

	baseline := &TaskBaseline{
		Count:               15,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
	}
	tasks := []TaskRecord{{ID: "task-1", Description: "Task with no self-report", TokensUsed: 900, ToolCalls: 5, Errors: 1, UserCorrections: 0, Completed: true, CreatedAt: time.Now()}}
	memories := []RetrievedMemoryRecord{{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: nil}}
	var updatedBaseline *TaskBaseline
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetTaskBaseline(gomock.Any(), "test-agent").Return(baseline, nil)
	store.EXPECT().GetCompletedTasks(gomock.Any(), "test-agent", gomock.Eq(time.Time{})).Return(tasks, nil)
	expectRetrievedMemories(store, "task-1", memories)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID}, 1, nil)
	expectUpdateTaskBaseline(store, &updatedBaseline, nil)

	task := NewRLTask(store, "test-agent")

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Memory should still be updated with default self-report of 0
	if len(updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(updatedWeights))
	}

	// Credit should be 0 when no self-report (selfReportNorm = 0/3 = 0)
	credit := updatedWeights[0].credit
	if credit != 0 {
		t.Errorf("expected 0 credit when no self-report, got %f", credit)
	}
}

func TestRLTask_processTask_SingleMemory(t *testing.T) {
	memoryID := ids.New()

	baseline := &TaskBaseline{
		Count:               15,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
	}
	memories := []RetrievedMemoryRecord{{MemoryID: memoryID, Similarity: 0.9, SelfReportScore: intPtr(3)}}
	var updatedWeights []updatedWeight
	ctrl := gomock.NewController(t)
	store := NewMockRLStore(ctrl)
	store.EXPECT().GetRetrievedMemories(gomock.Any(), "task-1").Return(memories, nil)
	expectUpdateMemoryWeights(store, &updatedWeights, []ids.UUID{memoryID}, 1, nil)

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
	if err := task.processTask(ctx, taskRecord, baseline); err != nil {
		t.Fatalf("processTask() error: %v", err)
	}

	// Single memory should get full distribution (no split)
	if len(updatedWeights) != 1 {
		t.Errorf("expected 1 weight update, got %d", len(updatedWeights))
	}
}

// intPtr returns a pointer to an int
func intPtr(i int) *int {
	return &i
}
