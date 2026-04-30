package fantasy

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
)

// ParallelToolRuntime executes tool calls concurrently when tools opt in via
// ToolInfo.Parallel. Non-parallel-safe tools act as barriers.
type ParallelToolRuntime struct {
	// MaxConcurrency limits concurrent tool execution within a parallel batch.
	// If <= 0, a safe default is used.
	MaxConcurrency int

	// Metrics emits optional runtime metrics.
	Metrics ToolRuntimeMetricsFunc

	// Log emits structured runtime events.
	Log ToolRuntimeLogFunc
}

func (r ParallelToolRuntime) Execute(ctx context.Context, tools []AgentTool, execProviderTools []ExecutableProviderTool, toolCalls []ToolCallContent, toolResultCallback func(result ToolResultContent) error) ([]ToolResultContent, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	metrics := func(m ToolRuntimeMetrics) {
		if r.Metrics != nil {
			r.Metrics(m)
		}
	}
	logEvent := func(e ToolRuntimeLogEvent) {
		if r.Log != nil {
			r.Log(e)
		}
	}

	maxConc := r.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 4
	}

	toolMap := make(map[string]AgentTool, len(tools))
	for _, t := range tools {
		toolMap[t.Info().Name] = t
	}
	execProviderToolMap := make(map[string]ExecutableProviderTool, len(execProviderTools))
	for _, t := range execProviderTools {
		execProviderToolMap[t.GetName()] = t
	}

	results := make([]ToolResultContent, len(toolCalls))

	isParallelSafe := func(tc ToolCallContent) bool {
		if tc.Invalid {
			return false
		}
		t, ok := toolMap[tc.ToolName]
		if !ok {
			return false
		}
		return t.Info().Parallel
	}

	sem := make(chan struct{}, maxConc)
	var inFlight atomic.Int64
	barrierWaits := 0
	i := 0
	emit := func() {
		metrics(ToolRuntimeMetrics{Queued: len(toolCalls) - i, InFlightParallel: int(inFlight.Load()), BarrierWaits: barrierWaits})
	}
	for i < len(toolCalls) {
		if !isParallelSafe(toolCalls[i]) {
			barrierWaits++
			emit()
			logEvent(ToolRuntimeLogEvent{Event: "barrier_start", ToolCallID: toolCalls[i].ToolCallID, ToolName: toolCalls[i].ToolName})
			res, critical := executeSingleTool(ctx, toolMap, execProviderToolMap, toolCalls[i], toolResultCallback)
			logEvent(ToolRuntimeLogEvent{Event: "barrier_finish", ToolCallID: toolCalls[i].ToolCallID, ToolName: toolCalls[i].ToolName})
			results[i] = res
			if critical {
				if errorResult, ok := res.Result.(ToolResultOutputContentError); ok && errorResult.Error != nil {
					return nil, errorResult.Error
				}
				return nil, errors.New("critical tool error")
			}
			i++
			continue
		}

		start := i
		for i < len(toolCalls) && isParallelSafe(toolCalls[i]) {
			i++
		}
		end := i

		type outcome struct {
			res      ToolResultContent
			critical bool
		}
		outcomes := make([]outcome, end-start)

		var wg sync.WaitGroup
		for bi := start; bi < end; bi++ {
			localIndex := bi - start
			tc := toolCalls[bi]
			wg.Add(1)
			go func() {
				defer wg.Done()
				logEvent(ToolRuntimeLogEvent{Event: "dispatch", ToolCallID: tc.ToolCallID, ToolName: tc.ToolName})
				sem <- struct{}{}
				inFlight.Add(1)
				emit()
				defer func() {
					<-sem
					inFlight.Add(-1)
					emit()
				}()

				res, critical := executeSingleTool(ctx, toolMap, execProviderToolMap, tc, nil)
				outcomes[localIndex] = outcome{res: res, critical: critical}
				logEvent(ToolRuntimeLogEvent{Event: "finish", ToolCallID: tc.ToolCallID, ToolName: tc.ToolName})
			}()
		}
		wg.Wait()

		for bi := start; bi < end; bi++ {
			o := outcomes[bi-start]
			results[bi] = o.res
			if toolResultCallback != nil {
				_ = toolResultCallback(o.res)
			}
			if o.critical {
				if errorResult, ok := o.res.Result.(ToolResultOutputContentError); ok && errorResult.Error != nil {
					return nil, errorResult.Error
				}
				return nil, errors.New("critical tool error")
			}
		}
	}

	return results, nil
}

func executeSingleTool(ctx context.Context, toolMap map[string]AgentTool, execProviderToolMap map[string]ExecutableProviderTool, toolCall ToolCallContent, toolResultCallback func(result ToolResultContent) error) (ToolResultContent, bool) {
	result := ToolResultContent{
		ToolCallID:       toolCall.ToolCallID,
		ToolName:         toolCall.ToolName,
		ProviderExecuted: false,
	}

	if toolCall.Invalid {
		result.Result = ToolResultOutputContentError{Error: toolCall.ValidationError}
		if toolResultCallback != nil {
			_ = toolResultCallback(result)
		}
		return result, false
	}

	var runTool func(context.Context, ToolCall) (ToolResponse, error)
	if tool, ok := toolMap[toolCall.ToolName]; ok {
		runTool = tool.Run
	} else if tool, ok := execProviderToolMap[toolCall.ToolName]; ok {
		runTool = tool.Run
	}
	if runTool == nil {
		result.Result = ToolResultOutputContentError{Error: errors.New("tool not found: " + toolCall.ToolName)}
		if toolResultCallback != nil {
			_ = toolResultCallback(result)
		}
		return result, false
	}

	toolResult, err := runTool(ctx, ToolCall{
		ID:    toolCall.ToolCallID,
		Name:  toolCall.ToolName,
		Input: toolCall.Input,
	})
	if err != nil {
		result.Result = ToolResultOutputContentError{Error: err}
		result.ClientMetadata = toolResult.Metadata
		result.StopTurn = toolResult.StopTurn
		if toolResultCallback != nil {
			_ = toolResultCallback(result)
		}
		return result, true
	}

	result.ClientMetadata = toolResult.Metadata
	result.StopTurn = toolResult.StopTurn
	if toolResult.IsError {
		result.Result = ToolResultOutputContentError{Error: errors.New(toolResult.Content)}
	} else if toolResult.Type == "image" || toolResult.Type == "media" {
		result.Result = ToolResultOutputContentMedia{
			Data:      base64.StdEncoding.EncodeToString(toolResult.Data),
			MediaType: toolResult.MediaType,
			Text:      toolResult.Content,
		}
	} else {
		result.Result = ToolResultOutputContentText{Text: toolResult.Content}
	}
	if toolResultCallback != nil {
		_ = toolResultCallback(result)
	}
	return result, false
}
