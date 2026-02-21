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
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	picoruntime "github.com/sipeed/picoclaw/pkg/runtime"
)

// Trace is the structured output emitted by the eval runner.
// Promptfoo parses this JSON to evaluate assertions.
type Trace struct {
	Output     string      `json:"output"`
	Steps      []TraceStep `json:"steps"`
	Metrics    Metrics     `json:"metrics"`
	Error      string      `json:"error,omitempty"`
	SessionKey string      `json:"session_key"`
}

type TraceStep struct {
	Index    int             `json:"index"`
	Type     string          `json:"type"`
	Tool     string          `json:"tool,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	Result   string          `json:"result,omitempty"`
	Duration int64           `json:"duration_ms,omitempty"`
}

type Metrics struct {
	TotalDurationMs int64 `json:"total_duration_ms"`
	StepCount       int   `json:"step_count"`
	ToolCallCount   int   `json:"tool_call_count"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

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

func emptyPromptTrace(prompt string) *Trace {
	if strings.TrimSpace(prompt) != "" {
		return nil
	}
	return &Trace{
		Output: "No prompt provided. Please provide a message.",
		Metrics: Metrics{
			TotalDurationMs: 0,
		},
	}
}

func resolveEvalConfig() (*config.Config, error) {
	return picoruntime.LoadEvalConfig(evalRunnerTimeout())
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

func runEval(cfg *config.Config, prompt string) Trace {
	start := time.Now()

	sessionKey := picoruntime.NewSessionKey("eval", start)
	runtime, initErr := newEvalRuntime(cfg)
	if initErr != nil {
		return Trace{Error: initErr.Error()}
	}
	defer runtime.close()

	result := picoruntime.RunPrompt(runtime.handle.Context(), runtime.handle, prompt, sessionKey)

	duration := result.Duration
	if duration <= 0 {
		duration = time.Since(start)
	}

	trace := Trace{
		Output:     result.Output,
		SessionKey: sessionKey,
		Steps:      buildSteps(runtime.instrumentedModel),
		Metrics: Metrics{
			TotalDurationMs: duration.Milliseconds(),
			StepCount:       len(runtime.instrumentedModel.calls),
			InputTokens:     runtime.instrumentedModel.totalUsage.InputTokens,
			OutputTokens:    runtime.instrumentedModel.totalUsage.OutputTokens,
			TotalTokens:     runtime.instrumentedModel.totalUsage.TotalTokens,
			ReasoningTokens: runtime.instrumentedModel.totalUsage.ReasoningTokens,
			CacheReadTokens: runtime.instrumentedModel.totalUsage.CacheReadTokens,
		},
	}

	if result.Error != "" {
		trace.Error = result.Error
	}

	for _, call := range runtime.instrumentedModel.calls {
		for range call.toolCalls {
			trace.Metrics.ToolCallCount++
		}
	}

	return trace
}

type evalRuntime struct {
	handle            *picoruntime.RuntimeHandle
	instrumentedModel *instrumentedLanguageModel
}

func newEvalRuntime(cfg *config.Config) (*evalRuntime, error) {
	var instrumentedModel *instrumentedLanguageModel
	handle, err := picoruntime.Bootstrap(context.Background(), cfg, picoruntime.BootstrapOptions{
		Timeout:      evalRunnerTimeout(),
		OutboundMode: picoruntime.OutboundModeDrop,
		WrapModel: func(inner fantasy.LanguageModel) fantasy.LanguageModel {
			instrumentedModel = &instrumentedLanguageModel{inner: inner}
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

func buildSteps(model *instrumentedLanguageModel) []TraceStep {
	var steps []TraceStep
	idx := 0

	for _, call := range model.calls {
		steps = append(steps, TraceStep{
			Index:    idx,
			Type:     "llm_call",
			Duration: call.duration.Milliseconds(),
		})
		idx++

		for _, tc := range call.toolCalls {
			argsRaw, _ := json.Marshal(tc.args)
			steps = append(steps, TraceStep{
				Index:  idx,
				Type:   "tool_call",
				Tool:   tc.name,
				Args:   argsRaw,
				Result: truncate(tc.result, 500),
			})
			idx++
		}
	}

	return steps
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

func evalRunnerTimeout() time.Duration {
	const defaultTimeout = 180 * time.Second
	raw := strings.TrimSpace(os.Getenv("PICOCLAW_EVAL_TIMEOUT_MS"))
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
	emitTrace(Trace{Error: msg})
}

func emitTrace(trace Trace) {
	out, _ := json.Marshal(trace)
	fmt.Println(string(out))
}

type instrumentedCall struct {
	duration  time.Duration
	toolCalls []instrumentedToolCall
	usage     fantasy.Usage
}

type instrumentedToolCall struct {
	name   string
	args   map[string]interface{}
	result string
}

type instrumentedLanguageModel struct {
	inner      fantasy.LanguageModel
	calls      []instrumentedCall
	totalUsage fantasy.Usage
}

var _ fantasy.LanguageModel = (*instrumentedLanguageModel)(nil)

func (m *instrumentedLanguageModel) Provider() string { return m.inner.Provider() }
func (m *instrumentedLanguageModel) Model() string    { return m.inner.Model() }

func (m *instrumentedLanguageModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return m.inner.GenerateObject(ctx, call)
}

func (m *instrumentedLanguageModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return m.inner.StreamObject(ctx, call)
}

func (m *instrumentedLanguageModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	start := time.Now()
	resp, err := m.inner.Generate(ctx, call)
	dur := time.Since(start)

	ic := instrumentedCall{duration: dur}

	if err == nil && resp != nil {
		ic.usage = resp.Usage
		m.totalUsage.InputTokens += resp.Usage.InputTokens
		m.totalUsage.OutputTokens += resp.Usage.OutputTokens
		m.totalUsage.TotalTokens += resp.Usage.TotalTokens
		m.totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		m.totalUsage.CacheReadTokens += resp.Usage.CacheReadTokens

		for _, tc := range resp.Content.ToolCalls() {
			var args map[string]interface{}
			_ = json.Unmarshal([]byte(tc.Input), &args)
			ic.toolCalls = append(ic.toolCalls, instrumentedToolCall{
				name: tc.ToolName,
				args: args,
			})
		}
	}

	m.calls = append(m.calls, ic)
	return resp, err
}

func (m *instrumentedLanguageModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	start := time.Now()
	stream, err := m.inner.Stream(ctx, call)
	if err != nil {
		m.calls = append(m.calls, instrumentedCall{duration: time.Since(start)})
		return nil, err
	}

	ic := instrumentedCall{}

	wrappedStream := func(yield func(fantasy.StreamPart) bool) {
		stream(func(part fantasy.StreamPart) bool {
			switch part.Type {
			case fantasy.StreamPartTypeFinish:
				ic.usage = part.Usage
				m.totalUsage.InputTokens += part.Usage.InputTokens
				m.totalUsage.OutputTokens += part.Usage.OutputTokens
				m.totalUsage.TotalTokens += part.Usage.TotalTokens
				m.totalUsage.ReasoningTokens += part.Usage.ReasoningTokens
				m.totalUsage.CacheReadTokens += part.Usage.CacheReadTokens
			case fantasy.StreamPartTypeToolCall:
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(part.ToolCallInput), &args)
				ic.toolCalls = append(ic.toolCalls, instrumentedToolCall{
					name: part.ToolCallName,
					args: args,
				})
			}
			return yield(part)
		})
		ic.duration = time.Since(start)
		m.calls = append(m.calls, ic)
	}

	return wrappedStream, nil
}
