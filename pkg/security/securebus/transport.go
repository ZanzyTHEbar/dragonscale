// Package securebus implements the SecureBus — the privilege boundary between
// the agent loop and tool execution. All tool calls pass through the SecureBus
// which enforces capability policies, injects secrets, scans output for leaks,
// and writes an append-only audit log.
//
// Transport abstracts the communication channel. The same SecureBus code runs
// on all backends:
//   - ChannelTransport: in-process Go channels (default, zero overhead)
//   - SocketTransport:  Unix domain socket (daemon mode, Layer 4)
//   - WasmTransport:    wazero isolate (untrusted tools, Layer 5)
package securebus

import (
	"context"
	"fmt"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
)

// Transport abstracts the communication channel between the agent loop (caller)
// and the SecureBus worker (executor). Implementations must be safe for
// concurrent use by multiple goroutines.
type Transport interface {
	// Send submits a request and blocks until the response is available or ctx
	// is cancelled.
	Send(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error)

	// Close shuts down the transport and releases any resources.
	Close() error
}

// ChannelTransport implements Transport using a pair of buffered Go channels.
// It runs the SecureBus in the same goroutine that handles the request, making
// it suitable for in-process use with near-zero overhead.
type ChannelTransport struct {
	reqCh  chan channelEnvelope
	closed chan struct{}
}

type channelEnvelope struct {
	ctx    context.Context
	req    itr.ToolRequest
	respCh chan channelResult
}

type channelResult struct {
	resp itr.ToolResponse
	err  error
}

// NewChannelTransport creates a ChannelTransport with the given buffer depth.
// depth 0 makes Send synchronous (no buffering). Typical value: 16.
func NewChannelTransport(depth int) *ChannelTransport {
	if depth < 0 {
		depth = 0
	}
	ct := &ChannelTransport{
		reqCh:  make(chan channelEnvelope, depth),
		closed: make(chan struct{}),
	}
	return ct
}

// Requests returns the channel that the SecureBus worker should read from.
// This is the server-side handle — the SecureBus calls this once to start
// processing.
func (ct *ChannelTransport) Requests() <-chan channelEnvelope {
	return ct.reqCh
}

// Send enqueues req and blocks until the response arrives or ctx is cancelled.
func (ct *ChannelTransport) Send(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error) {
	respCh := make(chan channelResult, 1)
	env := channelEnvelope{ctx: ctx, req: req, respCh: respCh}

	select {
	case ct.reqCh <- env:
	case <-ct.closed:
		return itr.ToolResponse{}, fmt.Errorf("transport closed")
	case <-ctx.Done():
		return itr.ToolResponse{}, ctx.Err()
	}

	select {
	case result := <-respCh:
		return result.resp, result.err
	case <-ct.closed:
		return itr.ToolResponse{}, fmt.Errorf("transport closed while waiting for response")
	case <-ctx.Done():
		return itr.ToolResponse{}, ctx.Err()
	}
}

// Close drains the request channel and signals that no new requests will be accepted.
func (ct *ChannelTransport) Close() error {
	select {
	case <-ct.closed:
	default:
		close(ct.closed)
	}
	return nil
}

// reply sends a response back to the caller of Send.
func (env channelEnvelope) reply(resp itr.ToolResponse, err error) {
	env.respCh <- channelResult{resp: resp, err: err}
}
