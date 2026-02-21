package dag

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopologicalOrderLinear(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{
		"a": newNodeState("a", nil),
		"b": newNodeState("b", []string{"a"}),
		"c": newNodeState("c", []string{"b"}),
	}

	waves, err := topologicalOrder(states)
	require.NoError(t, err)
	require.Len(t, waves, 3)
	assert.Empty(t, cmp.Diff([]string{"a"}, waves[0]))
	assert.Empty(t, cmp.Diff([]string{"b"}, waves[1]))
	assert.Empty(t, cmp.Diff([]string{"c"}, waves[2]))
}

func TestTopologicalOrderParallel(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{
		"a": newNodeState("a", nil),
		"b": newNodeState("b", nil),
		"c": newNodeState("c", []string{"a", "b"}),
	}

	waves, err := topologicalOrder(states)
	require.NoError(t, err)
	require.Len(t, waves, 2)

	assert.Len(t, waves[0], 2)
	assert.Contains(t, waves[0], "a")
	assert.Contains(t, waves[0], "b")
	assert.Empty(t, cmp.Diff([]string{"c"}, waves[1]))
}

func TestTopologicalOrderCycleDetection(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{
		"a": newNodeState("a", []string{"c"}),
		"b": newNodeState("b", []string{"a"}),
		"c": newNodeState("c", []string{"b"}),
	}

	_, err := topologicalOrder(states)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestTopologicalOrderSingleNode(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{
		"only": newNodeState("only", nil),
	}

	waves, err := topologicalOrder(states)
	require.NoError(t, err)
	require.Len(t, waves, 1)
	assert.Empty(t, cmp.Diff([]string{"only"}, waves[0]))
}

func TestResolveRefs(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{
		"search": newNodeState("search", nil),
	}
	states["search"].setResult("found: /tmp/file.txt", nil)

	input := `{"path":"#nodesearch"}`
	result := resolveRefs(input, states)
	assert.Contains(t, result, "found: /tmp/file.txt")
	assert.NotContains(t, result, "#nodesearch")
}

func TestResolveRefsNoMatch(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{}
	input := `{"path":"#nodemissing"}`
	result := resolveRefs(input, states)
	assert.Empty(t, cmp.Diff(input, result))
}

func TestResolveToolExecArgsNoRefs(t *testing.T) {
	t.Parallel()
	states := map[string]*nodeState{}
	input := `{"path":"/tmp/plain.txt"}`
	result := resolveToolExecArgs(input, states)
	assert.Empty(t, cmp.Diff(input, result))
}

func TestEscapeForJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{`has "quotes"`, `has \"quotes\"`},
		{"has\nnewline", `has\nnewline`},
		{"has\ttab", `has\ttab`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Empty(t, cmp.Diff(tt.expected, escapeForJSON(tt.input)))
		})
	}
}

func TestNodeStateSetAndGetResult(t *testing.T) {
	t.Parallel()
	ns := newNodeState("test", nil)

	go func() {
		ns.setResult("result-data", nil)
	}()

	<-ns.done

	result, err := ns.getResult()
	assert.NoError(t, err)
	assert.Empty(t, cmp.Diff("result-data", result))
}
