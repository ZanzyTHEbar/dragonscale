package dag

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractJSON_PlainJSON(t *testing.T) {
	input := `{"nodes": [{"id": "n1"}]}`
	assert.Equal(t, input, extractJSON(input))
}

func TestExtractJSON_MarkdownFenced(t *testing.T) {
	input := "Here is the plan:\n```json\n{\"nodes\": [{\"id\": \"n1\"}]}\n```\nDone."
	assert.Equal(t, `{"nodes": [{"id": "n1"}]}`, extractJSON(input))
}

func TestExtractJSON_GenericFenced(t *testing.T) {
	input := "```\n{\"nodes\": []}\n```"
	assert.Equal(t, `{"nodes": []}`, extractJSON(input))
}

func TestExtractJSON_LeadingText(t *testing.T) {
	input := "The plan is: {\"nodes\":[]}"
	assert.Equal(t, `{"nodes":[]}`, extractJSON(input))
}

func TestFindIndex(t *testing.T) {
	assert.Equal(t, 0, findIndex("abc", "a"))
	assert.Equal(t, 2, findIndex("abc", "c"))
	assert.Equal(t, -1, findIndex("abc", "z"))
	assert.Equal(t, -1, findIndex("", "a"))
	assert.Equal(t, -1, findIndex("ab", "abc"))
}

func TestValidatePlan_Valid(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "a", Type: itr.CmdToolExec},
			{ID: "b", Type: itr.CmdToolExec, DependsOn: []string{"a"}},
		},
	}
	assert.NoError(t, validatePlan(plan))
}

func TestValidatePlan_Empty(t *testing.T) {
	plan := &itr.DAGPlan{Nodes: nil}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no nodes")
}

func TestValidatePlan_DuplicateID(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "x", Type: itr.CmdToolExec},
			{ID: "x", Type: itr.CmdToolExec},
		},
	}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate node ID")
}

func TestValidatePlan_EmptyID(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{{ID: "", Type: itr.CmdToolExec}},
	}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty ID")
}

func TestValidatePlan_UnknownDependency(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "a", Type: itr.CmdToolExec, DependsOn: []string{"missing"}},
		},
	}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown node")
}

func TestValidatePlan_SelfDependency(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "a", Type: itr.CmdToolExec, DependsOn: []string{"a"}},
		},
	}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends on itself")
}

func TestValidatePlan_CyclicDependency(t *testing.T) {
	plan := &itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{ID: "a", Type: itr.CmdToolExec, DependsOn: []string{"b"}},
			{ID: "b", Type: itr.CmdToolExec, DependsOn: []string{"a"}},
		},
	}
	err := validatePlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestParsePlanResponse_ValidJSON(t *testing.T) {
	input := `{
		"nodes": [
			{"id": "n1", "type": "tool_exec", "payload": {"tool_name": "read_file", "args_json": "{\"path\":\"/tmp/a\"}"}}
		],
		"joiner_query": "summarize"
	}`

	plan, err := parsePlanResponse(input)
	require.NoError(t, err)
	require.Len(t, plan.Nodes, 1)
	assert.Equal(t, "n1", plan.Nodes[0].ID)
	assert.Equal(t, itr.CmdToolExec, plan.Nodes[0].Type)
	assert.Equal(t, "summarize", plan.JoinerQuery)

	te, ok := plan.Nodes[0].Payload.(itr.ToolExec)
	require.True(t, ok)
	assert.Equal(t, "read_file", te.ToolName)
}

func TestParsePlanResponse_WithMarkdownFence(t *testing.T) {
	input := "```json\n" + `{"nodes": [{"id": "x", "type": "tool_search", "payload": {"query": "files"}}]}` + "\n```"

	plan, err := parsePlanResponse(input)
	require.NoError(t, err)
	require.Len(t, plan.Nodes, 1)

	ts, ok := plan.Nodes[0].Payload.(itr.ToolSearch)
	require.True(t, ok)
	assert.Equal(t, "files", ts.Query)
}

func TestParsePlanResponse_InvalidJSON(t *testing.T) {
	_, err := parsePlanResponse("not json at all")
	assert.Error(t, err)
}

func TestPlannerPlanE2E(t *testing.T) {
	mockLLM := func(ctx context.Context, systemPrompt, userQuery string) (string, uint32, error) {
		plan := itr.DAGPlan{
			Nodes: []itr.DAGNode{
				{ID: "search", Type: itr.CmdToolExec,
					Payload: itr.ToolExec{ToolName: "search", ArgsJSON: `{"q":"test"}`}},
				{ID: "read", Type: itr.CmdToolExec,
					Payload:   itr.ToolExec{ToolName: "read_file", ArgsJSON: `{"path":"#nodesearch"}`},
					DependsOn: []string{"search"}},
			},
			JoinerQuery: "Combine results",
		}
		b, _ := jsonv2.Marshal(plan)
		return string(b), 100, nil
	}

	planner := NewPlanner(mockLLM, nil, DefaultPlannerConfig())
	plan, tokens, err := planner.Plan(context.Background(), "search and read", nil)
	require.NoError(t, err)
	assert.Equal(t, uint32(100), tokens)
	require.Len(t, plan.Nodes, 2)
	assert.Equal(t, "search", plan.Nodes[0].ID)
	assert.Equal(t, uint8(8), plan.MaxParallel)
}

func TestPlannerPlanLLMError(t *testing.T) {
	mockLLM := func(ctx context.Context, systemPrompt, userQuery string) (string, uint32, error) {
		return "", 50, assert.AnError
	}

	planner := NewPlanner(mockLLM, nil, DefaultPlannerConfig())
	_, tokens, err := planner.Plan(context.Background(), "anything", nil)
	assert.Error(t, err)
	assert.Equal(t, uint32(50), tokens)
}
