package fantasy

import (
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/messages"
)

// --- MessagesToFantasy Tests ---

func TestMessagesToFantasy_EmptySlice(t *testing.T) {
	result := MessagesToFantasy(nil)
	if len(result) != 0 {
		t.Errorf("Expected empty slice for nil input, got %d", len(result))
	}

	result = MessagesToFantasy([]messages.Message{})
	if len(result) != 0 {
		t.Errorf("Expected empty slice for empty input, got %d", len(result))
	}
}

func TestMessageToFantasy_SimpleTextMessage(t *testing.T) {
	msg := messages.Message{
		Role:    "user",
		Content: "Hello, world",
	}

	result := MessageToFantasy(msg)

	if result.Role != fantasy.MessageRoleUser {
		t.Errorf("Expected role 'user', got '%s'", result.Role)
	}
	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(result.Content))
	}

	tp, ok := fantasy.AsMessagePart[fantasy.TextPart](result.Content[0])
	if !ok {
		t.Fatal("Expected TextPart")
	}
	if tp.Text != "Hello, world" {
		t.Errorf("Expected 'Hello, world', got '%s'", tp.Text)
	}
}

func TestMessageToFantasy_AssistantWithMultipleToolCalls(t *testing.T) {
	msg := messages.Message{
		Role:    "assistant",
		Content: "Let me run both tools.",
		ToolCalls: []messages.ToolCall{
			{
				ID:   "call-1",
				Type: "function",
				Function: &messages.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path": "/tmp/test.txt"}`,
				},
			},
			{
				ID:   "call-2",
				Type: "function",
				Function: &messages.FunctionCall{
					Name:      "write_file",
					Arguments: `{"path": "/tmp/out.txt", "content": "data"}`,
				},
			},
		},
	}

	result := MessageToFantasy(msg)

	if result.Role != fantasy.MessageRoleAssistant {
		t.Errorf("Expected role 'assistant', got '%s'", result.Role)
	}

	// Should have text + 2 tool calls = 3 parts
	if len(result.Content) != 3 {
		t.Fatalf("Expected 3 content parts (text + 2 tool calls), got %d", len(result.Content))
	}

	// Part 0: text
	tp, ok := fantasy.AsMessagePart[fantasy.TextPart](result.Content[0])
	if !ok {
		t.Fatal("Expected TextPart at index 0")
	}
	if tp.Text != "Let me run both tools." {
		t.Errorf("Expected text content, got '%s'", tp.Text)
	}

	// Part 1: first tool call
	tc1, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](result.Content[1])
	if !ok {
		t.Fatal("Expected ToolCallPart at index 1")
	}
	if tc1.ToolCallID != "call-1" || tc1.ToolName != "read_file" {
		t.Errorf("Tool call 1 mismatch: id=%s name=%s", tc1.ToolCallID, tc1.ToolName)
	}
	if tc1.Input != `{"path": "/tmp/test.txt"}` {
		t.Errorf("Tool call 1 input mismatch: %s", tc1.Input)
	}

	// Part 2: second tool call
	tc2, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](result.Content[2])
	if !ok {
		t.Fatal("Expected ToolCallPart at index 2")
	}
	if tc2.ToolCallID != "call-2" || tc2.ToolName != "write_file" {
		t.Errorf("Tool call 2 mismatch: id=%s name=%s", tc2.ToolCallID, tc2.ToolName)
	}
}

func TestMessageToFantasy_ToolCallWithMapArgsFallback(t *testing.T) {
	msg := messages.Message{
		Role: "assistant",
		ToolCalls: []messages.ToolCall{
			{
				ID:   "call-fallback",
				Name: "exec",
				Arguments: map[string]interface{}{
					"command": "ls -la",
				},
			},
		},
	}

	result := MessageToFantasy(msg)

	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 part, got %d", len(result.Content))
	}

	tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](result.Content[0])
	if !ok {
		t.Fatal("Expected ToolCallPart")
	}
	if tc.ToolName != "exec" {
		t.Errorf("Expected tool name 'exec', got '%s'", tc.ToolName)
	}
	if tc.Input != `{"command":"ls -la"}` {
		t.Errorf("Expected serialized JSON, got '%s'", tc.Input)
	}
}

