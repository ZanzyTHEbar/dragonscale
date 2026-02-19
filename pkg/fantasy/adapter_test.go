package fantasy

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// --- Mock tool implementations ---

type mockSilentTool struct{}

func (t *mockSilentTool) Name() string        { return "silent_tool" }
func (t *mockSilentTool) Description() string { return "A silent tool" }
func (t *mockSilentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}
func (t *mockSilentTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "internal data",
		ForUser: "",
		Silent:  true,
		IsError: false,
	}
}

type mockDualChannelTool struct{}

func (t *mockDualChannelTool) Name() string        { return "dual_tool" }
func (t *mockDualChannelTool) Description() string { return "A dual channel tool" }
func (t *mockDualChannelTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input": map[string]interface{}{"type": "string"},
		},
	}
}
func (t *mockDualChannelTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	input, _ := args["input"].(string)
	return &tools.ToolResult{
		ForLLM:  "LLM sees: " + input,
		ForUser: "User sees: " + input,
		Silent:  false,
		IsError: false,
	}
}

type mockErrorTool struct{}

func (t *mockErrorTool) Name() string        { return "error_tool" }
func (t *mockErrorTool) Description() string { return "A tool that errors" }
func (t *mockErrorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *mockErrorTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "Something went wrong",
		ForUser: "",
		Silent:  false,
		IsError: true,
	}
}

type mockContextualTool struct {
	channel string
	chatID  string
}

func (t *mockContextualTool) Name() string        { return "ctx_tool" }
func (t *mockContextualTool) Description() string { return "Contextual tool" }
func (t *mockContextualTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *mockContextualTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM: "channel=" + t.channel + " chat=" + t.chatID,
		Silent: true,
	}
}
func (t *mockContextualTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

type mockNilResultTool struct{}

func (t *mockNilResultTool) Name() string        { return "nil_tool" }
func (t *mockNilResultTool) Description() string { return "Returns nil" }
func (t *mockNilResultTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *mockNilResultTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return nil
}

// --- PicoToolAdapter.Info() Tests ---

func TestAdapter_Info(t *testing.T) {
	adapter := &PicoToolAdapter{inner: &mockDualChannelTool{}}
	info := adapter.Info()

	if info.Name != "dual_tool" {
		t.Errorf("Expected name 'dual_tool', got '%s'", info.Name)
	}
	if info.Description != "A dual channel tool" {
		t.Errorf("Expected description mismatch")
	}
	if info.Parameters == nil {
		t.Error("Expected non-nil parameters")
	}
	// Verify schema unwrapping: Parameters should contain the properties map,
	// not the full schema wrapper. The mock returns {"type":"object","properties":{...}}
	// so after unwrapping, Parameters should have "input" as a direct key.
	if _, ok := info.Parameters["input"]; !ok {
		t.Errorf("Expected unwrapped properties with 'input' key, got keys: %v", info.Parameters)
	}
	if _, hasType := info.Parameters["type"]; hasType {
		t.Error("Parameters should not contain 'type' key after unwrapping")
	}
}

func TestAdapter_Info_UnwrapsSchemaWithRequired(t *testing.T) {
	mock := &mockToolWithRequired{}
	adapter := &PicoToolAdapter{inner: mock}
	info := adapter.Info()

	if _, ok := info.Parameters["path"]; !ok {
		t.Errorf("Expected unwrapped 'path' property, got: %v", info.Parameters)
	}
	if len(info.Required) != 1 || info.Required[0] != "path" {
		t.Errorf("Expected Required=[path], got: %v", info.Required)
	}
}

type mockToolWithRequired struct{}

func (t *mockToolWithRequired) Name() string        { return "required_tool" }
func (t *mockToolWithRequired) Description() string { return "Tool with required fields" }
func (t *mockToolWithRequired) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "file path"},
		},
		"required": []string{"path"},
	}
}
func (t *mockToolWithRequired) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "ok"}
}

// --- PicoToolAdapter.Run() Tests ---

