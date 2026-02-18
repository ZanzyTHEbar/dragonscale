package fantasy

import "context"

// ToolRuntime controls how tool calls are executed (sequential, parallel, DAG, etc).
//
// The default behavior is sequential execution identical to the legacy agent
// implementation.
type ToolRuntime interface {
	Execute(ctx context.Context, tools []AgentTool, toolCalls []ToolCallContent, toolResultCallback func(result ToolResultContent) error) ([]ToolResultContent, error)
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
