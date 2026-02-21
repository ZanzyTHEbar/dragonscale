package tools

import (
	"context"
	"os"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	jsonv2 "github.com/go-json-experiment/json"
)

func TestToolSearchTool_Name(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	s := NewToolSearchTool(r)
	if s.Name() != "tool_search" {
		t.Errorf("expected tool_search, got %s", s.Name())
	}
}

func TestToolSearchTool_Description(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	s := NewToolSearchTool(r)
	if s.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestToolSearchTool_Parameters(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file from disk"})
	r.Register(&stubTool{name: "write_file", desc: "Write content to a file"})
	r.Register(&stubTool{name: "web_search", desc: "Search the internet"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{})

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "First tool"})
	r.Register(&stubTool{name: "beta", desc: "Second tool"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": ""})

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	r.Register(&stubTool{name: "write_file", desc: "Write a file"})
	r.Register(&stubTool{name: "list_dir", desc: "List directory contents"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "read_file"})

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read contents of a file from filesystem"})
	r.Register(&stubTool{name: "write_file", desc: "Write content to a file on filesystem"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web for information"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "file"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	// Both file tools should match, web_search should not (unless "file" appears somewhere)
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'file', got %d", len(results))
	}
}

func TestToolSearchTool_DescriptionMatch(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "Search the internet for information"})
	r.Register(&stubTool{name: "beta", desc: "Read a local file"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "internet"})

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read contents of a file from disk"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web for information"})
	r.Register(&stubTool{name: "web_fetch", desc: "Fetch content from a URL"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "web search"})

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "zzzznonexistent"})

	if result.IsError {
		t.Error("should not be an error, just empty results message")
	}
	if result.ForLLM == "" {
		t.Error("expected a response message")
	}
}

func TestToolSearchTool_ExcludesMetaTools(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.RegisterMetaTools() // registers tool_search + tool_call
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})

	s, _ := r.Get("tool_search")
	result := s.Execute(t.Context(), map[string]interface{}{})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	for _, res := range results {
		if res.Name == "tool_search" || res.Name == "tool_call" {
			t.Errorf("meta-tool %s should not appear in search results", res.Name)
		}
	}
}

func TestToolSearchTool_UnifiedSearch_IncludesSkills(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web"})

	tmpDir := t.TempDir()
	sl := createTestSkillsDir(t, tmpDir, map[string]string{
		"git-commit": "---\nname: git-commit\ndescription: Git commit conventions\ntags: [git, workflow]\n---\n# Git Commit Skill",
		"trading":    "---\nname: trading\ndescription: Algorithmic trading strategies\ntags: [finance]\ndomain: quantitative\n---\n# Trading Skill",
	})

	s := NewToolSearchTool(r)
	s.SetSkillsLoader(sl)

	// Search for "git" — should find the skill but not the tools
	result := s.Execute(t.Context(), map[string]interface{}{"query": "git"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	foundSkill := false
	for _, res := range results {
		if res.Name == "git-commit" && res.Kind == "skill" {
			foundSkill = true
		}
	}
	if !foundSkill {
		t.Errorf("expected git-commit skill in results, got: %v", results)
	}
}

func TestToolSearchTool_UnifiedSearch_MixesToolsAndSkills(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "web_search", desc: "Search the web for information"})

	tmpDir := t.TempDir()
	sl := createTestSkillsDir(t, tmpDir, map[string]string{
		"web-scraper": "---\nname: web-scraper\ndescription: Web scraping techniques\ntags: [web]\n---\n# Web Scraper",
	})

	s := NewToolSearchTool(r)
	s.SetSkillsLoader(sl)

	result := s.Execute(t.Context(), map[string]interface{}{"query": "web"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	hasToolKind := false
	hasSkillKind := false
	for _, res := range results {
		if res.Kind == "tool" {
			hasToolKind = true
		}
		if res.Kind == "skill" {
			hasSkillKind = true
		}
	}
	if !hasToolKind || !hasSkillKind {
		t.Errorf("expected both tool and skill results, got: %v", results)
	}
}

func TestToolSearchTool_ListAll_IncludesSkills(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "First tool"})

	tmpDir := t.TempDir()
	sl := createTestSkillsDir(t, tmpDir, map[string]string{
		"beta-skill": "---\nname: beta-skill\ndescription: A skill\n---\n# Skill",
	})

	s := NewToolSearchTool(r)
	s.SetSkillsLoader(sl)

	result := s.Execute(t.Context(), map[string]interface{}{})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) != 2 {
		t.Errorf("expected 2 results (1 tool + 1 skill), got %d: %v", len(results), results)
	}
}

