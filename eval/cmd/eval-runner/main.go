package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	picofantasy "github.com/sipeed/picoclaw/pkg/fantasy"
	"github.com/sipeed/picoclaw/pkg/logger"
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

	prompt, err := readPrompt()
	if err != nil {
		emitError(fmt.Sprintf("failed to read prompt: %v", err))
		return
	}

	if strings.TrimSpace(prompt) == "" {
		emitError("empty prompt")
		return
	}

	cfg, err := loadEvalConfig()
	if err != nil {
		emitError(fmt.Sprintf("config error: %v", err))
		return
	}

	trace := runEval(cfg, prompt)
	out, _ := json.Marshal(trace)
	fmt.Println(string(out))
}

func readPrompt() (string, error) {
	if len(os.Args) > 1 && os.Args[1] == "--prompt" && len(os.Args) > 2 {
		return os.Args[2], nil
	}

	// promptfoo exec: provider passes the prompt as the first positional argument
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		raw := os.Args[1]
		var promptData struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal([]byte(raw), &promptData) == nil && promptData.Prompt != "" {
			return promptData.Prompt, nil
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
		var promptData struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(data, &promptData) == nil && promptData.Prompt != "" {
			return promptData.Prompt, nil
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", fmt.Errorf("no prompt provided (use positional arg, --prompt, or pipe to stdin)")
}

func loadEvalConfig() (*config.Config, error) {
	evalConfig := os.Getenv("PICOCLAW_EVAL_CONFIG")
	if evalConfig != "" {
		return config.LoadConfig(evalConfig)
	}

	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".picoclaw", "config.json")
	return config.LoadConfig(configPath)
}

func runEval(cfg *config.Config, prompt string) Trace {
	start := time.Now()

	sessionKey := fmt.Sprintf("eval:%d", start.UnixNano())

	fantasyProvider, err := picofantasy.CreateProvider(cfg)
	if err != nil {
		return Trace{Error: fmt.Sprintf("provider error: %v", err)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	languageModel, err := fantasyProvider.LanguageModel(ctx, picofantasy.ModelID(cfg))
	if err != nil {
		return Trace{Error: fmt.Sprintf("model error: %v", err)}
	}

	instrumentedModel := &instrumentedLanguageModel{
		inner: languageModel,
	}

	msgBus := bus.NewMessageBus()

	// Drain outbound messages to prevent blocking
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		for {
			_, ok := msgBus.SubscribeOutbound(ctx)
			if !ok {
				return
			}
		}
	}()

	agentLoop := agent.NewAgentLoop(cfg, msgBus, instrumentedModel)
	defer agentLoop.Stop()

	response, err := agentLoop.ProcessDirect(ctx, prompt, sessionKey)

	cancel() // Signals context done, which stops outbound drain
	<-outDone

	duration := time.Since(start)

	trace := Trace{
		Output:     response,
		SessionKey: sessionKey,
		Steps:      buildSteps(instrumentedModel),
		Metrics: Metrics{
			TotalDurationMs: duration.Milliseconds(),
			StepCount:       len(instrumentedModel.calls),
			InputTokens:     instrumentedModel.totalUsage.InputTokens,
			OutputTokens:    instrumentedModel.totalUsage.OutputTokens,
			TotalTokens:     instrumentedModel.totalUsage.TotalTokens,
			ReasoningTokens: instrumentedModel.totalUsage.ReasoningTokens,
			CacheReadTokens: instrumentedModel.totalUsage.CacheReadTokens,
		},
	}

	if err != nil {
		trace.Error = err.Error()
	}

	for _, call := range instrumentedModel.calls {
		for range call.toolCalls {
			trace.Metrics.ToolCallCount++
		}
	}

	return trace
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

func emitError(msg string) {
	trace := Trace{Error: msg}
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
			if part.Type == fantasy.StreamPartTypeFinish {
				ic.usage = part.Usage
				m.totalUsage.InputTokens += part.Usage.InputTokens
				m.totalUsage.OutputTokens += part.Usage.OutputTokens
				m.totalUsage.TotalTokens += part.Usage.TotalTokens
				m.totalUsage.ReasoningTokens += part.Usage.ReasoningTokens
				m.totalUsage.CacheReadTokens += part.Usage.CacheReadTokens
			}
			return yield(part)
		})
		ic.duration = time.Since(start)
		m.calls = append(m.calls, ic)
	}

	return wrappedStream, nil
}
