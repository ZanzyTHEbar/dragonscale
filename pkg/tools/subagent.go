package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
)

type SubagentTask struct {
	ID             string
	ParentTaskID   string
	Depth          int
	Task           string
	Label          string
	DelegatedScope string
	KeptWork       string
	OriginChannel  string
	OriginChatID   string
	Status         string
	Result         string
	Created        int64
}

// RunLoopFunc executes an agent tool loop. Injected from pkg/agent to break
// the import cycle between pkg/tools and pkg/fantasy.
type RunLoopFunc func(ctx context.Context, config ToolLoopConfig, systemPrompt, userPrompt, channel, chatID string) (*ToolLoopResult, error)

type delegationCtxKey string

const (
	delegationTaskIDKey  delegationCtxKey = "delegation_task_id"
	delegationDepthKey   delegationCtxKey = "delegation_depth"
	delegationSessionKey delegationCtxKey = "delegation_session_key"
)

func delegationTaskIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(delegationTaskIDKey).(string); ok {
		return v
	}
	return ""
}

func delegationDepthFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(delegationDepthKey).(int); ok {
		return v
	}
	return 0
}

func delegationSessionKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(delegationSessionKey).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func DelegationSessionKeyFromContext(ctx context.Context) string {
	return delegationSessionKeyFromContext(ctx)
}

func withDelegationContext(ctx context.Context, taskID string, depth int) context.Context {
	ctx = context.WithValue(ctx, delegationTaskIDKey, taskID)
	ctx = context.WithValue(ctx, delegationDepthKey, depth)
	return ctx
}

func withDelegationSessionKey(ctx context.Context, sessionKey string) context.Context {
	if strings.TrimSpace(sessionKey) == "" {
		return ctx
	}
	return context.WithValue(ctx, delegationSessionKey, sessionKey)
}

// DelegationAuditEvent captures lineage and outcomes for delegated work.
type DelegationAuditEvent struct {
	TaskID         string
	ParentTaskID   string
	Mode           string
	Depth          int
	Status         string
	Label          string
	DelegatedScope string
	KeptWork       string
	Iterations     int
	ResultChars    int
	Error          string
	OriginChannel  string
	OriginChatID   string
}

type SubagentManager struct {
	tasks          map[string]*SubagentTask
	mu             sync.RWMutex
	model          fantasy.LanguageModel
	defaultModel   string
	bus            *bus.MessageBus
	workspace      string
	tools          *ToolRegistry
	maxIterations  int
	maxDepth       int
	maxFanout      int
	activeChildren map[string]int
	nextID         int
	runLoop        RunLoopFunc
	auditHook      func(context.Context, DelegationAuditEvent)
}

func NewSubagentManager(model fantasy.LanguageModel, defaultModel, workspace string, bus *bus.MessageBus) *SubagentManager {
	return &SubagentManager{
		tasks:          make(map[string]*SubagentTask),
		model:          model,
		defaultModel:   defaultModel,
		bus:            bus,
		workspace:      workspace,
		tools:          NewToolRegistry(),
		maxIterations:  10,
		maxDepth:       3,
		maxFanout:      4,
		activeChildren: make(map[string]int),
		nextID:         1,
	}
}

// SetRunLoop injects the loop runner function. Must be called before any
// subagent execution.
func (sm *SubagentManager) SetRunLoop(fn RunLoopFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.runLoop = fn
}

// SetDelegationLimits configures nested delegation guardrails.
func (sm *SubagentManager) SetDelegationLimits(maxDepth, maxFanout int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if maxDepth > 0 {
		sm.maxDepth = maxDepth
	}
	if maxFanout > 0 {
		sm.maxFanout = maxFanout
	}
}

// SetAuditHook registers a callback for delegation lineage/events.
func (sm *SubagentManager) SetAuditHook(hook func(context.Context, DelegationAuditEvent)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.auditHook = hook
}

func (sm *SubagentManager) emitAudit(ctx context.Context, evt DelegationAuditEvent) {
	sm.mu.RLock()
	hook := sm.auditHook
	sm.mu.RUnlock()
	if hook != nil {
		hook(ctx, evt)
	}
}

func (sm *SubagentManager) getRunLoop() RunLoopFunc {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.runLoop
}

// SetTools sets the tool registry for subagent execution.
func (sm *SubagentManager) SetTools(tools *ToolRegistry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools = tools
}

