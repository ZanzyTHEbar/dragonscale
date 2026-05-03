package securebus_test

import (
	"context"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// staticTool always returns a fixed result.
type staticTool struct {
	name   string
	result string
	isErr  bool
}

func (s *staticTool) Name() string        { return s.name }
func (s *staticTool) Description() string { return "static test tool" }
func (s *staticTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (s *staticTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	r := &tools.ToolResult{ForLLM: s.result, IsError: s.isErr}
	return r
}

// echoTool returns the value of the "input" arg.
type echoTool struct{}

func (e *echoTool) Name() string        { return "echo" }
func (e *echoTool) Description() string { return "echo" }
func (e *echoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (e *echoTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	v, _ := args["input"].(string)
	return &tools.ToolResult{ForLLM: v}
}

type echoValidatedTool struct{}

func (e *echoValidatedTool) Name() string        { return "echo_validated" }
func (e *echoValidatedTool) Description() string { return "echo with validated args" }
func (e *echoValidatedTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"label": map[string]interface{}{"type": "string"},
		},
		"required": []string{"label"},
	}
}
func (e *echoValidatedTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	label, _ := args["label"].(string)
	input, _ := args["input"].(string)
	return &tools.ToolResult{ForLLM: label + ":" + input}
}

func makeArgsJSON(kv map[string]interface{}) string {
	if kv == nil {
		return "{}"
	}
	b, _ := jsonv2.Marshal(kv)
	return string(b)
}

func makeBus(t *testing.T, toolMap map[string]tools.Tool, secrets *security.SecretStore) *securebus.Bus {
	t.Helper()
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		tool, ok := toolMap[name]
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		tool, ok := toolMap[name]
		if !ok {
			return &tools.ToolResult{ForLLM: "tool not found: " + name, IsError: true}
		}
		return tool.Execute(ctx, args)
	}
	cfg := securebus.DefaultBusConfig()
	return securebus.New(cfg, secrets, capLookup, executor)
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestBus_SuccessfulToolExec(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "greet", result: "hello world"}
	bus := makeBus(t, map[string]tools.Tool{"greet": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-1", "sess", "tc-1", "greet", makeArgsJSON(nil))
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Empty(t, cmp.Diff("hello world", resp.Result))
	assert.Empty(t, cmp.Diff(1, bus.AuditLog().Len()))
	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.Empty(t, events[0].ExecutionError)
}

func TestBus_UnknownTool(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, map[string]tools.Tool{}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-2", "sess", "tc-2", "nonexistent", makeArgsJSON(nil))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
}

func TestBus_ToolReturnsError(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "fail", result: "something broke", isErr: true}
	bus := makeBus(t, map[string]tools.Tool{"fail": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-3", "sess", "tc-3", "fail", makeArgsJSON(nil))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.Empty(t, cmp.Diff(1, bus.AuditLog().Len()))
	events := bus.AuditLog().Events()
	assert.True(t, events[0].IsError)
}

func TestBus_LeakDetection(t *testing.T) {
	t.Parallel(
	// Tool output contains an API key — should be redacted.
	)

	apiKey := "AKIAIOSFODNN7EXAMPLE" // fake AWS key matching redactor pattern
	tool := &staticTool{name: "leaky", result: "result: " + apiKey}
	bus := makeBus(t, map[string]tools.Tool{"leaky": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-4", "sess", "tc-4", "leaky", makeArgsJSON(nil))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.LeakDetected, "should detect API key in output")
	assert.False(t, resp.IsError)
	assert.NotContains(t, resp.Result, apiKey, "raw API key must not appear in response")

	leakEvents := bus.AuditLog().LeakEvents()
	assert.Len(t, leakEvents, 1)
	assert.True(t, leakEvents[0].LeakDetected)
	assert.False(t, leakEvents[0].IsError)
	assert.NotEmpty(t, leakEvents[0].ExecutionError)
	assert.Contains(t, leakEvents[0].ExecutionError, "[REDACTED:AWS_ACCESS_KEY]")
	assert.NotContains(t, leakEvents[0].ExecutionError, apiKey)
}

func TestBus_LeakDetection_RedactsAuditInputAndError(t *testing.T) {
	t.Parallel()

	apiKey := "AKIAIOSFODNN7EXAMPLE"
	tool := &staticTool{name: "leaky_error", result: "failed with " + apiKey, isErr: true}
	bus := makeBus(t, map[string]tools.Tool{"leaky_error": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-leak-error", "sess", "tc-leak-error", "leaky_error", makeArgsJSON(map[string]interface{}{
		"token": apiKey,
	}))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.True(t, resp.LeakDetected)
	assert.NotContains(t, resp.Result, apiKey)

	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.NotContains(t, events[0].ToolInput, apiKey)
	assert.NotContains(t, events[0].ExecutionError, apiKey)
	assert.Contains(t, events[0].ExecutionError, "[REDACTED:AWS_ACCESS_KEY]")
	assert.True(t, events[0].LeakDetected)
}

func TestBus_SecretInjection_ArgVariant(t *testing.T) {
	t.Parallel(
	// Tool reads injected "token" arg from args map.
	)

	echoT := &echoTool{}

	// Give echo tool a capability that declares a secret injected as arg:input.
	type capEchoTool struct {
		echoTool
	}
	capTool := &struct {
		echoTool
	}{}
	_ = capTool

	// Use a capTool wrapper that adds arg injection capability.
	type wrappedEcho struct {
		*echoTool
	}
	toolMap := map[string]tools.Tool{"echo": echoT}

	// Seed a secret store.
	key, _ := security.GenerateKey()
	keyring := security.NewNoopKeyring(key)
	ss, err := security.NewSecretStore(t.TempDir()+"/secrets.json", keyring)
	require.NoError(t, err)
	require.NoError(t, ss.Set("my_token", []byte("supersecret")))

	// Override capLookup to inject via arg:input.
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "echo" {
			return tools.ToolCapabilities{
				Secrets: []tools.SecretRef{
					{Name: "my_token", InjectAs: "arg:input", Required: true},
				},
			}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return toolMap[name].Execute(ctx, args)
	}

	bus := securebus.New(securebus.DefaultBusConfig(), ss, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-5", "sess", "tc-5", "echo", makeArgsJSON(nil))
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Empty(t, cmp.Diff("supersecret", resp.Result), "injected secret should appear in tool output")

	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.Contains(t, events[0].SecretsAccessed, "my_token")
}

func TestBus_SecretInjection_ArgVariant_WithRegistryValidation(t *testing.T) {
	t.Parallel(
	// SecureBus injects an arg before dispatch; registry validation must allow it.
	)

	registry := tools.NewToolRegistry()
	registry.Register(&echoValidatedTool{})

	key, _ := security.GenerateKey()
	keyring := security.NewNoopKeyring(key)
	ss, err := security.NewSecretStore(t.TempDir()+"/secrets.json", keyring)
	require.NoError(t, err)
	require.NoError(t, ss.Set("my_token", []byte("supersecret")))

	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "echo_validated" {
			return tools.ToolCapabilities{
				Secrets: []tools.SecretRef{
					{Name: "my_token", InjectAs: "arg:input", Required: true},
				},
			}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return registry.ExecuteWithContext(ctx, name, args, "", "", nil)
	}

	bus := securebus.New(securebus.DefaultBusConfig(), ss, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-5b", "sess", "tc-5b", "echo_validated", makeArgsJSON(map[string]interface{}{
		"label": "hello",
	}))
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Empty(t, cmp.Diff("hello:supersecret", resp.Result))

	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.Contains(t, events[0].SecretsAccessed, "my_token")
}

func TestBus_SecretInjection_OptionalArgVariant_DoesNotTrustMissingSecretKey(t *testing.T) {
	t.Parallel()

	registry := tools.NewToolRegistry()
	registry.Register(&echoValidatedTool{})

	key, _ := security.GenerateKey()
	keyring := security.NewNoopKeyring(key)
	ss, err := security.NewSecretStore(t.TempDir()+"/secrets.json", keyring)
	require.NoError(t, err)

	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "echo_validated" {
			return tools.ToolCapabilities{
				Secrets: []tools.SecretRef{
					{Name: "optional_token", InjectAs: "arg:input", Required: false},
				},
			}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return registry.ExecuteWithContext(ctx, name, args, "", "", nil)
	}

	bus := securebus.New(securebus.DefaultBusConfig(), ss, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-5c", "sess", "tc-5c", "echo_validated", makeArgsJSON(map[string]interface{}{
		"label": "hello",
		"input": "user-supplied",
	}))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, `unexpected property "input"`)

	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.Empty(t, events[0].SecretsAccessed)
}

