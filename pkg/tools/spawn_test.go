package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
)

func TestSpawnTool_Execute_NestedDelegationGuardrails(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, _, _, _ string) (*ToolLoopResult, error) {
		return &ToolLoopResult{Content: "ok", Iterations: 1}, nil
	})

	tool := NewSpawnTool(manager)
	ctx := withDelegationContext(t.Context(), "parent", 1)

	missingMetadata := tool.Execute(ctx, map[string]interface{}{
		"task":  "nested task",
		"label": "n1",
	})
	if !missingMetadata.IsError {
		t.Fatal("expected missing delegated metadata to fail")
	}
	if !strings.Contains(missingMetadata.ForLLM, "nested delegation requires delegated_scope and kept_work") {
		t.Fatalf("unexpected error: %s", missingMetadata.ForLLM)
	}

	withMetadata := tool.Execute(ctx, map[string]interface{}{
		"task":            "nested task",
		"label":           "n2",
		"delegated_scope": "collect upstream context",
		"kept_work":       "final answer synthesis",
	})
	if withMetadata.IsError {
		t.Fatalf("expected nested delegation with metadata to succeed: %s", withMetadata.ForLLM)
	}
	if !withMetadata.Async {
		t.Fatal("expected spawn tool to return async result")
	}
}
