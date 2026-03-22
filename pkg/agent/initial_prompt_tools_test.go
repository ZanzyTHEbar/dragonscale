package agent

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

func TestInitialPromptToolsExposeSkillAndFileHelpers(t *testing.T) {
	reg := tools.NewToolRegistry()
	reg.Register(&namedTool{name: "tool_search"})
	reg.Register(&namedTool{name: "tool_call"})
	reg.Register(tools.NewExecTool(t.TempDir(), true))
	reg.Register(tools.NewReadFileTool(t.TempDir(), true))
	reg.Register(tools.NewWriteFileTool(t.TempDir(), true))
	reg.Register(tools.NewEditFileTool(t.TempDir(), true))
	reg.Register(tools.NewAppendFileTool(t.TempDir(), true))
	reg.Register(tools.NewListDirTool(t.TempDir(), true))
	reg.Register(&namedTool{name: "spawn"})
	reg.Register(&namedTool{name: "subagent"})
	reg.Register(&namedTool{name: "memory"})
	reg.Register(&namedTool{name: "obligation"})

	skillsDir := t.TempDir()
	loader := skills.NewSkillsLoader(skillsDir, "", "")
	reg.Register(tools.NewSkillSearchTool(loader))
	reg.Register(tools.NewSkillReadTool(loader))
	reg.Register(tools.NewSkillTraverseTool(loader))

	al := &AgentLoop{tools: reg}

	skillNames := toolNames(al.initialPromptTools("Read the 'eval-test-skill' skill and explain it."))
	if !containsAll(skillNames, "skill_search", "skill_read") {
		t.Fatalf("expected skill tools, got %v", skillNames)
	}

	editNames := toolNames(al.initialPromptTools("Edit notes.txt to replace foo with bar."))
	if !containsAll(editNames, "edit_file") {
		t.Fatalf("expected edit_file, got %v", editNames)
	}

	appendNames := toolNames(al.initialPromptTools("Append a final line to report.txt."))
	if !containsAll(appendNames, "append_file") {
		t.Fatalf("expected append_file, got %v", appendNames)
	}

	execNames := toolNames(al.initialPromptTools("Run the command 'echo dragonscale-eval-test' and tell me the output."))
	if !containsAll(execNames, "exec") {
		t.Fatalf("expected exec, got %v", execNames)
	}

	writeReadNames := toolNames(al.initialPromptTools("Write the text 'dragonscale eval checkpoint' to a file called eval_checkpoint.txt, then read it back and confirm the contents match."))
	if !containsAll(writeReadNames, "write_file", "read_file") {
		t.Fatalf("expected write_file/read_file, got %v", writeReadNames)
	}

	listNames := toolNames(al.initialPromptTools("Create a file called project/readme.txt with 'Project initialized'. Then list the project directory to verify it exists."))
	if !containsAll(listNames, "write_file", "list_dir") {
		t.Fatalf("expected write_file/list_dir, got %v", listNames)
	}

	spawnNames := toolNames(al.initialPromptTools("Spawn a background task to write the text 'async-spawn-test' to a file called spawn_output.txt."))
	if !containsAll(spawnNames, "spawn", "write_file") {
		t.Fatalf("expected spawn/write_file, got %v", spawnNames)
	}

	subagentNames := toolNames(al.initialPromptTools("Use a subagent to calculate the sum of 10 + 20 + 30 and report the result back to me."))
	if !containsAll(subagentNames, "subagent") {
		t.Fatalf("expected subagent, got %v", subagentNames)
	}

	memoryNames := toolNames(al.initialPromptTools("Track these commitments exactly: send rent receipt tonight, book vet appointment tomorrow, and submit sprint notes by Friday."))
	if !containsAll(memoryNames, "memory") {
		t.Fatalf("expected memory, got %v", memoryNames)
	}

	memorySearchNames := toolNames(al.initialPromptTools("Search your memory for 'xyzzy_nonexistent_topic_42' and tell me what you find."))
	if !containsAll(memorySearchNames, "memory") {
		t.Fatalf("expected memory for memory-search prompt, got %v", memorySearchNames)
	}

	memoryStatusNames := toolNames(al.initialPromptTools("Check your memory system status and tell me the current context pressure level."))
	if !containsAll(memoryStatusNames, "memory") {
		t.Fatalf("expected memory for memory-status prompt, got %v", memoryStatusNames)
	}

	discoveryNames := toolNames(al.initialPromptTools("Search for a tool that can read files."))
	if !containsAll(discoveryNames, "tool_search") || containsAll(discoveryNames, "read_file") {
		t.Fatalf("expected discovery prompt to expose only tool_search-like helpers, got %v", discoveryNames)
	}
}

func TestIsPlanningOnlyPrompt(t *testing.T) {
	t.Parallel()

	if !isPlanningOnlyPrompt("Create a 6-week proactive check-in schedule for learning Spanish with weekly milestones.") {
		t.Fatal("expected planning-only prompt to be detected")
	}

	if isPlanningOnlyPrompt("Run 'date +%Y' to get the current year, write that year to a file, then read it back.") {
		t.Fatal("expected action-oriented prompt not to be treated as planning-only")
	}

	if isPlanningOnlyPrompt("Capture these commitments and give me a reminder plan.") {
		t.Fatal("expected capture/reminder prompt not to be treated as planning-only")
	}

	if !isPlanningOnlyPrompt("I must send a proposal in 4 hours. Give me a reminder schedule and specify when the first reminder should fire.") {
		t.Fatal("expected reminder schedule request to be treated as planning-only")
	}
}

func containsAll(have []string, want ...string) bool {
	set := make(map[string]struct{}, len(have))
	for _, name := range have {
		set[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := set[name]; !ok {
			return false
		}
	}
	return true
}

type namedTool struct {
	name string
}

func (n *namedTool) Name() string { return n.name }

func (n *namedTool) Description() string { return n.name }

func (n *namedTool) Parameters() map[string]interface{} { return map[string]interface{}{} }

func (n *namedTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: n.name}
}
