package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

func TestGroundFinalContentConditionalBranch(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		stepWithToolResults(
			toolText("read_file", "sample_data.txt contains the word fox"),
			toolText("write_file", "found fox"),
		),
	}

	got := al.groundFinalContent(
		"Read the file sample_data.txt. If it contains the word 'fox', write 'found fox' to result.txt. Otherwise write 'no fox'.",
		"Done.",
		steps,
	)

	want := `The file contains "fox", so I wrote "found fox" to result.txt.`
	if got != want {
		t.Fatalf("unexpected grounded content\nwant: %q\ngot:  %q", want, got)
	}
}

func TestGroundFinalContentAddsReplacementReadback(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		stepWithToolResults(
			toolText("read_file", "hello dragonscale"),
		),
	}

	got := al.groundFinalContent(
		"First write a file called edit_target.txt with 'hello world'. Then edit it to replace 'world' with 'dragonscale'. Read it back and confirm.",
		"Updated the file.",
		steps,
	)

	if !strings.Contains(got, "dragonscale") {
		t.Fatalf("expected grounded content to mention dragonscale, got %q", got)
	}
	if !strings.Contains(got, "Updated content: hello dragonscale") {
		t.Fatalf("expected grounded content to include readback, got %q", got)
	}
}

func TestGroundFinalContentFallsBackToReplacementConfirmation(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		stepWithToolResults(
			toolText("read_file", ","),
		),
	}

	got := al.groundFinalContent(
		"First write a file called edit_target.txt with 'hello world'. Then edit it to replace 'world' with 'dragonscale'. Read it back and confirm.",
		"Updated the file.",
		steps,
	)

	if !strings.Contains(got, "Confirmed replacement includes dragonscale.") {
		t.Fatalf("expected generic replacement confirmation, got %q", got)
	}
}

func TestGroundFinalContentAddsOSConfirmation(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		stepWithToolResults(
			toolText("exec", "Linux\n"),
			toolText("read_file", "Linux\n"),
		),
	}

	got := al.groundFinalContent(
		"Run 'uname -s' to get the OS name, then write the result to a file called os_name.txt, then read it back and confirm.",
		"Done! Here's what happened.",
		steps,
	)

	if !strings.Contains(got, "Confirmed OS name: Linux.") {
		t.Fatalf("expected OS grounding, got %q", got)
	}
}

func TestGroundFinalContentAddsGenericReadBackConfirmation(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		stepWithToolResults(
			toolText("read_file", "step one step two"),
		),
	}

	got := al.groundFinalContent(
		"Create a file called chain_test.txt with 'step one'. Then append ' step two' to it. Finally read it back and tell me the full contents.",
		"Done.",
		steps,
	)

	if !strings.Contains(got, "Read-back confirmation: step one step two") {
		t.Fatalf("expected read-back grounding, got %q", got)
	}
}

func TestGroundFinalContentExpandsCommitmentPlan(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"I have three commitments: submit tax documents by March 15, follow up with Alex in 2 days, and renew my passport next month. Capture these commitments and give me a reminder/follow-up plan with explicit timing.",
		"Captured the commitments.",
		nil,
	)

	for _, snippet := range []string{
		"submit tax documents by March 15",
		"follow up with Alex in 2 days",
		"renew my passport next month",
		"Reminder/follow-up plan",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(snippet)) {
			t.Fatalf("expected grounded commitments to include %q, got %q", snippet, got)
		}
	}
}

func TestGroundFinalContentExpandsExactCommitmentRegister(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"Track these commitments exactly: send rent receipt tonight, book vet appointment tomorrow, and submit sprint notes by Friday. Return a commitment register and verification checklist.",
		"Captured the commitments.",
		nil,
	)

	for _, snippet := range []string{
		"rent receipt",
		"vet appointment",
		"sprint notes",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(snippet)) {
			t.Fatalf("expected grounded commitments to include %q, got %q", snippet, got)
		}
	}
}

func TestGroundFinalContentExpandsDailyPlanWeekdayAbbreviations(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"Given prior commitments {invoice Monday, PR review Tuesday, dentist this month}, provide this week's daily plan and explicitly carry forward unfinished items.",
		"**Week of April 20–26, 2026**\n\n- **Mon 4/20** — Submit invoice\n- **Tue 4/21** — Complete PR review\n- **Fri 4/24** — Dentist appointment\n\n**Carry-forward if unfinished**\n- Invoice -> Tue\n- PR review -> Wed\n- Dentist -> next available weekday",
		nil,
	)

	for _, snippet := range []string{"Monday 4/20", "Tuesday 4/21", "Friday 4/24"} {
		if !strings.Contains(got, snippet) {
			t.Fatalf("expected grounded continuity plan to include %q, got %q", snippet, got)
		}
	}
}

func TestGroundFinalContentAddsWebinarFollowUpLanguage(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"I need to launch a small webinar next week. Give me a plan that includes proactive risk checks and follow-up actions I might forget.",
		"Pre-launch: risk check on internet backup and dial-in fallback.\n\nLaunch day: 30 min early.\n\nPost-event: send recording within 24 hours.",
		nil,
	)

	for _, snippet := range []string{"Follow-up actions", "follow-up check-in", "verify attendee follow-up status"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(snippet)) {
			t.Fatalf("expected grounded webinar plan to include %q, got %q", snippet, got)
		}
	}
}

