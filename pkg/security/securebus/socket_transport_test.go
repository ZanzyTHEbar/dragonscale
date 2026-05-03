package securebus

import (
	"context"
	"errors"
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

func TestSocketTransportSendHonorsContextDeadline(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "send-deadline.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ServeContext(context.Background(), func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			close(handlerStarted)
			select {
			case <-releaseHandler:
			case <-reqCtx.Done():
			}
			return itr.ToolResponse{ID: req.ID, Result: "late"}
		})
	}()

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	sendCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.Send(sendCtx, itr.NewFinalRequest("req-deadline", "sess", 0, "done", ""))
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "Send error = %v, want context deadline", err)

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive request")
	}

	_, err = client.Send(t.Context(), itr.NewFinalRequest("req-after-timeout", "sess", 0, "done", ""))
	require.ErrorContains(t, err, "transport closed")

	close(releaseHandler)
	require.NoError(t, server.Close())
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ServeContext did not return after server close")
	}
}

func TestSocketTransportSendHonorsContextWhileSendInFlight(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "send-inflight.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ServeContext(context.Background(), func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			if req.ID == "req-block" {
				close(firstStarted)
				select {
				case <-releaseFirst:
				case <-reqCtx.Done():
				}
			}
			return itr.ToolResponse{ID: req.ID, Result: "ok"}
		})
	}()

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Send(t.Context(), itr.NewFinalRequest("req-block", "sess", 0, "done", ""))
		firstDone <- err
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach handler")
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.Send(sendCtx, itr.NewFinalRequest("req-waiting", "sess", 0, "done", ""))
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "waiting Send error = %v, want context deadline", err)

	close(releaseFirst)
	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("first Send did not finish after release")
	}

	resp, err := client.Send(t.Context(), itr.NewFinalRequest("req-after-wait-timeout", "sess", 0, "done", ""))
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("req-after-wait-timeout", resp.ID))

	require.NoError(t, server.Close())
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ServeContext did not return after server close")
	}
}

func TestSocketTransportCloseConcurrent(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "concurrent-close.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)

	panicCh := make(chan interface{}, 16)
	var wg sync.WaitGroup
	for i := 0; i < cap(panicCh); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			_ = server.Close()
		}()
	}
	wg.Wait()
	close(panicCh)

	for r := range panicCh {
		t.Fatalf("Close panicked under concurrent calls: %v", r)
	}
}

type socketTransportTestContextKey string

func TestSocketTransportServeContextPropagatesValues(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "ctx-value.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	const key socketTransportTestContextKey = "request-key"
	ctx := context.WithValue(context.Background(), key, "request-value")
	go func() {
		_ = server.ServeContext(ctx, func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			value, _ := reqCtx.Value(key).(string)
			return itr.ToolResponse{ID: req.ID, Result: value}
		})
	}()

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	resp, err := client.Send(t.Context(), itr.NewFinalRequest("req-ctx", "sess", 0, "done", ""))
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("request-value", resp.Result))
}

func TestSocketTransportServeContextCancelsHandlers(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "ctx-cancel.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	handlerStarted := make(chan struct{})
	handlerDone := make(chan error, 1)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ServeContext(ctx, func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			close(handlerStarted)
			<-reqCtx.Done()
			handlerDone <- reqCtx.Err()
			return itr.ToolResponse{ID: req.ID, IsError: true, Result: reqCtx.Err().Error()}
		})
	}()

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	type sendResult struct {
		resp itr.ToolResponse
		err  error
	}
	sendDone := make(chan sendResult, 1)
	go func() {
		sendCtx, sendCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer sendCancel()
		resp, err := client.Send(sendCtx, itr.NewFinalRequest("req-cancel", "sess", 0, "done", ""))
		sendDone <- sendResult{resp: resp, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-handlerStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case err := <-handlerDone:
		require.True(t, errors.Is(err, context.Canceled), "handler should observe server context cancellation, got %v", err)
	case <-time.After(time.Second):
		t.Fatal("handler did not observe server context cancellation")
	}

	select {
	case result := <-sendDone:
		if result.err == nil && !result.resp.IsError {
			t.Fatalf("expected cancellation response or socket error, got response %#v", result.resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("socket Send did not return after server context cancellation")
	}

	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ServeContext did not return after context cancellation")
	}
}

func TestSocketTransportCloseCancelsHandlers(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "close-cancel.sock")

	server, err := NewSocketTransportServer(sockPath)
	require.NoError(t, err)
	defer server.Close()

	handlerStarted := make(chan struct{})
	handlerDone := make(chan error, 1)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ServeContext(context.Background(), func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			close(handlerStarted)
			<-reqCtx.Done()
			handlerDone <- reqCtx.Err()
			return itr.ToolResponse{ID: req.ID, IsError: true, Result: reqCtx.Err().Error()}
		})
	}()

	client, err := NewSocketTransportClient(sockPath)
	require.NoError(t, err)
	defer client.Close()

	sendDone := make(chan struct{}, 1)
	go func() {
		_, _ = client.Send(t.Context(), itr.NewFinalRequest("req-close-cancel", "sess", 0, "done", ""))
		sendDone <- struct{}{}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-handlerStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, server.Close())

	select {
	case err := <-handlerDone:
		require.True(t, errors.Is(err, context.Canceled), "handler should observe server close cancellation, got %v", err)
	case <-time.After(time.Second):
		t.Fatal("handler did not observe server close cancellation")
	}

	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("socket Send did not return after server close")
	}

	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ServeContext did not return after server close")
	}
}
