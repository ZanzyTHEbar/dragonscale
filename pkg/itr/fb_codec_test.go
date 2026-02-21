package itr_test

import (
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFBCodec_RequestRoundtrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  itr.ToolRequest
	}{
		{
			name: "peek",
			req:  itr.NewPeekRequest("req-1", "sess-A", 2, 1024, 4096),
		},
		{
			name: "grep",
			req:  itr.NewGrepRequest("req-2", "sess-B", 1, "error.*fatal", 25, true),
		},
		{
			name: "partition",
			req:  itr.NewPartitionRequest("req-3", "sess-C", 0, 8, "semantic", 100, true),
		},
		{
			name: "recurse",
			req:  itr.NewRecurseRequest("req-4", "sess-D", 3, "summarize this", "ctx-key-7", 5),
		},
		{
			name: "tool exec",
			req:  itr.NewToolExecRequest("req-5", "sess-E", "tc-1", "read_file", `{"path":"/etc/hosts"}`),
		},
		{
			name: "exec wasm",
			req: itr.ToolRequest{
				ID:         "req-6",
				Type:       itr.CmdExecWasm,
				Payload:    itr.ExecWasm{ModuleKey: "mod-1", Entry: "main", InputJSON: `{"x":1}`},
				Timestamp:  time.Now().UnixNano(),
				SessionKey: "sess-F",
			},
		},
		{
			name: "final",
			req:  itr.NewFinalRequest("req-7", "sess-G", 2, "The answer is 42", "ans_var"),
		},
		{
			name: "tool search",
			req:  itr.NewToolSearchRequest("req-8", "sess-H", "find file tools", 5),
		},
		{
			name: "code exec",
			req:  itr.NewCodeExecRequest("req-9", "sess-I", "console.log('hi')", "javascript"),
		},
		{
			name: "dag plan",
			req: itr.NewDAGPlanRequest("req-10", "sess-J", itr.DAGPlan{
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
			}),
		},
		{
			name: "timestamp preserved",
			req: itr.ToolRequest{
				ID:        "ts-test",
				Type:      itr.CmdPeek,
				Payload:   itr.Peek{Start: 0, Length: 10},
				Timestamp: time.Now().UnixNano(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := itr.MarshalRequestFB(tt.req)
			require.NoError(t, err)

			got, err := itr.UnmarshalRequestFB(data)
			require.NoError(t, err)

			assert.Empty(t, cmp.Diff(tt.req, got))
		})
	}
}

func TestFBCodec_ResponseRoundtrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  itr.ToolResponse
	}{
		{
			name: "success",
			res:  itr.NewSuccessResponse("resp-1", "file contents here", 150),
		},
		{
			name: "error",
			res:  itr.NewErrorResponse("resp-2", "tool not found"),
		},
		{
			name: "leak",
			res:  itr.NewLeakResponse("resp-3", "redacted output", []string{"api_key", "password"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := itr.MarshalResponseFB(tt.res)
			require.NoError(t, err)

			got, err := itr.UnmarshalResponseFB(data)
			require.NoError(t, err)

			assert.Empty(t, cmp.Diff(tt.res, got))
		})
	}
}

func TestFBCodec_UnknownCommandType(t *testing.T) {
	t.Parallel()
	_, err := itr.MarshalRequestFB(itr.ToolRequest{
		ID:   "bad",
		Type: itr.CommandType("nonexistent"),
	})
	require.Error(t, err)
}
