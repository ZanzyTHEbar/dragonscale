package itr

import (
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolRequestMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  ToolRequest
	}{
		{
			name: "ToolExec",
			req:  NewToolExecRequest("id-1", "sess-1", "tc-1", "read_file", `{"path":"/tmp/a.txt"}`),
		},
		{
			name: "Peek",
			req:  NewPeekRequest("id-2", "sess-1", 0, 100, 500),
		},
		{
			name: "Grep",
			req:  NewGrepRequest("id-3", "sess-1", 1, "func main", 10, true),
		},
		{
			name: "Partition",
			req:  NewPartitionRequest("id-4", "sess-1", 2, 8, "semantic", 128, true),
		},
		{
			name: "Recurse",
			req:  NewRecurseRequest("id-5", "sess-1", 1, "summarize this", "ctx-key-99", 3),
		},
		{
			name: "Final",
			req:  NewFinalRequest("id-6", "sess-1", 0, "The answer is 42.", "final_ans"),
		},
		{
			name: "ToolSearch",
			req:  NewToolSearchRequest("id-7", "sess-1", "file operations", 5),
		},
		{
			name: "CodeExec",
			req:  NewCodeExecRequest("id-8", "sess-1", "print('hello')", "python-wasm"),
		},
		{
			name: "DAGPlan",
			req: NewDAGPlanRequest("id-9", "sess-1", DAGPlan{
				Nodes: []DAGNode{
					{ID: "n1", Type: CmdToolExec, Payload: ToolExec{ToolName: "search", ArgsJSON: `{"q":"test"}`}},
					{ID: "n2", Type: CmdToolExec, Payload: ToolExec{ToolName: "read", ArgsJSON: `{"path":"#noden1"}`}, DependsOn: []string{"n1"}},
				},
				MaxParallel: 4,
				TokenBudget: 50000,
				JoinerQuery: "Synthesize the results",
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.req.Marshal()
			require.NoError(t, err)
			require.NotEmpty(t, data)

			decoded, err := UnmarshalRequest(data)
			require.NoError(t, err)

			assert.Empty(t, cmp.Diff(tt.req, decoded))
		})
	}
}

func TestToolExecPayloadPreservation(t *testing.T) {
	t.Parallel()
	req := NewToolExecRequest("id-1", "s", "tc", "shell", `{"cmd":"ls -la"}`)
	data, err := req.Marshal()
	require.NoError(t, err)

	decoded, err := UnmarshalRequest(data)
	require.NoError(t, err)

	payload, ok := decoded.Payload.(ToolExec)
	require.True(t, ok, "payload should be ToolExec")
	assert.Equal(t, "shell", payload.ToolName)
	assert.Equal(t, `{"cmd":"ls -la"}`, payload.ArgsJSON)
}

func TestGrepPayloadPreservation(t *testing.T) {
	t.Parallel()
	req := NewGrepRequest("g1", "s", 2, "error.*fatal", 25, true)
	data, err := req.Marshal()
	require.NoError(t, err)

	decoded, err := UnmarshalRequest(data)
	require.NoError(t, err)

	payload, ok := decoded.Payload.(Grep)
	require.True(t, ok)
	assert.Equal(t, "error.*fatal", payload.Pattern)
	assert.Equal(t, uint32(25), payload.MaxMatches)
	assert.True(t, payload.CaseInsensitive)
}