func TestBus_PolicyViolation_RecursionDepth(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "ok", result: "fine"}
	bus := makeBus(t, map[string]tools.Tool{"ok": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-6", "sess", "tc-6", "ok", makeArgsJSON(nil))
	req.Depth = 255 // far exceeds MaxRecursionDepth=10

	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError, "depth violation should produce an error response")
	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].PolicyViolation)
}

func TestBus_PolicyViolation_NetworkTargetBeforeExecution(t *testing.T) {
	t.Parallel()
	called := false
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "web_fetch" {
			return tools.ToolCapabilities{Network: []tools.EndpointRule{{Pattern: "http://**"}, {Pattern: "https://**"}}}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		called = true
		return &tools.ToolResult{ForLLM: "should not run"}
	}
	bus := securebus.New(securebus.DefaultBusConfig(), nil, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-net-policy", "sess", "tc-net-policy", "web_fetch", makeArgsJSON(map[string]interface{}{
		"url": "http://127.0.0.1:8080/internal",
	}))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.False(t, called, "policy violation should happen before tool execution")
	assert.Contains(t, resp.Result, "policy violation")
	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].PolicyViolation)
}

func TestBus_PolicyViolation_FilesystemTargetBeforeExecution(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	outside := t.TempDir() + "/secret.txt"
	called := false
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "read_file" {
			return tools.ToolCapabilities{Filesystem: []tools.PathRule{{Pattern: "**", Mode: "r"}}}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		called = true
		return &tools.ToolResult{ForLLM: "should not run"}
	}
	cfg := securebus.DefaultBusConfig()
	cfg.Policy.AllowedWorkspace = workspace
	bus := securebus.New(cfg, nil, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-fs-policy", "sess", "tc-fs-policy", "read_file", makeArgsJSON(map[string]interface{}{
		"path": outside,
	}))
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.False(t, called, "policy violation should happen before tool execution")
	assert.Contains(t, resp.Result, "outside allowed workspace")
	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].PolicyViolation)
}

