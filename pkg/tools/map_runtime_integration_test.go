package tools

import (
	"context"
	"fmt"
	"sync"
	"testing"

	fantasy "charm.land/fantasy"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

type scriptedMapLLM struct {
	mu        sync.Mutex
	responses []scriptedMapLLMResponse
	index     int
}

type scriptedMapLLMResponse struct {
	Text string
	Err  error
}

func (m *scriptedMapLLM) Generate(_ context.Context, _ fantasy.Call) (*fantasy.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return nil, fmt.Errorf("no scripted llm responses")
	}
	resp := m.responses[m.index%len(m.responses)]
	m.index++
	if resp.Err != nil {
		return nil, resp.Err
	}
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: resp.Text}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *scriptedMapLLM) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	resp, err := m.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: resp.Content.Text()}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *scriptedMapLLM) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *scriptedMapLLM) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *scriptedMapLLM) Provider() string { return "mock" }
func (m *scriptedMapLLM) Model() string    { return "mock-map-llm" }

func newMapRuntimeForTest(t *testing.T, llm fantasy.LanguageModel, manager *SubagentManager) *MapRuntime {
	t.Helper()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })

	return NewMapRuntime(d.Queries(), pkg.NAME, llm, "mock-map-llm", manager)
}

func decodeResultMap(t *testing.T, result *ToolResult) map[string]interface{} {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)
	var payload map[string]interface{}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &payload))
	return payload
}

func TestLLMMap_WorkerJSONL_StatusAndRead(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Text: `{"label":"alpha","priority":1}`},
			{Text: `{"label":"beta","priority":2}`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)
	statusTool := NewMapRunStatusTool(runtime)
	readTool := NewMapRunReadTool(runtime)

	enqueue := decodeResultMap(t, mapTool.Execute(t.Context(), map[string]interface{}{
		"instruction":    "normalize to {label, priority}",
		"input_jsonl":    "{\"name\":\"a\"}\n{\"name\":\"b\"}",
		"execution_mode": "worker",
	}))
	runID, _ := enqueue["run_id"].(string)
	require.NotEmpty(t, runID)
	assert.Equal(t, "worker", enqueue["execution_mode"])

	status := decodeResultMap(t, statusTool.Execute(t.Context(), map[string]interface{}{
		"run_id":        runID,
		"process_steps": float64(20),
	}))
	assert.Equal(t, mapRunStatusSucceeded, status["status"])
	assert.EqualValues(t, 2, status["succeeded_items"])

	readJSON := decodeResultMap(t, readTool.Execute(t.Context(), map[string]interface{}{
		"run_id": runID,
		"limit":  float64(10),
		"format": "json",
	}))
	itemsAny, ok := readJSON["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, itemsAny, 2)

	readJSONL := decodeResultMap(t, readTool.Execute(t.Context(), map[string]interface{}{
		"run_id": runID,
		"format": "jsonl",
	}))
	jsonlText, _ := readJSONL["items_jsonl"].(string)
	assert.Contains(t, jsonlText, `"status":"succeeded"`)
	assert.Contains(t, jsonlText, "\n")
}

func TestLLMMap_IdempotencyReuse(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Text: `{"label":"alpha"}`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)

	args := map[string]interface{}{
		"instruction":     "normalize",
		"items":           []interface{}{map[string]interface{}{"name": "a"}},
		"execution_mode":  "worker",
		"idempotency_key": "run-1",
		"session_key":     "sess-a",
	}
	first := decodeResultMap(t, mapTool.Execute(t.Context(), args))
	second := decodeResultMap(t, mapTool.Execute(t.Context(), args))

	assert.Equal(t, first["run_id"], second["run_id"])
	assert.Equal(t, true, second["idempotent_reuse"])
}

func TestLLMMap_IdempotencyReuse_Concurrent(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Text: `{"label":"alpha"}`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)

	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	runIDs := make(chan string, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result := mapTool.Execute(t.Context(), map[string]interface{}{
				"instruction":     "normalize",
				"items":           []interface{}{map[string]interface{}{"name": "a"}},
				"execution_mode":  "worker",
				"idempotency_key": "run-1",
				"session_key":     "sess-a",
			})
			if result == nil || result.IsError {
				if result != nil && result.Err != nil {
					errs <- result.Err
					return
				}
				errs <- fmt.Errorf("unexpected map tool error: %v", result)
				return
			}
			var payload map[string]interface{}
			if err := jsonv2.Unmarshal([]byte(result.ForLLM), &payload); err != nil {
				errs <- fmt.Errorf("decode result: %w", err)
				return
			}
			runID, _ := payload["run_id"].(string)
			if runID == "" {
				errs <- fmt.Errorf("missing run_id in payload")
				return
			}
			runIDs <- runID
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	close(runIDs)

	for err := range errs {
		require.NoError(t, err)
	}

	unique := make(map[string]struct{})
	for runID := range runIDs {
		unique[runID] = struct{}{}
	}
	require.Len(t, unique, 1, "all concurrent requests should reuse the same run id")
}