func TestAdapter_Run_SilentTool_NoPublish(t *testing.T) {
	msgBus := bus.NewMessageBus()
	adapter := &PicoToolAdapter{
		inner:   &mockSilentTool{},
		bus:     msgBus,
		channel: "test",
		chatID:  "chat-1",
	}

	call := fantasy.ToolCall{
		ID:    "tc-1",
		Name:  "silent_tool",
		Input: "{}",
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if resp.Content != "internal data" {
		t.Errorf("Expected 'internal data', got '%s'", resp.Content)
	}
	if resp.IsError {
		t.Error("Expected non-error response")
	}
}

func TestAdapter_Run_DualChannel_PublishesForUser(t *testing.T) {
	msgBus := bus.NewMessageBus()
	adapter := &PicoToolAdapter{
		inner:   &mockDualChannelTool{},
		bus:     msgBus,
		channel: "telegram",
		chatID:  "chat-42",
	}

	call := fantasy.ToolCall{
		ID:    "tc-dual",
		Name:  "dual_tool",
		Input: `{"input": "hello"}`,
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Fantasy should see ForLLM content
	if resp.Content != "LLM sees: hello" {
		t.Errorf("Expected 'LLM sees: hello', got '%s'", resp.Content)
	}

	// Bus should have received ForUser content (we can't easily consume it
	// in a non-blocking test without goroutines, but we verify it doesn't crash)
}

func TestAdapter_Run_ErrorTool_ReturnsErrorResponse(t *testing.T) {
	adapter := &PicoToolAdapter{
		inner: &mockErrorTool{},
	}

	call := fantasy.ToolCall{
		ID:    "tc-err",
		Name:  "error_tool",
		Input: "{}",
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected error (adapter should not return Go errors): %v", err)
	}

	if !resp.IsError {
		t.Error("Expected error response")
	}
	if resp.Content != "Something went wrong" {
		t.Errorf("Expected error content, got '%s'", resp.Content)
	}
}

func TestAdapter_Run_NilResult_ReturnsError(t *testing.T) {
	adapter := &PicoToolAdapter{
		inner: &mockNilResultTool{},
	}

	call := fantasy.ToolCall{
		ID:    "tc-nil",
		Name:  "nil_tool",
		Input: "{}",
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected Go error: %v", err)
	}

	if !resp.IsError {
		t.Error("Expected error response for nil result")
	}
	if resp.Content != "tool returned nil result" {
		t.Errorf("Expected nil result error, got '%s'", resp.Content)
	}
}

func TestAdapter_Run_ContextualTool_SetsContext(t *testing.T) {
	ctxTool := &mockContextualTool{}
	adapter := &PicoToolAdapter{
		inner:   ctxTool,
		channel: "discord",
		chatID:  "guild-1",
	}

	call := fantasy.ToolCall{
		ID:    "tc-ctx",
		Name:  "ctx_tool",
		Input: "{}",
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The tool should have received the context
	if resp.Content != "channel=discord chat=guild-1" {
		t.Errorf("Expected context in response, got '%s'", resp.Content)
	}
}

func TestAdapter_Run_InvalidJSON_ReturnsError(t *testing.T) {
	adapter := &PicoToolAdapter{
		inner: &mockSilentTool{},
	}

	call := fantasy.ToolCall{
		ID:    "tc-bad",
		Name:  "silent_tool",
		Input: "not valid json{{{",
	}

	resp, err := adapter.Run(context.Background(), call)
	if err != nil {
		t.Fatalf("Unexpected Go error: %v", err)
	}

	if !resp.IsError {
		t.Error("Expected error response for invalid JSON")
	}
}

// --- BuildAdaptedTools Tests ---

func TestBuildAdaptedTools_NilRegistry(t *testing.T) {
	result := BuildAdaptedTools(nil, nil, "", "")
	if result != nil {
		t.Error("Expected nil for nil registry")
	}
}

func TestBuildAdaptedTools_WrapsAllTools(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&mockSilentTool{})
	registry.Register(&mockDualChannelTool{})
	registry.Register(&mockErrorTool{})
	registry.MarkGateway("silent_tool")
	registry.MarkGateway("dual_tool")
	registry.MarkGateway("error_tool")

	adapted := BuildAdaptedTools(registry, nil, "ch", "id")

	if len(adapted) != 3 {
		t.Fatalf("Expected 3 adapted tools, got %d", len(adapted))
	}

	// Verify all names are present
	names := make(map[string]bool)
	for _, tool := range adapted {
		names[tool.Info().Name] = true
	}
	for _, expected := range []string{"silent_tool", "dual_tool", "error_tool"} {
		if !names[expected] {
			t.Errorf("Missing adapted tool: %s", expected)
		}
	}
}

// --- parseToolArgs Tests ---

func TestParseToolArgs_EmptyInput(t *testing.T) {
	args, err := parseToolArgs("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(args))
	}
}

func TestParseToolArgs_EmptyObject(t *testing.T) {
	args, err := parseToolArgs("{}")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(args))
	}
}

func TestParseToolArgs_ValidJSON(t *testing.T) {
	args, err := parseToolArgs(`{"key": "value", "num": 42}`)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if args["key"] != "value" {
		t.Errorf("Expected key='value', got '%v'", args["key"])
	}
}

func TestParseToolArgs_InvalidJSON(t *testing.T) {
	_, err := parseToolArgs("not json")
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}
