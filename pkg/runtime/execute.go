package runtime

import (
	"context"
	"fmt"
	"time"
)

type RunResult struct {
	Output     string
	Error      string
	SessionKey string
	Duration   time.Duration
}

func RunPrompt(ctx context.Context, handle *RuntimeHandle, prompt, sessionKey string) RunResult {
	start := time.Now()
	result := RunResult{SessionKey: sessionKey}

	if handle == nil || handle.AgentLoop() == nil {
		result.Error = "runtime handle is not initialized"
		result.Duration = time.Since(start)
		return result
	}

	runCtx := ctx
	if runCtx == nil {
		runCtx = handle.Context()
	}

	output, err := handle.AgentLoop().ProcessDirect(runCtx, prompt, sessionKey)
	result.Output = output
	if err != nil {
		result.Error = err.Error()
	}
	result.Duration = time.Since(start)
	return result
}

func NewSessionKey(prefix string, now time.Time) string {
	if prefix == "" {
		prefix = "session"
	}
	return fmt.Sprintf("%s:%d", prefix, now.UnixNano())
}
