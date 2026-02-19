package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
)

// ToolSearchTool implements tool discovery via fuzzy search over the registry.
// The agent can query for tools by keyword and get back ranked summaries.
type ToolSearchTool struct {
	registry *ToolRegistry
}

// NewToolSearchTool creates a tool that searches the registry.
func NewToolSearchTool(registry *ToolRegistry) *ToolSearchTool {
	return &ToolSearchTool{registry: registry}
}

func (t *ToolSearchTool) Name() string { return "tool_search" }

func (t *ToolSearchTool) Description() string {
	return "Search for available tools by keyword. Returns tool names, descriptions, and parameter summaries. Use this to discover what tools are available before calling them with tool_call."
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
	Name        string `json:"name"`
	Description string `json:"description"`
	Score       int    `json:"score"`
}

func (t *ToolSearchTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return t.listAll()
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	t.registry.mu.RLock()
	defer t.registry.mu.RUnlock()

	var results []toolSearchResult

	for _, tool := range t.registry.tools {
		// Skip meta-tools from results
		if tool.Name() == "tool_search" || tool.Name() == "tool_call" {
			continue
		}

		score := fuzzyScore(tool.Name(), tool.Description(), queryTerms)
		if score > 0 {
			results = append(results, toolSearchResult{
				Name:        tool.Name(),
				Description: tool.Description(),
				Score:       score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit to top 10
	if len(results) > 10 {
		results = results[:10]
	}

	if len(results) == 0 {
		return &ToolResult{ForLLM: fmt.Sprintf("No tools match query: %q. Try a broader search or use tool_search with no query to list all.", query)}
	}

	b, _ := jsonv2.Marshal(results)
	return &ToolResult{ForLLM: string(b)}
}

func (t *ToolSearchTool) listAll() *ToolResult {
	t.registry.mu.RLock()
	defer t.registry.mu.RUnlock()

	var results []toolSearchResult
	for _, tool := range t.registry.tools {
		if tool.Name() == "tool_search" || tool.Name() == "tool_call" {
			continue
		}
		results = append(results, toolSearchResult{
			Name:        tool.Name(),
			Description: tool.Description(),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

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
