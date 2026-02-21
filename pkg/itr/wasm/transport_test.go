package wasm

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransportNonCodeExecForwarded(t *testing.T) {
	rt, err := NewRuntime(context.Background(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(context.Background())

	forwarded := false
	transport := NewTransport(rt, func(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error) {
		forwarded = true
		return itr.NewSuccessResponse(req.ID, "forwarded", 0), nil
	})

	req := itr.NewToolExecRequest("id-1", "sess", "tc", "read_file", `{"path":"/tmp"}`)
	resp, err := transport.Send(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, forwarded)
	assert.Equal(t, "forwarded", resp.Result)
	assert.False(t, resp.IsError)
}

func TestTransportNonCodeExecNoFallback(t *testing.T) {
	rt, err := NewRuntime(context.Background(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(context.Background())

	transport := NewTransport(rt, nil)

	req := itr.NewToolExecRequest("id-1", "sess", "tc", "read_file", `{"path":"/tmp"}`)
	resp, err := transport.Send(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, "unsupported command type")
}

func TestTransportCodeExecEmptyCode(t *testing.T) {
	rt, err := NewRuntime(context.Background(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(context.Background())

	transport := NewTransport(rt, nil)

	req := itr.NewCodeExecRequest("id-1", "sess", "", "javascript")
	resp, err := transport.Send(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, "empty code")
}

func TestTransportClose(t *testing.T) {
	rt, err := NewRuntime(context.Background(), DefaultRuntimeConfig())
	require.NoError(t, err)

	transport := NewTransport(rt, nil)
	assert.NoError(t, transport.Close())
}
