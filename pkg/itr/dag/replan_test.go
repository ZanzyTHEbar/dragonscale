package dag

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNeedsReplan(t *testing.T) {
	assert.True(t, needsReplan("The task is incomplete [NEEDS_MORE_STEPS]"))
	assert.True(t, needsReplan("[NEEDS_MORE_STEPS]"))
	assert.False(t, needsReplan("Task complete. Here is the answer."))
	assert.False(t, needsReplan(""))
}

func TestReplanSentinelIsConsistent(t *testing.T) {
	assert.Equal(t, "[NEEDS_MORE_STEPS]", replanSentinel)
}
