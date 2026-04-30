package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateToolArgs(t *testing.T) {
	t.Parallel()

	schema := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "integer"},
			"meta": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"enabled": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"enabled"},
			},
			"tags": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"name"},
	}

	tests := []struct {
		name    string
		schema  map[string]interface{}
		args    map[string]interface{}
		wantErr string
	}{
		{
			name:   "valid args",
			schema: schema,
			args: map[string]interface{}{
				"name": "alice",
				"age":  30,
				"meta": map[string]interface{}{"enabled": true},
				"tags": []string{"a", "b"},
			},
		},
		{
			name:    "missing required",
			schema:  schema,
			args:    map[string]interface{}{"age": 30},
			wantErr: "missing required property \"name\"",
		},
		{
			name:    "wrong type",
			schema:  schema,
			args:    map[string]interface{}{"name": 42},
			wantErr: "expected string",
		},
		{
			name:    "unexpected property rejected",
			schema:  schema,
			args:    map[string]interface{}{"name": "alice", "extra": true},
			wantErr: "unexpected property \"extra\"",
		},
		{
			name: "omitted additionalProperties allows extras",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
			},
			args: map[string]interface{}{"name": "alice", "extra": true},
		},
		{
			name:   "nested object missing field",
			schema: schema,
			args: map[string]interface{}{
				"name": "alice",
				"meta": map[string]interface{}{},
			},
			wantErr: "property \"meta\": missing required property \"enabled\"",
		},
		{
			name: "schema without properties accepts extras",
			schema: map[string]interface{}{
				"type": "object",
			},
			args: map[string]interface{}{"anything": "goes"},
		},
		{
			name: "closed schema without properties rejects extras",
			schema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
			},
			args:    map[string]interface{}{"anything": "goes"},
			wantErr: "unexpected property \"anything\"",
		},
		{
			name: "closed schema without properties accepts empty object",
			schema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
			},
			args: map[string]interface{}{},
		},
		{
			name: "additionalProperties true without properties accepts extras",
			schema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
			},
			args: map[string]interface{}{"anything": "goes"},
		},
		{
			name: "numeric minimum enforced",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"count": map[string]interface{}{"type": "integer", "minimum": 1.0},
				},
			},
			args:    map[string]interface{}{"count": 0},
			wantErr: "below minimum",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolArgs(tc.schema, tc.args)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestToolRegistry_ExecuteWithContext_ValidatesArguments(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	r.Register(&stubToolWithSchema{name: "read_file", desc: "Read a file"})

	result := r.Execute(t.Context(), "read_file", map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected validation error for missing required field")
	}
	var validationErr *toolArgValidationError
	if !errors.As(result.Err, &validationErr) {
		t.Fatalf("expected toolArgValidationError, got %T", result.Err)
	}
	if validationErr.cause.Error() != "missing required property \"path\"" {
		t.Fatalf("unexpected validation cause: %v", validationErr.cause)
	}
	if result.ForLLM != `invalid arguments for tool "read_file": missing required property "path"` {
		t.Fatalf("unexpected result message: %q", result.ForLLM)
	}

	result = r.Execute(t.Context(), "read_file", map[string]interface{}{"path": "/tmp/x.txt"})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
}

func TestToolRegistry_ExecuteWithContext_AllowsInjectedArgKeys(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	r.Register(&injectedArgTool{})

	ctx := WithInjectedArgKeys(t.Context(), "token")
	result := r.ExecuteWithContext(ctx, "injected_arg", map[string]interface{}{
		"path":  "/tmp/x.txt",
		"token": "secret",
	}, "", "", nil)
	if result.IsError {
		t.Fatalf("expected injected arg allowlist to pass validation, got: %s", result.ForLLM)
	}

	result = r.ExecuteWithContext(t.Context(), "injected_arg", map[string]interface{}{
		"path":  "/tmp/x.txt",
		"token": "secret",
	}, "", "", nil)
	if !result.IsError {
		t.Fatal("expected injected arg without allowlist to fail validation")
	}
}

func TestToolRegistry_ExecuteWithContext_InjectedArgDoesNotOpenNestedExtras(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	r.Register(&nestedInjectedArgTool{})

	ctx := WithInjectedArgKeys(t.Context(), "token")
	result := r.ExecuteWithContext(ctx, "nested_injected_arg", map[string]interface{}{
		"payload": map[string]interface{}{
			"name":  "ok",
			"token": "should-not-be-allowed-here",
		},
	}, "", "", nil)
	if !result.IsError {
		t.Fatal("expected nested token property to remain invalid")
	}
	if !strings.Contains(result.ForLLM, `unexpected property "token"`) {
		t.Fatalf("unexpected result: %s", result.ForLLM)
	}
}

type injectedArgTool struct{}

func (t *injectedArgTool) Name() string        { return "injected_arg" }
func (t *injectedArgTool) Description() string { return "Tool with injected args" }
func (t *injectedArgTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
		"required": []string{"path"},
	}
}
func (t *injectedArgTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	return SilentResult(args["path"].(string))
}

type nestedInjectedArgTool struct{}

func (t *nestedInjectedArgTool) Name() string        { return "nested_injected_arg" }
func (t *nestedInjectedArgTool) Description() string { return "Tool with nested schema" }
func (t *nestedInjectedArgTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"payload": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name"},
			},
		},
		"required": []string{"payload"},
	}
}
func (t *nestedInjectedArgTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return SilentResult("ok")
}
