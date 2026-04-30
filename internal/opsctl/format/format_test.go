package format

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
	"github.com/stretchr/testify/require"
)

func TestRenderJSONIncludesTaskAndError(t *testing.T) {
	buf := &bytes.Buffer{}
	result := TaskResult{
		Task:      "build",
		Command:   "go build",
		ExitCode:  1,
		Error:     "failed",
		Success:   false,
		StartedAt: time.Unix(1, 0),
		EndedAt:   time.Unix(2, 0),
	}

	require.NoError(t, Render(buf, OutputJSON, result, false))
	decoded := TaskResult{}
	require.NoError(t, json.NewDecoder(buf).Decode(&decoded))
	testcmp.RequireEqual(t, taskResultFields{Task: "build", Error: "failed", ExitCode: 1}, projectTaskResult(decoded))
}

func TestRenderTextIncludesDetails(t *testing.T) {
	buf := &bytes.Buffer{}
	result := TaskResult{
		Task:       "lint",
		ExitCode:   0,
		DurationMS: 123,
		Stdout:     "ok\n",
		NoColor:    true,
	}

	require.NoError(t, Render(buf, OutputText, result, false))
	output := buf.String()
	require.Contains(t, output, "lint")
	require.Contains(t, output, "exit=0")
	require.Contains(t, output, "stdout: ok")
	require.NotContains(t, output, "\x1b[")
}

func TestRenderTextRespectsNoColorFalse(t *testing.T) {
	buf := &bytes.Buffer{}
	result := TaskResult{
		Task:       "test",
		ExitCode:   0,
		DurationMS: 10,
		Success:    true,
		NoColor:    false,
	}

	require.NoError(t, Render(buf, OutputText, result, false))
	output := buf.String()
	require.Contains(t, output, "\x1b[32mok\x1b[0m")
}

func TestRenderRawHonorQuiet(t *testing.T) {
	buf := &bytes.Buffer{}
	result := TaskResult{Task: "run", ExitCode: 0, DurationMS: 42, Success: true}
	require.NoError(t, Render(buf, OutputRaw, result, true))
	require.Equal(t, "", buf.String())

	buf.Reset()
	require.NoError(t, Render(buf, OutputRaw, result, false))
	require.True(t, strings.Contains(buf.String(), "run"))
	require.True(t, strings.Contains(buf.String(), "(42ms)"))
}

func TestRenderTextIncludesCommandSummaryAndErrors(t *testing.T) {
	buf := &bytes.Buffer{}
	result := TaskResult{
		Task:       "eval",
		Command:    "if [ -n \"$X\" ]; then\necho ok\nfi",
		ExitCode:   1,
		Success:    false,
		DurationMS: 55,
		Error:      "command failed",
		Stdout:     "ok\n",
		Stderr:     `{"outcome":"success"}`,
	}
	require.NoError(t, Render(buf, OutputText, result, false))
	output := buf.String()
	require.Contains(t, output, "eval")
	require.Contains(t, output, "command:")
	require.Contains(t, output, "if [ -n")
	require.Contains(t, output, "error: command failed")
	require.Contains(t, output, "stdout: ok")
	require.Contains(t, output, `"outcome"`)
}

func TestRenderTextHonorsQuietOnlyForSuccess(t *testing.T) {
	buf := &bytes.Buffer{}
	successResult := TaskResult{Task: "run", ExitCode: 0, DurationMS: 1, Success: true}
	require.NoError(t, Render(buf, OutputText, successResult, true))
	require.Equal(t, "", buf.String())

	buf.Reset()
	failureResult := TaskResult{Task: "run", ExitCode: 1, DurationMS: 1, Success: false, Error: "boom"}
	require.NoError(t, Render(buf, OutputText, failureResult, true))
	require.Contains(t, buf.String(), "run")
	require.Contains(t, buf.String(), "exit=1")
	require.Contains(t, buf.String(), "error: boom")
}

func TestRenderJSONHonorsQuietOnlyForSuccess(t *testing.T) {
	buf := &bytes.Buffer{}
	successResult := TaskResult{Task: "run", ExitCode: 0, DurationMS: 1, Success: true, Error: "ok"}
	require.NoError(t, Render(buf, OutputJSON, successResult, true))
	require.Equal(t, "", buf.String())

	buf.Reset()
	failureResult := TaskResult{Task: "run", ExitCode: 1, DurationMS: 1, Success: false, Error: "boom", Stderr: "bad"}
	require.NoError(t, Render(buf, OutputJSON, failureResult, true))
	require.NotEqual(t, "", buf.String())

	decoded := TaskResult{}
	require.NoError(t, json.NewDecoder(buf).Decode(&decoded))
	testcmp.RequireEqual(t, taskResultFields{Task: "run", Error: "boom", Stderr: "bad", ExitCode: 1}, projectTaskResult(decoded))
	require.False(t, decoded.Success)
}

type taskResultFields struct {
	Task     string
	Error    string
	Stderr   string
	ExitCode int
}

func projectTaskResult(result TaskResult) taskResultFields {
	return taskResultFields{
		Task:     result.Task,
		Error:    result.Error,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}
}
