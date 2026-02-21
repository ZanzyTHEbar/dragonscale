package fantasy

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Example of a simple typed tool using the function approach
type CalculatorInput struct {
	Expression string `json:"expression" description:"Mathematical expression to evaluate"`
}

func TestTypedToolFuncExample(t *testing.T) {
	t.Parallel()

	tool := NewAgentTool(
		"calculator",
		"Evaluates simple mathematical expressions",
		func(ctx context.Context, input CalculatorInput, _ ToolCall) (ToolResponse, error) {
			if input.Expression == "2+2" {
				return NewTextResponse("4"), nil
			}
			return NewTextErrorResponse("unsupported expression"), nil
		},
	)

	// Check the tool info
	info := tool.Info()
	assert.Empty(t, cmp.Diff("calculator", info.Name))
	require.Len(t, info.Required, 1)
	assert.Empty(t, cmp.Diff("expression", info.Required[0]))

	// Test execution
	call := ToolCall{
		ID:    "test-1",
		Name:  "calculator",
		Input: `{"expression": "2+2"}`,
	}

	result, err := tool.Run(t.Context(), call)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("4", result.Content))
	require.False(t, result.IsError)
}

func TestEnumToolExample(t *testing.T) {
	t.Parallel()
	type WeatherInput struct {
		Location string `json:"location" description:"City name"`
		Units    string `json:"units" enum:"celsius,fahrenheit" description:"Temperature units"`
	}

	// Create a weather tool with enum support
	tool := NewAgentTool(
		"weather",
		"Gets current weather for a location",
		func(ctx context.Context, input WeatherInput, _ ToolCall) (ToolResponse, error) {
			temp := "22°C"
			if input.Units == "fahrenheit" {
				temp = "72°F"
			}
			return NewTextResponse(fmt.Sprintf("Weather in %s: %s, sunny", input.Location, temp)), nil
		},
	)
	// Check that the schema includes enum values
	info := tool.Info()
	unitsParam, ok := info.Parameters["units"].(map[string]any)
	require.True(t, ok, "Expected units parameter to exist")
	enumValues, ok := unitsParam["enum"].([]any)
	require.True(t, ok)
	require.Len(t, enumValues, 2)

	// Test execution with enum value
	call := ToolCall{
		ID:    "test-2",
		Name:  "weather",
		Input: `{"location": "San Francisco", "units": "fahrenheit"}`,
	}

	result, err := tool.Run(t.Context(), call)
	require.NoError(t, err)
	require.Contains(t, result.Content, "San Francisco")
	require.Contains(t, result.Content, "72°F")
}

func TestNewMediaResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mediaType string
		data      []byte
		wantType  string
	}{
		{
			name:      "image response",
			data:      []byte{0x89, 0x50, 0x4E, 0x47},
			mediaType: "image/png",
			wantType:  "image",
		},
		{
			name:      "audio response",
			data:      []byte{0x52, 0x49, 0x46, 0x46},
			mediaType: "audio/wav",
			wantType:  "media",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got ToolResponse
			if tt.wantType == "image" {
				got = NewImageResponse(tt.data, tt.mediaType)
			} else {
				got = NewMediaResponse(tt.data, tt.mediaType)
			}

			require.Empty(t, cmp.Diff(ToolResponse{
				Type:      tt.wantType,
				Data:      tt.data,
				MediaType: tt.mediaType,
				IsError:   false,
				Content:   "",
			}, got))
		})
	}
}