func TestBus_AllowsRelativeFilesystemTargetInsideWorkspace(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	called := false
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "read_file" {
			return tools.ToolCapabilities{Filesystem: []tools.PathRule{{Pattern: "**", Mode: "r"}}}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		called = true
		return &tools.ToolResult{ForLLM: "ok"}
	}
	cfg := securebus.DefaultBusConfig()
	cfg.Policy.AllowedWorkspace = workspace
	bus := securebus.New(cfg, nil, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-fs-allowed", "sess", "tc-fs-allowed", "read_file", makeArgsJSON(map[string]interface{}{
		"path": "docs/readme.md",
	}))
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.True(t, called)
	assert.Equal(t, "ok", resp.Result)
}

func TestBus_AuditLog_FilterBySession(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "t", result: "ok"}
	bus := makeBus(t, map[string]tools.Tool{"t": tool}, nil)
	defer bus.Close()

	for _, sk := range []string{"session-A", "session-A", "session-B"} {
		req := itr.NewToolExecRequest("req-audit-"+sk, sk, "tc", "t", makeArgsJSON(nil))
		bus.Execute(t.Context(), req)
	}

	assert.Empty(t, cmp.Diff(3, bus.AuditLog().Len()))
	assert.Len(t, bus.AuditLog().FilterBySession("session-A"), 2)
	assert.Len(t, bus.AuditLog().FilterBySession("session-B"), 1)
}

func TestBus_Transport_Send(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "ping", result: "pong"}
	bus := makeBus(t, map[string]tools.Tool{"ping": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-tr", "sess", "tc", "ping", makeArgsJSON(nil))
	resp, err := bus.Transport().Send(t.Context(), req)

	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("pong", resp.Result))
}

func TestBus_InvalidArgsJSON(t *testing.T) {
	t.Parallel()
	tool := &staticTool{name: "ok", result: "ok"}
	bus := makeBus(t, map[string]tools.Tool{"ok": tool}, nil)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-bad", "sess", "tc", "ok", "{invalid json")
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
}

func TestBus_RLMFinalCommand(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, nil, nil)
	defer bus.Close()

	req := itr.NewFinalRequest("req-final", "sess", 0, "the answer", "")
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Empty(t, cmp.Diff("the answer", resp.Result))
}

func TestBus_CloseIdempotent(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, nil, nil)

	assert.NotPanics(t, func() {
		bus.Close()
		bus.Close()
		bus.Close()
	}, "Close() must be safe to call multiple times")
}

func TestBus_CloseRejectsTransportSend(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, nil, nil)

	bus.Close()
	_, err := bus.Transport().Send(t.Context(), itr.NewFinalRequest("req-closed", "sess", 0, "done", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transport closed")
}

func TestBus_ToolSearch(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, nil, nil)
	defer bus.Close()

	bus.SetToolSearch(func(query string, maxResults int) string {
		return `[{"name":"read_file","description":"reads a file"}]`
	})

	req := itr.NewToolSearchRequest("req-search", "sess", "file operations", 5)
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Result, "read_file")
}

func TestBus_ToolSearchNotConfigured(t *testing.T) {
	t.Parallel()
	bus := makeBus(t, nil, nil)
	defer bus.Close()

	req := itr.NewToolSearchRequest("req-search2", "sess", "anything", 5)
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Result, "not configured")
}

func TestBus_NilToolResult(t *testing.T) {
	t.Parallel()
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return nil
	}
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		return tools.ZeroCapabilities(), true
	}
	bus := securebus.New(securebus.DefaultBusConfig(), nil, capLookup, executor)
	defer bus.Close()

	req := itr.NewToolExecRequest("req-nil", "sess", "tc", "something", "{}")
	resp := bus.Execute(t.Context(), req)

	assert.False(t, resp.IsError)
	assert.Empty(t, resp.Result)
}
