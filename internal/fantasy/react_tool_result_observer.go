package fantasy

import "context"

// ReActToolResultObserver observes client-side tool results.
type ReActToolResultObserver interface {
	OnReActToolResult(ctx context.Context, stepIndex int, result ToolResultContent)
}

// ReActToolResultObserverFunc adapts a function to ReActToolResultObserver.
type ReActToolResultObserverFunc func(ctx context.Context, stepIndex int, result ToolResultContent)

func (f ReActToolResultObserverFunc) OnReActToolResult(ctx context.Context, stepIndex int, result ToolResultContent) {
	if f != nil {
		f(ctx, stepIndex, result)
	}
}
