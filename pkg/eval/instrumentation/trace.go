package instrumentation

import (
	"encoding/json"
	"time"
)

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

const maxEvalOutputBytes = 500

func BuildSteps(model *InstrumentedLanguageModel) []TraceStep {
	var steps []TraceStep
	index := 0
	for _, call := range model.Calls() {
		steps = append(steps, TraceStep{
			Index:    index,
			Type:     "llm_call",
			Duration: call.duration.Milliseconds(),
		})
		index++

		for _, tc := range call.toolCalls {
			argsRaw, _ := json.Marshal(tc.Args)
			steps = append(steps, TraceStep{
				Index:  index,
				Type:   "tool_call",
				Tool:   tc.Name,
				Args:   argsRaw,
				Result: truncate(tc.Result, maxEvalOutputBytes),
			})
			index++
		}
	}
	return steps
}

func BuildMetrics(model *InstrumentedLanguageModel, duration time.Duration) Metrics {
	usage := model.Usage()
	metric := Metrics{
		TotalDurationMs: int64(duration / time.Millisecond),
		StepCount:       len(model.Calls()),
		InputTokens:     usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		TotalTokens:     usage.TotalTokens,
		ReasoningTokens: usage.ReasoningTokens,
		CacheReadTokens: usage.CacheReadTokens,
	}

	for _, call := range model.Calls() {
		metric.ToolCallCount += len(call.toolCalls)
	}

	return metric
}

func truncate(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...[truncated]"
}