// RegisterTool registers a tool for subagent execution.
func (sm *SubagentManager) RegisterTool(tool Tool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools.Register(tool)
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, delegatedScope, keptWork, originChannel, originChatID string, callback AsyncCallback) (string, error) {
	sm.mu.Lock()
	parentTaskID := delegationTaskIDFromContext(ctx)
	if parentTaskID == "" {
		parentTaskID = "root"
	}
	parentDepth := delegationDepthFromContext(ctx)
	childDepth := parentDepth + 1

	if childDepth > sm.maxDepth {
		sm.mu.Unlock()
		return "", fmt.Errorf("delegation depth exceeded: %d > %d", childDepth, sm.maxDepth)
	}
	if sm.activeChildren[parentTaskID] >= sm.maxFanout {
		sm.mu.Unlock()
		return "", fmt.Errorf("delegation fanout exceeded for %s: %d >= %d", parentTaskID, sm.activeChildren[parentTaskID], sm.maxFanout)
	}
	if parentDepth > 0 {
		if strings.TrimSpace(delegatedScope) == "" || strings.TrimSpace(keptWork) == "" {
			sm.mu.Unlock()
			return "", fmt.Errorf("nested delegation requires delegated_scope and kept_work")
		}
	}
	if keptWork != "" {
		slog.Debug("delegation scope-reduction",
			"parent_depth", parentDepth,
			"child_depth", childDepth,
			"kept_work", keptWork,
			"delegated_scope", delegatedScope,
		)
	}
	if sm.runLoop == nil {
		sm.mu.Unlock()
		return "", ErrRunLoopNotConfigured
	}

	taskID := fmt.Sprintf("subagent-%d", sm.nextID)
	sm.nextID++
	sm.activeChildren[parentTaskID]++

	subagentTask := &SubagentTask{
		ID:             taskID,
		ParentTaskID:   parentTaskID,
		Depth:          childDepth,
		Task:           task,
		Label:          label,
		DelegatedScope: delegatedScope,
		KeptWork:       keptWork,
		OriginChannel:  originChannel,
		OriginChatID:   originChatID,
		Status:         "running",
		Created:        time.Now().UnixMilli(),
	}
	sm.tasks[taskID] = subagentTask
	sm.mu.Unlock()

	sm.emitAudit(ctx, DelegationAuditEvent{
		TaskID:         taskID,
		ParentTaskID:   parentTaskID,
		Mode:           "spawn",
		Depth:          childDepth,
		Status:         "created",
		Label:          label,
		DelegatedScope: delegatedScope,
		KeptWork:       keptWork,
		OriginChannel:  originChannel,
		OriginChatID:   originChatID,
	})

	// Start task in background with context cancellation support.
	taskCtx := withDelegationSessionKey(ctx, SessionKeyFromContext(ctx))
	go sm.runTask(taskCtx, subagentTask, callback)

	if label != "" {
		return fmt.Sprintf("Spawned subagent '%s' for task: %s", label, task), nil
	}
	return fmt.Sprintf("Spawned subagent for task: %s", task), nil
}

