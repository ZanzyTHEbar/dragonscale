package tools

import (
	"context"
	"fmt"
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
	generate := func(_ context.Context, _ fantasy.Call) (*fantasy.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(responses) == 0 {
			return nil, fmt.Errorf("no responses configured")
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
	model.EXPECT().Generate(gomock.Any(), gomock.Any()).DoAndReturn(generate).AnyTimes()
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
		resp, err := generate(ctx, call)
		if err != nil {
			return nil, err
		}
		return func(yield func(fantasy.StreamPart) bool) {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: resp.Content.Text()}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
		}, nil
	}).AnyTimes()
	model.EXPECT().GenerateObject(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("not implemented")).AnyTimes()
	model.EXPECT().StreamObject(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("not implemented")).AnyTimes()
	return model
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
