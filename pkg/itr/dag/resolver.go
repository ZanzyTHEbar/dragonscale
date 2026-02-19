package dag

import (
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"regexp"
	"strings"
	"sync"
)

var nodeRefPattern = regexp.MustCompile(`#node([A-Za-z0-9_-]+)`)

// nodeState tracks execution state for a single DAG node.
type nodeState struct {
	ID        string
	DependsOn []string
	result    string
	err       error
	done      chan struct{}
	mu        sync.Mutex
}

func newNodeState(id string, deps []string) *nodeState {
	return &nodeState{
		ID:        id,
		DependsOn: deps,
		done:      make(chan struct{}),
	}
}

func (ns *nodeState) setResult(result string, err error) {
	ns.mu.Lock()
	ns.result = result
	ns.err = err
	ns.mu.Unlock()
	close(ns.done)
}

func (ns *nodeState) getResult() (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.result, ns.err
}

// resolveRefs replaces all "#nodeXYZ" references in argsJSON with the
// actual output from the referenced node. Returns the resolved JSON string.
func resolveRefs(argsJSON string, states map[string]*nodeState) string {
	return nodeRefPattern.ReplaceAllStringFunc(argsJSON, func(match string) string {
		nodeID := match[5:] // strip "#node"
		ns, ok := states[nodeID]
		if !ok {
			return match
		}
		result, _ := ns.getResult()
		return escapeForJSON(result)
	})
}

// resolveToolExecArgs resolves #nodeN references within a ToolExec payload's
// ArgsJSON field, returning the resolved args as a JSON string.
func resolveToolExecArgs(argsJSON string, states map[string]*nodeState) string {
	if !strings.Contains(argsJSON, "#node") {
		return argsJSON
	}
	return resolveRefs(argsJSON, states)
}

// escapeForJSON makes a string safe for embedding into a JSON value.
func escapeForJSON(s string) string {
	b, err := jsonv2.Marshal(s)
	if err != nil || len(b) < 2 {
		return s
	}
	return string(b[1 : len(b)-1])
}

// topologicalOrder returns node IDs in topological execution order.
// Returns an error if the graph contains a cycle.
func topologicalOrder(nodes map[string]*nodeState) ([][]string, error) {
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))

	for id, ns := range nodes {
		inDegree[id] = len(ns.DependsOn)
		for _, dep := range ns.DependsOn {
			dependents[dep] = append(dependents[dep], id)
		}
	}

	var waves [][]string
	for {
		var wave []string
		for id, deg := range inDegree {
			if deg == 0 {
				wave = append(wave, id)
			}
		}
		if len(wave) == 0 {
			break
		}

		waves = append(waves, wave)
		for _, id := range wave {
			delete(inDegree, id)
			for _, dep := range dependents[id] {
				inDegree[dep]--
			}
		}
	}

	if len(inDegree) > 0 {
		var remaining []string
		for id := range inDegree {
			remaining = append(remaining, id)
		}
		return nil, fmt.Errorf("cycle detected among nodes: %v", remaining)
	}

	return waves, nil
}
