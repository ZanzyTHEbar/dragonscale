package tools

import (
	"context"
	"strings"
	"testing"
)

func TestToolCallTool_Name(t *testing.T) {
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	if tc.Name() != "tool_call" {
		t.Errorf("expected tool_call, got %s", tc.Name())
	}
}

func TestToolCallTool_Description(t *testing.T) {
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	if tc.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestToolCallTool_Parameters(t *testing.T) {
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	params := tc.Parameters()

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["tool_name"]; !ok {
		t.Error("expected tool_name property")
	}
	if _, ok := props["arguments"]; !ok {
		t.Error("expected arguments property")
	}
}

func TestToolCallTool_MissingToolName(t *testing.T) {
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	result := tc.Execute(context.Background(), map[string]interface{}{})

	if !result.IsError {
		t.Error("expected error for missing tool_name")
	}
}

func TestToolCallTool_DispatchesToTool(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{"path": "/tmp/test.txt"},
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
	if result.ForLLM != "executed read_file" {
		t.Errorf("expected 'executed read_file', got %s", result.ForLLM)
	}
}

func TestToolCallTool_PreventRecursion_ToolCall(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterMetaTools()

	tc, _ := r.Get("tool_call")
	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "tool_call",
	})

	if !result.IsError {
		t.Error("expected error for recursive tool_call")
	}
}

func TestToolCallTool_PreventRecursion_ToolSearch(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterMetaTools()

	tc, _ := r.Get("tool_call")
	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "tool_search",
	})

	if !result.IsError {
		t.Error("expected error for recursive tool_search")
	}
}

func TestToolCallTool_JSONStringArguments(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&echoTool{})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "echo",
		"arguments": `{"msg":"hello"}`,
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
	if result.ForLLM != "hello" {
		t.Errorf("expected 'hello', got %s", result.ForLLM)
	}
}

func TestToolCallTool_NilArguments(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "no_args", desc: "Tool that needs no args"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "no_args",
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
}

func TestToolCallTool_InvalidJSONArguments(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": "not-json",
	})

	if !result.IsError {
		t.Error("expected error for invalid JSON arguments")
	}
	// Should include schema hint
	if !strings.Contains(result.ForLLM, "path") {
		t.Errorf("expected schema hint with 'path' parameter, got: %s", result.ForLLM)
	}
}

func TestToolCallTool_MissingRequiredArgs_IncludesSchemaHint(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{},
	})

	if !result.IsError {
		t.Error("expected error for missing required args")
	}
	if !strings.Contains(result.ForLLM, "missing required") {
		t.Errorf("expected 'missing required' in error, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "path") {
		t.Errorf("expected 'path' in schema hint, got: %s", result.ForLLM)
	}
}

func TestToolCallTool_ToolNotFoundSuggestsSearch(t *testing.T) {
	r := NewToolRegistry()
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "nonexistent",
	})

	if !result.IsError {
		t.Error("expected error for nonexistent tool")
	}
	if !strings.Contains(result.ForLLM, "tool_search") {
		t.Errorf("expected suggestion to use tool_search, got: %s", result.ForLLM)
	}
}

func TestToolCallTool_ContextPropagation(t *testing.T) {
	r := NewToolRegistry()
	ct := &contextCaptureTool{}
	r.Register(ct)

	tc := NewToolCallTool(r)
	tc.SetContext("test-channel", "test-chat")

	tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "capture",
		"arguments": map[string]interface{}{},
	})

	// ToolCallTool dispatches via registry.ExecuteWithContext, which propagates channel/chatID
	if ct.lastChannel != "test-channel" {
		t.Errorf("expected channel propagation, got %s", ct.lastChannel)
	}
}

func TestToolCallTool_ResourceProvider_LoadsResources(t *testing.T) {
	r := NewToolRegistry()
	rt := &resourceAwareTool{
		resources: map[string]string{
			"schema:users": `{"name": "string", "age": "int"}`,
		},
	}
	r.Register(rt)
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "resource_tool",
		"arguments": map[string]interface{}{},
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// The tool should have received resources via context
	if rt.receivedResources == nil {
		t.Fatal("expected resources to be injected via context")
	}
	if rt.receivedResources["schema:users"] == "" {
		t.Error("expected 'schema:users' resource")
	}
}

func TestToolCallTool_NormalizesCommaSeparatedToolName(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "write_file", desc: "write"})
	r.Register(&stubTool{name: "read_file", desc: "read"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "write_file, read_file",
		"arguments": map[string]interface{}{"path": "x.txt"},
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if result.ForLLM != "executed write_file" {
		t.Fatalf("expected normalized dispatch to write_file, got %q", result.ForLLM)
	}
}

func TestToolCallTool_NormalizesEmbeddedToolName(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "exec", desc: "exec"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "exec_tool_search_query_exec_run_shell_command_return_output_caution",
		"arguments": map[string]interface{}{"command": "echo hi"},
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if result.ForLLM != "executed exec" {
		t.Fatalf("expected normalized dispatch to exec, got %q", result.ForLLM)
	}
}

func TestToolCallTool_NoResourceProvider_StillWorks(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "plain", desc: "No resources"})
	tc := NewToolCallTool(r)

	result := tc.Execute(context.Background(), map[string]interface{}{
		"tool_name": "plain",
		"arguments": map[string]interface{}{},
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
}

func TestResourcesFromContext_Empty(t *testing.T) {
	res := ResourcesFromContext(context.Background())
	if res != nil {
		t.Error("expected nil for empty context")
	}
}

// --- test helpers ---

// resourceAwareTool implements both Tool and ResourceProvider
type resourceAwareTool struct {
	resources         map[string]string
	receivedResources map[string]string
}

func (r *resourceAwareTool) Name() string        { return "resource_tool" }
func (r *resourceAwareTool) Description() string { return "Tool with resources" }
func (r *resourceAwareTool) Parameters() map[string]interface{} {
	return map[string]interface{}{}
}
func (r *resourceAwareTool) ResourceKeys() []string {
	keys := make([]string, 0, len(r.resources))
	for k := range r.resources {
		keys = append(keys, k)
	}
	return keys
}
func (r *resourceAwareTool) LoadResources(_ context.Context) (map[string]string, error) {
	return r.resources, nil
}
func (r *resourceAwareTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	r.receivedResources = ResourcesFromContext(ctx)
	return &ToolResult{ForLLM: "ok"}
}

type echoTool struct{}

func (e *echoTool) Name() string        { return "echo" }
func (e *echoTool) Description() string { return "Echo a message" }
func (e *echoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{}
}
func (e *echoTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	msg, _ := args["msg"].(string)
	return &ToolResult{ForLLM: msg}
}

type contextCaptureTool struct {
	lastChannel string
	lastChatID  string
}

func (c *contextCaptureTool) Name() string        { return "capture" }
func (c *contextCaptureTool) Description() string { return "Capture context" }
func (c *contextCaptureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{}
}
func (c *contextCaptureTool) SetContext(channel, chatID string) {
	c.lastChannel = channel
	c.lastChatID = chatID
}
func (c *contextCaptureTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "captured"}
}
