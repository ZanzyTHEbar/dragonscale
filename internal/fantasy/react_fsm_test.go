package fantasy

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureObserver captures all transitions for assertion.
type captureObserver struct {
	mu          sync.Mutex
	transitions []ReActTransition
}

func (c *captureObserver) OnReActTransition(_ context.Context, t ReActTransition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transitions = append(c.transitions, t)
}

func (c *captureObserver) Snapshot() []ReActTransition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ReActTransition, len(c.transitions))
	copy(out, c.transitions)
	return out
}

func newTestFSM(t *testing.T, obs ReActTransitionObserver) (*reactFSM, *int) {
	t.Helper()
	idx := 0
	return newReActFSM(obs, &idx), &idx
}

// driveGeneratePath fires the full happy-path sequence for one step and
// returns via Continue so the caller can advance stepIndex.
func driveGeneratePath(ctx context.Context, f *reactFSM) {
	f.Fire(ctx, ReActTriggerPrepared)
	f.Fire(ctx, ReActTriggerLLMResponded)
	f.Fire(ctx, ReActTriggerToolsValidated)
	f.Fire(ctx, ReActTriggerToolsExecuted)
	f.Fire(ctx, ReActTriggerMessagesAppended)
}

// TestFSM_Start verifies the FSM transitions from Init to PrepareStep on Start.
func TestFSM_Start(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)

	transitions := obs.Snapshot()
	require.Len(t, transitions, 1)
	assert.Equal(t, ReActStateInit, transitions[0].From)
	assert.Equal(t, ReActStatePrepareStep, transitions[0].To)
	assert.Equal(t, ReActTriggerStart, transitions[0].Trigger)
}

// TestFSM_FullHappyPath drives one complete step through all states and
// ends in Done via Finished.
func TestFSM_FullHappyPath(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	driveGeneratePath(ctx, f)
	f.Fire(ctx, ReActTriggerFinished)

	transitions := obs.Snapshot()
	// Expected: Init->PrepareStep, PrepareStep->LLM, LLM->Validate, Validate->Execute, Execute->Append, Append->Stop, Stop->Done
	require.Len(t, transitions, 7)
	assert.Equal(t, ReActStateInit, transitions[0].From)
	assert.Equal(t, ReActStateDone, transitions[6].To)
}

// TestFSM_Continue verifies that the loop can re-enter PrepareStep after a
// tool-call step.
func TestFSM_Continue(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	driveGeneratePath(ctx, f)
	f.Fire(ctx, ReActTriggerContinue) // back to PrepareStep
	driveGeneratePath(ctx, f)
	f.Fire(ctx, ReActTriggerFinished) // final step done

	transitions := obs.Snapshot()
	// Two full loops: each has 5 states + Start + Continue + Finished = 13
	assert.Equal(t, 13, len(transitions))

	// Second loop re-enters PrepareStep
	assert.Equal(t, ReActStatePrepareStep, transitions[6].To)
}

// TestFSM_StopConditionMet verifies the alternative Done path.
func TestFSM_StopConditionMet(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	driveGeneratePath(ctx, f)
	f.Fire(ctx, ReActTriggerStopConditionMet)

	last := obs.Snapshot()
	assert.Equal(t, ReActStateDone, last[len(last)-1].To)
}

// TestFSM_ErrorTransition verifies the error state is reachable from any state.
func TestFSM_ErrorTransition(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	f.Fire(ctx, ReActTriggerPrepared)
	f.Fire(ctx, ReActTriggerErrored) // error mid-LLM call

	transitions := obs.Snapshot()
	last := transitions[len(transitions)-1]
	assert.Equal(t, ReActStateError, last.To)
}

// TestFSM_RecoveredContinue verifies the error → PrepareStep recovery path.
func TestFSM_RecoveredContinue(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	f, _ := newTestFSM(t, obs)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	f.Fire(ctx, ReActTriggerErrored) // error before prepared
	f.Fire(ctx, ReActTriggerRecoveredContinue)

	transitions := obs.Snapshot()
	last := transitions[len(transitions)-1]
	assert.Equal(t, ReActStatePrepareStep, last.To)
}

// TestFSM_UnhandledTriggerIsPermissive verifies that firing an invalid trigger
// from a given state does NOT return an error (permissive design).
func TestFSM_UnhandledTriggerIsPermissive(t *testing.T) {
	t.Parallel()
	f, _ := newTestFSM(t, nil)
	ctx := t.Context()

	// From Init, firing Finished is not a permitted transition.
	// The FSM must silently ignore it (no panic, no error).
	assert.NotPanics(t, func() {
		f.Fire(ctx, ReActTriggerFinished)
	})
}

// TestFSM_TransitionLog verifies the log accumulates correctly and Snapshot
// returns a copy.
func TestFSM_TransitionLog(t *testing.T) {
	t.Parallel()
	f, _ := newTestFSM(t, nil)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	f.Fire(ctx, ReActTriggerPrepared)

	snap1 := f.SnapshotTransitions()
	assert.Len(t, snap1, 2)

	f.Fire(ctx, ReActTriggerLLMResponded)
	snap2 := f.SnapshotTransitions()
	assert.Len(t, snap2, 3, "log must grow after each transition")
	assert.Len(t, snap1, 2, "first snapshot must be immutable")
}

// TestFSM_StepIndex verifies that the step index embedded in transitions
// reflects the pointer value at emission time.
func TestFSM_StepIndex(t *testing.T) {
	t.Parallel()
	idx := 0
	obs := &captureObserver{}
	f := newReActFSM(obs, &idx)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart) // stepIndex = 0
	idx = 1
	f.Fire(ctx, ReActTriggerPrepared) // stepIndex = 1

	transitions := obs.Snapshot()
	require.Len(t, transitions, 2)
	assert.Equal(t, 0, transitions[0].StepIndex)
	assert.Equal(t, 1, transitions[1].StepIndex)
}

// TestFSM_NilObserverSafe verifies no panic when no observer is attached.
func TestFSM_NilObserverSafe(t *testing.T) {
	t.Parallel()
	f, _ := newTestFSM(t, nil)
	ctx := t.Context()

	assert.NotPanics(t, func() {
		f.Fire(ctx, ReActTriggerStart)
		f.Fire(ctx, ReActTriggerPrepared)
	})
}

// TestReActTransitionLog_ConcurrentAppend verifies the log is safe under
// concurrent writes.
func TestReActTransitionLog_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	log := NewReActTransitionLog()
	const n = 100
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			log.Append(ReActTransition{StepIndex: i})
		}(i)
	}
	wg.Wait()

	snap := log.Snapshot()
	assert.Len(t, snap, n, "all concurrent appends must be recorded")
}
