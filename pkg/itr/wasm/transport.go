package wasm

import (
	"context"
	"fmt"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
)

// Transport implements the SecureBus transport interface for WASM-isolated
// tool execution. It handles CmdCodeExec requests by compiling and running
// the provided WASM binary in an isolated instance.
//
// Non-CodeExec requests are forwarded to a fallback transport.
type Transport struct {
	runtime  *Runtime
	fallback func(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error)
}

// NewTransport creates a WasmTransport backed by the given Runtime.
// fallback handles non-CodeExec requests.
func NewTransport(rt *Runtime, fallback func(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error)) *Transport {
	return &Transport{
		runtime:  rt,
		fallback: fallback,
	}
}

// Send dispatches a request. CodeExec requests are handled by the WASM runtime;
// all others are forwarded to the fallback.
func (t *Transport) Send(ctx context.Context, req itr.ToolRequest) (itr.ToolResponse, error) {
	if req.Type != itr.CmdCodeExec {
		if t.fallback != nil {
			return t.fallback(ctx, req)
		}
		return itr.ToolResponse{
			ID:      req.ID,
			IsError: true,
			Result:  "WasmTransport: unsupported command type: " + string(req.Type),
		}, nil
	}

	codeExec, ok := req.Payload.(itr.CodeExec)
	if !ok {
		return itr.ToolResponse{
			ID:      req.ID,
			IsError: true,
			Result:  fmt.Sprintf("expected CodeExec payload, got %T", req.Payload),
		}, nil
	}

	if len(codeExec.Code) == 0 {
		return itr.ToolResponse{
			ID:      req.ID,
			IsError: true,
			Result:  "CodeExec: empty code",
		}, nil
	}

	result, err := t.runtime.Execute(ctx, []byte(codeExec.Code), "")
	if err != nil {
		return itr.ToolResponse{
			ID:      req.ID,
			IsError: true,
			Result:  fmt.Sprintf("WASM execution failed: %v", err),
		}, nil
	}

	output := result.Stdout
	if result.Stderr != "" {
		output += "\n[stderr]\n" + result.Stderr
	}
	if result.ExitCode != 0 {
		return itr.ToolResponse{
			ID:      req.ID,
			IsError: true,
			Result:  fmt.Sprintf("exit code %d: %s", result.ExitCode, output),
		}, nil
	}

	return itr.ToolResponse{
		ID:     req.ID,
		Result: output,
	}, nil
}

// Close releases the WASM runtime resources.
func (t *Transport) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return t.runtime.Close(ctx)
}