func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	systemPrompt := `You are a subagent operating under the same runtime discipline as the main agent.
Use tools for actions. Do not claim actions without tool execution.
When discovering tools, call discovered tools directly; use tool_call only as fallback.
Complete the task independently and provide a clear summary of what was done.`

	// Check if context is already cancelled before starting
	select {
	case <-ctx.Done():
		sm.mu.Lock()
		task.Status = "cancelled"
		task.Result = "Task cancelled before execution"
		sm.mu.Unlock()
		return
	default:
	}

	// Run tool loop via Fantasy agent
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	sm.mu.RUnlock()

	runLoop := sm.getRunLoop()
	var loopResult *ToolLoopResult
	var err error
	if runLoop == nil {
		err = ErrRunLoopNotConfigured
	} else {
		taskCtx := withDelegationSessionKey(withDelegationContext(ctx, task.ID, task.Depth), delegationSessionKeyFromContext(ctx))
		loopResult, err = runLoop(taskCtx, ToolLoopConfig{
			Model:         sm.model,
			ModelID:       sm.defaultModel,
			Tools:         tools,
			Bus:           sm.bus,
			MaxIterations: maxIter,
		}, systemPrompt, task.Task, task.OriginChannel, task.OriginChatID)
	}

	sm.mu.Lock()
	var result *ToolResult
	iterations := 0
	resultChars := 0
	errText := ""
	finalStatus := task.Status
	defer func() {
		if n := sm.activeChildren[task.ParentTaskID]; n <= 1 {
			delete(sm.activeChildren, task.ParentTaskID)
		} else {
			sm.activeChildren[task.ParentTaskID] = n - 1
		}
		finalStatus = task.Status
		resultChars = len(task.Result)
		sm.mu.Unlock()
		sm.emitAudit(ctx, DelegationAuditEvent{
			TaskID:         task.ID,
			ParentTaskID:   task.ParentTaskID,
			Mode:           "spawn",
			Depth:          task.Depth,
			Status:         finalStatus,
			Label:          task.Label,
			DelegatedScope: task.DelegatedScope,
			KeptWork:       task.KeptWork,
			Iterations:     iterations,
			ResultChars:    resultChars,
			Error:          errText,
			OriginChannel:  task.OriginChannel,
			OriginChatID:   task.OriginChatID,
		})
		if callback != nil && result != nil {
			callback(ctx, result)
		}
	}()

	if err != nil {
		task.Status = "failed"
		task.Result = fmt.Sprintf("Error: %v", err)
		errText = err.Error()
		if ctx.Err() != nil {
			task.Status = "cancelled"
			task.Result = "Task cancelled during execution"
		}
		result = &ToolResult{
			ForLLM:  task.Result,
			ForUser: "",
			Silent:  false,
			IsError: true,
			Async:   false,
			Err:     err,
		}
	} else {
		task.Status = "completed"
		task.Result = loopResult.Content
		iterations = loopResult.Iterations
		result = &ToolResult{
			ForLLM:  fmt.Sprintf("Subagent '%s' completed (iterations: %d): %s", task.Label, loopResult.Iterations, loopResult.Content),
			ForUser: loopResult.Content,
			Silent:  false,
			IsError: false,
			Async:   false,
		}
	}

	// Send announce message back to main agent
	if sm.bus != nil {
		announceContent := fmt.Sprintf("Task '%s' completed.\n\nResult:\n%s", task.Label, task.Result)
		sm.bus.PublishInbound(bus.InboundMessage{
			Channel:  "system",
			SenderID: fmt.Sprintf("subagent:%s", task.ID),
			ChatID:   fmt.Sprintf("%s:%s", task.OriginChannel, task.OriginChatID),
			Content:  announceContent,
		})
	}
}

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	if !ok || task == nil {
		return nil, false
	}
	copied := *task
	return &copied, true
}

func (sm *SubagentManager) ListTasks() []*SubagentTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tasks := make([]*SubagentTask, 0, len(sm.tasks))
	for _, task := range sm.tasks {
		if task == nil {
			continue
		}
		copied := *task
		tasks = append(tasks, &copied)
	}
	return tasks
}

// SubagentTool executes a subagent task synchronously and returns the result.
type SubagentTool struct {
	manager       *SubagentManager
	originChannel string
	originChatID  string
}

func NewSubagentTool(manager *SubagentManager) *SubagentTool {
	return &SubagentTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *SubagentTool) Name() string {
	return "subagent"
}

func (t *SubagentTool) Description() string {
	return "Execute a subagent task synchronously and return the result. Use this for delegating specific tasks to an independent agent instance. Returns execution summary to user and full details to LLM."
}

