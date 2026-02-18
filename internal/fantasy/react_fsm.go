package fantasy

import (
	"sync"
	"time"
)

// ReActState represents the execution state of the agent loop.
//
// NOTE: This is intentionally generic and lives in the vendored fantasy module.
// Budgetsmith-specific persistence and instrumentation should be implemented via
// interfaces/hooks in the Budgetsmith repo.
type ReActState string

const (
	ReActStateInit           ReActState = "init"
	ReActStatePrepareStep    ReActState = "prepare_step"
	ReActStateLLMCall        ReActState = "llm_call"
	ReActStateToolValidation ReActState = "tool_validation"
	ReActStateToolExecution  ReActState = "tool_execution"
	ReActStateAppendMessages ReActState = "append_messages"
	ReActStateStopCheck      ReActState = "stop_check"
	ReActStateDone           ReActState = "done"
	ReActStateError          ReActState = "error"
)

// ReActTrigger is the discrete event that causes a state transition.
type ReActTrigger string

const (
	ReActTriggerStart             ReActTrigger = "start"
	ReActTriggerPrepared          ReActTrigger = "prepared"
	ReActTriggerLLMResponded      ReActTrigger = "llm_responded"
	ReActTriggerToolsValidated    ReActTrigger = "tools_validated"
	ReActTriggerToolsExecuted     ReActTrigger = "tools_executed"
	ReActTriggerMessagesAppended  ReActTrigger = "messages_appended"
	ReActTriggerStopConditionMet  ReActTrigger = "stop_condition_met"
	ReActTriggerContinue          ReActTrigger = "continue"
	ReActTriggerFinished          ReActTrigger = "finished"
	ReActTriggerErrored           ReActTrigger = "errored"
	ReActTriggerRecoveredContinue ReActTrigger = "recovered_continue"
)

// ReActTransition is a single transition taken by the agent loop.
type ReActTransition struct {
	From      ReActState     `json:"from"`
	To        ReActState     `json:"to"`
	Trigger   ReActTrigger   `json:"trigger"`
	At        time.Time      `json:"at"`
	StepIndex int            `json:"step_index"`
	Meta      map[string]any `json:"meta,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// ReActTransitionLog is an append-only log of state transitions.
// It is safe for concurrent use.
type ReActTransitionLog struct {
	mu   sync.Mutex
	list []ReActTransition
}

func NewReActTransitionLog() *ReActTransitionLog {
	return &ReActTransitionLog{}
}

func (l *ReActTransitionLog) Append(t ReActTransition) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, t)
}

func (l *ReActTransitionLog) Snapshot() []ReActTransition {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ReActTransition, len(l.list))
	copy(out, l.list)
	return out
}
