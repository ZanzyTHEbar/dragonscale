// Package dag implements the DAG Task Executor for PicoClaw. It receives a
// DAGPlan (a set of nodes with dependency edges), dispatches them in
// topological wave order via the SecureBus, and synthesises a final result
// using the Joiner pattern.
//
// Design principles (from ADR-001):
//   - LLMCompiler: single-pass DAG planning, parallel topological execution
//   - Anthropic PTC: intermediate results never enter the LLM's context;
//     only the Joiner's output crosses back to the agent loop
//   - RLM integration: when a node's context exceeds a threshold, the
//     RLMEngine expands it into a sub-DAG transparently
package dag

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/security/securebus"
)

// JoinerFunc synthesises a final answer from all node results.
// systemPrompt provides instruction context; resultSummary contains the
// concatenated node outputs for synthesis.
type JoinerFunc func(ctx context.Context, systemPrompt, resultSummary string) (string, uint32, error)

// RLMExpandFunc processes oversized context through recursive decomposition.
// It receives a session key, query, and context content; returns the
// synthesised answer and token cost. This bridges the DAG executor to the
// RLM engine without creating import cycles.
type RLMExpandFunc func(ctx context.Context, sessionKey, query, contextContent string) (string, uint32, error)

// Executor runs a DAGPlan through the SecureBus with topological dispatch.
type Executor struct {
	bus                *securebus.Bus
	joiner             JoinerFunc
	rlmExpand          RLMExpandFunc
	rlmThresholdBytes  int
	maxParallel        int
}

// ExecutorOption configures an Executor via the functional options pattern.
type ExecutorOption func(*Executor)

// WithRLMExpander enables automatic RLM expansion for nodes whose output
// exceeds threshold bytes.
func WithRLMExpander(fn RLMExpandFunc, thresholdBytes int) ExecutorOption {
	return func(e *Executor) {
		e.rlmExpand = fn
		e.rlmThresholdBytes = thresholdBytes
	}
}