func TestLLMMap_InlineRetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Err: fmt.Errorf("transient model outage")},
			{Text: `{"label":"ok"}`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)
	readTool := NewMapRunReadTool(runtime)

	result := decodeResultMap(t, mapTool.Execute(t.Context(), map[string]interface{}{
		"instruction":    "normalize",
		"items":          []interface{}{map[string]interface{}{"name": "retry"}},
		"execution_mode": "inline",
		"max_retries":    float64(2),
	}))
	assert.Equal(t, mapRunStatusSucceeded, result["status"])
	runID, _ := result["run_id"].(string)
	require.NotEmpty(t, runID)

	read := decodeResultMap(t, readTool.Execute(t.Context(), map[string]interface{}{
		"run_id": runID,
		"format": "json",
	}))
	itemsAny, ok := read["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, itemsAny, 1)
	item0 := itemsAny[0].(map[string]interface{})
	assert.EqualValues(t, 2, item0["attempts"])
	assert.Equal(t, mapItemStatusSucceeded, item0["status"])
}

func TestLLMMap_InlineExhaustedRetriesFailsRun(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Text: `not-json`},
			{Text: `still-not-json`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)

	payload := decodeResultMap(t, mapTool.Execute(t.Context(), map[string]interface{}{
		"instruction":    "normalize",
		"items":          []interface{}{map[string]interface{}{"name": "bad"}},
		"execution_mode": "inline",
		"max_retries":    float64(1),
	}))
	assert.Equal(t, mapRunStatusFailed, payload["status"])
	summary, ok := payload["summary"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 1, summary["failure_count"])
}

func TestMapRunRead_DetectsOutputHashMismatch(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{
			{Text: `{"label":"alpha"}`},
		},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)
	readTool := NewMapRunReadTool(runtime)

	result := decodeResultMap(t, mapTool.Execute(t.Context(), map[string]interface{}{
		"instruction":    "normalize",
		"items":          []interface{}{map[string]interface{}{"name": "a"}},
		"execution_mode": "inline",
	}))
	runIDText, _ := result["run_id"].(string)
	require.NotEmpty(t, runIDText)

	runID, err := ids.Parse(runIDText)
	require.NoError(t, err)
	rows, err := runtime.queries.ListMapItemsByRunPaged(t.Context(), memsqlc.ListMapItemsByRunPagedParams{
		RunID: runID,
		Lim:   10,
		Off:   0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	_, err = runtime.queries.MarkMapItemSucceeded(t.Context(), memsqlc.MarkMapItemSucceededParams{
		OutputFb:   rows[0].OutputFb,
		OutputHash: optionalStringPtr("forced-mismatch"),
		ID:         rows[0].ID,
	})
	require.NoError(t, err)

	readResult := readTool.Execute(t.Context(), map[string]interface{}{
		"run_id": runIDText,
		"format": "json",
	})
	require.NotNil(t, readResult)
	require.True(t, readResult.IsError)
	assert.Contains(t, readResult.ForLLM, "output hash mismatch")
}

func TestAgenticMap_WorkerLifecycle(t *testing.T) {
	t.Parallel()
	provider := &MockLanguageModel{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, userPrompt, _, _ string) (*ToolLoopResult, error) {
		return &ToolLoopResult{Content: "processed: " + userPrompt, Iterations: 1}, nil
	})

	runtime := newMapRuntimeForTest(t, &scriptedMapLLM{responses: []scriptedMapLLMResponse{{Text: `{"unused":true}`}}}, manager)
	tool := NewAgenticMapTool(manager)
	tool.SetRuntime(runtime)
	statusTool := NewMapRunStatusTool(runtime)
	readTool := NewMapRunReadTool(runtime)

	enqueue := decodeResultMap(t, tool.Execute(t.Context(), map[string]interface{}{
		"items":          []interface{}{map[string]interface{}{"name": "x"}},
		"task_template":  "Handle {{index}} => {{item_json}}",
		"execution_mode": "worker",
	}))
	runID, _ := enqueue["run_id"].(string)
	require.NotEmpty(t, runID)

	status := decodeResultMap(t, statusTool.Execute(t.Context(), map[string]interface{}{
		"run_id":        runID,
		"process_steps": float64(20),
	}))
	assert.Equal(t, mapRunStatusSucceeded, status["status"])

	read := decodeResultMap(t, readTool.Execute(t.Context(), map[string]interface{}{
		"run_id": runID,
		"format": "json",
	}))
	itemsAny, ok := read["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, itemsAny, 1)
	item0 := itemsAny[0].(map[string]interface{})
	assert.Equal(t, mapItemStatusSucceeded, item0["status"])
}

func TestLLMMap_InvalidJSONLIngestFails(t *testing.T) {
	t.Parallel()
	model := &scriptedMapLLM{
		responses: []scriptedMapLLMResponse{{Text: `{"ok":true}`}},
	}
	runtime := newMapRuntimeForTest(t, model, nil)
	mapTool := NewLLMMapTool(model, "mock-map-llm")
	mapTool.SetRuntime(runtime)

	result := mapTool.Execute(t.Context(), map[string]interface{}{
		"instruction": "normalize",
		"input_jsonl": "{\"name\":\"ok\"}\nnot-json",
	})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid JSONL")
}
