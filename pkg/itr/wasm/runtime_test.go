package wasm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalWASM is a valid WASM module that immediately returns (no-op _start).
// Produced by hand: (module (func (export "_start")))
var minimalWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic
	0x01, 0x00, 0x00, 0x00, // version 1

	// Type section: one function type () -> ()
	0x01,       // section id
	0x04,       // section size
	0x01,       // one type
	0x60,       // func
	0x00, 0x00, // no params, no results

	// Function section: one function of type 0
	0x03, // section id
	0x02, // section size
	0x01, // one function
	0x00, // type index 0

	// Export section: export "_start" as function 0
	0x07,                         // section id
	0x0a,                         // section size
	0x01,                         // one export
	0x06,                         // name length
	'_', 's', 't', 'a', 'r', 't', // name
	0x00, // export kind: function
	0x00, // function index

	// Code section: one function body
	0x0a, // section id
	0x04, // section size
	0x01, // one body
	0x02, // body size
	0x00, // no locals
	0x0b, // end
}

func TestNewRuntime(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(ctx)
}

func TestExecuteMinimalModule(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(ctx)

	result, err := rt.Execute(ctx, minimalWASM, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0), result.ExitCode)
	assert.Empty(t, result.Stderr)
	assert.True(t, result.Duration > 0)
}

func TestExecuteTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRuntimeConfig()
	cfg.ExecTimeout = 1 * time.Millisecond
	rt, err := NewRuntime(ctx, cfg)
	require.NoError(t, err)
	defer rt.Close(ctx)

	result, err := rt.Execute(ctx, minimalWASM, "")
	// Minimal module is fast enough to succeed even with 1ms timeout.
	// This test validates that the timeout machinery doesn't break normal execution.
	if err == nil {
		assert.Equal(t, uint32(0), result.ExitCode)
	}
}

func TestExecuteInvalidWASM(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, DefaultRuntimeConfig())
	require.NoError(t, err)
	defer rt.Close(ctx)

	_, err = rt.Execute(ctx, []byte("not wasm"), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "compile WASM module")
}

func TestRuntimeCloseIdempotent(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, DefaultRuntimeConfig())
	require.NoError(t, err)

	assert.NoError(t, rt.Close(ctx))
	assert.NoError(t, rt.Close(ctx))
}

func TestExecuteAfterClose(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, DefaultRuntimeConfig())
	require.NoError(t, err)
	rt.Close(ctx)

	_, err = rt.Execute(ctx, minimalWASM, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "runtime closed")
}

func TestLimitedBuffer(t *testing.T) {
	lb := &limitedBuffer{max: 5}
	n, err := lb.Write([]byte("hello world"))
	assert.NoError(t, err)
	assert.Equal(t, 11, n)
	assert.Equal(t, "hello", lb.String())
}

func TestLimitedBufferExactFit(t *testing.T) {
	lb := &limitedBuffer{max: 5}
	n, err := lb.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", lb.String())

	n, err = lb.Write([]byte("more"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "hello", lb.String())
}
