package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/eval/instrumentation"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	dragonruntime "github.com/ZanzyTHEbar/dragonscale/pkg/runtime"
)

func main() {
	logger.SetLevel(logger.ERROR)

	prompt, err := resolvePrompt()
	if err != nil {
		emitError(fmt.Sprintf("failed to read prompt: %v", err))
		return
	}

	if empty := emptyPromptTrace(prompt); empty != nil {
		emitTrace(*empty)
		return
	}

	cfg, err := resolveEvalConfig()
	if err != nil {
		emitError(fmt.Sprintf("config error: %v", err))
		return
	}

	trace := runEval(cfg, prompt)
	emitTrace(trace)
}

func resolvePrompt() (string, error) {
	return readPrompt()
}

func emptyPromptTrace(prompt string) *instrumentation.Trace {
	if strings.TrimSpace(prompt) != "" {
		return nil
	}
	return &instrumentation.Trace{
		Output: "No prompt provided. Please provide a message.",
		Metrics: instrumentation.Metrics{
			TotalDurationMs: 0,
		},
	}
}

func resolveEvalConfig() (*config.Config, error) {
	return dragonruntime.LoadEvalConfig(evalRunnerTimeout())
}

func readPrompt() (string, error) {
	if len(os.Args) > 1 && os.Args[1] == "--prompt" && len(os.Args) > 2 {
		return os.Args[2], nil
	}

	// promptfoo exec: provider passes the prompt as the first positional argument
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		raw := os.Args[1]
		if prompt, ok := parsePromptPayload([]byte(raw)); ok {
			return prompt, nil
		}
		return strings.TrimSpace(raw), nil
	}

	// Fallback: read from stdin (for manual testing / piping)
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		if prompt, ok := parsePromptPayload(data); ok {
			return prompt, nil
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", fmt.Errorf("no prompt provided (use positional arg, --prompt, or pipe to stdin)")
}

func parsePromptPayload(raw []byte) (string, bool) {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Prompt == "" {
		return "", false
	}
	return payload.Prompt, true
}

func runEval(cfg *config.Config, prompt string) instrumentation.Trace {
	start := time.Now()
	timeout := evalRunnerTimeout()

	sessionKey := dragonruntime.NewSessionKey("eval", start)
	runtime, initErr := newEvalRuntime(cfg, timeout)
	if initErr != nil {
		return instrumentation.Trace{Error: initErr.Error()}
	}
	defer runtime.close()

	result, duration := runEvalPrompt(runtime.handle, sessionKey, prompt, start)
	return buildTrace(runtime.instrumentedModel, sessionKey, result, duration)
}

func runEvalPrompt(handle *dragonruntime.RuntimeHandle, sessionKey, prompt string, start time.Time) (dragonruntime.RunResult, time.Duration) {
	result := dragonruntime.RunPrompt(handle.Context(), handle, prompt, sessionKey)
	duration := result.Duration
	if duration <= 0 {
		duration = time.Since(start)
	}
	return result, duration
}

func buildTrace(model *instrumentation.InstrumentedLanguageModel, sessionKey string, result dragonruntime.RunResult, duration time.Duration) instrumentation.Trace {
	trace := instrumentation.Trace{
		Output:     result.Output,
		SessionKey: sessionKey,
		Steps:      instrumentation.BuildSteps(model),
		Metrics:    instrumentation.BuildMetrics(model, duration),
	}
	if result.Error != "" {
		trace.Error = result.Error
	}
	return trace
}

type evalRuntime struct {
	handle            *dragonruntime.RuntimeHandle
	instrumentedModel *instrumentation.InstrumentedLanguageModel
}

func newEvalRuntime(cfg *config.Config, timeout time.Duration) (*evalRuntime, error) {
	var instrumentedModel *instrumentation.InstrumentedLanguageModel
	handle, err := dragonruntime.Bootstrap(context.Background(), cfg, dragonruntime.BootstrapOptions{
		Timeout:      timeout,
		OutboundMode: dragonruntime.OutboundModeDrop,
		WrapModel: func(inner fantasy.LanguageModel) fantasy.LanguageModel {
			instrumentedModel = instrumentation.Wrap(inner)
			return instrumentedModel
		},
	})
	if err != nil {
		return nil, err
	}
	if instrumentedModel == nil {
		handle.Close()
		return nil, fmt.Errorf("instrumented model wrapper not initialized")
	}

	return &evalRuntime{
		handle:            handle,
		instrumentedModel: instrumentedModel,
	}, nil
}

func (r *evalRuntime) close() {
	if r.handle != nil {
		r.handle.Close()
	}
}

func evalRunnerTimeout() time.Duration {
	const defaultTimeout = 180 * time.Second
	raw := strings.TrimSpace(os.Getenv("DRAGONSCALE_EVAL_TIMEOUT_MS"))
	if raw == "" {
		return defaultTimeout
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return defaultTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func emitError(msg string) {
	emitTrace(instrumentation.Trace{Error: msg})
}

func emitTrace(trace instrumentation.Trace) {
	out, _ := json.Marshal(trace)
	fmt.Println(string(out))
}
