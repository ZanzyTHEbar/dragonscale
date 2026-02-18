package fantasy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DAGToolRuntime executes tool calls according to an explicit dependency DAG.
//
// Dependencies are discovered via BuildToolDAG (using $tool.<id> references in
// JSON tool inputs). Independent nodes may be executed concurrently, subject to
// MaxConcurrency and tool parallel-safety.
type DAGToolRuntime struct {
	MaxConcurrency int

	// Metrics emits optional runtime metrics.
	Metrics ToolRuntimeMetricsFunc

	// Log emits structured runtime events.
	Log ToolRuntimeLogFunc
}

func (r DAGToolRuntime) Execute(ctx context.Context, tools []AgentTool, toolCalls []ToolCallContent, toolResultCallback func(result ToolResultContent) error) ([]ToolResultContent, error) {
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

	dag, err := BuildToolDAG(toolCalls)
	if err != nil {
		return nil, err
	}

	toolMap := make(map[string]AgentTool, len(tools))
	for _, t := range tools {
		toolMap[t.Info().Name] = t
	}

	idToIndex := make(map[string]int, len(toolCalls))
	for i, tc := range toolCalls {
		idToIndex[tc.ToolCallID] = i
	}

	indegree := make(map[string]int, len(dag.Nodes))
	for id, n := range dag.Nodes {
		indegree[id] = len(n.Dependencies)
	}

	executed := make(map[string]bool, len(dag.Nodes))
	doneResults := make(map[string]ToolResultContent, len(dag.Nodes))
	results := make([]ToolResultContent, len(toolCalls))

	isParallelSafeTool := func(tc ToolCallContent) bool {
		if tc.Invalid {
			return false
		}
		t, ok := toolMap[tc.ToolName]
		if !ok {
			return false
		}
		return t.Info().Parallel
	}

	remaining := len(dag.Nodes)
	barrierWaits := 0
	for remaining > 0 {
		var readyIDs []string
		for id := range dag.Nodes {
			if executed[id] {
				continue
			}
			if indegree[id] == 0 {
				readyIDs = append(readyIDs, id)
			}
		}

		if len(readyIDs) == 0 {
			return nil, errors.New("tool dependency cycle detected (no ready nodes)")
		}

		sort.Slice(readyIDs, func(i, j int) bool {
			return idToIndex[readyIDs[i]] < idToIndex[readyIDs[j]]
		})

		metrics(ToolRuntimeMetrics{
			Queued:           remaining,
			InFlightParallel: 0,
			BarrierWaits:     barrierWaits,
		})

		// If any ready node is non-parallel-safe, execute the earliest one as a barrier.
		barrierID := ""
		for _, id := range readyIDs {
			n := dag.Nodes[id]
			if n == nil {
				continue
			}
			if !isParallelSafeTool(n.ToolCall) {
				barrierID = id
				break
			}
		}

		if barrierID != "" {
			n := dag.Nodes[barrierID]
			barrierWaits++
			logEvent(ToolRuntimeLogEvent{Event: "barrier_start", ToolCallID: n.ToolCall.ToolCallID, ToolName: n.ToolCall.ToolName})
			res, critical, execErr := executeDAGNode(ctx, toolMap, n.ToolCall, doneResults)
			if execErr != nil {
				return nil, execErr
			}
			if toolResultCallback != nil {
				_ = toolResultCallback(res)
			}
			logEvent(ToolRuntimeLogEvent{Event: "barrier_finish", ToolCallID: n.ToolCall.ToolCallID, ToolName: n.ToolCall.ToolName})
			if critical {
				if errorResult, ok := res.Result.(ToolResultOutputContentError); ok && errorResult.Error != nil {
					return nil, errorResult.Error
				}
				return nil, errors.New("critical tool error")
			}

			executed[barrierID] = true
			doneResults[barrierID] = res
			results[idToIndex[barrierID]] = res
			remaining--

			for _, dep := range dag.Edges[barrierID] {
				indegree[dep]--
			}
			continue
		}

		// Parallel wave: execute up to maxConc ready nodes concurrently.
		if len(readyIDs) > maxConc {
			readyIDs = readyIDs[:maxConc]
		}

		type outcome struct {
			id       string
			res      ToolResultContent
			critical bool
			execErr  error
		}

		outcomes := make([]outcome, len(readyIDs))
		var wg sync.WaitGroup
		wg.Add(len(readyIDs))
		for i, id := range readyIDs {
			i, id := i, id
			tc := dag.Nodes[id].ToolCall
			go func() {
				defer wg.Done()
				logEvent(ToolRuntimeLogEvent{Event: "dispatch", ToolCallID: tc.ToolCallID, ToolName: tc.ToolName})
				res, critical, execErr := executeDAGNode(ctx, toolMap, tc, doneResults)
				outcomes[i] = outcome{
					id:       id,
					res:      res,
					critical: critical,
					execErr:  execErr,
				}
				logEvent(ToolRuntimeLogEvent{Event: "finish", ToolCallID: tc.ToolCallID, ToolName: tc.ToolName})
			}()
		}
		wg.Wait()

		// Commit results in deterministic order, and abort on first critical error in that order.
		for i := range outcomes {
			o := outcomes[i]
			if o.execErr != nil {
				return nil, o.execErr
			}
			if toolResultCallback != nil {
				_ = toolResultCallback(o.res)
			}
			if o.critical {
				if errorResult, ok := o.res.Result.(ToolResultOutputContentError); ok && errorResult.Error != nil {
					return nil, errorResult.Error
				}
				return nil, errors.New("critical tool error")
			}

			executed[o.id] = true
			doneResults[o.id] = o.res
			results[idToIndex[o.id]] = o.res
			remaining--

			for _, dep := range dag.Edges[o.id] {
				indegree[dep]--
			}
		}
	}

	return results, nil
}