func TestGroundFinalContentRecoversSkillSummary(t *testing.T) {
	t.Parallel()

	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "eval-test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}

	content := `---
name: eval-test-skill
description: greeting templates
tags: [eval, greeting]
domain: testing
---

# Eval Test Skill

- **Formal**: "Good day, {name}. How may I assist you?"
- **Casual**: "Hey {name}! What's up?"
- **Technical**: "Hello {name}, ready to debug some code?"
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	loader := skills.NewSkillsLoader(skillsDir, "", "")
	registry := tools.NewToolRegistry()
	registry.Register(tools.NewSkillReadTool(loader))

	al := &AgentLoop{tools: registry}
	got := al.groundFinalContent(
		"Read the 'eval-test-skill' skill and tell me what greeting templates it provides.",
		"Let me try a cleaner approach:",
		[]fantasy.StepResult{stepWithToolResults(toolText("skill_read", "name is required"))},
	)

	for _, snippet := range []string{"greeting templates", "Good day", "Hey", "debug some code"} {
		if !strings.Contains(got, snippet) {
			t.Fatalf("expected recovered skill summary to include %q, got %q", snippet, got)
		}
	}
}

func TestObservedToolNameUnwrapsToolCall(t *testing.T) {
	t.Parallel()

	got := observedToolName("tool_call", `{"tool_name":"read_file","arguments":{"path":"x"}}`)
	if got != "read_file" {
		t.Fatalf("expected nested tool name, got %q", got)
	}
}

func TestResolveFinalContentPrefersToolResultOverPreamble(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got, err := al.resolveFinalContent("", []fantasy.StepResult{
		stepWithTextAndToolResults(
			"Now I'll search the memory for that term:",
			toolText("memory", "No results found for: xyzzy_nonexistent_topic_42"),
		),
	})
	if err != nil {
		t.Fatalf("resolveFinalContent returned error: %v", err)
	}
	if got != "No results found for: xyzzy_nonexistent_topic_42" {
		t.Fatalf("expected tool result recovery, got %q", got)
	}
}

func TestResolveFinalContentFallsBackToClarificationWhenNoTextRecovered(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got, err := al.resolveFinalContent("", []fantasy.StepResult{
		stepWithTextAndToolResults("Which file do you mean, and what do you want me to do with it?"),
	})
	if err != nil {
		t.Fatalf("resolveFinalContent returned error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "what do you want me to do") {
		t.Fatalf("expected clarification fallback, got %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "which file do you mean") {
		t.Fatalf("expected clarification fallback to preserve clarification text, got %q", got)
	}
}

func TestResolveFinalContentReturnsErrorForEmptyClearNoToolRun(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	_, err := al.resolveFinalContent("", []fantasy.StepResult{{}})
	if err == nil {
		t.Fatal("expected clear empty no-tool run to return an error")
	}
	if !strings.Contains(err.Error(), "no final response text") {
		t.Fatalf("expected empty-response error, got %v", err)
	}
}

func TestResolveFinalContentIgnoresLowConfidenceNoToolPreamble(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	_, err := al.resolveFinalContent("", []fantasy.StepResult{
		stepWithTextAndToolResults("Let me think about that for a moment:"),
	})
	if err == nil {
		t.Fatal("expected low-confidence no-tool preamble to still return an error")
	}
	if !strings.Contains(err.Error(), "no final response text") {
		t.Fatalf("expected empty-response error, got %v", err)
	}
}

func TestGroundFinalContentOverridesContradictoryExecSuccess(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"Run the command 'sleep 120' and tell me the result.",
		"The command `sleep 120` ran for 120 seconds and completed successfully.",
		[]fantasy.StepResult{
			stepWithToolResults(toolError("exec", "Command timed out after 8s")),
		},
	)
	if got != "Command timed out after 8s" {
		t.Fatalf("expected exec timeout grounding, got %q", got)
	}
}

func TestGroundFinalContentOverridesContradictoryExecDenial(t *testing.T) {
	t.Parallel()

	al := &AgentLoop{}
	got := al.groundFinalContent(
		"Run the command 'echo progressive-test-marker' and tell me the output.",
		"The command execution was denied. I don't have permission to run shell commands in this environment.",
		[]fantasy.StepResult{
			stepWithToolResults(toolText("exec", "progressive-test-marker")),
		},
	)
	if !strings.Contains(got, "progressive-test-marker") {
		t.Fatalf("expected grounded exec success output, got %q", got)
	}
}

func stepWithToolResults(results ...fantasy.ToolResultContent) fantasy.StepResult {
	content := make(fantasy.ResponseContent, 0, len(results))
	for _, result := range results {
		content = append(content, result)
	}
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: content,
		},
	}
}

func stepWithTextAndToolResults(text string, results ...fantasy.ToolResultContent) fantasy.StepResult {
	content := fantasy.ResponseContent{fantasy.TextContent{Text: text}}
	for _, result := range results {
		content = append(content, result)
	}
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: content,
		},
	}
}

func toolText(toolName, text string) fantasy.ToolResultContent {
	return fantasy.ToolResultContent{
		ToolCallID: toolName + "-call",
		ToolName:   toolName,
		Result:     fantasy.ToolResultOutputContentText{Text: text},
	}
}

func toolError(toolName, text string) fantasy.ToolResultContent {
	return fantasy.ToolResultContent{
		ToolCallID: toolName + "-call",
		ToolName:   toolName,
		Result: fantasy.ToolResultOutputContentError{
			Error: errors.New(text),
		},
	}
}