func TestDAGPlanPayloadPreservation(t *testing.T) {
	t.Parallel()
	plan := DAGPlan{
		Nodes: []DAGNode{
			{ID: "a", Type: CmdToolSearch, Payload: ToolSearch{Query: "files", MaxResults: 5}, DependsOn: nil},
			{ID: "b", Type: CmdToolExec, Payload: ToolExec{ToolName: "read", ArgsJSON: `{"path":"#nodea"}`}, DependsOn: []string{"a"}},
		},
		MaxParallel: 2,
		JoinerQuery: "combine results",
	}
	req := NewDAGPlanRequest("d1", "s", plan)
	data, err := req.Marshal()
	require.NoError(t, err)

	decoded, err := UnmarshalRequest(data)
	require.NoError(t, err)

	dagPlan, ok := decoded.Payload.(DAGPlan)
	require.True(t, ok)
	assert.Len(t, dagPlan.Nodes, 2)
	assert.Equal(t, "a", dagPlan.Nodes[0].ID)
	assert.Equal(t, CmdToolSearch, dagPlan.Nodes[0].Type)
	assert.Equal(t, []string{"a"}, dagPlan.Nodes[1].DependsOn)
	assert.Equal(t, uint8(2), dagPlan.MaxParallel)
	assert.Equal(t, "combine results", dagPlan.JoinerQuery)

	ts, ok := dagPlan.Nodes[0].Payload.(ToolSearch)
	require.True(t, ok, "ToolSearch payload should be retyped after unmarshal")
	assert.Equal(t, "files", ts.Query)
	assert.Equal(t, uint8(5), ts.MaxResults)

	te, ok := dagPlan.Nodes[1].Payload.(ToolExec)
	require.True(t, ok, "ToolExec payload should be retyped after unmarshal")
	assert.Equal(t, "read", te.ToolName)
	assert.Contains(t, te.ArgsJSON, "#nodea")
}

func TestToolResponseMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		resp ToolResponse
	}{
		{
			name: "success",
			resp: NewSuccessResponse("r1", `{"content":"hello"}`, 150),
		},
		{
			name: "error",
			resp: NewErrorResponse("r2", "tool not found: badtool"),
		},
		{
			name: "leak",
			resp: NewLeakResponse("r3", "redacted content", []string{"api_key", "token"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.resp.Marshal()
			require.NoError(t, err)

			decoded, err := UnmarshalResponse(data)
			require.NoError(t, err)

			assert.Empty(t, cmp.Diff(tt.resp, decoded))
		})
	}
}

func TestUnmarshalRequestJSON_UnknownType(t *testing.T) {
	t.Parallel()
	data, _ := jsonv2.Marshal(map[string]interface{}{
		"id":   "bad",
		"type": "nonexistent_command",
	})
	_, err := UnmarshalRequestJSON(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command type")
}

func TestUnmarshalRequestJSON_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := UnmarshalRequestJSON([]byte(`{invalid`))
	assert.Error(t, err)
}

func TestMarshalRequestFB_UnknownType(t *testing.T) {
	t.Parallel()
	_, err := MarshalRequestFB(ToolRequest{ID: "bad", Type: CommandType("bogus")})
	assert.Error(t, err)
}

func TestUnmarshalRequestFB_Garbage(t *testing.T) {
	t.Parallel()
	_, err := UnmarshalRequestFB([]byte{0, 0, 0, 0})
	assert.Error(t, err)
}

func TestRequestJSON_Roundtrip(t *testing.T) {
	t.Parallel()
	orig := NewToolExecRequest("j1", "s", "tc", "shell", `{"cmd":"ls"}`)
	data, err := jsonv2.Marshal(orig)
	require.NoError(t, err)

	decoded, err := UnmarshalRequestJSON(data)
	require.NoError(t, err)
	assert.Equal(t, orig.ID, decoded.ID)
	p := decoded.Payload.(ToolExec)
	assert.Equal(t, "shell", p.ToolName)
}

func TestResponseJSON_Roundtrip(t *testing.T) {
	t.Parallel()
	orig := NewSuccessResponse("j2", "ok", 10)
	data, err := jsonv2.Marshal(orig)
	require.NoError(t, err)

	decoded, err := UnmarshalResponseJSON(data)
	require.NoError(t, err)
	assert.Equal(t, orig.ID, decoded.ID)
	assert.Equal(t, "ok", decoded.Result)
}
