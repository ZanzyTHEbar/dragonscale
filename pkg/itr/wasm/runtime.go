// Package wasm provides sandboxed code execution via wazero (pure Go, no CGO).
// Each invocation runs in an isolated WASM instance with its own linear memory,
// capability-gated WASI imports, and configurable resource limits.
//
// This is the Layer 5 isolation mechanism for untrusted tool code within the
// Isolated Tool Runtime. It integrates with the SecureBus via the CmdCodeExec
// command type.
package wasm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// RuntimeConfig controls resource limits for WASM execution.
type RuntimeConfig struct {
	MaxMemoryPages uint32        // max WASM memory pages (64KiB each); 0 → 256 (16MiB)
	ExecTimeout    time.Duration // per-invocation timeout; 0 → 30s
	MaxOutputBytes int           // max stdout capture; 0 → 1MiB
	EnableWASI     bool          // mount WASI snapshot_preview1 imports
}

// DefaultRuntimeConfig returns safe defaults for sandboxed execution.
func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		MaxMemoryPages: 256,
		ExecTimeout:    30 * time.Second,
		MaxOutputBytes: 1 << 20,
		EnableWASI:     true,
	}
}

// Runtime manages a pool of wazero runtimes for executing WASM modules.
// Each invocation gets a fresh module instance with private linear memory.
type Runtime struct {
	mu     sync.Mutex
	config RuntimeConfig
	engine wazero.Runtime
	closed bool
}

// NewRuntime creates a WASM runtime backed by wazero's compiler.
func NewRuntime(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.MaxMemoryPages == 0 {
		cfg.MaxMemoryPages = 256
	}
	if cfg.ExecTimeout == 0 {
		cfg.ExecTimeout = 30 * time.Second
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 1 << 20
	}

	runtimeCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(cfg.MaxMemoryPages)

	engine := wazero.NewRuntimeWithConfig(ctx, runtimeCfg)

	if cfg.EnableWASI {
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, engine); err != nil {
			engine.Close(ctx)
			return nil, fmt.Errorf("instantiate WASI: %w", err)
		}
	}

	return &Runtime{
		config: cfg,
		engine: engine,
	}, nil
}

// ExecResult holds the output of a WASM execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode uint32
	Duration time.Duration
}

// Execute compiles and runs a WASM binary with the given stdin input.
// Each invocation gets a fresh module instance — no state leaks between calls.
func (r *Runtime) Execute(ctx context.Context, wasmBinary []byte, stdin string) (*ExecResult, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("runtime closed")
	}
	r.mu.Unlock()

	execCtx, cancel := context.WithTimeout(ctx, r.config.ExecTimeout)
	defer cancel()

	compiled, err := r.engine.CompileModule(execCtx, wasmBinary)
	if err != nil {
		return nil, fmt.Errorf("compile WASM module: %w", err)
	}
	defer compiled.Close(execCtx)

	var stdout, stderr limitedBuffer
	stdout.max = r.config.MaxOutputBytes
	stderr.max = r.config.MaxOutputBytes

	moduleCfg := wazero.NewModuleConfig().
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithStdin(strings.NewReader(stdin)).
		WithName("")

	start := time.Now()
	mod, err := r.engine.InstantiateModule(execCtx, compiled, moduleCfg)
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := extractExitCode(err); ok {
			result.ExitCode = exitErr
			return result, nil
		}
		return result, fmt.Errorf("execute WASM: %w", err)
	}

	if mod != nil {
		_ = mod.Close(execCtx)
	}

	return result, nil
}

// Close releases all wazero resources.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true
	return r.engine.Close(ctx)
}

// limitedBuffer captures output up to a maximum size.
type limitedBuffer struct {
	buf strings.Builder
	max int
	n   int
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	remaining := lb.max - lb.n
	if remaining <= 0 {
		return total, nil
	}
	toWrite := p
	if len(toWrite) > remaining {
		toWrite = toWrite[:remaining]
	}
	n, err := lb.buf.Write(toWrite)
	lb.n += n
	if err != nil {
		return n, err
	}
	return total, nil
}

func (lb *limitedBuffer) String() string {
	return lb.buf.String()
}

var _ io.Writer = (*limitedBuffer)(nil)

// extractExitCode checks if err is a sys.ExitError and returns the code.
func extractExitCode(err error) (uint32, bool) {
	if err == nil {
		return 0, false
	}
	errStr := err.Error()
	if strings.Contains(errStr, "exit_code(") {
		var code uint32
		if _, scanErr := fmt.Sscanf(errStr, "module closed with exit_code(%d)", &code); scanErr == nil {
			return code, true
		}
	}
	return 0, false
}
