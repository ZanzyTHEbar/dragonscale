package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type secureBusTestDB struct {
	delegate *delegate.LibSQLDelegate
}

func newSecureBusTestDB(t *testing.T) *secureBusTestDB {
	t.Helper()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })
	return &secureBusTestDB{delegate: d}
}

func newSecureBusConversation(t *testing.T, q *sqlc.Queries) ids.UUID {
	t.Helper()
	id := ids.New()
	title := "securebus-test-conv"
	_, err := q.CreateAgentConversation(t.Context(), sqlc.CreateAgentConversationParams{
		ID:    id,
		Title: &title,
	})
	require.NoError(t, err)
	return id
}

type countingTool struct {
	calls atomic.Int32
	text  string
	err   bool
}

func (t *countingTool) Name() string        { return "echo" }
func (t *countingTool) Description() string { return "echo" }
func (t *countingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *countingTool) Execute(_ context.Context, args map[string]interface{}) *tools.ToolResult {
	t.calls.Add(1)
	text, _ := args["text"].(string)
	if t.text != "" {
		text = t.text
	}
	return &tools.ToolResult{ForLLM: text, IsError: t.err}
}

func makeSecureBusRuntimeFixture(t *testing.T, tool tools.Tool, policy securebus.PolicyConfig) (SecureBusToolRuntime, *sqlc.Queries, KVDelegate, ids.UUID) {
	t.Helper()
	db := newSecureBusTestDB(t)
	q := db.delegate.Queries()
	convID := newSecureBusConversation(t, q)
	stateStore := NewStateStore(q)
	run, err := stateStore.CreateRun(t.Context(), convID)
	require.NoError(t, err)

	kv := NewDelegateKV(db.delegate, "securebus-runtime-test")
	auditCh := make(chan *memory.AuditEntry, 16)
	auditDone := make(chan struct{})
	al := &AgentLoop{memDelegate: db.delegate, auditChan: auditCh, auditDone: auditDone}
	go al.auditWorker(t.Context(), auditCh, auditDone)
	t.Cleanup(func() {
		close(auditCh)
		<-auditDone
	})
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name != tool.Name() {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		if name != tool.Name() {
			return &tools.ToolResult{ForLLM: "tool not found", IsError: true}
		}
		return tool.Execute(ctx, args)
	}
	auditSink := newSecureBusAuditSink(al.enqueueAuditEntry)
	bus := securebus.New(securebus.BusConfig{Policy: policy, Workers: 1}, nil, capLookup, executor, auditSink)
	t.Cleanup(bus.Close)

	return SecureBusToolRuntime{
		Offloader: OffloadingToolRuntime{
			KV:             kv,
			Queries:        q,
			ConversationID: convID,
			RunID:          run.ID,
			ThresholdChars: 4_000,
			ChunkChars:     2_000,
		},
		Bus:        bus,
		SessionKey: "securebus-test-session",
		StateStore: stateStore,
		RunID:      run.ID,
	}, q, kv, run.ID
}

func listAuditEntriesBySession(t *testing.T, q *sqlc.Queries, sessionKey string) []sqlc.AgentAuditLog {
	t.Helper()
	rows, err := q.ListAuditEntriesBySession(t.Context(), sqlc.ListAuditEntriesBySessionParams{
		AgentID:    pkg.NAME,
		SessionKey: sessionKey,
		Lim:        32,
	})
	require.NoError(t, err)
	return rows
}

func waitForSecureBusAuditEvent(t *testing.T, q *sqlc.Queries, sessionKey, action, toolCallID string) sqlc.AgentAuditLog {
	t.Helper()
	var matched sqlc.AgentAuditLog
	require.Eventually(t, func() bool {
		rows := listAuditEntriesBySession(t, q, sessionKey)
		for _, row := range rows {
			if row.Action == action && row.ToolCallID == toolCallID {
				matched = row
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)
	return matched
}

func TestRepairToolCallInputRepairsDirectExecPlaceholder(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "exec",
			Input:      `{"command":":"}`,
		},
		"Run the command 'echo progressive-test-marker' and tell me the output.",
	)

	if !strings.Contains(got.Input, `"echo progressive-test-marker"`) {
		t.Fatalf("expected repaired exec command, got %q", got.Input)
	}
}

func TestRepairToolCallInputForcesExplicitExecCommand(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "exec",
			Input:      `{"command":":true"}`,
		},
		"Run the command 'echo dragonscale-eval-test' and tell me the output.",
	)

	if !strings.Contains(got.Input, `"echo dragonscale-eval-test"`) {
		t.Fatalf("expected forced exec command, got %q", got.Input)
	}
}

