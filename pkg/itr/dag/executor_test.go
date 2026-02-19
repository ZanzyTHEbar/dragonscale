package dag_test

import (
	"context"
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/itr/dag"
	"github.com/sipeed/picoclaw/pkg/security/securebus"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeBus(t *testing.T, toolMap map[string]tools.Tool) *securebus.Bus {
	t.Helper()
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		tool, ok := toolMap[name]
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		tool, ok := toolMap[name]
		if !ok {
			return &tools.ToolResult{ForLLM: "tool not found: " + name, IsError: true}
		}
		return tool.Execute(ctx, args)
	}
	return securebus.New(securebus.DefaultBusConfig(), nil, capLookup, executor)
}

type staticTool struct {
	name   string
	result string
}

func (s *staticTool) Name() string        { return s.name }
func (s *staticTool) Description() string { return "test" }
func (s *staticTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (s *staticTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	if input, ok := args["input"].(string); ok {
		return &tools.ToolResult{ForLLM: fmt.Sprintf("%s:%s", s.result, input)}
	}
	return &tools.ToolResult{ForLLM: s.result}
}

func TestExecutor_LinearDependencyChain(t *testing.T) {
	toolMap := map[string]tools.Tool{
		"step1": &staticTool{name: "step1", result: "r1"},
		"step2": &staticTool{name: "step2", result: "r2"},
	}
	bus := makeBus(t, toolMap)
	defer bus.Close()

	executor := dag.NewExecutor(bus, nil)

	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "a", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "step1", ArgsJSON: "{}"}},
			{ID: "b", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "step2", ArgsJSON: `{"input": "#nodea"}`}, DependsOn: []string{"a"}},
		},
	}

	result, err := executor.Execute(context.Background(), "test-sess", plan)
	require.NoError(t, err)
	assert.Contains(t, result.NodeResults["a"], "r1")
	assert.Contains(t, result.NodeResults["b"], "r2")
}

func TestExecutor_ParallelNodes(t *testing.T) {
	toolMap := map[string]tools.Tool{
		"alpha": &staticTool{name: "alpha", result: "a-result"},
		"beta":  &staticTool{name: "beta", result: "b-result"},
	}
	bus := makeBus(t, toolMap)
	defer bus.Close()

	executor := dag.NewExecutor(bus, nil)

	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "n1", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "alpha", ArgsJSON: "{}"}},
			{ID: "n2", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "beta", ArgsJSON: "{}"}},
		},
	}

	result, err := executor.Execute(context.Background(), "test-sess", plan)
	require.NoError(t, err)
	assert.Equal(t, "a-result", result.NodeResults["n1"])
	assert.Equal(t, "b-result", result.NodeResults["n2"])
}

func TestExecutor_CycleDetection(t *testing.T) {
	bus := makeBus(t, map[string]tools.Tool{})
	defer bus.Close()

	executor := dag.NewExecutor(bus, nil)

	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "x", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "t", ArgsJSON: "{}"}, DependsOn: []string{"y"}},
			{ID: "y", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "t", ArgsJSON: "{}"}, DependsOn: []string{"x"}},
		},
	}

	_, err := executor.Execute(context.Background(), "test-sess", plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestExecutor_WithJoiner(t *testing.T) {
	toolMap := map[string]tools.Tool{
		"tool1": &staticTool{name: "tool1", result: "data-A"},
		"tool2": &staticTool{name: "tool2", result: "data-B"},
	}
	bus := makeBus(t, toolMap)
	defer bus.Close()

	joiner := func(_ context.Context, _, userQuery string) (string, uint32, error) {
		n := len(userQuery)
		if n > 20 {
			n = 20
		}
		return "synthesized: " + userQuery[:n], 50, nil
	}

	executor := dag.NewExecutor(bus, joiner)

	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "n1", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "tool1", ArgsJSON: "{}"}},
			{ID: "n2", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "tool2", ArgsJSON: "{}"}},
		},
		JoinerQuery: "Combine the results into a summary",
	}

	result, err := executor.Execute(context.Background(), "test-sess", plan)
	require.NoError(t, err)
	assert.Contains(t, result.FinalAnswer, "synthesized:")
	assert.Equal(t, uint32(50), result.TotalTokens)
}