func executeDAGNode(ctx context.Context, toolMap map[string]AgentTool, toolCall ToolCallContent, prior map[string]ToolResultContent) (ToolResultContent, bool, error) {
	resolvedInput, err := resolveToolRefsInInput(toolCall.Input, prior)
	if err != nil {
		return ToolResultContent{}, false, err
	}
	tc := toolCall
	tc.Input = resolvedInput
	res, critical := executeSingleToolCompat(ctx, toolMap, tc, nil)
	return res, critical, nil
}

func resolveToolRefsInInput(input string, results map[string]ToolResultContent) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return input, nil
	}

	var v any
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		// Not JSON; nothing to resolve.
		return input, nil
	}

	updated, err := rewriteJSONToolRefs(v, results)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(updated)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func rewriteJSONToolRefs(v any, results map[string]ToolResultContent) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			rv, err := rewriteJSONToolRefs(vv, results)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			rv, err := rewriteJSONToolRefs(vv, results)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case string:
		s := strings.TrimSpace(t)
		if !strings.HasPrefix(s, "$tool.") {
			return t, nil
		}
		val, err := resolveToolRefValue(s, results)
		if err != nil {
			return nil, err
		}
		return val, nil
	default:
		return v, nil
	}
}

func resolveToolRefValue(ref string, results map[string]ToolResultContent) (any, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(ref), "$tool.")
	if rest == "" {
		return nil, fmt.Errorf("invalid tool ref: %q", ref)
	}

	parts := strings.Split(rest, ".")
	depID := strings.TrimSpace(parts[0])
	if depID == "" {
		return nil, fmt.Errorf("invalid tool ref: %q", ref)
	}

	r, ok := results[depID]
	if !ok {
		return nil, fmt.Errorf("tool ref %q not available yet", ref)
	}

	base := ""
	switch v := r.Result.(type) {
	case ToolResultOutputContentText:
		base = v.Text
	case ToolResultOutputContentMedia:
		if v.Text != "" {
			base = v.Text
		} else {
			base = v.Data
		}
	case ToolResultOutputContentError:
		if v.Error != nil {
			base = v.Error.Error()
		}
	default:
		base = fmt.Sprint(r.Result)
	}

	// No path: substitute as string.
	if len(parts) == 1 {
		return base, nil
	}

	// Path resolution: interpret base as JSON and walk.
	var cur any
	if err := json.Unmarshal([]byte(base), &cur); err != nil {
		return nil, fmt.Errorf("tool ref %q path requires JSON output, got non-JSON", ref)
	}

	for _, seg := range parts[1:] {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return nil, fmt.Errorf("invalid tool ref path in %q", ref)
		}

		switch typed := cur.(type) {
		case map[string]any:
			nv, ok := typed[seg]
			if !ok {
				return nil, fmt.Errorf("tool ref %q missing key %q", ref, seg)
			}
			cur = nv
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil {
				return nil, fmt.Errorf("tool ref %q array segment %q is not an int", ref, seg)
			}
			if idx < 0 || idx >= len(typed) {
				return nil, fmt.Errorf("tool ref %q array index %d out of range", ref, idx)
			}
			cur = typed[idx]
		default:
			return nil, fmt.Errorf("tool ref %q path segment %q on non-container", ref, seg)
		}
	}

	return cur, nil
}
