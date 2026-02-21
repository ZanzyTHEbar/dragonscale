package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
)

// SkillSearchTool performs fuzzy search across skill names, descriptions, tags, and domains.
// This is the first step in progressive skill disclosure: scan before you read.
type SkillSearchTool struct {
	loader *skills.SkillsLoader
	mu     sync.Mutex
	graph  *skills.SkillGraph
}

func NewSkillSearchTool(loader *skills.SkillsLoader) *SkillSearchTool {
	return &SkillSearchTool{loader: loader}
}

func (t *SkillSearchTool) getGraph() *skills.SkillGraph {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.graph == nil {
		t.graph = t.loader.BuildGraph()
	}
	return t.graph
}

func (t *SkillSearchTool) Name() string { return "skill_search" }

func (t *SkillSearchTool) Description() string {
	return "Search available skills by keyword. Returns matching skill names, descriptions, tags, and domains without loading full content. Use this to discover relevant skills before reading them."
}

func (t *SkillSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query to match against skill names, descriptions, tags, and domains",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SkillSearchTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required")
	}

	g := t.getGraph()
	results := g.SearchSkills(query)
	if len(results) == 0 {
		return SilentResult("No skills matched the query.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching skills:\n\n", len(results)))
	for _, node := range results {
		sb.WriteString(fmt.Sprintf("- **%s** (%s)", node.Name, node.Source))
		if node.Description != "" {
			sb.WriteString(fmt.Sprintf(": %s", node.Description))
		}
		sb.WriteByte('\n')
		if len(node.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("  tags: %s\n", strings.Join(node.Tags, ", ")))
		}
		if node.Domain != "" {
			sb.WriteString(fmt.Sprintf("  domain: %s\n", node.Domain))
		}
		if len(node.Links) > 0 {
			sb.WriteString(fmt.Sprintf("  links: %s\n", strings.Join(node.Links, ", ")))
		}
	}

	return SilentResult(sb.String())
}

// SkillReadTool loads the full content of a specific skill by name.
// This is the second step in progressive disclosure: read after search.
type SkillReadTool struct {
	loader *skills.SkillsLoader
}

func NewSkillReadTool(loader *skills.SkillsLoader) *SkillReadTool {
	return &SkillReadTool{loader: loader}
}

func (t *SkillReadTool) Name() string { return "skill_read" }

func (t *SkillReadTool) Description() string {
	return "Load the full content of a specific skill by name. Use skill_search first to find relevant skill names, then read the ones you need."
}

func (t *SkillReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Exact name of the skill to read (from skill_search results)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *SkillReadTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return ErrorResult("name is required")
	}

	content, ok := t.loader.LoadSkill(name)
	if !ok {
		return ErrorResult(fmt.Sprintf("skill '%s' not found", name))
	}

	return SilentResult(fmt.Sprintf("# Skill: %s\n\n%s", name, content))
}

// SkillTraverseTool follows wikilink chains from a skill node.
// This is the third step: after reading a skill, explore its connections.
type SkillTraverseTool struct {
	loader *skills.SkillsLoader
	mu     sync.Mutex
	graph  *skills.SkillGraph
}

func NewSkillTraverseTool(loader *skills.SkillsLoader) *SkillTraverseTool {
	return &SkillTraverseTool{loader: loader}
}

func (t *SkillTraverseTool) getGraph() *skills.SkillGraph {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.graph == nil {
		t.graph = t.loader.BuildGraph()
	}
	return t.graph
}

func (t *SkillTraverseTool) Name() string { return "skill_traverse" }

func (t *SkillTraverseTool) Description() string {
	return "Follow wikilink connections from a skill to discover related skills. Returns linked skill summaries (names, descriptions, tags) without loading full content. Use skill_read to load any that seem relevant."
}

func (t *SkillTraverseTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the skill to traverse from",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": "How many link hops to follow (1-3, default 1)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *SkillTraverseTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return ErrorResult("name is required")
	}

	depth := 1
	if d, ok := args["depth"].(float64); ok && d >= 1 && d <= 3 {
		depth = int(d)
	}

	g := t.getGraph()
	if g.GetNode(name) == nil {
		return ErrorResult(fmt.Sprintf("skill '%s' not found in graph", name))
	}

	reachable := g.TraverseFrom(name, depth)
	if len(reachable) == 0 {
		return SilentResult(fmt.Sprintf("Skill '%s' has no outgoing links.", name))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Skills reachable from '%s' (depth %d):\n\n", name, depth))
	for _, node := range reachable {
		sb.WriteString(fmt.Sprintf("- **%s**", node.Name))
		if node.Description != "" {
			sb.WriteString(fmt.Sprintf(": %s", node.Description))
		}
		sb.WriteByte('\n')
		if len(node.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("  tags: %s\n", strings.Join(node.Tags, ", ")))
		}
		if node.IsMOC {
			sb.WriteString("  [Map of Content]\n")
		}
	}

	return SilentResult(sb.String())
}