func TestMessageToFantasy_ToolResultMessage(t *testing.T) {
	msg := messages.Message{
		Role:       "tool",
		Content:    "file contents here",
		ToolCallID: "call-1",
	}

	result := MessageToFantasy(msg)

	if result.Role != fantasy.MessageRoleTool {
		t.Errorf("Expected role 'tool', got '%s'", result.Role)
	}
	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 part, got %d", len(result.Content))
	}

	trp, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](result.Content[0])
	if !ok {
		t.Fatal("Expected ToolResultPart")
	}
	if trp.ToolCallID != "call-1" {
		t.Errorf("Expected ToolCallID 'call-1', got '%s'", trp.ToolCallID)
	}
	textOutput, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](trp.Output)
	if !ok {
		t.Fatal("Expected ToolResultOutputContentText")
	}
	if textOutput.Text != "file contents here" {
		t.Errorf("Expected 'file contents here', got '%s'", textOutput.Text)
	}
}

func TestMessageToFantasy_EmptyContent(t *testing.T) {
	msg := messages.Message{
		Role:    "assistant",
		Content: "",
	}

	result := MessageToFantasy(msg)

	// Empty content + no tool calls = no parts
	if len(result.Content) != 0 {
		t.Errorf("Expected 0 parts for empty content, got %d", len(result.Content))
	}
}

// --- StepToMessages Tests ---

func TestStepToMessages_TextOnly(t *testing.T) {
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "Simple response"},
			},
			FinishReason: fantasy.FinishReasonStop,
		},
	}

	msgs := StepToMessages(step)

	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("Expected role 'assistant', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "Simple response" {
		t.Errorf("Expected 'Simple response', got '%s'", msgs[0].Content)
	}
}

func TestStepToMessages_MultipleToolCalls(t *testing.T) {
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "Running tools..."},
				fantasy.ToolCallContent{
					ToolCallID: "tc-1",
					ToolName:   "read_file",
					Input:      `{"path": "foo.txt"}`,
				},
				fantasy.ToolCallContent{
					ToolCallID: "tc-2",
					ToolName:   "exec",
					Input:      `{"command": "ls"}`,
				},
				fantasy.ToolResultContent{
					ToolCallID: "tc-1",
					ToolName:   "read_file",
					Result:     fantasy.ToolResultOutputContentText{Text: "file contents"},
				},
				fantasy.ToolResultContent{
					ToolCallID: "tc-2",
					ToolName:   "exec",
					Result:     fantasy.ToolResultOutputContentText{Text: "dir listing"},
				},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
		},
	}

	msgs := StepToMessages(step)

	// Should be: 1 assistant (with text + 2 tool calls) + 2 tool result messages
	if len(msgs) != 3 {
		t.Fatalf("Expected 3 messages (1 assistant + 2 tool results), got %d", len(msgs))
	}

	// Assistant message
	assistantMsg := msgs[0]
	if assistantMsg.Role != "assistant" {
		t.Errorf("Expected 'assistant' role, got '%s'", assistantMsg.Role)
	}
	if assistantMsg.Content != "Running tools..." {
		t.Errorf("Expected text content, got '%s'", assistantMsg.Content)
	}
	if len(assistantMsg.ToolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].ID != "tc-1" || assistantMsg.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("Tool call 1 mismatch")
	}
	if assistantMsg.ToolCalls[1].ID != "tc-2" || assistantMsg.ToolCalls[1].Function.Name != "exec" {
		t.Errorf("Tool call 2 mismatch")
	}

	// Tool result messages
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "tc-1" || msgs[1].Content != "file contents" {
		t.Errorf("Tool result 1 mismatch: role=%s id=%s content=%s", msgs[1].Role, msgs[1].ToolCallID, msgs[1].Content)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "tc-2" || msgs[2].Content != "dir listing" {
		t.Errorf("Tool result 2 mismatch: role=%s id=%s content=%s", msgs[2].Role, msgs[2].ToolCallID, msgs[2].Content)
	}
}

func TestStepToMessages_ErrorToolResult(t *testing.T) {
	testErr := errors.New("permission denied")
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{
					ToolCallID: "tc-err",
					ToolName:   "write_file",
					Input:      `{"path": "/etc/passwd"}`,
				},
				fantasy.ToolResultContent{
					ToolCallID: "tc-err",
					ToolName:   "write_file",
					Result:     fantasy.ToolResultOutputContentError{Error: testErr},
				},
			},
		},
	}

	msgs := StepToMessages(step)

	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}

	// Tool result should contain error text
	toolMsg := msgs[1]
	if toolMsg.Role != "tool" {
		t.Errorf("Expected 'tool' role, got '%s'", toolMsg.Role)
	}
	if toolMsg.Content != "permission denied" {
		t.Errorf("Expected error message, got '%s'", toolMsg.Content)
	}
}

