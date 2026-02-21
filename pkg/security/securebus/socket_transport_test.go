package securebus

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSocketTransportRoundTrip(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	handler := func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse {
		return itr.ToolResponse{
			ID:     req.ID,
			Result: `{"echo":"` + req.ID + `"}`,
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = server.Serve(handler)
	}()

	time.Sleep(50 * time.Millisecond)

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	req := itr.ToolRequest{
		ID:        "req-001",
		Type:      itr.CmdToolExec,
		Payload:   itr.ToolExec{ToolName: "echo", ArgsJSON: `{}`},
		Timestamp: time.Now().UnixNano(),
	}

	resp, err := client.Send(t.Context(), req)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("req-001", resp.ID))
	assert.Contains(t, resp.Result, "req-001")

	server.Close()
	wg.Wait()
}

func TestSocketTransportMultipleRequests(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "multi.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	handler := func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse {
		return itr.ToolResponse{ID: req.ID, Result: req.ID + "-done"}
	}

	go func() { _ = server.Serve(handler) }()
	time.Sleep(50 * time.Millisecond)

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	for i := 0; i < 10; i++ {
		req := itr.NewToolExecRequest(
			"req-"+string(rune('A'+i)),
			"sess",
			"",
			"echo",
			`{}`,
		)
		resp, err := client.Send(t.Context(), req)
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(req.ID, resp.ID))
	}
}

func TestSocketTransportCleanup(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "cleanup.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)

	_, statErr := os.Stat(sockPath)
	assert.NoError(t, statErr, "socket file should exist")

	server.Close()

	_, statErr = os.Stat(sockPath)
	assert.True(t, os.IsNotExist(statErr), "socket file should be removed after Close")
}

func TestSocketTransportClientSendOnClosed(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "closed.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	go func() {
		_ = server.Serve(func(ctx context.Context, req itr.ToolRequest) itr.ToolResponse {
			return itr.ToolResponse{ID: req.ID}
		})
	}()
	time.Sleep(50 * time.Millisecond)

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)

	client.Close()

	_, err = client.Send(t.Context(), itr.ToolRequest{ID: "fail"})
	assert.Error(t, err)

	server.Close()
}
