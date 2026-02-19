package tools

import (
	"context"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
)

func TestToolSearchTool_Name(t *testing.T) {
	r := NewToolRegistry()
	s := NewToolSearchTool(r)
	if s.Name() != "tool_search" {
		t.Errorf("expected tool_search, got %s", s.Name())
	}
}

func TestToolSearchTool_Description(t *testing.T) {
	r := NewToolRegistry()
	s := NewToolSearchTool(r)
	if s.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestToolSearchTool_Parameters(t *testing.T) {
	r := NewToolRegistry()
	s := NewToolSearchTool(r)
	params := s.Parameters()
	if params["type"] != "object" {
		t.Error("expected object type parameters")
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["query"]; !ok {
		t.Error("expected query property")
	}
}

func TestToolSearchTool_ListAll(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file from disk"})
	r.Register(&stubTool{name: "write_file", desc: "Write content to a file"})
	r.Register(&stubTool{name: "web_search", desc: "Search the internet"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	var results []toolSearchResult
	if err := jsonv2.Unmarshal([]byte(result.ForLLM), &results); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestToolSearchTool_EmptyQuery_ListsAll(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "First tool"})
	r.Register(&stubTool{name: "beta", desc: "Second tool"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": ""})

	var results []toolSearchResult
	if err := jsonv2.Unmarshal([]byte(result.ForLLM), &results); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Should be sorted alphabetically
	if results[0].Name != "alpha" {
		t.Errorf("expected alpha first, got %s", results[0].Name)
	}
}

func TestToolSearchTool_ExactNameMatch(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	r.Register(&stubTool{name: "write_file", desc: "Write a file"})
	r.Register(&stubTool{name: "list_dir", desc: "List directory contents"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": "read_file"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// read_file should be ranked first (exact match)
	if results[0].Name != "read_file" {
		t.Errorf("expected read_file first, got %s", results[0].Name)
	}
}

func TestToolSearchTool_PartialMatch(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read contents of a file from filesystem"})
	r.Register(&stubTool{name: "write_file", desc: "Write content to a file on filesystem"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web for information"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": "file"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	// Both file tools should match, web_search should not (unless "file" appears somewhere)
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'file', got %d", len(results))
	}
}

func TestToolSearchTool_DescriptionMatch(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "Search the internet for information"})
	r.Register(&stubTool{name: "beta", desc: "Read a local file"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": "internet"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if len(results) > 0 && results[0].Name != "alpha" {
		t.Errorf("expected alpha, got %s", results[0].Name)
	}
}

func TestToolSearchTool_MultiTermQuery(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read contents of a file from disk"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web for information"})
	r.Register(&stubTool{name: "web_fetch", desc: "Fetch content from a URL"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": "web search"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// web_search should rank highest (matches both terms in name+desc)
	if results[0].Name != "web_search" {
		t.Errorf("expected web_search first, got %s", results[0].Name)
	}
}

func TestToolSearchTool_NoMatch(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})

	s := NewToolSearchTool(r)
	result := s.Execute(context.Background(), map[string]interface{}{"query": "zzzznonexistent"})

	if result.IsError {
		t.Error("should not be an error, just empty results message")
	}
	if result.ForLLM == "" {
		t.Error("expected a response message")
	}
}

func TestToolSearchTool_ExcludesMetaTools(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterMetaTools() // registers tool_search + tool_call
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})

	s, _ := r.Get("tool_search")
	result := s.Execute(context.Background(), map[string]interface{}{})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	for _, res := range results {
		if res.Name == "tool_search" || res.Name == "tool_call" {
			t.Errorf("meta-tool %s should not appear in search results", res.Name)
		}
	}
}

// --- fuzzyScore tests ---

func TestFuzzyScore_ExactMatch(t *testing.T) {
	score := fuzzyScore("read_file", "Read a file", []string{"read_file"})
	if score < 100 {
		t.Errorf("expected score >= 100 for exact match, got %d", score)
	}
}

func TestFuzzyScore_ContainsMatch(t *testing.T) {
	score := fuzzyScore("read_file", "Read a file", []string{"read"})
	if score < 50 {
		t.Errorf("expected score >= 50 for name contains, got %d", score)
	}
}

func TestFuzzyScore_DescriptionMatch(t *testing.T) {
	score := fuzzyScore("alpha_tool", "Search the internet", []string{"internet"})
	if score < 20 {
		t.Errorf("expected score >= 20 for description match, got %d", score)
	}
}

func TestFuzzyScore_NoMatch(t *testing.T) {
	score := fuzzyScore("read_file", "Read a file", []string{"zzzzz"})
	if score != 0 {
		t.Errorf("expected 0 for no match, got %d", score)
	}
}

// --- subsequenceMatch tests ---

func TestSubsequenceMatch_True(t *testing.T) {
	tests := []struct {
		haystack, needle string
		expected         bool
	}{
		{"read_file", "rf", true},
		{"read_file", "rfl", true},
		{"abcdef", "ace", true},
		{"abc", "abc", true},
	}

	for _, tc := range tests {
		if got := subsequenceMatch(tc.haystack, tc.needle); got != tc.expected {
			t.Errorf("subsequenceMatch(%q, %q) = %v, want %v", tc.haystack, tc.needle, got, tc.expected)
		}
	}
}

func TestSubsequenceMatch_False(t *testing.T) {
	tests := []struct {
		haystack, needle string
	}{
		{"abc", "abcd"},
		{"abc", "xyz"},
		{"ab", "ba"},
	}

	for _, tc := range tests {
		if subsequenceMatch(tc.haystack, tc.needle) {
			t.Errorf("subsequenceMatch(%q, %q) should be false", tc.haystack, tc.needle)
		}
	}
}

// --- stubTool for testing ---
type stubTool struct {
	name string
	desc string
}

func (s *stubTool) Name() string                       { return s.name }
func (s *stubTool) Description() string                { return s.desc }
func (s *stubTool) Parameters() map[string]interface{} { return map[string]interface{}{} }
func (s *stubTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "executed " + s.name}
}
