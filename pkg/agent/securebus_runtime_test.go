package agent

import (
	"strings"
	"testing"

	fantasy "charm.land/fantasy"
)

func TestRepairToolCallInputRepairsDirectExecPlaceholder(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "exec",
			Input:      `{"command":":"}`,
		},
		"Run the command 'echo progressive-test-marker' and tell me the output.",
	)

	if !strings.Contains(got.Input, `"echo progressive-test-marker"`) {
		t.Fatalf("expected repaired exec command, got %q", got.Input)
	}
}

func TestRepairToolCallInputForcesExplicitExecCommand(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "exec",
			Input:      `{"command":":true"}`,
		},
		"Run the command 'echo dragonscale-eval-test' and tell me the output.",
	)

	if !strings.Contains(got.Input, `"echo dragonscale-eval-test"`) {
		t.Fatalf("expected forced exec command, got %q", got.Input)
	}
}

func TestRepairToolCallInputRepairsNestedToolCallPlaceholder(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "tool_call",
			Input:      `{"tool_name":"skill_read","arguments":{"name":":"}}`,
		},
		"Read the 'eval-test-skill' skill and tell me what greeting templates it provides.",
	)

	if !strings.Contains(got.Input, `"eval-test-skill"`) {
		t.Fatalf("expected repaired nested skill name, got %q", got.Input)
	}
}

func TestRepairToolCallInputRepairsWriteAndListPlaceholders(t *testing.T) {
	t.Parallel()

	write := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-write",
			ToolName:   "write_file",
			Input:      `{"path":"}","content":","}`,
		},
		"Create a file called project/readme.txt with 'Project initialized'. Then list the project directory to verify it exists.",
	)
	if !strings.Contains(write.Input, `"project/readme.txt"`) || !strings.Contains(write.Input, `"Project initialized"`) {
		t.Fatalf("expected repaired write args, got %q", write.Input)
	}

	list := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-list",
			ToolName:   "list_dir",
			Input:      `{}`,
		},
		"Create a file called project/readme.txt with 'Project initialized'. Then list the project directory to verify it exists.",
	)
	if !strings.Contains(list.Input, `"project"`) {
		t.Fatalf("expected repaired list_dir path, got %q", list.Input)
	}
}

func TestRepairToolCallInputRepairsReadBackAndFetchPlaceholders(t *testing.T) {
	t.Parallel()

	read := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-read",
			ToolName:   "read_file",
			Input:      `{"path":"}"}`,
		},
		"Create a file called test_steps.txt with the content 'step test', then read it back to confirm.",
	)
	if !strings.Contains(read.Input, `"test_steps.txt"`) {
		t.Fatalf("expected repaired read_file path, got %q", read.Input)
	}

	fetch := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-fetch",
			ToolName:   "web_fetch",
			Input:      `{"url":".example.com"}`,
		},
		"Fetch the contents of https://example.com and tell me the title of the page.",
	)
	if !strings.Contains(fetch.Input, `"https://example.com"`) {
		t.Fatalf("expected repaired web_fetch url, got %q", fetch.Input)
	}
}

func TestSanitizePolicyErrorPreservesSafeExecErrors(t *testing.T) {
	t.Parallel()

	raw := "command timed out after 8s"
	if got := sanitizePolicyError(raw); got != raw {
		t.Fatalf("expected timeout text to survive sanitization, got %q", got)
	}
}

func TestSanitizePolicyErrorRedactsPolicyViolations(t *testing.T) {
	t.Parallel()

	got := sanitizePolicyError("filesystem access denied: /etc/passwd")
	if got != "policy violation: filesystem access denied" {
		t.Fatalf("expected redacted policy text, got %q", got)
	}
}
