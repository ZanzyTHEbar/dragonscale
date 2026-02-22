package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type CommandSpec struct {
	Name          string
	Args          []string
	Dir           string
	Env           []string
	InheritOutput bool
}

type CommandResult struct {
	Command  string
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	Err      error
}

type Runner interface {
	Run(ctx context.Context, spec CommandSpec) (CommandResult, error)
}

type OSRunner struct{}

func (r OSRunner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}

	cmd.Env = append(os.Environ(), spec.Env...)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	if spec.InheritOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
	}

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := CommandResult{
		Command:  spec.Name,
		ExitCode: exitCode,
		Duration: time.Since(start),
		Err:      err,
	}
	if !spec.InheritOutput {
		result.Stdout = stdoutBuf.String()
		result.Stderr = stderrBuf.String()
	}

	if err != nil {
		return result, fmt.Errorf("command failed: %w", err)
	}
	return result, nil
}

type FakeRunner struct {
	Calls   []CommandSpec
	Result  CommandResult
	Handler func(CommandSpec) CommandResult
}

func (r *FakeRunner) Run(_ context.Context, spec CommandSpec) (CommandResult, error) {
	r.Calls = append(r.Calls, spec)
	if r.Handler != nil {
		res := r.Handler(spec)
		return res, res.Err
	}
	return r.Result, r.Result.Err
}
