package dag

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestNeedsReplan(t *testing.T) {
	t.Parallel()
	assert.True(t, needsReplan("The task is incomplete [NEEDS_MORE_STEPS]"))
	assert.True(t, needsReplan("[NEEDS_MORE_STEPS]"))
	assert.False(t, needsReplan("Task complete. Here is the answer."))
	assert.False(t, needsReplan(""))
}

func TestReplanSentinelIsConsistent(t *testing.T) {
	t.Parallel()
	assert.Empty(t, cmp.Diff("[NEEDS_MORE_STEPS]", replanSentinel))
}
