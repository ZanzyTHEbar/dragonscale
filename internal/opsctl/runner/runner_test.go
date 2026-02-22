package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeRunnerRecordsCalls(t *testing.T) {
	called := false
	r := &FakeRunner{
		Handler: func(spec CommandSpec) CommandResult {
			called = true
			return CommandResult{
				Command:  "ok",
				ExitCode: 11,
			}
		},
	}

	result, err := r.Run(context.Background(), CommandSpec{Name: "test", Args: []string{"--flag"}, Env: []string{"A=1"}})
	require.NoError(t, err)
	require.True(t, called)
	require.Len(t, r.Calls, 1)
	require.Equal(t, "test", r.Calls[0].Name)
	require.Equal(t, []string{"--flag"}, r.Calls[0].Args)
	require.Equal(t, []string{"A=1"}, r.Calls[0].Env)
	require.Equal(t, 11, result.ExitCode)
}

func TestFakeRunnerPropagatesHandlerError(t *testing.T) {
	r := &FakeRunner{
		Handler: func(_ CommandSpec) CommandResult {
			err := errors.New("boom")
			return CommandResult{Command: "bad", ExitCode: -1, Err: err}
		},
	}

	result, err := r.Run(context.Background(), CommandSpec{Name: "bad"})
	require.Error(t, err)
	require.Equal(t, "bad", result.Command)
	require.Equal(t, -1, result.ExitCode)
}

func TestOSRunnerReturnsCommandError(t *testing.T) {
	r := OSRunner{}
	result, err := r.Run(context.Background(), CommandSpec{Name: "sh", Args: []string{"-c", "echo stdout; echo stderr 1>&2; exit 3"}})
	require.Error(t, err)
	require.Equal(t, "command failed: exit status 3", err.Error())
	require.Equal(t, 3, result.ExitCode)
	require.Equal(t, "stdout\n", result.Stdout)
	require.Equal(t, "stderr\n", result.Stderr)
}
