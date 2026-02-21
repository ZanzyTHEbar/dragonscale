package tools

import (
	"context"
	"errors"
	"testing"
)

func TestRunToolLoop_ReturnsContractError(t *testing.T) {
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{}, "", "", "", "")
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
