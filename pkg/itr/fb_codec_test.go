package itr_test

import (
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFBCodec_RequestRoundtrip_Peek(t *testing.T) {
	orig := itr.NewPeekRequest("req-1", "sess-A", 2, 1024, 4096)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	assert.Equal(t, orig.ID, got.ID)
	assert.Equal(t, orig.Type, got.Type)
	assert.Equal(t, orig.Depth, got.Depth)
	assert.Equal(t, orig.SessionKey, got.SessionKey)
	origP, ok := orig.Payload.(itr.Peek)
	require.True(t, ok, "orig payload should be Peek")
	gotP, ok := got.Payload.(itr.Peek)
	require.True(t, ok, "got payload should be Peek")
	assert.Equal(t, origP, gotP)
}

func TestFBCodec_RequestRoundtrip_Grep(t *testing.T) {
	orig := itr.NewGrepRequest("req-2", "sess-B", 1, "error.*fatal", 25, true)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	assert.Equal(t, orig.Type, got.Type)
	p, ok := got.Payload.(itr.Grep)
	require.True(t, ok, "payload should be Grep")
	assert.Equal(t, "error.*fatal", p.Pattern)
	assert.Equal(t, uint32(25), p.MaxMatches)
	assert.True(t, p.CaseInsensitive)
}

func TestFBCodec_RequestRoundtrip_Partition(t *testing.T) {
	orig := itr.NewPartitionRequest("req-3", "sess-C", 0, 8, "semantic", 100, true)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.Partition)
	require.True(t, ok, "payload should be Partition")
	assert.Equal(t, uint32(8), p.K)
	assert.Equal(t, "semantic", p.Method)
	assert.Equal(t, uint32(100), p.Overlap)
	assert.True(t, p.Semantic)
}

func TestFBCodec_RequestRoundtrip_Recurse(t *testing.T) {
	orig := itr.NewRecurseRequest("req-4", "sess-D", 3, "summarize this", "ctx-key-7", 5)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.Recurse)
	require.True(t, ok, "payload should be Recurse")
	assert.Equal(t, "summarize this", p.SubQuery)
	assert.Equal(t, "ctx-key-7", p.ContextKey)
	assert.Equal(t, uint8(5), p.DepthHint)
}

func TestFBCodec_RequestRoundtrip_ToolExec(t *testing.T) {
	orig := itr.NewToolExecRequest("req-5", "sess-E", "tc-1", "read_file", `{"path":"/etc/hosts"}`)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.ToolExec)
	require.True(t, ok, "payload should be ToolExec")
	assert.Equal(t, "read_file", p.ToolName)
	assert.Equal(t, `{"path":"/etc/hosts"}`, p.ArgsJSON)
	assert.Equal(t, "tc-1", got.ToolCallID)
}

func TestFBCodec_RequestRoundtrip_ExecWasm(t *testing.T) {
	orig := itr.ToolRequest{
		ID:         "req-6",
		Type:       itr.CmdExecWasm,
		Payload:    itr.ExecWasm{ModuleKey: "mod-1", Entry: "main", InputJSON: `{"x":1}`},
		Timestamp:  time.Now().UnixNano(),
		SessionKey: "sess-F",
	}

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.ExecWasm)
	require.True(t, ok, "payload should be ExecWasm")
	assert.Equal(t, "mod-1", p.ModuleKey)
	assert.Equal(t, "main", p.Entry)
	assert.Equal(t, `{"x":1}`, p.InputJSON)
}

func TestFBCodec_RequestRoundtrip_Final(t *testing.T) {
	orig := itr.NewFinalRequest("req-7", "sess-G", 2, "The answer is 42", "ans_var")

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.Final)
	require.True(t, ok, "payload should be Final")
	assert.Equal(t, "The answer is 42", p.Answer)
	assert.Equal(t, "ans_var", p.VarName)
}

func TestFBCodec_RequestRoundtrip_ToolSearch(t *testing.T) {
	orig := itr.NewToolSearchRequest("req-8", "sess-H", "find file tools", 5)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.ToolSearch)
	require.True(t, ok, "payload should be ToolSearch")
	assert.Equal(t, "find file tools", p.Query)
	assert.Equal(t, uint8(5), p.MaxResults)
}

