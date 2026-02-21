package tools

import (
	"errors"
	"testing"
)

func TestRunToolLoop_ReturnsContractError(t *testing.T) {
	t.Parallel()
	result, err := RunToolLoop(t.Context(), ToolLoopConfig{}, "", "", "", "")
	if result != nil {
		t.Fatalf("expected nil result when run loop is not configured, got %#v", result)
	}
	if err == nil {
		t.Fatal("expected contract error from RunToolLoop fallback")
	}
	if !errors.Is(err, ErrRunLoopNotConfigured) {
		t.Fatalf("expected ErrRunLoopNotConfigured, got: %v", err)
	}
}
