package format

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	require.Equal(t, "build", decoded.Task)
	require.Equal(t, "failed", decoded.Error)
	require.Equal(t, 1, decoded.ExitCode)
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