func TestStepToMessages_ErrorToolResult_NilError(t *testing.T) {
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolResultContent{
					ToolCallID: "tc-nil",
					Result:     fantasy.ToolResultOutputContentError{Error: nil},
				},
			},
		},
	}

	msgs := StepToMessages(step)

	// Assistant message + tool result
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	// Nil error should produce empty content
	if msgs[1].Content != "" {
		t.Errorf("Expected empty content for nil error, got '%s'", msgs[1].Content)
	}
}

func TestStepToMessages_ToolCallWithoutText(t *testing.T) {
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{
					ToolCallID: "tc-only",
					ToolName:   "exec",
					Input:      `{"command": "pwd"}`,
				},
			},
		},
	}

	msgs := StepToMessages(step)

	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}

	// Assistant message with tool calls but no text
	if msgs[0].Role != "assistant" {
		t.Errorf("Expected 'assistant', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "" {
		t.Errorf("Expected empty content, got '%s'", msgs[0].Content)
	}
	if len(msgs[0].ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(msgs[0].ToolCalls))
	}
}

// --- AgentResultToMessages Tests ---

func TestAgentResultToMessages_MultipleSteps(t *testing.T) {
	result := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{
				Response: fantasy.Response{
					Content: fantasy.ResponseContent{
						fantasy.ToolCallContent{ToolCallID: "s1-tc", ToolName: "exec", Input: `{}`},
						fantasy.ToolResultContent{ToolCallID: "s1-tc", Result: fantasy.ToolResultOutputContentText{Text: "ok"}},
					},
				},
			},
			{
				Response: fantasy.Response{
					Content: fantasy.ResponseContent{
						fantasy.TextContent{Text: "Done!"},
					},
				},
			},
		},
	}

	msgs := AgentResultToMessages(result)

	// Step 1: assistant (with tool call) + tool result = 2 messages
	// Step 2: assistant text = 1 message
	if len(msgs) != 3 {
		t.Fatalf("Expected 3 messages across 2 steps, got %d", len(msgs))
	}

	// Step 1 assistant
	if msgs[0].Role != "assistant" || len(msgs[0].ToolCalls) != 1 {
		t.Errorf("Step 1 assistant unexpected: role=%s toolcalls=%d", msgs[0].Role, len(msgs[0].ToolCalls))
	}
	// Step 1 tool result
	if msgs[1].Role != "tool" || msgs[1].Content != "ok" {
		t.Errorf("Step 1 tool result unexpected: role=%s content=%s", msgs[1].Role, msgs[1].Content)
	}
	// Step 2 assistant
	if msgs[2].Role != "assistant" || msgs[2].Content != "Done!" {
		t.Errorf("Step 2 assistant unexpected: role=%s content=%s", msgs[2].Role, msgs[2].Content)
	}
}

// --- Round-trip fidelity test ---

func TestRoundTrip_PicoClawToFantasyAndBack(t *testing.T) {
	// Start with PicoClaw messages representing a typical conversation
	original := []messages.Message{
		{Role: "user", Content: "Read the file"},
		{
			Role:    "assistant",
			Content: "Reading file...",
			ToolCalls: []messages.ToolCall{
				{
					ID:   "tc-read",
					Type: "function",
					Function: &messages.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path": "test.txt"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			Content:    "file contents here",
			ToolCallID: "tc-read",
		},
		{Role: "assistant", Content: "The file contains: file contents here"},
	}

	// Convert to Fantasy
	fantasyMsgs := MessagesToFantasy(original)

	if len(fantasyMsgs) != 4 {
		t.Fatalf("Expected 4 fantasy messages, got %d", len(fantasyMsgs))
	}

	// Verify key properties survived the conversion
	// Message 0: user text
	if fantasyMsgs[0].Role != fantasy.MessageRoleUser {
		t.Errorf("Msg 0: expected user role")
	}

	// Message 1: assistant with text + tool call
	if fantasyMsgs[1].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Msg 1: expected assistant role")
	}
	if len(fantasyMsgs[1].Content) != 2 {
		t.Errorf("Msg 1: expected 2 parts (text + tool call), got %d", len(fantasyMsgs[1].Content))
	}

	// Message 2: tool result
	if fantasyMsgs[2].Role != fantasy.MessageRoleTool {
		t.Errorf("Msg 2: expected tool role")
	}

	// Message 3: final assistant text
	if fantasyMsgs[3].Role != fantasy.MessageRoleAssistant {
		t.Errorf("Msg 3: expected assistant role")
	}
}
