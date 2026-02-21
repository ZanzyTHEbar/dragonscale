package dag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouteExplicitModes(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()

	assert.Equal(t, ModeReAct, Route(ModeReAct, "anything", cfg))
	assert.Equal(t, ModeDAG, Route(ModeDAG, "anything", cfg))
}

func TestRouteAutoSimpleQuery(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	assert.Equal(t, ModeReAct, Route(ModeAuto, "what is the weather?", cfg))
}

func TestRouteAutoComplexQuery(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	longQuery := strings.Repeat("word ", 35)
	assert.Equal(t, ModeDAG, Route(ModeAuto, longQuery, cfg))
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
			assert.Equal(t, ModeDAG, Route(ModeAuto, q, cfg))
		})
	}
}

func TestRouteAutoToolSignals(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	q := "search the codebase, read the file, then execute the command"
	assert.Equal(t, ModeDAG, Route(ModeAuto, q, cfg))
}

func TestToolLoopModeString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "react", ModeReAct.String())
	assert.Equal(t, "dag", ModeDAG.String())
	assert.Equal(t, "auto", ModeAuto.String())
	assert.Equal(t, "unknown", ToolLoopMode(99).String())
}

func TestClassifyQueryDefault(t *testing.T) {
	t.Parallel()
	cfg := DefaultRouterConfig()
	assert.Equal(t, ModeReAct, classifyQuery("hello", cfg))
}
