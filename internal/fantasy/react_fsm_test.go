package fantasy

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestFSM_FullHappyPath(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	idx := 0
	f := newReActFSM(obs, &idx)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	f.Fire(ctx, ReActTriggerPrepared)
	f.Fire(ctx, ReActTriggerLLMResponded)
	f.Fire(ctx, ReActTriggerToolsValidated)
	f.Fire(ctx, ReActTriggerToolsExecuted)
	f.Fire(ctx, ReActTriggerMessagesAppended)
	f.Fire(ctx, ReActTriggerFinished)

	transitions := obs.Snapshot()
	require.Len(t, transitions, 7)
	assert.Equal(t, ReActStateInit, transitions[0].From)
	assert.Equal(t, ReActStateDone, transitions[len(transitions)-1].To)
}

func TestFSM_StepIndex(t *testing.T) {
	t.Parallel()
	obs := &captureObserver{}
	idx := 0
	f := newReActFSM(obs, &idx)
	ctx := t.Context()

	f.Fire(ctx, ReActTriggerStart)
	idx = 1
	f.Fire(ctx, ReActTriggerPrepared)

	transitions := obs.Snapshot()
	require.Len(t, transitions, 2)
	assert.Equal(t, 0, transitions[0].StepIndex)
	assert.Equal(t, 1, transitions[1].StepIndex)
}

func TestFSM_UnhandledTriggerIsPermissive(t *testing.T) {
	t.Parallel()
	idx := 0
	f := newReActFSM(nil, &idx)

	assert.NotPanics(t, func() {
		f.Fire(t.Context(), ReActTriggerFinished)
	})
}
