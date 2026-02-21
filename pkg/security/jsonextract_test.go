package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractJSON_RawJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal interface{}
	}{
		{"clean object", `{"importance": 0.8}`, "importance", 0.8},
		{"with whitespace", `  {"key": "val"}  `, "key", "val"},
		{"nested", `{"outer": {"inner": true}}`, "outer", map[string]interface{}{"inner": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result map[string]interface{}
			err := ExtractJSON(tc.input, &result, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVal, result[tc.wantKey])
		})
	}
}

func TestExtractJSON_CodeFence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{
			"json fence",
			"Here is the result:\n```json\n{\"score\": 42}\n```\n",
		},
		{
			"plain fence",
			"```\n{\"score\": 42}\n```",
		},
		{
			"fence with prose before and after",
			"I analyzed the data.\n```json\n{\"score\": 42}\n```\nDone!",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result map[string]interface{}
			err := ExtractJSON(tc.input, &result, nil)
			require.NoError(t, err)
			assert.Equal(t, float64(42), result["score"])
		})
	}
}

func TestExtractJSON_EmbeddedInProse(t *testing.T) {
	t.Parallel()
	input := `Based on my analysis, the result is {"importance": 0.9, "sector": "semantic"} which indicates high relevance.`
	var result struct {
		Importance float64 `json:"importance"`
		Sector     string  `json:"sector"`
	}
	err := ExtractJSON(input, &result, nil)
	require.NoError(t, err)
	assert.Equal(t, 0.9, result.Importance)
	assert.Equal(t, "semantic", result.Sector)
}

func TestExtractJSON_NestedBracesInStrings(t *testing.T) {
	t.Parallel()
	input := `{"content": "function() { return {}; }", "count": 1}`
	var result map[string]interface{}
	err := ExtractJSON(input, &result, nil)
	require.NoError(t, err)
	assert.Equal(t, "function() { return {}; }", result["content"])
	assert.Equal(t, float64(1), result["count"])
}

func TestExtractJSON_InjectionAttempts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		opts  *ExtractJSONOptions
		check func(t *testing.T, err error)
	}{
		{
			"oversized input",
			strings.Repeat("x", 100*1024),
			nil,
			func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrInputTooLarge)
			},
		},
		{
			"no json at all",
			"This is just prose with no JSON.",
			nil,
			func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrNoJSON)
			},
		},
		{
			"unknown fields rejected",
			`{"importance": 0.5, "injected_key": "malicious"}`,
			&ExtractJSONOptions{DisallowUnknownFields: true},
			func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unknown object member name")
			},
		},
		{
			"custom size limit",
			`{"key": "val"}`,
			&ExtractJSONOptions{MaxInputBytes: 5},
			func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrInputTooLarge)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result struct {
				Importance float64 `json:"importance"`
			}
			err := ExtractJSON(tc.input, &result, tc.opts)
			tc.check(t, err)
		})
	}
}

func TestExtractJSON_EmptyInput(t *testing.T) {
	t.Parallel()
	var result map[string]interface{}
	err := ExtractJSON("", &result, nil)
	assert.ErrorIs(t, err, ErrNoJSON)
}

func TestExtractJSON_MultipleFences_TakesFirst(t *testing.T) {
	t.Parallel()
	input := "```json\n{\"first\": true}\n```\nmore text\n```json\n{\"second\": true}\n```"
	var result map[string]interface{}
	err := ExtractJSON(input, &result, nil)
	require.NoError(t, err)
	assert.Equal(t, true, result["first"])
	_, hasSecond := result["second"]
	assert.False(t, hasSecond)
}

func TestSanitizeToolArgs_ValidInput(t *testing.T) {
	t.Parallel()
	schema := map[string]ArgSpec{
		"path":    {Type: ArgString, Required: true, MaxLength: 256},
		"content": {Type: ArgString, Required: true},
		"mode":    {Type: ArgString, Required: false, Default: "overwrite"},
	}

	args := map[string]interface{}{
		"path":    "/tmp/test.txt",
		"content": "hello world",
	}

	result, err := SanitizeToolArgs(args, schema)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test.txt", result["path"])
	assert.Equal(t, "hello world", result["content"])
	assert.Equal(t, "overwrite", result["mode"])
}

func TestSanitizeToolArgs_MissingRequired(t *testing.T) {
	t.Parallel()
	schema := map[string]ArgSpec{
		"path": {Type: ArgString, Required: true},
	}

	_, err := SanitizeToolArgs(map[string]interface{}{}, schema)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing required argument")
}

func TestSanitizeToolArgs_ExceedsMaxLength(t *testing.T) {
	t.Parallel()
	schema := map[string]ArgSpec{
		"cmd": {Type: ArgString, Required: true, MaxLength: 10},
	}

	_, err := SanitizeToolArgs(map[string]interface{}{"cmd": "very long command string"}, schema)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max length")
}

func TestSanitizeToolArgs_TypeCoercion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		argType  ArgType
		input    interface{}
		expected interface{}
		wantErr  bool
	}{
		{"string from string", ArgString, "hello", "hello", false},
		{"string from float", ArgString, float64(42), "42", false},
		{"int from float", ArgInt, float64(42), 42, false},
		{"float from int", ArgFloat, 42, float64(42), false},
		{"bool valid", ArgBool, true, true, false},
		{"bool invalid", ArgBool, "true", nil, true},
		{"object valid", ArgObject, map[string]interface{}{"k": "v"}, map[string]interface{}{"k": "v"}, false},
		{"array valid", ArgArray, []interface{}{"a"}, []interface{}{"a"}, false},
		{"array invalid", ArgArray, "not array", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]ArgSpec{
				"arg": {Type: tc.argType, Required: true},
			}
			result, err := SanitizeToolArgs(map[string]interface{}{"arg": tc.input}, schema)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result["arg"])
			}
		})
	}
}

func TestExtractFirstBraced_EscapedQuotes(t *testing.T) {
	t.Parallel()
	input := `{"msg": "say \"hello\" world"}`
	result := extractFirstBraced(input)
	assert.Equal(t, input, result)
}

func TestExtractFirstBraced_UnbalancedBraces(t *testing.T) {
	t.Parallel()
	input := `text { not closed`
	result := extractFirstBraced(input)
	assert.Empty(t, result)
}
