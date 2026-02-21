package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/worker"
	jsonv2 "github.com/go-json-experiment/json"
)

const (
	mapRunStatusQueued    = "queued"
	mapRunStatusRunning   = "running"
	mapRunStatusSucceeded = "succeeded"
	mapRunStatusFailed    = "failed"
	mapRunStatusCancelled = "cancelled"

	mapItemStatusQueued    = "queued"
	mapItemStatusRunning   = "running"
	mapItemStatusSucceeded = "succeeded"
	mapItemStatusFailed    = "failed"

	mapJobKindLLMItem     = "map_item_llm"
	mapJobKindAgenticItem = "map_item_agentic"
)

type mapJobPayload struct {
	RunID  string `json:"run_id"`
	ItemID string `json:"item_id"`
}

type MapRunProjection struct {
	RunID          string `json:"run_id"`
	OperatorKind   string `json:"operator_kind"`
	Status         string `json:"status"`
	TotalItems     int64  `json:"total_items"`
	QueuedItems    int64  `json:"queued_items"`
	RunningItems   int64  `json:"running_items"`
	SucceededItems int64  `json:"succeeded_items"`
	FailedItems    int64  `json:"failed_items"`
	LastError      string `json:"last_error,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	CompletedAt    string `json:"completed_at,omitempty"`
}

type MapItemProjection struct {
	ItemID     string      `json:"item_id"`
	Index      int64       `json:"index"`
	Status     string      `json:"status"`
	Attempts   int64       `json:"attempts"`
	Input      interface{} `json:"input,omitempty"`
	Output     interface{} `json:"output,omitempty"`
	Error      string      `json:"error,omitempty"`
	InputHash  string      `json:"input_hash,omitempty"`
	OutputHash string      `json:"output_hash,omitempty"`
}

type MapRuntime struct {
	queries         *memsqlc.Queries
	agentID         string
	llmModel        fantasy.LanguageModel
	llmModelID      string
	subagentManager *SubagentManager
	workerLockedBy  string
	processMu       sync.Mutex
}

func NewMapRuntime(
	queries *memsqlc.Queries,
	agentID string,
	llmModel fantasy.LanguageModel,
	llmModelID string,
	subagentManager *SubagentManager,
) *MapRuntime {
	if queries == nil {
		return nil
	}
	return &MapRuntime{
		queries:         queries,
		agentID:         agentID,
		llmModel:        llmModel,
		llmModelID:      llmModelID,
		subagentManager: subagentManager,
		workerLockedBy:  "map-runtime",
	}
}

func (rt *MapRuntime) EnqueueRun(
	ctx context.Context,
	sessionKey string,
	spec MapRunSpec,
	items []mapBoundaryItem,
	idempotencyKey string,
) (memsqlc.MapRun, bool, error) {
	if rt == nil || rt.queries == nil {
		return memsqlc.MapRun{}, false, fmt.Errorf("map runtime is not configured")
	}
	if len(items) == 0 {
		return memsqlc.MapRun{}, false, fmt.Errorf("at least one item is required")
	}
	if sessionKey == "" {
		sessionKey = "default"
	}
	if spec.OperatorKind != MapOperatorLLM && spec.OperatorKind != MapOperatorAgentic {
		return memsqlc.MapRun{}, false, fmt.Errorf("unsupported operator kind %q", spec.OperatorKind)
	}
	seenIndices := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.Index < 0 || uint64(item.Index) > uint64(^uint32(0)) {
			return memsqlc.MapRun{}, false, fmt.Errorf("item index %d out of uint32 range", item.Index)
		}
		if _, exists := seenIndices[item.Index]; exists {
			return memsqlc.MapRun{}, false, fmt.Errorf("duplicate item index %d", item.Index)
		}
		seenIndices[item.Index] = struct{}{}
	}

	specFB, err := EncodeMapRunSpecFlatBuffer(spec)
	if err != nil {
		return memsqlc.MapRun{}, false, fmt.Errorf("encode run spec: %w", err)
	}
	idemPtr := optionalStringPtr(strings.TrimSpace(idempotencyKey))
	if idemPtr != nil {
		existing, err := rt.queries.GetMapRunByIdempotencyKey(ctx, memsqlc.GetMapRunByIdempotencyKeyParams{
			AgentID:        rt.agentID,
			SessionKey:     sessionKey,
			OperatorKind:   string(spec.OperatorKind),
			IdempotencyKey: idemPtr,
		})
		if err == nil {
			return existing, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return memsqlc.MapRun{}, false, fmt.Errorf("lookup idempotent run: %w", err)
		}
	}

	runID := ids.New()
	run, err := rt.queries.InsertMapRun(ctx, memsqlc.InsertMapRunParams{
		ID:             runID,
		AgentID:        rt.agentID,
		SessionKey:     sessionKey,
		OperatorKind:   string(spec.OperatorKind),
		IdempotencyKey: idemPtr,
		Status:         mapRunStatusQueued,
		TotalItems:     int64(len(items)),
		QueuedItems:    int64(len(items)),
		RunningItems:   0,
		SucceededItems: 0,
		FailedItems:    0,
		SpecFb:         specFB,
		LastError:      nil,
	})
	if err != nil {
		if idemPtr != nil && isUniqueConstraintError(err) {
			existing, lookupErr := rt.queries.GetMapRunByIdempotencyKey(ctx, memsqlc.GetMapRunByIdempotencyKeyParams{
				AgentID:        rt.agentID,
				SessionKey:     sessionKey,
				OperatorKind:   string(spec.OperatorKind),
				IdempotencyKey: idemPtr,
			})
			if lookupErr == nil {
				return existing, true, nil
			}
		}
		return memsqlc.MapRun{}, false, fmt.Errorf("insert map run: %w", err)
	}

	jobKind := mapJobKindLLMItem
	if spec.OperatorKind == MapOperatorAgentic {
		jobKind = mapJobKindAgenticItem
	}
	maxAttempts := int64(spec.MaxRetries) + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for _, item := range items {
		inputFB, encErr := EncodeMapItemInputFlatBuffer(MapItemInputRecord{
			ItemIndex: uint32(item.Index),
			ItemJSON:  item.ItemJSON,
			InputHash: item.InputHash,
		})
		if encErr != nil {
			msg := fmt.Sprintf("encode item %d: %v", item.Index, encErr)
			_, _ = rt.failRun(ctx, run.ID, &msg)
			return memsqlc.MapRun{}, false, errors.New(msg)
		}
		itemID := ids.New()
		if _, err := rt.queries.InsertMapItem(ctx, memsqlc.InsertMapItemParams{
			ID:         itemID,
			RunID:      run.ID,
			ItemIndex:  int64(item.Index),
			Status:     mapItemStatusQueued,
			Attempts:   0,
			LastError:  nil,
			InputFb:    inputFB,
			OutputFb:   nil,
			InputHash:  optionalStringPtr(item.InputHash),
			OutputHash: nil,
		}); err != nil {
			msg := fmt.Sprintf("insert map item %d: %v", item.Index, err)
			_, _ = rt.failRun(ctx, run.ID, &msg)
			return memsqlc.MapRun{}, false, errors.New(msg)
		}

		dedupeKey := fmt.Sprintf("map:%s:%d", run.ID.String(), item.Index)
		if _, err := rt.queries.EnqueueJob(ctx, memsqlc.EnqueueJobParams{
			ID:          ids.New(),
			Kind:        jobKind,
			DedupeKey:   &dedupeKey,
			MaxAttempts: maxAttempts,
			RunAt:       time.Now().UTC(),
			PayloadJson: []byte(`{}`),
		}); err != nil {
			msg := fmt.Sprintf("enqueue map item %d: %v", item.Index, err)
			_, _ = rt.failRun(ctx, run.ID, &msg)
			return memsqlc.MapRun{}, false, errors.New(msg)
		}
	}

	logger.InfoCF("map_runtime", "map run enqueued", map[string]interface{}{
		"run_id":        run.ID.String(),
		"operator_kind": run.OperatorKind,
		"items":         len(items),
		"idempotency":   idempotencyKey,
	})
	return run, false, nil
}

func (rt *MapRuntime) ProcessPending(ctx context.Context, maxSteps int) (int, error) {
	if rt == nil || rt.queries == nil {
		return 0, nil
	}
	if maxSteps <= 0 {
		maxSteps = 1
	}
	opts := &worker.Options{
		LockedBy: rt.workerLockedBy,
		Handlers: map[string]worker.HandlerFunc{
			mapJobKindLLMItem:     rt.handleLLMMapJob,
			mapJobKindAgenticItem: rt.handleAgenticMapJob,
		},
		Backoff: func(_ int) time.Duration {
			return 10 * time.Millisecond
		},
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
	rt.processMu.Lock()
	defer rt.processMu.Unlock()

	processed := 0
	for processed < maxSteps {
		if _, err := rt.queries.FindNextRunnableJob(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return processed, err
		}
		if err := worker.RunOnce(ctx, rt.queries, opts); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (rt *MapRuntime) ProcessRunToTerminal(ctx context.Context, runID ids.UUID, maxSteps int) (memsqlc.MapRun, error) {
	if maxSteps <= 0 {
		maxSteps = 2000
	}
	for i := 0; i < maxSteps; i++ {
		run, err := rt.queries.GetMapRunByID(ctx, memsqlc.GetMapRunByIDParams{ID: runID})
		if err != nil {
			return memsqlc.MapRun{}, err
		}
		if isTerminalMapRunStatus(run.Status) {
			return run, nil
		}
		processed, err := rt.ProcessPending(ctx, 1)
		if err != nil {
			return memsqlc.MapRun{}, err
		}
		if processed == 0 {
			select {
			case <-ctx.Done():
				return memsqlc.MapRun{}, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return memsqlc.MapRun{}, fmt.Errorf("map run %s did not reach terminal status within %d steps", runID.String(), maxSteps)
}

func (rt *MapRuntime) GetRun(ctx context.Context, runID ids.UUID) (memsqlc.MapRun, error) {
	if rt == nil || rt.queries == nil {
		return memsqlc.MapRun{}, fmt.Errorf("map runtime is not configured")
	}
	return rt.queries.GetMapRunByID(ctx, memsqlc.GetMapRunByIDParams{ID: runID})
}

func (rt *MapRuntime) ReadRunItems(ctx context.Context, runID ids.UUID, offset, limit int) ([]MapItemProjection, error) {
	if rt == nil || rt.queries == nil {
		return nil, fmt.Errorf("map runtime is not configured")
	}
	rows, err := rt.queries.ListMapItemsByRunPaged(ctx, memsqlc.ListMapItemsByRunPagedParams{
		RunID: runID,
		Lim:   int64(limit),
		Off:   int64(offset),
	})
	if err != nil {
		return nil, err
	}
	items := make([]MapItemProjection, 0, len(rows))
	for _, row := range rows {
		inputRec, inErr := DecodeMapItemInputFlatBuffer(row.InputFb)
		if inErr != nil {
			return nil, fmt.Errorf("decode item input %s: %w", row.ID.String(), inErr)
		}
		var inputAny interface{}
		if err := jsonv2.Unmarshal([]byte(inputRec.ItemJSON), &inputAny); err != nil {
			return nil, fmt.Errorf("decode item JSON %s: %w", row.ID.String(), err)
		}

		proj := MapItemProjection{
			ItemID:   row.ID.String(),
			Index:    row.ItemIndex,
			Status:   row.Status,
			Attempts: row.Attempts,
			Input:    inputAny,
		}
		if row.InputHash != nil {
			proj.InputHash = *row.InputHash
		}
		if row.LastError != nil {
			proj.Error = *row.LastError
		}

		if len(row.OutputFb) > 0 {
			outputRec, outErr := DecodeMapItemOutputFlatBuffer(row.OutputFb)
			if outErr != nil {
				return nil, fmt.Errorf("decode item output %s: %w", row.ID.String(), outErr)
			}
			if row.OutputHash != nil && outputRec.OutputHash != "" && *row.OutputHash != outputRec.OutputHash {
				return nil, fmt.Errorf("output hash mismatch for item %s", row.ID.String())
			}
			if outputRec.ErrorText != "" && proj.Error == "" {
				proj.Error = outputRec.ErrorText
			}
			if outputRec.OutputHash != "" {
				proj.OutputHash = outputRec.OutputHash
			} else if row.OutputHash != nil {
				proj.OutputHash = *row.OutputHash
			}
			if strings.TrimSpace(outputRec.OutputJSON) != "" {
				var outputAny interface{}
				if err := jsonv2.Unmarshal([]byte(outputRec.OutputJSON), &outputAny); err == nil {
					proj.Output = outputAny
				} else {
					// Keep malformed model output visible to callers for debugging.
					proj.Output = outputRec.OutputJSON
				}
			}
		}
		items = append(items, proj)
	}
	return items, nil
}

func (rt *MapRuntime) handleLLMMapJob(ctx context.Context, q *memsqlc.Queries, job memsqlc.Job) error {
	if rt.llmModel == nil {
		return fmt.Errorf("llm_map runtime model is not configured")
	}
	item, run, spec, inputRec, err := rt.loadMapJobState(ctx, q, job)
	if err != nil {
		return err
	}
	if spec.OperatorKind != MapOperatorLLM {
		errMsg := fmt.Sprintf("run %s operator kind mismatch: expected llm_map got %s", run.ID.String(), spec.OperatorKind)
		_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &errMsg, ID: item.ID})
		if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &errMsg); refreshErr != nil {
			logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
				"run_id": run.ID.String(),
				"error":  refreshErr.Error(),
			})
		}
		return nil
	}

	outJSON, callErr := rt.executeLLMMapItem(ctx, spec, inputRec.ItemJSON)
	if callErr != nil {
		if job.Attempts >= job.MaxAttempts {
			msg := callErr.Error()
			_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &msg, ID: item.ID})
			if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &msg); refreshErr != nil {
				logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
					"run_id": run.ID.String(),
					"error":  refreshErr.Error(),
				})
			}
			return nil
		}
		return callErr
	}

	outHash := hashString(outJSON)
	outputFB, err := EncodeMapItemOutputFlatBuffer(MapItemOutputRecord{
		ItemIndex:  uint32(item.ItemIndex),
		Success:    true,
		Attempts:   uint16(item.Attempts),
		OutputJSON: outJSON,
		OutputHash: outHash,
	})
	if err != nil {
		msg := fmt.Sprintf("encode output for item %s: %v", item.ID.String(), err)
		_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &msg, ID: item.ID})
		if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &msg); refreshErr != nil {
			logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
				"run_id": run.ID.String(),
				"error":  refreshErr.Error(),
			})
		}
		return nil
	}
	if _, err := q.MarkMapItemSucceeded(ctx, memsqlc.MarkMapItemSucceededParams{
		OutputFb:   outputFB,
		OutputHash: &outHash,
		ID:         item.ID,
	}); err != nil {
		return err
	}
	if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, nil); refreshErr != nil {
		logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
			"run_id": run.ID.String(),
			"error":  refreshErr.Error(),
		})
	}
	return nil
}

func (rt *MapRuntime) handleAgenticMapJob(ctx context.Context, q *memsqlc.Queries, job memsqlc.Job) error {
	if rt.subagentManager == nil {
		return fmt.Errorf("agentic_map runtime manager is not configured")
	}
	item, run, spec, inputRec, err := rt.loadMapJobState(ctx, q, job)
	if err != nil {
		return err
	}
	if spec.OperatorKind != MapOperatorAgentic {
		errMsg := fmt.Sprintf("run %s operator kind mismatch: expected agentic_map got %s", run.ID.String(), spec.OperatorKind)
		_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &errMsg, ID: item.ID})
		if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &errMsg); refreshErr != nil {
			logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
				"run_id": run.ID.String(),
				"error":  refreshErr.Error(),
			})
		}
		return nil
	}
	outJSON, callErr := rt.executeAgenticMapItem(ctx, spec, int(item.ItemIndex), inputRec.ItemJSON)
	if callErr != nil {
		if job.Attempts >= job.MaxAttempts {
			msg := callErr.Error()
			_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &msg, ID: item.ID})
			if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &msg); refreshErr != nil {
				logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
					"run_id": run.ID.String(),
					"error":  refreshErr.Error(),
				})
			}
			return nil
		}
		return callErr
	}

	outHash := hashString(outJSON)
	outputFB, err := EncodeMapItemOutputFlatBuffer(MapItemOutputRecord{
		ItemIndex:  uint32(item.ItemIndex),
		Success:    true,
		Attempts:   uint16(item.Attempts),
		OutputJSON: outJSON,
		OutputHash: outHash,
	})
	if err != nil {
		msg := fmt.Sprintf("encode output for item %s: %v", item.ID.String(), err)
		_, _ = q.MarkMapItemFailed(ctx, memsqlc.MarkMapItemFailedParams{LastError: &msg, ID: item.ID})
		if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, &msg); refreshErr != nil {
			logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
				"run_id": run.ID.String(),
				"error":  refreshErr.Error(),
			})
		}
		return nil
	}
	if _, err := q.MarkMapItemSucceeded(ctx, memsqlc.MarkMapItemSucceededParams{
		OutputFb:   outputFB,
		OutputHash: &outHash,
		ID:         item.ID,
	}); err != nil {
		return err
	}
	if _, refreshErr := rt.refreshRunProgress(ctx, q, run.ID, nil); refreshErr != nil {
		logger.WarnCF("map_runtime", "failed to refresh run progress", map[string]interface{}{
			"run_id": run.ID.String(),
			"error":  refreshErr.Error(),
		})
	}
	return nil
}

func (rt *MapRuntime) loadMapJobState(
	ctx context.Context,
	q *memsqlc.Queries,
	job memsqlc.Job,
) (memsqlc.MapItem, memsqlc.MapRun, MapRunSpec, MapItemInputRecord, error) {
	itemID, dedupeErr := mapJobItemIDFromDedupeKey(ctx, q, job.DedupeKey)
	if dedupeErr != nil {
		payload, payloadErr := parseMapJobPayload(job.PayloadJson)
		if payloadErr != nil {
			return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, fmt.Errorf(
				"invalid map job identity: dedupe_key_error=%v payload_error=%v",
				dedupeErr,
				payloadErr,
			)
		}
		parsedID, parseErr := ids.Parse(payload.ItemID)
		if parseErr != nil {
			return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, fmt.Errorf("invalid item id in job payload: %w", parseErr)
		}
		itemID = parsedID
	}
	item, err := q.MarkMapItemRunning(ctx, memsqlc.MarkMapItemRunningParams{ID: itemID})
	if err != nil {
		return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, err
	}
	run, err := q.GetMapRunByID(ctx, memsqlc.GetMapRunByIDParams{ID: item.RunID})
	if err != nil {
		return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, err
	}
	spec, err := DecodeMapRunSpecFlatBuffer(run.SpecFb)
	if err != nil {
		return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, fmt.Errorf("decode run spec: %w", err)
	}
	inputRec, err := DecodeMapItemInputFlatBuffer(item.InputFb)
	if err != nil {
		return memsqlc.MapItem{}, memsqlc.MapRun{}, MapRunSpec{}, MapItemInputRecord{}, fmt.Errorf("decode item input: %w", err)
	}
	return item, run, spec, inputRec, nil
}

func (rt *MapRuntime) executeLLMMapItem(ctx context.Context, spec MapRunSpec, itemJSON string) (string, error) {
	maxOut := int64(512)
	prompt := fmt.Sprintf(
		"Instruction:\n%s\n\nItem JSON:\n%s\n\nReturn exactly one JSON object and nothing else.",
		spec.Instruction,
		itemJSON,
	)
	call := fantasy.Call{
		Prompt: fantasy.Prompt{
			fantasy.NewSystemMessage("You are a pure mapper. Return only strict JSON object output."),
			fantasy.NewUserMessage(prompt),
		},
		MaxOutputTokens: &maxOut,
	}
	resp, err := rt.llmModel.Generate(ctx, call)
	if err != nil {
		return "", err
	}
	outText := strings.TrimSpace(resp.Content.Text())
	var out map[string]interface{}
	if err := jsonv2.Unmarshal([]byte(outText), &out); err != nil {
		return "", fmt.Errorf("llm_map output is not valid JSON object: %w", err)
	}
	if strings.TrimSpace(spec.OutputSchemaJSON) != "" {
		var schema map[string]interface{}
		if err := jsonv2.Unmarshal([]byte(spec.OutputSchemaJSON), &schema); err != nil {
			return "", fmt.Errorf("invalid output schema in run spec: %w", err)
		}
		if err := validateLLMMapOutputSchema(out, schema); err != nil {
			return "", fmt.Errorf("schema validation failed: %w", err)
		}
	}
	outJSON, err := canonicalJSONString(out)
	if err != nil {
		return "", fmt.Errorf("serialize model output: %w", err)
	}
	return outJSON, nil
}

func (rt *MapRuntime) executeAgenticMapItem(ctx context.Context, spec MapRunSpec, index int, itemJSON string) (string, error) {
	if strings.TrimSpace(spec.TaskTemplate) == "" {
		return "", fmt.Errorf("task_template is required for agentic_map")
	}
	if !strings.Contains(spec.TaskTemplate, "{{item_json}}") || !strings.Contains(spec.TaskTemplate, "{{index}}") {
		return "", fmt.Errorf("task_template must include both {{item_json}} and {{index}} placeholders")
	}
	task := strings.ReplaceAll(spec.TaskTemplate, "{{item_json}}", itemJSON)
	task = strings.ReplaceAll(task, "{{index}}", fmt.Sprintf("%d", index))

	subTool := NewSubagentTool(rt.subagentManager)
	originChannel := spec.OriginChannel
	if originChannel == "" {
		originChannel = "cli"
	}
	originChatID := spec.OriginChatID
	if originChatID == "" {
		originChatID = "direct"
	}
	subTool.SetContext(originChannel, originChatID)

	callArgs := map[string]interface{}{
		"task":  task,
		"label": fmt.Sprintf("agentic-map-%d", index),
	}
	if strings.TrimSpace(spec.DelegatedScope) != "" {
		callArgs["delegated_scope"] = spec.DelegatedScope
	}
	if strings.TrimSpace(spec.KeptWork) != "" {
		callArgs["kept_work"] = spec.KeptWork
	}

	result := subTool.Execute(ctx, callArgs)
	if result == nil {
		return "", fmt.Errorf("subagent returned nil result")
	}
	if result.IsError {
		if result.Err != nil {
			return "", result.Err
		}
		return "", errors.New(result.ForLLM)
	}

	outputPayload := map[string]interface{}{
		"for_user": result.ForUser,
		"for_llm":  result.ForLLM,
	}
	outJSON, err := canonicalJSONString(outputPayload)
	if err != nil {
		return "", fmt.Errorf("serialize agentic output: %w", err)
	}
	return outJSON, nil
}

func (rt *MapRuntime) refreshRunProgress(
	ctx context.Context,
	q *memsqlc.Queries,
	runID ids.UUID,
	lastErr *string,
) (memsqlc.MapRun, error) {
	total, err := q.CountMapItemsByRun(ctx, memsqlc.CountMapItemsByRunParams{RunID: runID})
	if err != nil {
		return memsqlc.MapRun{}, err
	}
	statusCounts, err := q.CountMapItemsByRunAndStatus(ctx, memsqlc.CountMapItemsByRunAndStatusParams{RunID: runID})
	if err != nil {
		return memsqlc.MapRun{}, err
	}

	var queued, running, succeeded, failed int64
	for _, row := range statusCounts {
		switch row.Status {
		case mapItemStatusQueued:
			queued = row.Count
		case mapItemStatusRunning:
			running = row.Count
		case mapItemStatusSucceeded:
			succeeded = row.Count
		case mapItemStatusFailed:
			failed = row.Count
		}
	}

	status := mapRunStatusQueued
	if running > 0 {
		status = mapRunStatusRunning
	}
	if queued > 0 && running == 0 {
		status = mapRunStatusQueued
	}
	if queued == 0 && running == 0 {
		switch {
		case failed > 0:
			status = mapRunStatusFailed
		case succeeded == total:
			status = mapRunStatusSucceeded
		default:
			status = mapRunStatusFailed
		}
	}

	var completedAt *time.Time
	if isTerminalMapRunStatus(status) {
		now := time.Now().UTC()
		completedAt = &now
	}
	return q.UpdateMapRunProgress(ctx, memsqlc.UpdateMapRunProgressParams{
		Status:         status,
		QueuedItems:    queued,
		RunningItems:   running,
		SucceededItems: succeeded,
		FailedItems:    failed,
		LastError:      lastErr,
		CompletedAt:    completedAt,
		ID:             runID,
	})
}

func (rt *MapRuntime) failRun(ctx context.Context, runID ids.UUID, lastErr *string) (memsqlc.MapRun, error) {
	return rt.queries.UpdateMapRunProgress(ctx, memsqlc.UpdateMapRunProgressParams{
		Status:         mapRunStatusFailed,
		QueuedItems:    0,
		RunningItems:   0,
		SucceededItems: 0,
		FailedItems:    0,
		LastError:      lastErr,
		CompletedAt:    ptrTime(time.Now().UTC()),
		ID:             runID,
	})
}

func parseMapJobPayload(raw []byte) (mapJobPayload, error) {
	var payload mapJobPayload
	if err := jsonv2.Unmarshal(raw, &payload); err != nil {
		return mapJobPayload{}, err
	}
	if payload.RunID == "" || payload.ItemID == "" {
		return mapJobPayload{}, fmt.Errorf("run_id and item_id are required")
	}
	return payload, nil
}

func mapJobItemIDFromDedupeKey(ctx context.Context, q *memsqlc.Queries, dedupeKey *string) (ids.UUID, error) {
	runID, itemIndex, err := parseMapJobDedupeKey(dedupeKey)
	if err != nil {
		return ids.UUID{}, err
	}
	item, err := q.GetMapItemByRunAndIndex(ctx, memsqlc.GetMapItemByRunAndIndexParams{
		RunID:     runID,
		ItemIndex: itemIndex,
	})
	if err != nil {
		return ids.UUID{}, fmt.Errorf("resolve map item by dedupe key: %w", err)
	}
	return item.ID, nil
}

func parseMapJobDedupeKey(dedupeKey *string) (ids.UUID, int64, error) {
	if dedupeKey == nil || strings.TrimSpace(*dedupeKey) == "" {
		return ids.UUID{}, 0, fmt.Errorf("dedupe key is required")
	}
	parts := strings.Split(strings.TrimSpace(*dedupeKey), ":")
	if len(parts) != 3 || parts[0] != "map" {
		return ids.UUID{}, 0, fmt.Errorf("invalid dedupe key format: %q", strings.TrimSpace(*dedupeKey))
	}
	runID, err := ids.Parse(parts[1])
	if err != nil {
		return ids.UUID{}, 0, fmt.Errorf("invalid run id in dedupe key: %w", err)
	}
	itemIndex, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return ids.UUID{}, 0, fmt.Errorf("invalid item index in dedupe key: %w", err)
	}
	if itemIndex < 0 {
		return ids.UUID{}, 0, fmt.Errorf("item index in dedupe key must be non-negative")
	}
	return runID, itemIndex, nil
}

func toMapRunProjection(run memsqlc.MapRun) MapRunProjection {
	p := MapRunProjection{
		RunID:          run.ID.String(),
		OperatorKind:   run.OperatorKind,
		Status:         run.Status,
		TotalItems:     run.TotalItems,
		QueuedItems:    run.QueuedItems,
		RunningItems:   run.RunningItems,
		SucceededItems: run.SucceededItems,
		FailedItems:    run.FailedItems,
		CreatedAt:      run.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      run.UpdatedAt.Format(time.RFC3339Nano),
	}
	if run.LastError != nil {
		p.LastError = *run.LastError
	}
	if run.CompletedAt != nil {
		p.CompletedAt = run.CompletedAt.Format(time.RFC3339Nano)
	}
	return p
}

func isTerminalMapRunStatus(status string) bool {
	switch status {
	case mapRunStatusSucceeded, mapRunStatusFailed, mapRunStatusCancelled:
		return true
	default:
		return false
	}
}

func optionalStringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	out := strings.TrimSpace(v)
	return &out
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "unique constraint") ||
		strings.Contains(lower, "constraint failed") ||
		strings.Contains(lower, "duplicate key")
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
