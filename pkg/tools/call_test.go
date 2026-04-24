package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToolCallTool_Name(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	if tc.Name() != "tool_call" {
		t.Errorf("expected tool_call, got %s", tc.Name())
	}
}

func TestToolCallTool_Description(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	if tc.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestToolCallTool_Parameters(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	r := NewToolRegistry()
	tc := NewToolCallTool(r)
	result := tc.Execute(t.Context(), map[string]interface{}{})

	if !result.IsError {
		t.Error("expected error for missing tool_name")
	}
}

func TestToolCallTool_DispatchesToTool(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.RegisterMetaTools()

	tc, _ := r.Get("tool_call")
	result := tc.Execute(t.Context(), map[string]interface{}{
		"tool_name": "tool_call",
	})

	if !result.IsError {
		t.Error("expected error for recursive tool_call")
	}
}

func TestToolCallTool_PreventRecursion_ToolSearch(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.RegisterMetaTools()

	tc, _ := r.Get("tool_call")
	result := tc.Execute(t.Context(), map[string]interface{}{
		"tool_name": "tool_search",
	})

	if !result.IsError {
		t.Error("expected error for recursive tool_search")
	}
}

func TestToolCallTool_JSONStringArguments(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&echoTool{})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "no_args", desc: "Tool that needs no args"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
		"tool_name": "no_args",
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
}

func TestToolCallTool_InvalidJSONArguments(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	ct := &contextCaptureTool{}
	r.Register(ct)

	tc := NewToolCallTool(r)
	ctx := WithExecutionTarget(t.Context(), "test-channel", "test-chat")

	tc.Execute(ctx, map[string]interface{}{
		"tool_name": "capture",
		"arguments": map[string]interface{}{},
	})

	// ToolCallTool dispatches via registry.ExecuteWithContext, which propagates channel/chatID
	if ct.lastChannel != "test-channel" {
		t.Errorf("expected channel propagation, got %s", ct.lastChannel)
	}
}

func TestToolCallTool_ForwardsAsyncCallbackAndExecutionTarget(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	asyncTool := &callbackCaptureTool{}
	r.Register(asyncTool)
	tc := NewToolCallTool(r)

	ctx := WithExecutionTarget(t.Context(), "telegram", "chat-77")
	callbackDone := make(chan *ToolResult, 1)
	ctx = WithAsyncCallback(ctx, func(_ context.Context, result *ToolResult) {
		callbackDone <- result
	})

	result := tc.Execute(ctx, map[string]interface{}{
		"tool_name": "spawn",
		"arguments": map[string]interface{}{"task": "background"},
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !result.Async {
		t.Fatal("expected async result from nested spawn tool")
	}

	select {
	case callbackResult := <-callbackDone:
		if callbackResult == nil {
			t.Fatal("expected callback result")
		}
		if callbackResult.ForUser != "async completion on telegram:chat-77" {
			t.Fatalf("unexpected callback result: %s", callbackResult.ForUser)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async callback")
	}

	if asyncTool.lastChannel != "telegram" || asyncTool.lastChatID != "chat-77" {
		t.Fatalf("expected execution target propagation, got %s:%s", asyncTool.lastChannel, asyncTool.lastChatID)
	}
}

func TestToolCallTool_LegacyAsyncToolStillReceivesCallback(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	legacyTool := &legacyAsyncCallbackTool{}
	r.Register(legacyTool)
	tc := NewToolCallTool(r)

	callbackDone := make(chan *ToolResult, 1)
	ctx := WithAsyncCallback(t.Context(), func(_ context.Context, result *ToolResult) {
		callbackDone <- result
	})

	result := tc.Execute(ctx, map[string]interface{}{
		"tool_name": "legacy_async",
		"arguments": map[string]interface{}{},
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !result.Async {
		t.Fatal("expected async result from legacy async tool")
	}

	select {
	case callbackResult := <-callbackDone:
		if callbackResult == nil || callbackResult.ForUser != "legacy completion" {
			t.Fatalf("unexpected legacy callback result: %#v", callbackResult)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for legacy async callback")
	}
}

func TestToolCallTool_ResourceProvider_LoadsResources(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	rt := &resourceAwareTool{
		resources: map[string]string{
			"schema:users": `{"name": "string", "age": "int"}`,
		},
	}
	r.Register(rt)
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "write_file", desc: "write"})
	r.Register(&stubTool{name: "read_file", desc: "read"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "exec", desc: "exec"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "plain", desc: "No resources"})
	tc := NewToolCallTool(r)

	result := tc.Execute(t.Context(), map[string]interface{}{
		"tool_name": "plain",
		"arguments": map[string]interface{}{},
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
}

func TestResourcesFromContext_Empty(t *testing.T) {
	t.Parallel()
	res := ResourcesFromContext(t.Context())
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
func (c *contextCaptureTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	if channel, chatID := ExecutionTargetFromContext(ctx); channel != "" || chatID != "" {
		c.lastChannel = channel
		c.lastChatID = chatID
	}
	return &ToolResult{ForLLM: "captured"}
}

type callbackCaptureTool struct {
	lastChannel string
	lastChatID  string
}

func (c *callbackCaptureTool) Name() string        { return "spawn" }
func (c *callbackCaptureTool) Description() string { return "captures async callback propagation" }
func (c *callbackCaptureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (c *callbackCaptureTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	c.lastChannel, c.lastChatID = ExecutionTargetFromContext(ctx)
	if callback := AsyncCallbackFromContext(ctx); callback != nil {
		callback(ctx, &ToolResult{ForLLM: "done", ForUser: fmt.Sprintf("async completion on %s:%s", c.lastChannel, c.lastChatID)})
	}
	return AsyncResult("spawned")
}

type legacyAsyncCallbackTool struct {
	callback AsyncCallback
}

func (t *legacyAsyncCallbackTool) Name() string        { return "legacy_async" }
func (t *legacyAsyncCallbackTool) Description() string { return "legacy async callback tool" }
func (t *legacyAsyncCallbackTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *legacyAsyncCallbackTool) SetCallback(cb AsyncCallback) { t.callback = cb }
func (t *legacyAsyncCallbackTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	if t.callback != nil {
		go t.callback(ctx, &ToolResult{ForLLM: "legacy completion", ForUser: "legacy completion"})
	}
	return AsyncResult("legacy started")
}
