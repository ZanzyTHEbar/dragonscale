package dag

import (
	"context"
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// PlannerFunc calls the LLM with a system prompt and user query, returning
// the raw text response and token cost.
type PlannerFunc func(ctx context.Context, systemPrompt, userQuery string) (string, uint32, error)

// Planner generates DAGPlans from natural-language queries by prompting an
// LLM to produce a structured JSON plan. The LLM outputs a list of nodes
// with dependency edges in a single inference pass (LLMCompiler pattern).
type Planner struct {
	callModel   PlannerFunc
	registry    *tools.ToolRegistry
	maxParallel uint8
	tokenBudget uint32
}

// PlannerConfig configures the DAG planner.
type PlannerConfig struct {
	MaxParallel uint8
	TokenBudget uint32
}

// DefaultPlannerConfig returns sensible defaults.
func DefaultPlannerConfig() PlannerConfig {
	return PlannerConfig{
		MaxParallel: 8,
		TokenBudget: 0,
	}
}

// NewPlanner creates a DAG planner.
func NewPlanner(callModel PlannerFunc, registry *tools.ToolRegistry, cfg PlannerConfig) *Planner {
	return &Planner{
		callModel:   callModel,
		registry:    registry,
		maxParallel: cfg.MaxParallel,
		tokenBudget: cfg.TokenBudget,
	}
}

// Plan generates a DAGPlan for the given query. It calls the LLM once to
// produce a structured plan with nodes and dependencies, then validates it.
func (p *Planner) Plan(ctx context.Context, query string, availableTools []string) (*itr.DAGPlan, uint32, error) {
	systemPrompt := p.buildSystemPrompt(availableTools)
	userPrompt := fmt.Sprintf("Create an execution plan for this task:\n\n%s", query)

	response, tokens, err := p.callModel(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, tokens, fmt.Errorf("planner LLM call failed: %w", err)
	}

	plan, err := parsePlanResponse(response)
	if err != nil {
		return nil, tokens, fmt.Errorf("failed to parse plan: %w", err)
	}

	plan.MaxParallel = p.maxParallel
	plan.TokenBudget = p.tokenBudget

	if err := validatePlan(plan); err != nil {
		return nil, tokens, fmt.Errorf("invalid plan: %w", err)
	}

	return plan, tokens, nil
}

func (p *Planner) buildSystemPrompt(availableTools []string) string {
	toolList := ""
	if p.registry != nil {
		for _, name := range availableTools {
			tool, ok := p.registry.Get(name)
			if !ok {
				continue
			}
			toolList += fmt.Sprintf("- %s: %s\n", tool.Name(), tool.Description())
		}
	}

	return fmt.Sprintf(`You are a task planner that decomposes complex queries into a DAG of tool calls.

Available tools:
%s
Output ONLY a JSON object with this schema:
{
  "nodes": [
    {
      "id": "unique_string",
      "type": "tool_exec",
      "payload": {"tool_name": "...", "args_json": "{...}"},
      "depends_on": ["other_node_id"]
    }
  ],
  "joiner_query": "Synthesize the results into a final answer for: <original query>"
}

Rules:
- Node IDs must be unique strings.
- Use "#nodeID" in args_json to reference output from another node.
- Nodes with no dependencies run in parallel.
- Keep the plan minimal: prefer fewer nodes with clear dependencies.
- Output valid JSON only, no markdown fences or explanation.`, toolList)
}

// parsePlanResponse extracts a DAGPlan from the LLM's JSON response.
func parsePlanResponse(response string) (*itr.DAGPlan, error) {
	response = extractJSON(response)

	var plan itr.DAGPlan
	if err := jsonv2.Unmarshal([]byte(response), &plan, itr.LLMJSONOpts()); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w\nraw: %s", err, truncate(response, 500))
	}

	return &plan, nil
}

// extractJSON strips markdown code fences if present.
func extractJSON(s string) string {
	// Strip ```json ... ``` fences
	if idx := findIndex(s, "```json"); idx >= 0 {
		s = s[idx+7:]
		if end := findIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
	} else if idx := findIndex(s, "```"); idx >= 0 {
		s = s[idx+3:]
		if end := findIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
	}

	// Find first { and last }
	start := -1
	end := -1
	for i, c := range s {
		if c == '{' {
			start = i
			break
		}
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '}' {
			end = i + 1
			break
		}
	}
	if start >= 0 && end > start {
		return s[start:end]
	}
	return s
}

func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// validatePlan checks structural integrity of a DAGPlan.
func validatePlan(plan *itr.DAGPlan) error {
	if len(plan.Nodes) == 0 {
		return fmt.Errorf("plan has no nodes")
	}

	ids := make(map[string]bool, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node has empty ID")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node ID: %s", n.ID)
		}
		ids[n.ID] = true
	}

	for _, n := range plan.Nodes {
		for _, dep := range n.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("node %s depends on unknown node %s", n.ID, dep)
			}
			if dep == n.ID {
				return fmt.Errorf("node %s depends on itself", n.ID)
			}
		}
	}

	// Verify no cycles using topological sort on temporary nodeStates.
	states := make(map[string]*nodeState, len(plan.Nodes))
	for _, n := range plan.Nodes {
		states[n.ID] = newNodeState(n.ID, n.DependsOn)
	}
	_, err := topologicalOrder(states)
	return err
}
