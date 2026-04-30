package fantasy

import "context"

// ReActStepObserver observes completed agent steps.
type ReActStepObserver interface {
	OnReActStep(ctx context.Context, stepIndex int, step StepResult)
}

// ReActStepObserverFunc adapts a function to ReActStepObserver.
type ReActStepObserverFunc func(ctx context.Context, stepIndex int, step StepResult)

func (f ReActStepObserverFunc) OnReActStep(ctx context.Context, stepIndex int, step StepResult) {
	if f != nil {
		f(ctx, stepIndex, step)
	}
}
