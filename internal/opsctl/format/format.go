package format

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
)

type OutputMode string

const (
	OutputText OutputMode = "text"
	OutputJSON OutputMode = "json"
	OutputRaw  OutputMode = "raw"
)

type TaskResult struct {
	Task       string     `json:"task"`
	Command    string     `json:"command"`
	ExitCode   int        `json:"exit_code"`
	DurationMS int64      `json:"duration_ms"`
	Stdout     string     `json:"stdout,omitempty"`
	Stderr     string     `json:"stderr,omitempty"`
	Error      string     `json:"error,omitempty"`
	Success    bool       `json:"success"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    time.Time  `json:"ended_at"`
	Env        []string   `json:"env,omitempty"`
	Mode       OutputMode `json:"mode"`
	NoColor    bool       `json:"no_color,omitempty"`
}

func NewTaskResult(name string, cmd string, result runner.CommandResult, startedAt time.Time, err error) TaskResult {
	tr := TaskResult{
		Task:       name,
		Command:    cmd,
		ExitCode:   result.ExitCode,
		DurationMS: int64(result.Duration / time.Millisecond),
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		StartedAt:  startedAt,
		EndedAt:    startedAt.Add(result.Duration),
		Mode:       OutputText,
		Success:    err == nil && result.ExitCode == 0,
	}
	if err != nil {
		tr.Error = err.Error()
	}
	return tr
}

func Render(w io.Writer, mode OutputMode, res TaskResult, quiet bool) error {
	res.Mode = mode
	switch mode {
	case OutputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	case OutputRaw:
		if quiet {
			return nil
		}
		prefix := statusPrefix(!res.NoColor, res.Success)
		_, _ = fmt.Fprintf(w, "%s %s (%dms)\n", prefix, res.Task, res.DurationMS)
		return nil
	default:
		if quiet {
			return nil
		}
		status := "ok"
		if !res.Success {
			status = "failed"
		}
		status = formatStatus(!res.NoColor, res.Success)
		_, _ = fmt.Fprintf(w, "%s: %s (exit=%d, duration=%dms)\n", res.Task, status, res.ExitCode, res.DurationMS)
		if res.Error != "" {
			_, _ = fmt.Fprintf(w, "  error: %s\n", res.Error)
		}
		if strings.TrimSpace(res.Stdout) != "" {
			_, _ = fmt.Fprintf(w, "  stdout: %s\n", res.Stdout)
		}
		if strings.TrimSpace(res.Stderr) != "" {
			_, _ = fmt.Fprintf(w, "  stderr: %s\n", res.Stderr)
		}
		return nil
	}
}

func formatStatus(colored bool, success bool) string {
	if !colored {
		if !success {
			return "failed"
		}
		return "ok"
	}
	if success {
		return colorize("ok", "32")
	}
	return colorize("failed", "31")
}

func statusPrefix(colored bool, success bool) string {
	if !colored {
		if !success {
			return "[err]"
		}
		return "[ok]"
	}
	if success {
		return colorize("[ok]", "32")
	}
	return colorize("[err]", "31")
}

func colorize(text string, colorCode string) string {
	return "\x1b[" + colorCode + "m" + text + "\x1b[0m"
}
