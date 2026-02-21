package dag

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestRouteExplicitModes(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()

	assert.Empty(t, cmp.Diff(ModeReAct, Route(ModeReAct, "anything", cfg)))
	assert.Empty(t, cmp.Diff(ModeDAG, Route(ModeDAG, "anything", cfg)))
}

func TestRouteAutoSimpleQuery(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	assert.Empty(t, cmp.Diff(ModeReAct, Route(ModeAuto, "what is the weather?", cfg)))
}

func TestRouteAutoComplexQuery(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	longQuery := strings.Repeat("word ", 35)
	assert.Empty(t, cmp.Diff(ModeDAG, Route(ModeAuto, longQuery, cfg)))
}

func TestRouteAutoParallelKeywords(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()

	keywords := []string{
		"search files and then fetch URL",
		"do both tasks simultaneously",
		"compare results from multiple sources",
		"aggregate data from several endpoints",
	}
	for _, q := range keywords {
		t.Run(q, func(t *testing.T) {
			assert.Empty(t, cmp.Diff(ModeDAG, Route(ModeAuto, q, cfg)))
		})
	}
}

func TestRouteAutoToolSignals(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	q := "search the codebase, read the file, then execute the command"
	assert.Empty(t, cmp.Diff(ModeDAG, Route(ModeAuto, q, cfg)))
}

func TestToolLoopModeString(t *testing.T) {
	t.Parallel()
	assert.Empty(t, cmp.Diff("react", ModeReAct.String()))
	assert.Empty(t, cmp.Diff("dag", ModeDAG.String()))
	assert.Empty(t, cmp.Diff("auto", ModeAuto.String()))
	assert.Empty(t, cmp.Diff("unknown", ToolLoopMode(99).String()))
}

func TestClassifyQueryDefault(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	assert.Empty(t, cmp.Diff(ModeReAct, classifyQuery("hello", cfg)))
}
