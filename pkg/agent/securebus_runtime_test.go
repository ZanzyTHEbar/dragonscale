package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
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

type secureBusPolicyTool struct {
	calls atomic.Int32
	name  string
	text  string
	caps  tools.ToolCapabilities
}

func (t *secureBusPolicyTool) Name() string        { return t.name }
func (t *secureBusPolicyTool) Description() string { return t.name }
func (t *secureBusPolicyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
	}
}
func (t *secureBusPolicyTool) Capabilities() tools.ToolCapabilities { return t.caps }
func (t *secureBusPolicyTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	t.calls.Add(1)
	return &tools.ToolResult{ForLLM: t.text}
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

func waitForSecureBusAuditTargetEvent(t *testing.T, q *sqlc.Queries, sessionKey, action, toolCallID, target string) sqlc.AgentAuditLog {
	t.Helper()
	var matched sqlc.AgentAuditLog
	require.Eventually(t, func() bool {
		rows := listAuditEntriesBySession(t, q, sessionKey)
		for _, row := range rows {
			if row.Action == action && row.ToolCallID == toolCallID && row.Target == target {
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
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	testcmp.AssertEqual(t, int32(1), tool.calls.Load())
	testcmp.AssertEqual(t, "hello", results[0].Result.(fantasy.ToolResultOutputContentText).Text)
}

func TestSecureBusToolRuntime_ExecutesEachToolCallOnce(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, _, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{
			{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"one"}`},
			{ToolCallID: "call-2", ToolName: "echo", Input: `{"text":"two"}`},
		},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	testcmp.AssertEqual(t, int32(2), tool.calls.Load())
	testcmp.AssertEqual(t, "one", results[0].Result.(fantasy.ToolResultOutputContentText).Text)
	testcmp.AssertEqual(t, "two", results[1].Result.(fantasy.ToolResultOutputContentText).Text)
}

func TestSecureBusToolRuntime_LeakRedactionIsPersisted(t *testing.T) {
	t.Parallel()

	secret := "AKIAIOSFODNN7EXAMPLE"
	tool := &countingTool{text: "result: " + secret}
	runtime, q, kv, runID := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"ignored"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	testcmp.AssertEqual(t, int32(1), tool.calls.Load())

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

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_leak", "call-1")
	assert.True(t, row.Success)
	assert.NotContains(t, row.ErrorMsg, secret)
	assert.Contains(t, row.ErrorMsg, "[REDACTED:AWS_ACCESS_KEY]")
	require.NotNil(t, row.Output)
	assert.NotContains(t, *row.Output, secret)

	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.LeakDetected)
	assert.False(t, payload.IsError)
	assert.NotContains(t, payload.ExecutionError, secret)
	assert.Contains(t, payload.ExecutionError, "[REDACTED:AWS_ACCESS_KEY]")
}

func TestSecureBusToolRuntime_EnforcesFilesystemPolicyAndPersistsAuditEvents(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	policy := securebus.DefaultPolicyConfig()
	policy.AllowedWorkspace = workspace
	tool := &secureBusPolicyTool{
		name: "read_file",
		text: "workspace file contents",
		caps: tools.ToolCapabilities{Filesystem: []tools.PathRule{{
			Pattern: "**",
			Mode:    "r",
		}}},
	}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, policy)

	allowedInput, err := json.Marshal(map[string]string{"path": "docs/readme.md"})
	require.NoError(t, err)
	allowedResults, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-allow", ToolName: "read_file", Input: string(allowedInput)}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, allowedResults, 1)
	allowedText, ok := allowedResults[0].Result.(fantasy.ToolResultOutputContentText)
	require.True(t, ok)
	testcmp.AssertEqual(t, "workspace file contents", allowedText.Text)
	testcmp.AssertEqual(t, int32(1), tool.calls.Load())

	allowedAudit := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec", "call-allow")
	assert.True(t, allowedAudit.Success)
	require.NotNil(t, allowedAudit.Output)
	var allowedPayload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*allowedAudit.Output), &allowedPayload))
	assert.False(t, allowedPayload.IsError)
	assert.Empty(t, allowedPayload.PolicyViolation)
	testcmp.AssertEqual(t, "read_file", allowedPayload.ToolName)

	outsidePath := filepath.Join(t.TempDir(), "secret.txt")
	deniedInput, err := json.Marshal(map[string]string{"path": outsidePath})
	require.NoError(t, err)
	deniedResults, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 1),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-deny", ToolName: "read_file", Input: string(deniedInput)}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, deniedResults, 1)
	deniedError, ok := deniedResults[0].Result.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	testcmp.AssertEqual(t, "policy violation: filesystem access denied", deniedError.Error.Error())
	assert.Equal(t, int32(1), tool.calls.Load(), "policy deny must happen before tool execution")

	deniedAudit := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_policy_violation", "call-deny")
	assert.False(t, deniedAudit.Success)
	assert.Contains(t, deniedAudit.ErrorMsg, "filesystem access denied")
	require.NotNil(t, deniedAudit.Output)
	var deniedPayload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*deniedAudit.Output), &deniedPayload))
	assert.True(t, deniedPayload.IsError)
	assert.Contains(t, deniedPayload.PolicyViolation, "filesystem access denied")
	testcmp.AssertEqual(t, "read_file", deniedPayload.ToolName)
}

func TestSecureBusToolRuntime_PersistsToolExecutionErrorResult(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "boom", err: true}
	runtime, q, kv, runID := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	results, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 2),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	testcmp.AssertEqual(t, int32(1), tool.calls.Load())

	errorResult, ok := results[0].Result.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	testcmp.AssertEqual(t, "tool execution denied", errorResult.Error.Error())

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
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)

	errorResult, ok := results[0].Result.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	assert.Contains(t, errorResult.Error.Error(), "policy violation")
	testcmp.AssertEqual(t, int32(0), tool.calls.Load())

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

func TestSecureBusToolRuntime_RejectsExecutableProviderTools(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, _, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())
	providerTool := fantasy.NewExecutableProviderTool(
		fantasy.ProviderDefinedTool{ID: "provider.local", Name: "provider_local"},
		func(_ context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse("should not run"), nil
		},
	)

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		[]fantasy.ExecutableProviderTool{providerTool},
		[]fantasy.ToolCallContent{{ToolCallID: "provider-call", ToolName: "provider_local", Input: `{}`}},
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not execute provider-defined tool")
	testcmp.AssertEqual(t, int32(0), tool.calls.Load())
}

func TestSecureBusToolRuntime_PersistsBusNativeAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec", "call-1")
	testcmp.RequireEqual(t, "echo", row.Target)
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	testcmp.AssertEqual(t, string(itr.CmdToolExec), payload.CommandType)
	testcmp.AssertEqual(t, "echo", payload.ToolName)
	testcmp.AssertEqual(t, "call-1", payload.ToolCallID)
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
	testcmp.AssertEqual(t, "echo", row.Target)
	assert.False(t, row.Success)
	assert.Contains(t, row.ErrorMsg, "recursion depth")
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.IsError)
	assert.NotEmpty(t, payload.PolicyViolation)
	testcmp.AssertEqual(t, "echo", payload.ToolName)
}

func TestSecureBusToolRuntime_PersistsBusNativeToolErrorAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "boom", err: true}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-error", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_error", "call-error")
	testcmp.AssertEqual(t, "echo", row.Target)
	assert.False(t, row.Success)
	testcmp.AssertEqual(t, "boom", row.ErrorMsg)
	require.NotNil(t, row.Output)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	assert.True(t, payload.IsError)
	testcmp.AssertEqual(t, "boom", payload.ExecutionError)
	testcmp.AssertEqual(t, "", payload.PolicyViolation)
	assert.False(t, payload.LeakDetected)
}

func TestSecureBusToolRuntime_PersistsBusNativeLeakAuditEvent(t *testing.T) {
	t.Parallel()

	tool := &countingTool{text: "result: AKIAIOSFODNN7EXAMPLE"}
	runtime, q, _, _ := makeSecureBusRuntimeFixture(t, tool, securebus.DefaultPolicyConfig())

	_, err := runtime.Execute(
		fantasy.WithStepIndex(t.Context(), 0),
		nil,
		nil,
		[]fantasy.ToolCallContent{{ToolCallID: "call-leak", ToolName: "echo", Input: `{"text":"hello"}`}},
		nil,
	)
	require.NoError(t, err)

	row := waitForSecureBusAuditEvent(t, q, runtime.SessionKey, "securebus_tool_exec_leak", "call-leak")
	testcmp.AssertEqual(t, "echo", row.Target)
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
	testcmp.AssertEqual(t, "securebus_tool_exec", captured.Action)
	testcmp.AssertEqual(t, "echo", captured.Target)
	testcmp.AssertEqual(t, "call-drop", captured.ToolCallID)
}

func TestToolCallReentersSecureBusForNestedTarget(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	policy := securebus.DefaultPolicyConfig()
	policy.AllowedWorkspace = workspace

	// Register tool_call and a filesystem-capable target tool.
	registry := tools.NewToolRegistry()
	target := &secureBusPolicyTool{
		name: "read_file",
		text: "workspace file contents",
		caps: tools.ToolCapabilities{Filesystem: []tools.PathRule{{Pattern: "**", Mode: "r"}}},
	}
	registry.Register(target)
	registry.RegisterMetaTools()
	_, registered := registry.Get("tool_call")
	require.True(t, registered)

	// Auditing setup.
	db := newSecureBusTestDB(t)
	q := db.delegate.Queries()
	auditCh := make(chan *memory.AuditEntry, 16)
	auditDone := make(chan struct{})
	al := &AgentLoop{memDelegate: db.delegate, auditChan: auditCh, auditDone: auditDone}
	go al.auditWorker(t.Context(), auditCh, auditDone)
	t.Cleanup(func() {
		close(auditCh)
		<-auditDone
	})

	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		tl, ok := registry.Get(name)
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tl), true
	}

	auditSink := newSecureBusAuditSink(al.enqueueAuditEntry)

	var bus *securebus.Bus
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		if bus != nil {
			ctx = tools.WithSecureBusDispatcher(ctx, bus.Execute)
		}
		channel, chatID := tools.ExecutionTargetFromContext(ctx)
		return registry.ExecuteWithContext(ctx, name, args, channel, chatID, nil)
	}
	bus = securebus.New(securebus.BusConfig{Policy: policy, Workers: 1}, nil, capLookup, executor, auditSink)
	t.Cleanup(bus.Close)

	sessionKey := "toolcall-bus-test"
	execCtx := tools.WithSessionKey(t.Context(), sessionKey)

	// ── Allow path: tool_call -> read_file inside workspace ──
	allowedInput, err := json.Marshal(map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{"path": "docs/readme.md"},
	})
	require.NoError(t, err)
	req := itr.NewToolExecRequest(ids.New().String(), sessionKey, "call-allow", "tool_call", string(allowedInput))
	resp := bus.Execute(execCtx, req)
	require.False(t, resp.IsError, "expected allow, got: %s", resp.Result)
	require.Equal(t, "workspace file contents", resp.Result)

	// Audit row for the nested read_file should be persisted with the outer
	// tool_call ID preserved for correlation.
	row := waitForSecureBusAuditTargetEvent(t, q, sessionKey, "securebus_tool_exec", "call-allow", "read_file")
	require.NotNil(t, row.Input)
	require.Contains(t, *row.Input, "docs/readme.md")

	// ── Deny path: tool_call -> read_file outside workspace ──
	outsidePath := filepath.Join(t.TempDir(), "secret.txt")
	deniedInput, err := json.Marshal(map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{"path": outsidePath},
	})
	require.NoError(t, err)
	req = itr.NewToolExecRequest(ids.New().String(), sessionKey, "call-deny", "tool_call", string(deniedInput))
	resp = bus.Execute(execCtx, req)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Result, "policy violation: filesystem access denied")

	// Audit row for the policy violation must exist (the nested read_file
	// request uses the bus entry point which records its own audit row with
	// the given session key).
	require.Eventually(t, func() bool {
		rows := listAuditEntriesBySession(t, q, sessionKey)
		for _, r := range rows {
			if r.Action == "securebus_tool_exec_policy_violation" && r.ToolCallID == "call-deny" && r.Target == "read_file" {
				row = r
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)
	require.False(t, row.Success)
	require.Contains(t, row.ErrorMsg, "filesystem access denied")
}