func (t *SubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"delegated_scope": map[string]interface{}{
				"type":        "string",
				"description": "What part of the parent task is being delegated. Required for nested delegation.",
			},
			"kept_work": map[string]interface{}{
				"type":        "string",
				"description": "What work remains with the delegator. Required for nested delegation.",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SubagentTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *SubagentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	task, ok := args["task"].(string)
	if !ok {
		return ErrorResult("task is required").WithError(fmt.Errorf("task parameter is required"))
	}

	label, _ := args["label"].(string)
	delegatedScope, _ := args["delegated_scope"].(string)
	keptWork, _ := args["kept_work"].(string)

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured").WithError(fmt.Errorf("manager is nil"))
	}
	originChannel, originChatID := ResolveExecutionTarget(ctx, t.originChannel, t.originChatID)

	sm := t.manager
	parentTaskID := delegationTaskIDFromContext(ctx)
	if parentTaskID == "" {
		parentTaskID = "root"
	}
	parentDepth := delegationDepthFromContext(ctx)
	childDepth := parentDepth + 1
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	maxDepth := sm.maxDepth
	sm.mu.RUnlock()
	if childDepth > maxDepth {
		return ErrorResult(fmt.Sprintf("delegation depth exceeded: %d > %d", childDepth, maxDepth))
	}
	if parentDepth > 0 {
		if strings.TrimSpace(delegatedScope) == "" || strings.TrimSpace(keptWork) == "" {
			return ErrorResult("nested delegation requires delegated_scope and kept_work")
		}
	}
	if keptWork != "" {
		slog.Debug("delegation scope-reduction",
			"parent_depth", parentDepth,
			"child_depth", childDepth,
			"kept_work", keptWork,
			"delegated_scope", delegatedScope,
		)
	}

	systemPrompt := "You are a subagent operating with main-loop control flow. Execute actions via tools, call discovered tools directly, and provide a clear concise result."

	sm.mu.Lock()
	if sm.activeChildren[parentTaskID] >= sm.maxFanout {
		sm.mu.Unlock()
		return ErrorResult(fmt.Sprintf("delegation fanout exceeded for %s: %d >= %d", parentTaskID, sm.activeChildren[parentTaskID], sm.maxFanout))
	}
	sm.activeChildren[parentTaskID]++
	sm.mu.Unlock()
	defer func() {
		sm.mu.Lock()
		if n := sm.activeChildren[parentTaskID]; n <= 1 {
			delete(sm.activeChildren, parentTaskID)
		} else {
			sm.activeChildren[parentTaskID] = n - 1
		}
		sm.mu.Unlock()
	}()

	taskID := fmt.Sprintf("subagent-sync-%d", time.Now().UnixNano())
	taskCtx := withDelegationSessionKey(withDelegationContext(ctx, taskID, childDepth), SessionKeyFromContext(ctx))
	sm.emitAudit(ctx, DelegationAuditEvent{
		TaskID:         taskID,
		ParentTaskID:   parentTaskID,
		Mode:           "sync",
		Depth:          childDepth,
		Status:         "created",
		Label:          label,
		DelegatedScope: delegatedScope,
		KeptWork:       keptWork,
		OriginChannel:  originChannel,
		OriginChatID:   originChatID,
	})

	runLoop := sm.getRunLoop()
	if runLoop == nil {
		return ErrorResult("Subagent runtime is not configured").WithError(ErrRunLoopNotConfigured)
	}
	loopResult, err := runLoop(taskCtx, ToolLoopConfig{
		Model:         sm.model,
		ModelID:       sm.defaultModel,
		Tools:         tools,
		Bus:           sm.bus,
		MaxIterations: maxIter,
	}, systemPrompt, task, originChannel, originChatID)

	if err != nil {
		sm.emitAudit(ctx, DelegationAuditEvent{
			TaskID:         taskID,
			ParentTaskID:   parentTaskID,
			Mode:           "sync",
			Depth:          childDepth,
			Status:         "failed",
			Label:          label,
			DelegatedScope: delegatedScope,
			KeptWork:       keptWork,
			Error:          err.Error(),
			OriginChannel:  originChannel,
			OriginChatID:   originChatID,
		})
		return ErrorResult(fmt.Sprintf("Subagent execution failed: %v", err)).WithError(err)
	}

	// ForUser: Brief summary for user (truncated if too long)
	userContent := loopResult.Content
	maxUserLen := 500
	if len(userContent) > maxUserLen {
		userContent = userContent[:maxUserLen] + "..."
	}

	// ForLLM: Full execution details
	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	llmContent := fmt.Sprintf("Subagent task completed:\nLabel: %s\nIterations: %d\nResult: %s",
		labelStr, loopResult.Iterations, loopResult.Content)
	sm.emitAudit(ctx, DelegationAuditEvent{
		TaskID:         taskID,
		ParentTaskID:   parentTaskID,
		Mode:           "sync",
		Depth:          childDepth,
		Status:         "completed",
		Label:          label,
		DelegatedScope: delegatedScope,
		KeptWork:       keptWork,
		Iterations:     loopResult.Iterations,
		ResultChars:    len(loopResult.Content),
		OriginChannel:  originChannel,
		OriginChatID:   originChatID,
	})

	return &ToolResult{
		ForLLM:  llmContent,
		ForUser: userContent,
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}
