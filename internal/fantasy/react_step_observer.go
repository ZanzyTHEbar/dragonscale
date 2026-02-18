package fantasy

import "context"

// ReActStepObserver can observe the completion of each agent step in Generate().
//
// This is primarily intended for callers to persist step snapshots or to build
// debugging/telemetry around multi-step execution.
type ReActStepObserver interface {
	OnReActStep(ctx context.Context, stepIndex int, step StepResult)
}

// ReActStepObserverFunc is a functional adapter for ReActStepObserver.
type ReActStepObserverFunc func(ctx context.Context, stepIndex int, step StepResult)

func (f ReActStepObserverFunc) OnReActStep(ctx context.Context, stepIndex int, step StepResult) {
	f(ctx, stepIndex, step)
}
