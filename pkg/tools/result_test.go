package tools

import (
	"errors"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
)

func TestToolResultConstructors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  *ToolResult
		want ToolResult
	}{
		{
			name: "new result",
			got:  NewToolResult("basic content"),
			want: ToolResult{ForLLM: "basic content"},
		},
		{
			name: "silent result",
			got:  SilentResult("silent content"),
			want: ToolResult{ForLLM: "silent content", Silent: true},
		},
		{
			name: "async result",
			got:  AsyncResult("async content"),
			want: ToolResult{ForLLM: "async content", Async: true},
		},
		{
			name: "error result",
			got:  ErrorResult("error content"),
			want: ToolResult{ForLLM: "error content", IsError: true},
		},
		{
			name: "user result",
			got:  UserResult("user visible content"),
			want: ToolResult{ForLLM: "user visible content", ForUser: "user visible content"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, cmp.Diff(tt.want, *tt.got, cmpopts.IgnoreFields(ToolResult{}, "Err")))
		})
	}
}

func TestToolResultJSONSerialization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result *ToolResult
	}{
		{
			name:   "basic result",
			result: NewToolResult("basic content"),
		},
		{
			name:   "silent result",
			result: SilentResult("silent content"),
		},
		{
			name:   "async result",
			result: AsyncResult("async content"),
		},
		{
			name:   "error result",
			result: ErrorResult("error content"),
		},
		{
			name:   "user result",
			result: UserResult("user content"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := jsonv2.Marshal(tt.result)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Unmarshal back
			var decoded ToolResult
			if err := jsonv2.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Verify fields match (Err should be excluded)
			if diff := cmp.Diff(*tt.result, decoded, cmpopts.IgnoreFields(ToolResult{}, "Err")); diff != "" {
				t.Errorf("ToolResult mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestToolResultWithErrors(t *testing.T) {
	t.Parallel()
	err := errors.New("underlying error")
	result := ErrorResult("error message").WithError(err)

	if result.Err == nil {
		t.Error("Expected Err to be set")
	}
	if result.Err.Error() != "underlying error" {
		t.Errorf("Expected Err message 'underlying error', got '%s'", result.Err.Error())
	}

	// Verify Err is not serialized
	data, marshalErr := jsonv2.Marshal(result)
	if marshalErr != nil {
		t.Fatalf("Failed to marshal: %v", marshalErr)
	}

	var decoded ToolResult
	if unmarshalErr := jsonv2.Unmarshal(data, &decoded); unmarshalErr != nil {
		t.Fatalf("Failed to unmarshal: %v", unmarshalErr)
	}

	if decoded.Err != nil {
		t.Error("Expected Err to be nil after JSON round-trip (should not be serialized)")
	}
}

func TestToolResultJSONStructure(t *testing.T) {
	t.Parallel()
	result := UserResult("test content")

	data, err := jsonv2.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Verify JSON structure
	var parsed map[string]interface{}
	if err := jsonv2.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Check expected keys exist
	if _, ok := parsed["for_llm"]; !ok {
		t.Error("Expected 'for_llm' key in JSON")
	}
	if _, ok := parsed["for_user"]; !ok {
		t.Error("Expected 'for_user' key in JSON")
	}
	if _, ok := parsed["silent"]; !ok {
		t.Error("Expected 'silent' key in JSON")
	}
	if _, ok := parsed["is_error"]; !ok {
		t.Error("Expected 'is_error' key in JSON")
	}
	if _, ok := parsed["async"]; !ok {
		t.Error("Expected 'async' key in JSON")
	}

	// Check that 'err' is NOT present (it should have json:"-" tag)
	if _, ok := parsed["err"]; ok {
		t.Error("Expected 'err' key to be excluded from JSON")
	}

	// Verify values
	if parsed["for_llm"] != "test content" {
		t.Errorf("Expected for_llm 'test content', got %v", parsed["for_llm"])
	}
	if parsed["silent"] != false {
		t.Errorf("Expected silent false, got %v", parsed["silent"])
	}
}