func TestRepairToolCallInputRepairsNestedToolCallPlaceholder(t *testing.T) {
	t.Parallel()

	got := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "tool_call",
			Input:      `{"tool_name":"skill_read","arguments":{"name":":"}}`,
		},
		"Read the 'eval-test-skill' skill and tell me what greeting templates it provides.",
	)

	if !strings.Contains(got.Input, `"eval-test-skill"`) {
		t.Fatalf("expected repaired nested skill name, got %q", got.Input)
	}
}

func TestRepairToolCallInputRepairsWriteAndListPlaceholders(t *testing.T) {
	t.Parallel()

	write := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-write",
			ToolName:   "write_file",
			Input:      `{"path":"}","content":","}`,
		},
		"Create a file called project/readme.txt with 'Project initialized'. Then list the project directory to verify it exists.",
	)
	if !strings.Contains(write.Input, `"project/readme.txt"`) || !strings.Contains(write.Input, `"Project initialized"`) {
		t.Fatalf("expected repaired write args, got %q", write.Input)
	}

	list := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-list",
			ToolName:   "list_dir",
			Input:      `{}`,
		},
		"Create a file called project/readme.txt with 'Project initialized'. Then list the project directory to verify it exists.",
	)
	if !strings.Contains(list.Input, `"project"`) {
		t.Fatalf("expected repaired list_dir path, got %q", list.Input)
	}
}

func TestRepairToolCallInputRepairsReadBackAndFetchPlaceholders(t *testing.T) {
	t.Parallel()

	read := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-read",
			ToolName:   "read_file",
			Input:      `{"path":"}"}`,
		},
		"Create a file called test_steps.txt with the content 'step test', then read it back to confirm.",
	)
	if !strings.Contains(read.Input, `"test_steps.txt"`) {
		t.Fatalf("expected repaired read_file path, got %q", read.Input)
	}

	fetch := repairToolCallInput(
		fantasy.ToolCallContent{
			ToolCallID: "call-fetch",
			ToolName:   "web_fetch",
			Input:      `{"url":".example.com"}`,
		},
		"Fetch the contents of https://example.com and tell me the title of the page.",
	)
	if !strings.Contains(fetch.Input, `"https://example.com"`) {
		t.Fatalf("expected repaired web_fetch url, got %q", fetch.Input)
	}
}

func TestSanitizePolicyErrorPreservesSafeExecErrors(t *testing.T) {
	t.Parallel()

	raw := "command timed out after 8s"
	if got := sanitizePolicyError(raw); got != raw {
		t.Fatalf("expected timeout text to survive sanitization, got %q", got)
	}
}

func TestSanitizePolicyErrorRedactsPolicyViolations(t *testing.T) {
	t.Parallel()

	got := sanitizePolicyError("filesystem access denied: /etc/passwd")
	if got != "policy violation: filesystem access denied" {
		t.Fatalf("expected redacted policy text, got %q", got)
	}
}

func TestSecureBusToolRuntime_ExecutesToolExactlyOnce(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, _, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, int32(1), tool.calls.Load())
	assert.Equal(t, "hello", results[0].Result.(fantasy.ToolResultOutputContentText).Text)
}

