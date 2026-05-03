package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
)

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestSubagentManager_SpawnRequiresRunLoop(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())

	_, err := manager.Spawn(t.Context(), "task-without-loop", "label", "", "", "cli", "chat", nil)
	if err == nil {
		t.Fatal("expected spawn to fail when run loop is not configured")
	}
	if !strings.Contains(err.Error(), ErrRunLoopNotConfigured.Error()) {
		t.Fatalf("expected run loop contract error, got: %v", err)
	}
}

func TestSubagentManager_SpawnDelegationGuardrails(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	manager.SetDelegationLimits(2, 1)

	block := make(chan struct{})
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, _, _, _ string) (*ToolLoopResult, error) {
		<-block
		return &ToolLoopResult{Content: "done", Iterations: 1}, nil
	})

	_, err := manager.Spawn(t.Context(), "task-1", "one", "", "", "cli", "chat", nil)
	if err != nil {
		t.Fatalf("first spawn should succeed: %v", err)
	}

	_, err = manager.Spawn(t.Context(), "task-2", "two", "", "", "cli", "chat", nil)
	if err == nil || !strings.Contains(err.Error(), "delegation fanout exceeded") {
		t.Fatalf("expected fanout error, got: %v", err)
	}

	nestedCtx := withDelegationContext(t.Context(), "parent", 1)
	_, err = manager.Spawn(nestedCtx, "task-3", "three", "", "", "cli", "chat", nil)
	if err == nil || !strings.Contains(err.Error(), "nested delegation requires delegated_scope and kept_work") {
		t.Fatalf("expected nested delegation metadata error, got: %v", err)
	}

	deepCtx := withDelegationContext(t.Context(), "parent", 2)
	_, err = manager.Spawn(deepCtx, "task-4", "four", "lookup", "synthesize", "cli", "chat", nil)
	if err == nil || !strings.Contains(err.Error(), "delegation depth exceeded") {
		t.Fatalf("expected depth error, got: %v", err)
	}

	close(block)
	waitForCondition(t, 2*time.Second, func() bool {
		for _, task := range manager.ListTasks() {
			if task.Status == "running" {
				return false
			}
		}
		return true
	})
}

func TestSubagentManager_ConcurrentSpawnRespectsFanout(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	manager.SetDelegationLimits(3, 2)

	block := make(chan struct{})
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, _, _, _ string) (*ToolLoopResult, error) {
		<-block
		return &ToolLoopResult{Content: "done", Iterations: 1}, nil
	})

	const attempts = 10
	start := make(chan struct{})
	var wg sync.WaitGroup

	var mu sync.Mutex
	successes := 0
	fanoutErrors := 0
	otherErrors := 0

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := manager.Spawn(t.Context(), fmt.Sprintf("task-%d", i), fmt.Sprintf("label-%d", i), "", "", "cli", "chat", nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			if strings.Contains(err.Error(), "delegation fanout exceeded") {
				fanoutErrors++
				return
			}
			otherErrors++
		}(i)
	}

	close(start)
	wg.Wait()

	mu.Lock()
	assertSuccesses := successes
	assertFanoutErrors := fanoutErrors
	assertOtherErrors := otherErrors
	mu.Unlock()

	if assertSuccesses != 2 {
		t.Fatalf("expected exactly 2 successful spawns, got %d", assertSuccesses)
	}
	if assertFanoutErrors != attempts-2 {
		t.Fatalf("expected %d fanout errors, got %d", attempts-2, assertFanoutErrors)
	}
	if assertOtherErrors != 0 {
		t.Fatalf("expected 0 non-fanout errors, got %d", assertOtherErrors)
	}

	close(block)
	waitForCondition(t, 2*time.Second, func() bool {
		for _, task := range manager.ListTasks() {
			if task.Status == "running" {
				return false
			}
		}
		return true
	})
}

func TestSubagentManager_SpawnAuditLineageAndRuntimeContext(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())

	var gotTaskID string
	var gotDepth int
	var gotChannel string
	var gotChatID string
	manager.SetRunLoop(func(ctx context.Context, _ ToolLoopConfig, _, _, channel, chatID string) (*ToolLoopResult, error) {
		gotTaskID = delegationTaskIDFromContext(ctx)
		gotDepth = delegationDepthFromContext(ctx)
		gotChannel = channel
		gotChatID = chatID
		return &ToolLoopResult{Content: "delegated work complete", Iterations: 3}, nil
	})

	eventsCh := make(chan DelegationAuditEvent, 4)
	manager.SetAuditHook(func(_ context.Context, evt DelegationAuditEvent) {
		eventsCh <- evt
	})

	parentCtx := withDelegationContext(t.Context(), "parent-9", 1)
	_, err := manager.Spawn(parentCtx, "task-a", "label-a", "collect facts", "final synthesis", "telegram", "chat-7", nil)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	var created *DelegationAuditEvent
	var completed *DelegationAuditEvent
	timeout := time.After(2 * time.Second)
	for created == nil || completed == nil {
		select {
		case evt := <-eventsCh:
			e := evt
			switch evt.Status {
			case "created":
				created = &e
			case "completed":
				completed = &e
			}
		case <-timeout:
			t.Fatal("timed out waiting for delegation audit events")
		}
	}

	if created.ParentTaskID != "parent-9" {
		t.Fatalf("expected parent task parent-9, got %s", created.ParentTaskID)
	}
	if created.Depth != 2 {
		t.Fatalf("expected child depth 2, got %d", created.Depth)
	}
	if created.DelegatedScope != "collect facts" || created.KeptWork != "final synthesis" {
		t.Fatalf("unexpected delegation metadata: %+v", *created)
	}

	if completed.TaskID != created.TaskID {
		t.Fatalf("expected completion for created task %s, got %s", created.TaskID, completed.TaskID)
	}
	if completed.Iterations != 3 {
		t.Fatalf("expected completion iterations=3, got %d", completed.Iterations)
	}
	if completed.ResultChars == 0 {
		t.Fatal("expected completion to include non-zero result chars")
	}

	if gotTaskID != created.TaskID {
		t.Fatalf("run loop context task id mismatch: got %s want %s", gotTaskID, created.TaskID)
	}
	if gotDepth != 2 {
		t.Fatalf("run loop context depth mismatch: got %d want 2", gotDepth)
	}
	if gotChannel != "telegram" || gotChatID != "chat-7" {
		t.Fatalf("run loop origin context mismatch: channel=%s chat=%s", gotChannel, gotChatID)
	}
}
