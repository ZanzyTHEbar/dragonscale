package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	jsonv2 "github.com/go-json-experiment/json"
)

// ToolSearchTool implements unified discovery via fuzzy search over the registry
// and (optionally) the skills library. Returns ranked results for both tools
// and skills so the agent has a single entry point for capability discovery.
type ToolSearchTool struct {
	registry     *ToolRegistry
	skillsLoader *skills.SkillsLoader
}

// NewToolSearchTool creates a tool that searches the registry.
func NewToolSearchTool(registry *ToolRegistry) *ToolSearchTool {
	return &ToolSearchTool{registry: registry}
}

// SetSkillsLoader enables unified search across tools and skills.
func (t *ToolSearchTool) SetSkillsLoader(sl *skills.SkillsLoader) {
	t.skillsLoader = sl
}

func (t *ToolSearchTool) Name() string { return "tool_search" }

func (t *ToolSearchTool) Description() string {
	return "Search for available tools and skills by keyword. Returns names, descriptions, parameter schemas, and kind (tool or skill). Discovered tools become directly callable in your next step — no need to use tool_call. For skills, use skill_read to load full content."
}

func (t *ToolSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query to match against tool names and descriptions.",
			},
		},
		"required": []string{"query"},
	}
}

type toolSearchResult struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Kind        string                 `json:"kind"`
	Score       int                    `json:"score,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Domain      string                 `json:"domain,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Required    []string               `json:"required,omitempty"`
}

func (t *ToolSearchTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return t.listAll()
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	var results []toolSearchResult

	// Search tools — include parameter schemas so the LLM can call them correctly
	t.registry.mu.RLock()
	for _, tool := range t.registry.tools {
		if tool.Name() == "tool_search" || tool.Name() == "tool_call" {
			continue
		}
		score := fuzzyScore(tool.Name(), tool.Description(), queryTerms)
		if score > 0 {
			params, required := extractSchemaFields(tool.Parameters())
			results = append(results, toolSearchResult{
				Name:        tool.Name(),
				Description: tool.Description(),
				Kind:        "tool",
				Score:       score,
				Parameters:  params,
				Required:    required,
			})
		}
	}
	t.registry.mu.RUnlock()

	// Search skills (unified discovery)
	if t.skillsLoader != nil {
		for _, si := range t.skillsLoader.ListSkills() {
			combined := si.Name + " " + si.Description + " " + si.Domain + " " + strings.Join(si.Tags, " ")
			score := fuzzyScore(si.Name, combined, queryTerms)
			if score > 0 {
				results = append(results, toolSearchResult{
					Name:        si.Name,
					Description: si.Description,
					Kind:        "skill",
					Score:       score,
					Tags:        si.Tags,
					Domain:      si.Domain,
				})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > 15 {
		results = results[:15]
	}

	if len(results) == 0 {
		return &ToolResult{ForLLM: fmt.Sprintf("No tools or skills match query: %q. Try a broader search or use tool_search with no query to list all.", query)}
	}

	// Record discovered tool names so PrepareStep can promote them to native callables.
	discoveredNames := make([]string, 0, len(results))
	for _, r := range results {
		if r.Kind == "tool" {
			discoveredNames = append(discoveredNames, r.Name)
		}
	}
	if len(discoveredNames) > 0 {
		t.registry.MarkDiscovered(discoveredNames...)
	}

	b, _ := jsonv2.Marshal(results)
	return &ToolResult{ForLLM: string(b)}
}

// extractSchemaFields pulls the properties map and required list from a
// tool's full JSON Schema parameters object.
func extractSchemaFields(params map[string]interface{}) (map[string]interface{}, []string) {
	props, hasProps := params["properties"].(map[string]interface{})
	if !hasProps {
		return nil, nil
	}
	var required []string
	switch r := params["required"].(type) {
	case []string:
		required = r
	case []interface{}:
		for _, v := range r {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	return props, required
}

func (t *ToolSearchTool) listAll() *ToolResult {
	var results []toolSearchResult

	t.registry.mu.RLock()
	for _, tool := range t.registry.tools {
		if tool.Name() == "tool_search" || tool.Name() == "tool_call" {
			continue
		}
		params, required := extractSchemaFields(tool.Parameters())
		results = append(results, toolSearchResult{
			Name:        tool.Name(),
			Description: tool.Description(),
			Kind:        "tool",
			Parameters:  params,
			Required:    required,
		})
	}
	t.registry.mu.RUnlock()

	if t.skillsLoader != nil {
		for _, si := range t.skillsLoader.ListSkills() {
			results = append(results, toolSearchResult{
				Name:        si.Name,
				Description: si.Description,
				Kind:        "skill",
				Tags:        si.Tags,
				Domain:      si.Domain,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	// Record all tool names for promotion
	discoveredNames := make([]string, 0)
	for _, r := range results {
		if r.Kind == "tool" {
			discoveredNames = append(discoveredNames, r.Name)
		}
	}
	if len(discoveredNames) > 0 {
		t.registry.MarkDiscovered(discoveredNames...)
	}

	b, _ := jsonv2.Marshal(results)
	return &ToolResult{ForLLM: string(b)}
}

// fuzzyScore scores how well a tool matches the query terms.
// Higher score = better match. Returns 0 for no match.
func fuzzyScore(name, description string, queryTerms []string) int {
	nameLower := strings.ToLower(name)
	descLower := strings.ToLower(description)
	score := 0

	for _, term := range queryTerms {
		// Exact name match — highest signal
		if nameLower == term {
			score += 100
			continue
		}

		// Name contains term
		if strings.Contains(nameLower, term) {
			score += 50
			continue
		}

		// Description contains term
		if strings.Contains(descLower, term) {
			score += 20
			continue
		}

		// Partial match: any character subsequence in name
		if subsequenceMatch(nameLower, term) {
			score += 10
			continue
		}
	}

	return score
}

// subsequenceMatch checks if needle characters appear in order within haystack.
func subsequenceMatch(haystack, needle string) bool {
	hi := 0
	for ni := 0; ni < len(needle) && hi < len(haystack); hi++ {
		if haystack[hi] == needle[ni] {
			ni++
			if ni == len(needle) {
				return true
			}
		}
	}
	return false
}
