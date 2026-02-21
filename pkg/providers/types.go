package providers

import (
	"context"

	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
)

// Type aliases: canonical types now live in pkg/messages.
// These aliases maintain backward compatibility during migration.
type ToolCall = messages.ToolCall
type FunctionCall = messages.FunctionCall
type UsageInfo = messages.UsageInfo
type Message = messages.Message
type ToolDefinition = messages.ToolDefinition
type ToolFunctionDefinition = messages.ToolFunctionDefinition

// LLMResponse is the response from an LLM provider API call.
type LLMResponse struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        *UsageInfo `json:"usage,omitempty"`
}

// LLMProvider is the interface for LLM provider implementations.
type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error)
	GetDefaultModel() string
}
