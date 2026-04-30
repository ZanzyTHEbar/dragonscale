package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
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
	testcmp.RequireEqual(t, CommandSpec{Name: "test", Args: []string{"--flag"}, Env: []string{"A=1"}}, r.Calls[0])
	testcmp.RequireEqual(t, commandResultFields{Command: "ok", ExitCode: 11}, projectCommandResult(result))
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
	testcmp.RequireEqual(t, commandResultFields{Command: "bad", ExitCode: -1}, projectCommandResult(result))
}

func TestOSRunnerReturnsCommandError(t *testing.T) {
	r := OSRunner{}
	result, err := r.Run(context.Background(), CommandSpec{Name: "sh", Args: []string{"-c", "echo stdout; echo stderr 1>&2; exit 3"}})
	require.Error(t, err)
	require.Equal(t, "command failed: exit status 3", err.Error())
	testcmp.RequireEqual(t, commandResultFields{Command: "sh", ExitCode: 3, Stdout: "stdout\n", Stderr: "stderr\n"}, projectCommandResult(result))
}

type commandResultFields struct {
	Command  string
	ExitCode int
	Stdout   string
	Stderr   string
}

func projectCommandResult(result CommandResult) commandResultFields {
	return commandResultFields{
		Command:  result.Command,
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}
