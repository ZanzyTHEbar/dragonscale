package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
	"github.com/stretchr/testify/require"
)

func TestTaskRegistryAndDispatch(t *testing.T) {
	root := t.TempDir()
	ops := New(runner.OSRunner{}, root)

	task := &captureTask{
		result: Result{
			Task:     "demo",
			Command:  "echo hi",
			ExitCode: 0,
			Stdout:   "ok",
		},
	}
	ops.Register(task)
	require.True(t, ops.HasTask("demo"))
	require.False(t, ops.HasTask("missing"))

	tr, err := ops.Run(context.Background(), "demo", []string{"--flag", "value"}, &Context{Cwd: root})
	require.NoError(t, err)
	require.True(t, task.called)
	require.Equal(t, []string{"--flag", "value"}, task.receivedArgs)
	require.Equal(t, root, task.receivedCtx.Cwd)
	require.Equal(t, "demo", tr.Task)
	require.Equal(t, 0, tr.ExitCode)
	require.Equal(t, "ok", tr.Stdout)
}

func TestRunPropagatesTaskError(t *testing.T) {
	root := t.TempDir()
	ops := New(runner.OSRunner{}, root)
	taskErr := errors.New("boom")
	resultErr := Result{Task: "failed", Command: "false", ExitCode: 2, Err: taskErr.Error()}
	ops.Register(&captureTask{result: resultErr})

	tr, err := ops.Run(context.Background(), "failed", []string{}, &Context{Root: root})
	require.Error(t, err)
	require.Equal(t, "failed", tr.Task)
	require.Equal(t, 2, tr.ExitCode)
	require.Equal(t, "boom", tr.Error)
}

func TestRunAppliesTimeoutContext(t *testing.T) {
	root := t.TempDir()
	ops := New(runner.OSRunner{}, root)
	called := false
	blocking := &captureTask{
		result: Result{Task: "slow", ExitCode: 0},
		withContext: func(ctx context.Context) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.True(t, time.Now().Before(deadline))
			called = true
		},
	}
	ops.Register(blocking)

	_, err := ops.Run(context.Background(), "slow", nil, &Context{Root: root, Timeout: 250 * time.Millisecond})
	require.NoError(t, err)
	require.True(t, called)
	require.True(t, blocking.called)
}

func TestListIsStableWithoutDuplicateRegistration(t *testing.T) {
	root := t.TempDir()
	ops := New(runner.OSRunner{}, root)
	ops.Register(&captureTask{result: Result{Task: "dup", ExitCode: 0}})
	ops.Register(&captureTask{result: Result{Task: "dup", ExitCode: 0}})

	items := ops.List()
	require.Len(t, items, 1)
	require.Equal(t, "dup", items[0].Name())
}

type captureTask struct {
	called       bool
	result       Result
	receivedArgs []string
	receivedCtx  *Context
	withContext  func(context.Context)
}

func (t *captureTask) Name() string {
	return t.result.Task
}

func (t *captureTask) Description() string {
	return "capture task"
}

func (t *captureTask) Run(ctx context.Context, _ runner.Runner, c *Context) (Result, error) {
	t.called = true
	t.receivedArgs = append([]string{}, c.Argv...)
	if c.Cwd != "" {
		t.receivedCtx = &Context{Root: c.Root, Cwd: c.Cwd, Argv: append([]string{}, c.Argv...), Quiet: c.Quiet, Timeout: c.Timeout}
	}
	if t.withContext != nil {
		t.withContext(ctx)
	}
	if t.result.Err != "" {
		return t.result, errors.New(t.result.Err)
	}
	return t.result, nil
}
