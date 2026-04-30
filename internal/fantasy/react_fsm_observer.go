package fantasy

import "context"

// ReActTransitionObserver observes ReAct FSM transitions.
type ReActTransitionObserver interface {
	OnReActTransition(ctx context.Context, transition ReActTransition)
}

// ReActTransitionObserverFunc adapts a function to ReActTransitionObserver.
type ReActTransitionObserverFunc func(ctx context.Context, transition ReActTransition)

func (f ReActTransitionObserverFunc) OnReActTransition(ctx context.Context, transition ReActTransition) {
	if f != nil {
		f(ctx, transition)
	}
}