// createTestSkillsDir sets up a temp skills directory with SKILL.md files
func createTestSkillsDir(t *testing.T, baseDir string, skillContents map[string]string) *skills.SkillsLoader {
	t.Helper()
	for name, content := range skillContents {
		skillDir := baseDir + "/" + name
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillDir+"/SKILL.md", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return skills.NewSkillsLoader(baseDir, "", "")
}

// --- fuzzyScore tests ---

func TestFuzzyScore_ExactMatch(t *testing.T) {
	t.Parallel()
	score := fuzzyScore("read_file", "Read a file", []string{"read_file"})
	if score < 100 {
		t.Errorf("expected score >= 100 for exact match, got %d", score)
	}
}

func TestFuzzyScore_ContainsMatch(t *testing.T) {
	t.Parallel()
	score := fuzzyScore("read_file", "Read a file", []string{"read"})
	if score < 50 {
		t.Errorf("expected score >= 50 for name contains, got %d", score)
	}
}

func TestFuzzyScore_DescriptionMatch(t *testing.T) {
	t.Parallel()
	score := fuzzyScore("alpha_tool", "Search the internet", []string{"internet"})
	if score < 20 {
		t.Errorf("expected score >= 20 for description match, got %d", score)
	}
}

func TestFuzzyScore_NoMatch(t *testing.T) {
	t.Parallel()
	score := fuzzyScore("read_file", "Read a file", []string{"zzzzz"})
	if score != 0 {
		t.Errorf("expected 0 for no match, got %d", score)
	}
}

// --- subsequenceMatch tests ---

func TestSubsequenceMatch_True(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestToolToSchema_WithExamples(t *testing.T) {
	t.Parallel()
	tool := &stubToolWithExamples{
		stubTool: stubTool{name: "create_ticket", desc: "Create a support ticket"},
		examples: []map[string]interface{}{
			{"title": "Login page 500 error", "priority": "critical"},
			{"title": "Add dark mode"},
		},
	}

	schema := ToolToSchema(tool)
	fn := schema["function"].(map[string]interface{})

	examples, ok := fn["input_examples"].([]map[string]interface{})
	if !ok || len(examples) != 2 {
		t.Fatalf("expected 2 input_examples, got: %v", fn["input_examples"])
	}
	if examples[0]["priority"] != "critical" {
		t.Errorf("expected critical priority in first example, got: %v", examples[0])
	}
}

func TestToolToSchema_WithoutExamples(t *testing.T) {
	t.Parallel()
	tool := &stubTool{name: "read_file", desc: "Read a file"}
	schema := ToolToSchema(tool)
	fn := schema["function"].(map[string]interface{})
	if _, exists := fn["input_examples"]; exists {
		t.Error("expected no input_examples for tool without ExampleProvider")
	}
}

// --- Discovery tracking tests ---

func TestToolSearchTool_DiscoveryTracking(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "edit_file", desc: "Edit a file"})
	r.Register(&stubToolWithSchema{name: "web_search", desc: "Search the web"})

	s := NewToolSearchTool(r)
	s.Execute(t.Context(), map[string]interface{}{"query": "edit"})

	discovered := r.DrainDiscovered()
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(discovered))
	}
	if discovered[0].Name() != "edit_file" {
		t.Errorf("expected edit_file, got %s", discovered[0].Name())
	}

	// Second drain should be empty
	discovered2 := r.DrainDiscovered()
	if len(discovered2) != 0 {
		t.Errorf("expected 0 after drain, got %d", len(discovered2))
	}
}

func TestToolSearchTool_DiscoverySkipsGateway(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})
	r.MarkGateway("read_file")

	s := NewToolSearchTool(r)
	s.Execute(t.Context(), map[string]interface{}{"query": "read"})

	discovered := r.DrainDiscovered()
	if len(discovered) != 0 {
		t.Errorf("gateway tools should not appear in discovered set, got %d", len(discovered))
	}
}

func TestToolSearchTool_DiscoverySkipsMetaTools(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.RegisterMetaTools()

	s, _ := r.Get("tool_search")
	s.Execute(t.Context(), map[string]interface{}{})

	discovered := r.DrainDiscovered()
	for _, d := range discovered {
		if d.Name() == "tool_search" || d.Name() == "tool_call" {
			t.Errorf("meta-tool %s should not be in discovered set", d.Name())
		}
	}
}

// --- Schema in search results tests ---

func TestToolSearchTool_ReturnsSchema(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{"query": "read"})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Parameters == nil {
		t.Error("expected parameters in search result")
	}
	if _, ok := results[0].Parameters["path"]; !ok {
		t.Error("expected 'path' in parameters")
	}
	if len(results[0].Required) == 0 || results[0].Required[0] != "path" {
		t.Errorf("expected required=['path'], got %v", results[0].Required)
	}
}

func TestToolSearchTool_ListAll_ReturnsSchema(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})

	s := NewToolSearchTool(r)
	result := s.Execute(t.Context(), map[string]interface{}{})

	var results []toolSearchResult
	jsonv2.Unmarshal([]byte(result.ForLLM), &results)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Parameters == nil {
		t.Error("expected parameters in listAll result")
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

// stubToolWithSchema returns a realistic JSON Schema parameters object.
type stubToolWithSchema struct {
	name string
	desc string
}

func (s *stubToolWithSchema) Name() string        { return s.name }
func (s *stubToolWithSchema) Description() string { return s.desc }
func (s *stubToolWithSchema) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the file",
			},
		},
		"required": []string{"path"},
	}
}
func (s *stubToolWithSchema) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "executed " + s.name}
}

type stubToolWithExamples struct {
	stubTool
	examples []map[string]interface{}
}

func (s *stubToolWithExamples) InputExamples() []map[string]interface{} {
	return s.examples
}
