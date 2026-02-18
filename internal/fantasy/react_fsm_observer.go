package fantasy

import "context"

// ReActTransitionObserver can observe transitions taken by the ReAct state machine.
//
// This is a generic hook intended for callers (like Budgetsmith) to persist or
// debug agent execution without coupling the fantasy module to any particular
// storage layer.
type ReActTransitionObserver interface {
	OnReActTransition(ctx context.Context, t ReActTransition)
}

// ReActTransitionObserverFunc is a functional adapter for ReActTransitionObserver.
type ReActTransitionObserverFunc func(ctx context.Context, t ReActTransition)

func (f ReActTransitionObserverFunc) OnReActTransition(ctx context.Context, t ReActTransition) {
	f(ctx, t)
}
