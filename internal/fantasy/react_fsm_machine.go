package fantasy

import (
	"context"
	"time"

	"github.com/qmuntal/stateless"
)

// reactFSM is a thin wrapper around a stateless.StateMachine that emits
// transitions to a log and an optional observer.
//
// TODO: For now, this is used by Agent.Generate (Generate-first). Streaming parity is
// implemented later.
type reactFSM struct {
	sm       *stateless.StateMachine
	log      *ReActTransitionLog
	observer ReActTransitionObserver

	// stepIndex is a pointer to the current step index for the in-flight call.
	// It is intentionally owned by the caller; transitions capture its value at
	// emission time.
	stepIndex *int
}

func newReActFSM(observer ReActTransitionObserver, stepIndex *int) *reactFSM {
	f := &reactFSM{
		sm:        stateless.NewStateMachine(ReActStateInit),
		log:       NewReActTransitionLog(),
		observer:  observer,
		stepIndex: stepIndex,
	}

	// Be permissive: instrumentation should not break core behavior.
	f.sm.OnUnhandledTrigger(func(context.Context, stateless.State, stateless.Trigger, []string) error {
		return nil
	})

	// Emit transitions.
	f.sm.OnTransitioned(func(ctx context.Context, tr stateless.Transition) {
		t := ReActTransition{
			At: time.Now().UTC(),
		}
		if from, ok := tr.Source.(ReActState); ok {
			t.From = from
		}
		if to, ok := tr.Destination.(ReActState); ok {
			t.To = to
		}
		if trig, ok := tr.Trigger.(ReActTrigger); ok {
			t.Trigger = trig
		}
		if f.stepIndex != nil {
			t.StepIndex = *f.stepIndex
		}

		f.log.Append(t)
		if f.observer != nil {
			f.observer.OnReActTransition(ctx, t)
		}
	})

	// State graph for Generate().
	f.configure()

	return f
}

func (f *reactFSM) configure() {
	if f == nil || f.sm == nil {
		return
	}

	f.sm.Configure(ReActStateInit).
		Permit(ReActTriggerStart, ReActStatePrepareStep).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStatePrepareStep).
		Permit(ReActTriggerPrepared, ReActStateLLMCall).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateLLMCall).
		Permit(ReActTriggerLLMResponded, ReActStateToolValidation).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateToolValidation).
		Permit(ReActTriggerToolsValidated, ReActStateToolExecution).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateToolExecution).
		Permit(ReActTriggerToolsExecuted, ReActStateAppendMessages).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateAppendMessages).
		Permit(ReActTriggerMessagesAppended, ReActStateStopCheck).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateStopCheck).
		Permit(ReActTriggerStopConditionMet, ReActStateDone).
		Permit(ReActTriggerFinished, ReActStateDone).
		Permit(ReActTriggerContinue, ReActStatePrepareStep).
		Permit(ReActTriggerErrored, ReActStateError)

	f.sm.Configure(ReActStateError).
		Permit(ReActTriggerFinished, ReActStateDone).
		Permit(ReActTriggerRecoveredContinue, ReActStatePrepareStep)
}

func (f *reactFSM) Fire(ctx context.Context, trigger ReActTrigger) {
	if f == nil || f.sm == nil {
		return
	}
	_ = f.sm.FireCtx(ctx, trigger)
}

func (f *reactFSM) SnapshotTransitions() []ReActTransition {
	if f == nil {
		return nil
	}
	return f.log.Snapshot()
}
