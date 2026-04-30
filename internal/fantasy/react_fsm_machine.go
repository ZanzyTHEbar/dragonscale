package fantasy

import (
	"context"
	"time"

	"github.com/qmuntal/stateless"
)

// reactFSM emits ReAct loop transitions without affecting agent behavior.
type reactFSM struct {
	sm       *stateless.StateMachine
	log      *ReActTransitionLog
	observer ReActTransitionObserver

	// stepIndex is owned by the caller; transitions capture its value at emission.
	stepIndex *int
}

func newReActFSM(observer ReActTransitionObserver, stepIndex *int) *reactFSM {
	f := &reactFSM{
		sm:        stateless.NewStateMachine(ReActStateInit),
		log:       NewReActTransitionLog(),
		observer:  observer,
		stepIndex: stepIndex,
	}

	// Instrumentation should never break core agent behavior.
	f.sm.OnUnhandledTrigger(func(context.Context, stateless.State, stateless.Trigger, []string) error {
		return nil
	})

	f.sm.OnTransitioned(func(ctx context.Context, tr stateless.Transition) {
		t := ReActTransition{At: time.Now().UTC()}
		if from, ok := tr.Source.(ReActState); ok {
			t.From = from
		}
		if to, ok := tr.Destination.(ReActState); ok {
			t.To = to
		}
		if trigger, ok := tr.Trigger.(ReActTrigger); ok {
			t.Trigger = trigger
		}
		if f.stepIndex != nil {
			t.StepIndex = *f.stepIndex
		}

		f.log.Append(t)
		if f.observer != nil {
			f.observer.OnReActTransition(ctx, t)
		}
	})

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