func TestFBCodec_RequestRoundtrip_CodeExec(t *testing.T) {
	orig := itr.NewCodeExecRequest("req-9", "sess-I", "console.log('hi')", "javascript")

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	p, ok := got.Payload.(itr.CodeExec)
	require.True(t, ok, "payload should be CodeExec")
	assert.Equal(t, "console.log('hi')", p.Code)
	assert.Equal(t, "javascript", p.Language)
}

func TestFBCodec_RequestRoundtrip_DAGPlan(t *testing.T) {
	plan := itr.DAGPlan{
		Nodes: []itr.DAGNode{
			{
				ID:      "a",
				Type:    itr.CmdToolExec,
				Payload: itr.ToolExec{ToolName: "read_file", ArgsJSON: `{"path":"x.txt"}`},
			},
			{
				ID:        "b",
				Type:      itr.CmdToolSearch,
				Payload:   itr.ToolSearch{Query: "search tools", MaxResults: 3},
				DependsOn: []string{"a"},
			},
		},
		MaxParallel: 4,
		TokenBudget: 10000,
		JoinerQuery: "Summarize the results",
	}
	orig := itr.NewDAGPlanRequest("req-10", "sess-J", plan)

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	assert.Equal(t, itr.CmdDAGPlan, got.Type)

	gotPlan, ok := got.Payload.(itr.DAGPlan)
	require.True(t, ok)

	assert.Equal(t, uint8(4), gotPlan.MaxParallel)
	assert.Equal(t, uint32(10000), gotPlan.TokenBudget)
	assert.Equal(t, "Summarize the results", gotPlan.JoinerQuery)
	require.Len(t, gotPlan.Nodes, 2)

	nodeA := gotPlan.Nodes[0]
	assert.Equal(t, "a", nodeA.ID)
	assert.Equal(t, itr.CmdToolExec, nodeA.Type)
	te, ok := nodeA.Payload.(itr.ToolExec)
	require.True(t, ok)
	assert.Equal(t, "read_file", te.ToolName)
	assert.Equal(t, `{"path":"x.txt"}`, te.ArgsJSON)

	nodeB := gotPlan.Nodes[1]
	assert.Equal(t, "b", nodeB.ID)
	assert.Equal(t, itr.CmdToolSearch, nodeB.Type)
	ts, ok := nodeB.Payload.(itr.ToolSearch)
	require.True(t, ok)
	assert.Equal(t, "search tools", ts.Query)
	assert.Equal(t, uint8(3), ts.MaxResults)
	assert.Equal(t, []string{"a"}, nodeB.DependsOn)
}

func TestFBCodec_ResponseRoundtrip_Success(t *testing.T) {
	orig := itr.NewSuccessResponse("resp-1", "file contents here", 150)

	data, err := itr.MarshalResponseFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalResponseFB(data)
	require.NoError(t, err)

	assert.Equal(t, "resp-1", got.ID)
	assert.Equal(t, "file contents here", got.Result)
	assert.False(t, got.IsError)
	assert.Equal(t, uint32(150), got.CostTokens)
}

func TestFBCodec_ResponseRoundtrip_Error(t *testing.T) {
	orig := itr.NewErrorResponse("resp-2", "tool not found")

	data, err := itr.MarshalResponseFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalResponseFB(data)
	require.NoError(t, err)

	assert.True(t, got.IsError)
	assert.Equal(t, "tool not found", got.Result)
}

func TestFBCodec_ResponseRoundtrip_Leak(t *testing.T) {
	orig := itr.NewLeakResponse("resp-3", "redacted output", []string{"api_key", "password"})

	data, err := itr.MarshalResponseFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalResponseFB(data)
	require.NoError(t, err)

	assert.True(t, got.LeakDetected)
	assert.Equal(t, []string{"api_key", "password"}, got.RedactedKeys)
}

func TestFBCodec_TimestampPreserved(t *testing.T) {
	ts := time.Now().UnixNano()
	orig := itr.ToolRequest{
		ID:        "ts-test",
		Type:      itr.CmdPeek,
		Payload:   itr.Peek{Start: 0, Length: 10},
		Timestamp: ts,
	}

	data, err := itr.MarshalRequestFB(orig)
	require.NoError(t, err)

	got, err := itr.UnmarshalRequestFB(data)
	require.NoError(t, err)

	assert.Equal(t, ts, got.Timestamp)
}

func TestFBCodec_UnknownCommandType(t *testing.T) {
	_, err := itr.MarshalRequestFB(itr.ToolRequest{
		ID:   "bad",
		Type: itr.CommandType("nonexistent"),
	})
	assert.Error(t, err)
}