func TestExecutor_EmptyPlan(t *testing.T) {
	bus := makeBus(t, map[string]tools.Tool{})
	defer bus.Close()

	executor := dag.NewExecutor(bus, nil)

	result, err := executor.Execute(context.Background(), "test-sess", &itr.DAGPlan{})
	require.NoError(t, err)
	assert.Empty(t, result.NodeResults)
}

func TestResolver_NodeRefSubstitution(t *testing.T) {
	argsJSON := `{"query": "search for #nodeprev results"}`
	toolMap := map[string]tools.Tool{
		"search": &staticTool{name: "search", result: "found"},
		"prev":   &staticTool{name: "prev", result: "previous-output"},
	}
	bus := makeBus(t, toolMap)
	defer bus.Close()

	executor := dag.NewExecutor(bus, nil)

	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "prev", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "prev", ArgsJSON: "{}"}},
			{ID: "search", Type: itr.CmdToolExec, Payload: itr.ToolExec{ToolName: "search", ArgsJSON: argsJSON}, DependsOn: []string{"prev"}},
		},
	}

	result, err := executor.Execute(context.Background(), "test-sess", plan)
	require.NoError(t, err)
	assert.Contains(t, result.NodeResults["prev"], "previous-output")
	assert.Contains(t, result.NodeResults["search"], "found")
}

func TestRouter_SimpleQuerySelectsReAct(t *testing.T) {
	cfg := dag.DefaultRouterConfig()
	mode := dag.Route(dag.ModeAuto, "What is the weather?", cfg)
	assert.Equal(t, dag.ModeReAct, mode)
}

func TestRouter_ComplexQuerySelectsDAG(t *testing.T) {
	cfg := dag.DefaultRouterConfig()
	mode := dag.Route(dag.ModeAuto, "Search for the latest news about AI, read the top 3 articles, and compare their viewpoints to create a summary report with aggregate statistics", cfg)
	assert.Equal(t, dag.ModeDAG, mode)
}

func TestRouter_ExplicitModeOverridesAuto(t *testing.T) {
	cfg := dag.DefaultRouterConfig()
	mode := dag.Route(dag.ModeReAct, "Do many complex parallel things simultaneously", cfg)
	assert.Equal(t, dag.ModeReAct, mode)
}

func TestPlanner_ValidatePlan(t *testing.T) {
	tests := []struct {
		name    string
		plan    string
		wantErr bool
	}{
		{
			name:    "valid simple plan",
			plan:    `{"nodes":[{"id":"a","type":"tool_exec","payload":{"tool_name":"read_file","args_json":"{}"}}],"joiner_query":"summarize"}`,
			wantErr: false,
		},
		{
			name:    "empty nodes",
			plan:    `{"nodes":[],"joiner_query":"summarize"}`,
			wantErr: true,
		},
		{
			name:    "duplicate IDs",
			plan:    `{"nodes":[{"id":"a","type":"tool_exec","payload":{}},{"id":"a","type":"tool_exec","payload":{}}]}`,
			wantErr: true,
		},
		{
			name:    "unknown dependency",
			plan:    `{"nodes":[{"id":"a","type":"tool_exec","payload":{},"depends_on":["nonexistent"]}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var plan itr.DAGPlan
			err := jsonv2.Unmarshal([]byte(tt.plan), &plan)
			require.NoError(t, err)

			// Use planner with a mock that returns the pre-built plan JSON
			mockModel := func(_ context.Context, _, _ string) (string, uint32, error) {
				return tt.plan, 10, nil
			}
			planner := dag.NewPlanner(mockModel, nil, dag.DefaultPlannerConfig())
			_, _, planErr := planner.Plan(context.Background(), "test query", nil)
			if tt.wantErr {
				assert.Error(t, planErr)
			} else {
				assert.NoError(t, planErr)
			}
		})
	}
}
