package agent

import (
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
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

func TestSystemPromptForTurnLimitsToolSectionToRelevantHints(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	reg := tools.NewToolRegistry()
	reg.Register(&namedTool{name: "memory"})
	reg.Register(&namedTool{name: "obligation"})
	reg.Register(&namedTool{name: "write_file"})
	reg.Register(&namedTool{name: "read_file"})
	cb.SetToolsRegistry(reg)

	prompt := cb.BuildSystemPromptForTurn("session-a", "Capture these commitments and give me a reminder/follow-up plan with explicit timing.", 0)

	if !strings.Contains(prompt, "`memory`") {
		t.Fatalf("expected turn-specific prompt to include memory tool summary, got: %s", prompt)
	}
	if strings.Contains(prompt, "`obligation`") {
		t.Fatalf("did not expect turn-specific prompt to advertise obligation, got: %s", prompt)
	}
	if strings.Contains(prompt, "`write_file`") || strings.Contains(prompt, "`read_file`") {
		t.Fatalf("did not expect unrelated file tools in turn-specific prompt, got: %s", prompt)
	}
}
