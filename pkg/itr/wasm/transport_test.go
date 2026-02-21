package wasm

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransportNonCodeExecForwarded(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime(t.Context(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(t.Context())

	forwarded := false
	transport := NewTransport(rt, func(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error) {
		forwarded = true
		return itr.NewSuccessResponse(req.ID, "forwarded", 0), nil
	})

	req := itr.NewToolExecRequest("id-1", "sess", "tc", "read_file", `{"path":"/tmp"}`)
	resp, err := transport.Send(t.Context(), req)
	require.NoError(t, err)
	assert.True(t, forwarded)
	assert.Empty(t, cmp.Diff("forwarded", resp.Result))
	assert.False(t, resp.IsError)
}

func TestTransportNonCodeExecNoFallback(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime(t.Context(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(t.Context())

	transport := NewTransport(rt, nil)

	req := itr.NewToolExecRequest("id-1", "sess", "tc", "read_file", `{"path":"/tmp"}`)
	resp, err := transport.Send(t.Context(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, "unsupported command type")
}

func TestTransportCodeExecEmptyCode(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime(t.Context(), DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(t.Context())

	transport := NewTransport(rt, nil)

	req := itr.NewCodeExecRequest("id-1", "sess", "", "javascript")
	resp, err := transport.Send(t.Context(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, "empty code")
}

func TestTransportClose(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime(t.Context(), DefaultRuntimeConfig())
	require.NoError(t, err)

	transport := NewTransport(rt, nil)
	assert.NoError(t, transport.Close())
}