// NewExecutor creates a DAG executor.
// joiner is called after all nodes complete to synthesise the final answer.
func NewExecutor(bus *securebus.Bus, joiner JoinerFunc, opts ...ExecutorOption) *Executor {
	e := &Executor{
		bus:               bus,
		joiner:            joiner,
		rlmThresholdBytes: 8192,
		maxParallel:       runtime.GOMAXPROCS(0),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ExecuteResult holds the output of a DAG execution.
type ExecuteResult struct {
	FinalAnswer string
	NodeResults map[string]string
	TotalTokens uint32
}

// Execute runs the full DAGPlan: topological dispatch, parallel execution,
// dependency resolution, and Joiner synthesis.
func (e *Executor) Execute(ctx context.Context, sessionKey string, plan *itr.DAGPlan) (*ExecuteResult, error) {
	if len(plan.Nodes) == 0 {
		return &ExecuteResult{}, nil
	}

	states := make(map[string]*nodeState, len(plan.Nodes))
	for i := range plan.Nodes {
		n := &plan.Nodes[i]
		states[n.ID] = newNodeState(n.ID, n.DependsOn)
	}

	waves, err := topologicalOrder(states)
	if err != nil {
		return nil, err
	}

	maxPar := int(plan.MaxParallel)
	if maxPar <= 0 {
		maxPar = e.maxParallel
	}

	var totalTokens uint32
	var tokensMu sync.Mutex

	for _, wave := range waves {
		sem := make(chan struct{}, maxPar)
		var wg sync.WaitGroup
		var waveErr error
		var errOnce sync.Once

		for _, nodeID := range wave {
			nodeID := nodeID
			ns := states[nodeID]

			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()

				// Wait for all dependencies (should already be done since
				// we execute wave-by-wave, but handles cross-wave edges).
				for _, dep := range ns.DependsOn {
					depState, ok := states[dep]
					if !ok {
						ns.setResult("", fmt.Errorf("unknown dependency: %s", dep))
						errOnce.Do(func() { waveErr = fmt.Errorf("unknown dependency: %s", dep) })
						return
					}
					select {
					case <-depState.done:
						if _, depErr := depState.getResult(); depErr != nil {
							ns.setResult("", fmt.Errorf("dependency %s failed: %w", dep, depErr))
							errOnce.Do(func() { waveErr = depErr })
							return
						}
					case <-ctx.Done():
						ns.setResult("", ctx.Err())
						errOnce.Do(func() { waveErr = ctx.Err() })
						return
					}
				}

				// Find the original node to get its payload
				var node *itr.DAGNode
				for i := range plan.Nodes {
					if plan.Nodes[i].ID == nodeID {
						node = &plan.Nodes[i]
						break
					}
				}
				if node == nil {
					ns.setResult("", fmt.Errorf("node %s not found in plan", nodeID))
					return
				}

				resp := e.executeNode(ctx, sessionKey, node, states)

				tokensMu.Lock()
				totalTokens += resp.CostTokens
				tokensMu.Unlock()

				if resp.IsError {
					ns.setResult(resp.Result, fmt.Errorf("node %s: %s", nodeID, resp.Result))
				} else {
					result := resp.Result
					// RLM expansion: if the result exceeds the threshold,
					// recursively decompose it via the RLM engine.
					if e.rlmExpand != nil && len(result) > e.rlmThresholdBytes {
						expanded, rlmTokens, rlmErr := e.rlmExpand(ctx, sessionKey,
							"Summarize and extract key information from this content", result)
						if rlmErr == nil {
							result = expanded
							tokensMu.Lock()
							totalTokens += rlmTokens
							tokensMu.Unlock()
						}
					}
					ns.setResult(result, nil)
				}
			}()
		}

		wg.Wait()

		if waveErr != nil {
			return nil, fmt.Errorf("dag execution failed: %w", waveErr)
		}
	}

	// Collect all node results.
	nodeResults := make(map[string]string, len(states))
	for id, ns := range states {
		result, _ := ns.getResult()
		nodeResults[id] = result
	}

	// Joiner synthesis: combine all node results into a final answer.
	finalAnswer, joinerTokens, err := e.join(ctx, plan, nodeResults)
	if err != nil {
		return nil, fmt.Errorf("joiner synthesis failed: %w", err)
	}
	totalTokens += joinerTokens

	return &ExecuteResult{
		FinalAnswer: finalAnswer,
		NodeResults: nodeResults,
		TotalTokens: totalTokens,
	}, nil
}

// executeNode dispatches a single node through the SecureBus after resolving
// dependency references in its payload.
func (e *Executor) executeNode(ctx context.Context, sessionKey string, node *itr.DAGNode, states map[string]*nodeState) itr.ToolResponse {
	req := nodeToRequest(sessionKey, node, states)
	return e.bus.Execute(ctx, req)
}

// nodeToRequest converts a DAGNode into a ToolRequest, resolving #nodeN
// references in tool arguments.
func nodeToRequest(sessionKey string, node *itr.DAGNode, states map[string]*nodeState) itr.ToolRequest {
	switch node.Type {
	case itr.CmdToolExec:
		te, ok := node.Payload.(itr.ToolExec)
		if !ok {
			if m, ok := node.Payload.(map[string]interface{}); ok {
				b, _ := json.Marshal(m)
				_ = json.Unmarshal(b, &te)
			}
		}
		te.ArgsJSON = resolveToolExecArgs(te.ArgsJSON, states)
		return itr.NewToolExecRequest(node.ID, sessionKey, node.ID, te.ToolName, te.ArgsJSON)

	case itr.CmdToolSearch:
		ts, ok := node.Payload.(itr.ToolSearch)
		if !ok {
			if m, ok := node.Payload.(map[string]interface{}); ok {
				b, _ := json.Marshal(m)
				_ = json.Unmarshal(b, &ts)
			}
		}
		return itr.NewToolSearchRequest(node.ID, sessionKey, ts.Query, ts.MaxResults)

	default:
		return itr.ToolRequest{
			ID:         node.ID,
			Type:       node.Type,
			Payload:    node.Payload,
			SessionKey: sessionKey,
		}
	}
}

// join calls the JoinerFunc to synthesise a final answer from node results.
// If no JoinerFunc is configured, concatenates results.
func (e *Executor) join(ctx context.Context, plan *itr.DAGPlan, nodeResults map[string]string) (string, uint32, error) {
	if e.joiner == nil || plan.JoinerQuery == "" {
		return concatenateResults(nodeResults), 0, nil
	}

	var sb strings.Builder
	sb.WriteString("Node results:\n")
	for id, result := range nodeResults {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", id, truncate(result, 2000)))
	}

	systemPrompt := "You are synthesizing results from parallel tool executions into a coherent final answer. Be concise and precise."
	userQuery := plan.JoinerQuery + "\n\n" + sb.String()

	return e.joiner(ctx, systemPrompt, userQuery)
}

func concatenateResults(results map[string]string) string {
	var parts []string
	for _, v := range results {
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
