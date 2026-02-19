// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package fantasy

import (
	"charm.land/fantasy"
	jsonv2 "github.com/go-json-experiment/json"

	"github.com/sipeed/picoclaw/pkg/messages"
)

// MessagesToFantasy converts PicoClaw session messages to Fantasy's multipart format.
func MessagesToFantasy(msgs []messages.Message) []fantasy.Message {
	out := make([]fantasy.Message, 0, len(msgs))

	for _, msg := range msgs {
		out = append(out, MessageToFantasy(msg))
	}

	return out
}

// MessageToFantasy converts a single PicoClaw message to Fantasy format.
func MessageToFantasy(msg messages.Message) fantasy.Message {
	var parts []fantasy.MessagePart

	// Tool result messages have a ToolCallID — they map to a ToolResultPart.
	if msg.ToolCallID != "" {
		parts = append(parts, fantasy.ToolResultPart{
			ToolCallID: msg.ToolCallID,
			Output:     fantasy.ToolResultOutputContentText{Text: msg.Content},
		})
		return fantasy.Message{
			Role:    fantasy.MessageRole(msg.Role),
			Content: parts,
		}
	}

	// Add text content if present.
	if msg.Content != "" {
		parts = append(parts, fantasy.TextPart{Text: msg.Content})
	}

	// Convert tool calls to ToolCallParts.
	for _, tc := range msg.ToolCalls {
		name := tc.Name
		input := ""

		// Prefer Function.Name and Function.Arguments (OpenAI format).
		if tc.Function != nil {
			name = tc.Function.Name
			input = tc.Function.Arguments
		} else if tc.Arguments != nil {
			// Fallback: serialize the map to JSON.
			data, _ := jsonv2.Marshal(tc.Arguments)
			input = string(data)
		}

		parts = append(parts, fantasy.ToolCallPart{
			ToolCallID: tc.ID,
			ToolName:   name,
			Input:      input,
		})
	}

	return fantasy.Message{
		Role:    fantasy.MessageRole(msg.Role),
		Content: parts,
	}
}

// StepToMessages converts a Fantasy StepResult back to PicoClaw message format
// for session storage. Each step may produce an assistant message with tool calls
// and zero or more tool result messages.
func StepToMessages(step fantasy.StepResult) []messages.Message {
	var out []messages.Message

	// Build the assistant message from step content.
	assistantMsg := messages.Message{
		Role: "assistant",
	}

	var toolResults []messages.Message

	for _, c := range step.Response.Content {
		switch ct := c.(type) {
		case fantasy.TextContent:
			assistantMsg.Content += ct.Text

		case fantasy.ToolCallContent:
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, messages.ToolCall{
				ID:   ct.ToolCallID,
				Type: "function",
				Function: &messages.FunctionCall{
					Name:      ct.ToolName,
					Arguments: ct.Input,
				},
			})

		case fantasy.ToolResultContent:
			resultText := ""
			if ct.Result != nil {
				switch r := ct.Result.(type) {
				case fantasy.ToolResultOutputContentText:
					resultText = r.Text
				case fantasy.ToolResultOutputContentError:
					if r.Error != nil {
						resultText = r.Error.Error()
					}
				}
			}
			toolResults = append(toolResults, messages.Message{
				Role:       "tool",
				Content:    resultText,
				ToolCallID: ct.ToolCallID,
			})
		}
	}

	// Always emit the assistant message (even if empty content with tool calls).
	out = append(out, assistantMsg)

	// Append tool result messages after the assistant message.
	out = append(out, toolResults...)

	return out
}

// AgentResultToMessages converts a complete AgentResult to PicoClaw messages.
// This flattens all steps into a single message sequence.
func AgentResultToMessages(result *fantasy.AgentResult) []messages.Message {
	var out []messages.Message
	for _, step := range result.Steps {
		out = append(out, StepToMessages(step)...)
	}
	return out
}
