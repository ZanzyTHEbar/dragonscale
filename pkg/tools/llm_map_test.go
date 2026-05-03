package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	fantasy "charm.land/fantasy"
)

func newScriptedLLMMapModel(t *testing.T, responses ...string) *MockLanguageModel {
	t.Helper()
	var mu sync.Mutex
	next := 0
	generate := func(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(responses) == 0 {
			return nil, fmt.Errorf("no responses configured")
		}
		require.NotNil(t, call.MaxOutputTokens)
		assert.Empty(t, cmp.Diff(int64(512), *call.MaxOutputTokens))
		require.Len(t, call.Prompt, 2)
		assert.Empty(t, cmp.Diff(fantasy.MessageRoleSystem, call.Prompt[0].Role))
		assert.Empty(t, cmp.Diff(fantasy.MessageRoleUser, call.Prompt[1].Role))

		systemPrompt := textFromMessage(t, call.Prompt[0])
		assert.Contains(t, systemPrompt, "pure mapper")
		assert.Contains(t, systemPrompt, "strict JSON object")

		userPrompt := textFromMessage(t, call.Prompt[1])
		assert.Contains(t, userPrompt, "Return exactly one JSON object and nothing else.")
		assert.Contains(t, userPrompt, "Instruction:")
		assert.Contains(t, userPrompt, "Item JSON:")
		if len(responses) > 1 {
			assert.Contains(t, userPrompt, "Convert item to {label, priority}")
		} else {
			assert.Contains(t, userPrompt, "Return normalized object")
		}
		if len(responses) > 1 && next == 0 {
			assert.True(t, strings.Contains(userPrompt, `"name":"A"`) || strings.Contains(userPrompt, `"name": "A"`), "first prompt should include first item JSON: %s", userPrompt)
		}
		if len(responses) > 1 && next == 1 {
			assert.True(t, strings.Contains(userPrompt, `"name":"B"`) || strings.Contains(userPrompt, `"name": "B"`), "second prompt should include second item JSON: %s", userPrompt)
		}

		resp := responses[next%len(responses)]
		next++
		return &fantasy.Response{
			Content:      fantasy.ResponseContent{fantasy.TextContent{Text: resp}},
			FinishReason: fantasy.FinishReasonStop,
		}, nil
	}
	model := NewMockLanguageModel(gomock.NewController(t))
	model.EXPECT().Provider().Return("mock").AnyTimes()
	model.EXPECT().Model().Return("mock-llm-map").AnyTimes()
	model.EXPECT().Generate(gomock.Any(), gomock.AssignableToTypeOf(fantasy.Call{})).DoAndReturn(generate).Times(len(responses))
	return model
}

func textFromMessage(t *testing.T, message fantasy.Message) string {
	t.Helper()
	require.Len(t, message.Content, 1)
	part, ok := fantasy.AsMessagePart[fantasy.TextPart](message.Content[0])
	require.True(t, ok, "message content should be TextPart")
	return part.Text
}

func TestLLMMapTool_Execute_Success(t *testing.T) {
	t.Parallel()
	model := newScriptedLLMMapModel(t,
		`{"label":"alpha","priority":1}`,
		`{"label":"beta","priority":2}`,
	)
	tool := NewLLMMapTool(model, "mock-llm-map")

	result := tool.Execute(t.Context(), map[string]interface{}{
		"instruction": "Convert item to {label, priority}",
		"items": []interface{}{
			map[string]interface{}{"name": "A"},
			map[string]interface{}{"name": "B"},
		},
		"output_schema": map[string]interface{}{
			"required": []interface{}{"label", "priority"},
			"properties": map[string]interface{}{
				"label":    map[string]interface{}{"type": "string"},
				"priority": map[string]interface{}{"type": "integer"},
			},
		},
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)

	var payload struct {
		Count   int                      `json:"count"`
		Results []map[string]interface{} `json:"results"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &payload))
	assert.Empty(t, cmp.Diff(2, payload.Count))
	require.Len(t, payload.Results, 2)
	assert.Empty(t, cmp.Diff("alpha", payload.Results[0]["label"]))
}

func TestLLMMapTool_Execute_SchemaValidationFailure(t *testing.T) {
	t.Parallel()
	model := newScriptedLLMMapModel(t, `{"only":"value"}`)
	tool := NewLLMMapTool(model, "mock-llm-map")

	result := tool.Execute(t.Context(), map[string]interface{}{
		"instruction": "Return normalized object",
		"items":       []interface{}{map[string]interface{}{"name": "x"}},
		"output_schema": map[string]interface{}{
			"required": []interface{}{"label"},
			"properties": map[string]interface{}{
				"label": map[string]interface{}{"type": "string"},
			},
		},
	})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "schema validation failed")
}
