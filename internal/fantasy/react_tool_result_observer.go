package fantasy

import "context"

// ReActToolResultObserver can observe tool results produced during Generate().
//
// This hook is intended for callers to persist tool outputs (e.g., filesystem
// offloading + DB indexing) without coupling the fantasy module to a storage
// layer.
type ReActToolResultObserver interface {
	OnReActToolResult(ctx context.Context, stepIndex int, result ToolResultContent)
}

// ReActToolResultObserverFunc is a functional adapter for ReActToolResultObserver.
type ReActToolResultObserverFunc func(ctx context.Context, stepIndex int, result ToolResultContent)

func (f ReActToolResultObserverFunc) OnReActToolResult(ctx context.Context, stepIndex int, result ToolResultContent) {
	f(ctx, stepIndex, result)
}
