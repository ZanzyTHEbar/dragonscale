package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// --- Mock language model that simulates tool calls ---

// toolCallingModel simulates an LLM that requests tool calls on first round,
// then produces a final text response incorporating tool results.
type toolCallingModel struct {
	callCount int
}

func (m *toolCallingModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	m.callCount++

	// First call: check if any tool results already exist in the prompt.
	// If no tool results found, request a tool call.
	hasToolResults := false
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			if part.GetType() == fantasy.ContentTypeToolResult {
				hasToolResults = true
			}
		}
	}

	if !hasToolResults && len(call.Tools) > 0 {
		// Request a tool call
		return &fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{
					ToolCallID: "call-1",
					ToolName:   "echo",
					Input:      `{"text": "hello from tool"}`,
				},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
		}, nil
	}

	// After tool results: produce final response
	return &fantasy.Response{
		Content: fantasy.ResponseContent{
			fantasy.TextContent{Text: "Integration test response with tool output"},
		},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *toolCallingModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	resp, err := m.Generate(context.Background(), call)
	if err != nil {
		return nil, err
	}

	return func(yield func(fantasy.StreamPart) bool) {
		// Check if response has tool calls
		hasToolCalls := false
		for _, c := range resp.Content {
			if c.GetType() == fantasy.ContentTypeToolCall {
				hasToolCalls = true
			}
		}

		if hasToolCalls {
			// Emit tool calls as stream parts
			for _, c := range resp.Content {
				if tc, ok := c.(fantasy.ToolCallContent); ok {
					if !yield(fantasy.StreamPart{
						Type:          fantasy.StreamPartTypeToolCall,
						ID:            tc.ToolCallID,
						ToolCallName:  tc.ToolName,
						ToolCallInput: tc.Input,
					}) {
						return
					}
				}
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls}) {
				return
			}
		} else {
			// Emit text as proper stream sequence
			text := resp.Content.Text()
			if text != "" {
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text-0"}) {
					return
				}
				// Emit text in chunks to simulate real streaming
				for i := 0; i < len(text); i += 10 {
					end := i + 10
					if end > len(text) {
						end = len(text)
					}
					if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text-0", Delta: text[i:end]}) {
						return
					}
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text-0"}) {
					return
				}
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
		}
	}, nil
}

func (m *toolCallingModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *toolCallingModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *toolCallingModel) Provider() string { return "mock" }
func (m *toolCallingModel) Model() string    { return "mock-tool-model" }

// --- Simple echo tool for integration testing ---

type echoTool struct{}

func (t *echoTool) Name() string        { return "echo" }
func (t *echoTool) Description() string { return "Echo the given text back" }
func (t *echoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to echo",
			},
		},
		"required": []string{"text"},
	}
}

func (t *echoTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	text, _ := args["text"].(string)
	return &tools.ToolResult{
		ForLLM:  "Echo: " + text,
		ForUser: "",
		Silent:  true,
		IsError: false,
	}
}

// --- Integration Tests ---

// TestIntegration_FullAgentLoop_SimpleResponse tests the full agent loop
// with a simple mock model that returns text directly (no tool calls).
func TestIntegration_FullAgentLoop_SimpleResponse(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("Hello from Fantasy agent")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "Say hello",
		SessionKey: "test-session-simple",
	}

	response, err := al.processMessage(ctx, msg)
	if err != nil {
		t.Fatalf("processMessage failed: %v", err)
	}

	if response != "Hello from Fantasy agent" {
		t.Errorf("Expected 'Hello from Fantasy agent', got: %s", response)
	}

	// Verify session has messages saved
	history := al.sessions.GetHistory("test-session-simple")
	if len(history) == 0 {
		t.Error("Expected session history to have messages")
	}

	// First message should be the user's
	foundUser := false
	for _, m := range history {
		if m.Role == "user" && m.Content == "Say hello" {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Error("Expected user message in session history")
	}
}

// TestIntegration_FullAgentLoop_WithToolCalls tests the full agent loop
// including tool call execution and response incorporation.
func TestIntegration_FullAgentLoop_WithToolCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-tools-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "mock-tool-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := &toolCallingModel{}
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Register the echo tool
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "Use the echo tool",
		SessionKey: "test-session-tools",
	}

	response, err := al.processMessage(ctx, msg)
	if err != nil {
		t.Fatalf("processMessage failed: %v", err)
	}

	// The model returns "Integration test response with tool output" after tool execution
	if !strings.Contains(response, "Integration test response") {
		t.Errorf("Expected response to contain 'Integration test response', got: %s", response)
	}

	// Model should have been called at least twice (tool call + final response)
	if model.callCount < 2 {
		t.Errorf("Expected model to be called at least 2 times, got: %d", model.callCount)
	}
}

// TestIntegration_ProcessDirect tests the ProcessDirect method
// which is used by CLI mode for one-shot message processing.
func TestIntegration_ProcessDirect(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-direct-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("Direct CLI response")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	response, err := al.ProcessDirect(ctx, "Direct message", "direct-session")
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}

	if response != "Direct CLI response" {
		t.Errorf("Expected 'Direct CLI response', got: %s", response)
	}
}

