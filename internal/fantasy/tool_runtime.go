package fantasy

import "context"

// ToolRuntime controls how client-side tool calls are executed.
type ToolRuntime interface {
	Execute(ctx context.Context, tools []AgentTool, execProviderTools []ExecutableProviderTool, toolCalls []ToolCallContent, toolResultCallback func(result ToolResultContent) error) ([]ToolResultContent, error)
}

// ToolRuntimeMetrics captures lightweight execution counters.
type ToolRuntimeMetrics struct {
	Queued           int
	InFlightParallel int
	BarrierWaits     int
}

// ToolRuntimeLogEvent is an optional structured log emitted by runtimes.
type ToolRuntimeLogEvent struct {
	Event      string
	ToolCallID string
	ToolName   string
	Detail     string
}

// ToolRuntimeMetricsFunc emits metrics.
type ToolRuntimeMetricsFunc = func(ToolRuntimeMetrics)

// ToolRuntimeLogFunc emits structured log events.
type ToolRuntimeLogFunc = func(ToolRuntimeLogEvent)
