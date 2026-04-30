package fantasy

import "context"

type ctxStepIndexKey struct{}

// WithStepIndex annotates execution context with the current agent step.
func WithStepIndex(ctx context.Context, stepIndex int) context.Context {
	return context.WithValue(ctx, ctxStepIndexKey{}, stepIndex)
}

// StepIndexFromCtx returns the current agent step carried in context.
// Missing values default to zero so callers can remain best-effort.
func StepIndexFromCtx(ctx context.Context) int {
	v := ctx.Value(ctxStepIndexKey{})
	if v == nil {
		return 0
	}
	if i, ok := v.(int); ok {
		return i
	}
	return 0
}
