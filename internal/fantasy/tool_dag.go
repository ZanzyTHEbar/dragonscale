package fantasy

import (
	"errors"
	"fmt"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
)

// ToolDAGNode is a node in a tool execution dependency DAG.
type ToolDAGNode struct {
	ID           string
	ToolCall     ToolCallContent
	Dependencies []string
}

// ToolDAG is a dependency graph where edges point from dependency to dependent.
type ToolDAG struct {
	Nodes map[string]*ToolDAGNode
	Edges map[string][]string
}

// BuildToolDAG builds a dependency graph for tool calls by scanning JSON inputs
// for $tool.<toolCallID> references.
func BuildToolDAG(toolCalls []ToolCallContent) (*ToolDAG, error) {
	if len(toolCalls) == 0 {
		return &ToolDAG{Nodes: map[string]*ToolDAGNode{}, Edges: map[string][]string{}}, nil
	}

	nodes := make(map[string]*ToolDAGNode, len(toolCalls))
	for _, tc := range toolCalls {
		if strings.TrimSpace(tc.ToolCallID) == "" {
			return nil, errors.New("tool call id is empty")
		}
		if _, exists := nodes[tc.ToolCallID]; exists {
			return nil, fmt.Errorf("duplicate tool call id: %s", tc.ToolCallID)
		}
		nodes[tc.ToolCallID] = &ToolDAGNode{ID: tc.ToolCallID, ToolCall: tc}
	}

	edges := make(map[string][]string, len(toolCalls))
	for _, tc := range toolCalls {
		deps, err := extractToolDependencies(tc.Input)
		if err != nil {
			return nil, fmt.Errorf("parse tool dependencies for %s: %w", tc.ToolCallID, err)
		}

		filtered := make([]string, 0, len(deps))
		seen := make(map[string]struct{}, len(deps))
		for _, dep := range deps {
			dep = strings.TrimSpace(dep)
			if dep == "" || dep == tc.ToolCallID {
				continue
			}
			if _, ok := nodes[dep]; !ok {
				return nil, fmt.Errorf("tool call %s depends on unknown tool call id %s", tc.ToolCallID, dep)
			}
			if _, ok := seen[dep]; ok {
				continue
			}
			seen[dep] = struct{}{}
			filtered = append(filtered, dep)
		}

		nodes[tc.ToolCallID].Dependencies = filtered
		for _, dep := range filtered {
			edges[dep] = append(edges[dep], tc.ToolCallID)
		}
	}

	return &ToolDAG{Nodes: nodes, Edges: edges}, nil
}

func extractToolDependencies(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	var v any
	if err := jsonv2.Unmarshal([]byte(input), &v); err != nil {
		return nil, nil
	}

	var out []string
	walkJSON(v, func(s string) {
		out = append(out, extractToolRefIDsFromString(s)...)
	})
	return out, nil
}

func walkJSON(v any, visitString func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for _, vv := range t {
			walkJSON(vv, visitString)
		}
	case []any:
		for _, vv := range t {
			walkJSON(vv, visitString)
		}
	case string:
		visitString(t)
	}
}

func extractToolRefIDsFromString(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "$tool.") {
		return nil
	}

	rest := strings.TrimPrefix(s, "$tool.")
	if rest == "" {
		return nil
	}
	id := rest
	if idx := strings.IndexByte(rest, '.'); idx >= 0 {
		id = rest[:idx]
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return []string{id}
}
