package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptIncludesDirectToolRoutingHints(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	prompt := cb.BuildSystemPromptWithBudget(0)

	expectedSnippets := []string{
		"Plans vs actions",
		"Direct tool routing",
		"Do NOT infer SKILL.md paths",
		"use skill_search to discover skills",
		"use memory to capture/store commitments",
		"{\"command\":\"uname -s\"}",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q", snippet)
		}
	}
}