// --- Streaming-specific mock ---

// streamingModel simulates an LLM with proper streaming token emission.
// It emits tokens one word at a time via Stream() and also supports Generate()
// for non-streaming fallback.
type streamingModel struct {
	words []string
}

func newStreamingModel(text string) *streamingModel {
	return &streamingModel{words: strings.Fields(text)}
}

func (m *streamingModel) Generate(_ context.Context, _ fantasy.Call) (*fantasy.Response, error) {
	fullText := strings.Join(m.words, " ")
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: fullText}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *streamingModel) Stream(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	words := m.words
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "s-0"}) {
			return
		}
		for i, w := range words {
			delta := w
			if i < len(words)-1 {
				delta += " "
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "s-0", Delta: delta}) {
				return
			}
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "s-0"}) {
			return
		}
		yield(fantasy.StreamPart{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonStop,
			Usage:        fantasy.Usage{InputTokens: 10, OutputTokens: int64(len(words)), TotalTokens: 10 + int64(len(words))},
		})
	}, nil
}

func (m *streamingModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *streamingModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *streamingModel) Provider() string { return "mock" }
func (m *streamingModel) Model() string    { return "streaming-mock" }

// TestIntegration_Streaming_TextDeltas tests that the streaming agent loop
// publishes text deltas to the bus and returns the complete text.
func TestIntegration_Streaming_TextDeltas(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-stream-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "streaming-mock",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newStreamingModel("Hello from streaming agent response")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Collect stream deltas in background
	var deltas []string
	var deltaDone = make(chan struct{})
	go func() {
		defer close(deltaDone)
		for {
			msg, ok := msgBus.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			if msg.StreamDelta {
				deltas = append(deltas, msg.Content)
			}
		}
	}()

	// Process with streaming
	response, err := al.ProcessDirectStreaming(ctx, "Stream me", "stream-session", "test", "chat-1")
	if err != nil {
		t.Fatalf("ProcessDirectStreaming failed: %v", err)
	}

	// Cancel to stop delta collector
	cancel()
	<-deltaDone

	// Verify complete response
	if response != "Hello from streaming agent response" {
		t.Errorf("Expected 'Hello from streaming agent response', got: %s", response)
	}

	// Verify stream deltas were published
	if len(deltas) == 0 {
		t.Error("Expected stream deltas to be published to bus")
	}

	// Reconstruct full text from deltas
	fullFromDeltas := strings.Join(deltas, "")
	if fullFromDeltas != "Hello from streaming agent response" {
		t.Errorf("Delta reconstruction mismatch: got '%s'", fullFromDeltas)
	}

	// Verify session was saved
	history := al.sessions.GetHistory("stream-session")
	if len(history) < 2 { // user + assistant
		t.Errorf("Expected at least 2 messages in session, got %d", len(history))
	}
}

// TestIntegration_Streaming_WithToolCalls tests streaming with a model
// that requests tool calls before producing a final streamed response.
func TestIntegration_Streaming_WithToolCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-stream-tools-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "mock-tool-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := &toolCallingModel{}
	al := mustNewAgentLoop(t, cfg, msgBus, model)
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Collect deltas
	var deltas []string
	var deltaDone = make(chan struct{})
	go func() {
		defer close(deltaDone)
		for {
			msg, ok := msgBus.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			if msg.StreamDelta {
				deltas = append(deltas, msg.Content)
			}
		}
	}()

	response, err := al.ProcessDirectStreaming(ctx, "Use the echo tool (streaming)", "stream-tools-session", "test", "chat-1")
	cancel()
	<-deltaDone

	if err != nil {
		t.Fatalf("ProcessDirectStreaming with tools failed: %v", err)
	}

	if !strings.Contains(response, "Integration test response") {
		t.Errorf("Expected response containing 'Integration test response', got: %s", response)
	}

	// Model should have been called at least twice (tool call step + final response step)
	if model.callCount < 2 {
		t.Errorf("Expected at least 2 model calls, got %d", model.callCount)
	}
}

// TestIntegration_MultipleMessages tests sequential message processing
// to verify session history accumulation.
func TestIntegration_MultipleMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-integration-multi-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("Response")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	sessionKey := "multi-msg-session"
	ctx := context.Background()

	// Send 3 messages
	for i := 0; i < 3; i++ {
		msg := bus.InboundMessage{
			Channel:    "test",
			SenderID:   "user1",
			ChatID:     "chat1",
			Content:    fmt.Sprintf("Message %d", i+1),
			SessionKey: sessionKey,
		}

		_, err := al.processMessage(ctx, msg)
		if err != nil {
			t.Fatalf("processMessage #%d failed: %v", i+1, err)
		}
	}

	// Verify session history grew
	history := al.sessions.GetHistory(sessionKey)
	if len(history) < 6 { // At least 3 user messages + 3 assistant messages
		t.Errorf("Expected at least 6 messages in history, got: %d", len(history))
	}
}
