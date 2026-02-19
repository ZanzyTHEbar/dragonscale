package securebus

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSocketTransportRoundTrip(t *testing.T) {
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

	resp, err := client.Send(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "req-001", resp.ID)
	assert.Contains(t, resp.Result, "req-001")

	server.Close()
	wg.Wait()
}

func TestSocketTransportMultipleRequests(t *testing.T) {
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
		req := itr.ToolRequest{
			ID:        "req-" + string(rune('A'+i)),
			Type:      itr.CmdToolExec,
			Timestamp: time.Now().UnixNano(),
		}
		resp, err := client.Send(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, req.ID, resp.ID)
	}
}

func TestSocketTransportCleanup(t *testing.T) {
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

	_, err = client.Send(context.Background(), itr.ToolRequest{ID: "fail"})
	assert.Error(t, err)

	server.Close()
}