func TestSecureBusToolRuntime_ExecutesEachToolCallOnce(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, _, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{
			{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"one"}`},
			{ToolCallID: "call-2", ToolName: "echo", Input: `{"text":"two"}`},
		},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, int32(2), tool.calls.Load())
	assert.Equal(t, "one", results[0].Result.(fantasy.ToolResultOutputContentText).Text)
	assert.Equal(t, "two", results[1].Result.(fantasy.ToolResultOutputContentText).Text)
}

func TestSecureBusToolRuntime_LeakRedactionIsPersisted(t *testing.T) {
	t.Parallel()

	secret := "AKIAIOSFODNN7EXAMPLE"
	tool := &countingTool{text: "result: " + secret}
	runtime, q, kv, runID := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"ignored"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, int32(1), tool.calls.Load())

	textResult, ok := results[0].Result.(fantasy.ToolResultOutputContentText)
	require.True(t, ok)
	assert.NotContains(t, textResult.Text, secret)

	rows, err := q.ListAgentToolResultsByRunID(t.Context(), sqlc.ListAgentToolResultsByRunIDParams{RunID: runID, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Preview)
	assert.NotContains(t, *rows[0].Preview, secret)

	persisted, err := kv.Get(t.Context(), rows[0].FullKey)
	require.NoError(t, err)
	assert.NotContains(t, string(persisted), secret)
}

func TestSecureBusToolRuntime_PersistsToolExecutionErrorResult(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "boom", err: true}
	runtime, q, kv, runID := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 2),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, int32(1), tool.calls.Load())

	errorResult, ok := results[0].Result.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	assert.Equal(t, "tool execution denied", errorResult.Error.Error())

	rows, err := q.ListAgentToolResultsByRunID(t.Context(), sqlc.ListAgentToolResultsByRunIDParams{RunID: runID, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Preview)
	assert.Contains(t, *rows[0].Preview, "tool execution denied")

	persisted, err := kv.Get(t.Context(), rows[0].FullKey)
	require.NoError(t, err)
	assert.Contains(t, string(persisted), `"type":"error"`)
	assert.Contains(t, string(persisted), "tool execution denied")
}

func TestSecureBusToolRuntime_PersistsInvalidArgsErrorResult(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, q, kv, runID := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)

	errorResult, ok := results[0].Result.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	assert.Contains(t, errorResult.Error.Error(), "policy violation")
	assert.Equal(t, int32(0), tool.calls.Load())

	rows, err := q.ListAgentToolResultsByRunID(t.Context(), sqlc.ListAgentToolResultsByRunIDParams{RunID: runID, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Preview)
	assert.Contains(t, *rows[0].Preview, "policy violation")

	persisted, err := kv.Get(t.Context(), rows[0].FullKey)
	require.NoError(t, err)
	assert.Contains(t, string(persisted), `"type":"error"`)
	assert.Contains(t, string(persisted), "policy violation")
}

func TestSecureBusToolRuntime_PersistsBusNativeAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec", "call-1")
	require.Equal(t, "echo", row.Target)
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.Equal(t, string(itr.CmdToolExec), payload.CommandType)
	assert.Equal(t, "echo", payload.ToolName)
	assert.Equal(t, "call-1", payload.ToolCallID)
	assert.NotEmpty(t, payload.RequestID)
	require.NotNil(t, row.DurationMs)
	assert.GreaterOrEqual(t, *row.DurationMs, int64(0))
	assert.True(t, row.Success)
}

func TestSecureBusToolRuntime_PersistsBusNativePolicyViolationAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())
	req := itr.NewToolExecRequest("req-policy", runtime.SessionKey, "call-policy", "echo", `{}`)
	req.Depth = 255

	resp := runtime.Bus.Execute(t.Context(), req)
	assert.True(t, resp.IsError)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_policy_violation", "call-policy")
	assert.Equal(t, "echo", row.Target)
	assert.False(t, row.Success)
	assert.Contains(t, row.ErrorMsg, "recursion depth")
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.IsError)
	assert.NotEmpty(t, payload.PolicyViolation)
	assert.Equal(t, "echo", payload.ToolName)
}

func TestSecureBusToolRuntime_PersistsBusNativeToolErrorAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "boom", err: true}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-error", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_error", "call-error")
	assert.Equal(t, "echo", row.Target)
	assert.False(t, row.Success)
	assert.Equal(t, "boom", row.ErrorMsg)
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.IsError)
	assert.Equal(t, "boom", payload.ExecutionError)
	assert.Equal(t, "", payload.PolicyViolation)
	assert.False(t, payload.LeakDetected)
}

func TestSecureBusToolRuntime_PersistsBusNativeLeakAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "result: AKIAIOSFODNN7EXAMPLE"}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-leak", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_leak", "call-leak")
	assert.Equal(t, "echo", row.Target)
	assert.True(t, row.Success)
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.LeakDetected)
	assert.False(t, payload.IsError)
}

func TestSecureBusAuditSink_WriteReturnsErrorWhenEnqueueFails(t *testing.T) {
	t.Parallel()

	var captured *memory.AuditEntry
	sink := newSecureBusAuditSink(func(entry *memory.AuditEntry) bool {
		captured = entry
		return false
	})
	require.NotNil(t, sink)

	err := sink.Write(securebus.AuditEvent{
		RequestID:   "req-drop",
		SessionKey:  "sink-session",
		ToolCallID:  "call-drop",
		ToolName:    "echo",
		CommandType: string(itr.CmdToolExec),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "securebus audit enqueue failed")
	require.NotNil(t, captured)
	assert.Equal(t, "securebus_tool_exec", captured.Action)
	assert.Equal(t, "echo", captured.Target)
	assert.Equal(t, "call-drop", captured.ToolCallID)
}
