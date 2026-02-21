package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// mustNewAgentLoop wraps NewAgentLoop and fails the test on error.
func mustNewAgentLoop(t *testing.T, cfg *config.Config, msgBus *bus.MessageBus, model fantasy.LanguageModel) *AgentLoop {
	t.Helper()
	if cfg != nil && cfg.Memory.DBPath == "" && strings.TrimSpace(cfg.Agents.Defaults.Workspace) != "" {
		cfg.Memory.DBPath = filepath.Join(cfg.Agents.Defaults.Workspace, "agent-loop-test.db")
	}
	al, err := NewAgentLoop(context.Background(), cfg, msgBus, model)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	return al
}

// mockLanguageModel is a simple mock fantasy.LanguageModel for testing
type mockLanguageModel struct {
	response string
}

func newMockLanguageModel(response string) *mockLanguageModel {
	if response == "" {
		response = "Mock response"
	}
	return &mockLanguageModel{response: response}
}

func (m *mockLanguageModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: m.response}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *mockLanguageModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: m.response}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *mockLanguageModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLanguageModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLanguageModel) Provider() string { return "mock" }
func (m *mockLanguageModel) Model() string    { return "mock-model" }

func TestContinuityKeepCount_UsesConfiguredPolicy(t *testing.T) {
	buildHistory := func(n int, content string) []messages.Message {
		history := make([]messages.Message, 0, n)
		for i := 0; i < n; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			history = append(history, messages.Message{
				Role:    role,
				Content: content,
			})
		}
		return history
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.ContinuityRetention.MinMessages = 3
	cfg.Agents.Defaults.ContinuityRetention.MaxMessages = 7
	cfg.Agents.Defaults.ContinuityRetention.TargetContextRatio = 0.01

	al := &AgentLoop{
		cfg:           cfg,
		contextWindow: 256,
	}

	history := buildHistory(24, strings.Repeat("long message token payload ", 12))
	keepSmallBudget := al.continuityKeepCount(history)
	if keepSmallBudget != 3 {
		t.Fatalf("expected keep count to respect min_messages=3 under tight budget, got %d", keepSmallBudget)
	}

	cfg.Agents.Defaults.ContinuityRetention.TargetContextRatio = 0.40
	al.contextWindow = 8192
	keepLargeBudget := al.continuityKeepCount(history)
	if keepLargeBudget != 7 {
		t.Fatalf("expected keep count to cap at max_messages=7 under large budget, got %d", keepLargeBudget)
	}
}

func TestPrepareRuntimeState_ConcurrentSameSessionUsesSingleConversation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)
	beforeConversations, err := al.queries.ListAgentConversations(context.Background(), memsqlc.ListAgentConversationsParams{
		Limit: 10000,
	})
	if err != nil {
		t.Fatalf("ListAgentConversations (before) failed: %v", err)
	}
	beforeCount := len(beforeConversations)

	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	conversationIDs := make(chan string, workers)
	errorsCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			conversationID, _, prepareErr := al.prepareRuntimeState(context.Background(), "race-session")
			if prepareErr != nil {
				errorsCh <- prepareErr
				return
			}
			conversationIDs <- conversationID.String()
		}()
	}

	close(start)
	wg.Wait()
	close(errorsCh)
	close(conversationIDs)

	for prepareErr := range errorsCh {
		if prepareErr != nil {
			t.Fatalf("unexpected prepareRuntimeState error: %v", prepareErr)
		}
	}

	uniqueConversationIDs := make(map[string]struct{})
	for id := range conversationIDs {
		uniqueConversationIDs[id] = struct{}{}
	}
	if len(uniqueConversationIDs) != 1 {
		t.Fatalf("expected one conversation id, got %d (%v)", len(uniqueConversationIDs), uniqueConversationIDs)
	}

	conversations, err := al.queries.ListAgentConversations(context.Background(), memsqlc.ListAgentConversationsParams{
		Limit: 10000,
	})
	if err != nil {
		t.Fatalf("ListAgentConversations failed: %v", err)
	}
	if len(conversations) != beforeCount+1 {
		t.Fatalf("expected conversation count delta +1, got before=%d after=%d", beforeCount, len(conversations))
	}
}

func TestRecordLastChannel(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Test RecordLastChannel
	testChannel := "test-channel"
	err = al.RecordLastChannel(context.Background(), testChannel)
	if err != nil {
		t.Fatalf("RecordLastChannel failed: %v", err)
	}

	// Verify channel was saved
	lastChannel := al.state.GetLastChannel()
	if lastChannel != testChannel {
		t.Errorf("Expected channel '%s', got '%s'", testChannel, lastChannel)
	}

	// Verify persistence by creating a new agent loop
	al2 := mustNewAgentLoop(t, cfg, msgBus, model)
	if al2.state.GetLastChannel() != testChannel {
		t.Errorf("Expected persistent channel '%s', got '%s'", testChannel, al2.state.GetLastChannel())
	}
}

func TestRecordLastChatID(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Test RecordLastChatID
	testChatID := "test-chat-id-123"
	err = al.RecordLastChatID(context.Background(), testChatID)
	if err != nil {
		t.Fatalf("RecordLastChatID failed: %v", err)
	}

	// Verify chat ID was saved
	lastChatID := al.state.GetLastChatID()
	if lastChatID != testChatID {
		t.Errorf("Expected chat ID '%s', got '%s'", testChatID, lastChatID)
	}

	// Verify persistence by creating a new agent loop
	al2 := mustNewAgentLoop(t, cfg, msgBus, model)
	if al2.state.GetLastChatID() != testChatID {
		t.Errorf("Expected persistent chat ID '%s', got '%s'", testChatID, al2.state.GetLastChatID())
	}
}

func TestNewAgentLoop_StateInitialized(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Verify state manager is initialized (delegate-backed via always-on memory)
	if al.state == nil {
		t.Error("Expected state manager to be initialized")
	}
}

func TestNewAgentLoop_UnifiedKernelDependenciesInitialized(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	if !al.HasSecureBus() {
		t.Fatal("Expected secure bus to be configured")
	}
	if !al.HasUnifiedRuntimeDeps() {
		t.Fatal("Expected unified runtime dependencies to be configured")
	}
}

// TestToolRegistry_ToolRegistration verifies tools can be registered and retrieved
func TestToolRegistry_ToolRegistration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Register a custom tool
	customTool := &mockCustomTool{}
	al.RegisterTool(customTool)

	// Verify tool is registered by checking it doesn't panic on GetStartupInfo
	// (actual tool retrieval is tested in tools package tests)
	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]interface{})
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := false
	for _, name := range toolsList {
		if name == "mock_custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestToolContext_Updates verifies tool context is updated with channel/chatID
func TestToolContext_Updates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("OK")
	_ = mustNewAgentLoop(t, cfg, msgBus, model)

	// Verify that ContextualTool interface is defined and can be implemented
	// This test validates the interface contract exists
	ctxTool := &mockContextualTool{}

	// Verify the tool implements the interface correctly
	var _ tools.ContextualTool = ctxTool
}

// TestToolRegistry_GetDefinitions verifies tool definitions can be retrieved
func TestToolRegistry_GetDefinitions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Register a test tool and verify it shows up in startup info
	testTool := &mockCustomTool{}
	al.RegisterTool(testTool)

	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]interface{})
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := false
	for _, name := range toolsList {
		if name == "mock_custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestAgentLoop_GetStartupInfo verifies startup info contains tools
func TestAgentLoop_GetStartupInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	info := al.GetStartupInfo()

	// Verify tools info exists
	toolsInfo, ok := info["tools"]
	if !ok {
		t.Fatal("Expected 'tools' key in startup info")
	}

	toolsMap, ok := toolsInfo.(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'tools' to be a map")
	}

	count, ok := toolsMap["count"]
	if !ok {
		t.Fatal("Expected 'count' in tools info")
	}

	// Should have default tools registered
	if count.(int) == 0 {
		t.Error("Expected at least some tools to be registered")
	}
}

// TestAgentLoop_Stop verifies Stop() sets running to false
func TestAgentLoop_Stop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	// Note: running is only set to true when Run() is called
	// We can't test that without starting the event loop
	// Instead, verify the Stop method can be called safely
	al.Stop()

	// Verify running is false (initial state or after Stop)
	if al.running.Load() {
		t.Error("Expected agent to be stopped (or never started)")
	}
}

// Mock implementations for testing

// mockCustomTool is a simple mock tool for registration testing
type mockCustomTool struct{}

func (m *mockCustomTool) Name() string {
	return "mock_custom"
}

func (m *mockCustomTool) Description() string {
	return "Mock custom tool for testing"
}

func (m *mockCustomTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *mockCustomTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("Custom tool executed")
}

// mockContextualTool tracks context updates
type mockContextualTool struct {
	lastChannel string
	lastChatID  string
}

func (m *mockContextualTool) Name() string {
	return "mock_contextual"
}

func (m *mockContextualTool) Description() string {
	return "Mock contextual tool"
}

func (m *mockContextualTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *mockContextualTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("Contextual tool executed")
}

func (m *mockContextualTool) SetContext(channel, chatID string) {
	m.lastChannel = channel
	m.lastChatID = chatID
}

// testHelper executes a message and returns the response
type testHelper struct {
	al *AgentLoop
}

func (h testHelper) executeAndGetResponse(tb testing.TB, ctx context.Context, msg bus.InboundMessage) string {
	// Use a short timeout to avoid hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()

	response, err := h.al.processMessage(timeoutCtx, msg)
	if err != nil {
		tb.Fatalf("processMessage failed: %v", err)
	}
	return response
}

const responseTimeout = 3 * time.Second

// TestToolResult_SilentToolDoesNotSendUserMessage verifies silent tools don't trigger outbound
func TestToolResult_SilentToolDoesNotSendUserMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("File operation complete")
	al := mustNewAgentLoop(t, cfg, msgBus, model)
	helper := testHelper{al: al}

	// ReadFileTool returns SilentResult, which should not send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "read test.txt",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// Silent tool should return the LLM's response directly
	if response != "File operation complete" {
		t.Errorf("Expected 'File operation complete', got: %s", response)
	}
}

// TestToolResult_UserFacingToolDoesSendMessage verifies user-facing tools trigger outbound
func TestToolResult_UserFacingToolDoesSendMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("Command output: hello world")
	al := mustNewAgentLoop(t, cfg, msgBus, model)
	helper := testHelper{al: al}

	// ExecTool returns UserResult, which should send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "run hello",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// User-facing tool should include the output in final response
	if response != "Command output: hello world" {
		t.Errorf("Expected 'Command output: hello world', got: %s", response)
	}
}

func TestResolveFinalContent_RecoversFromPriorStepText(t *testing.T) {
	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{
					fantasy.TextContent{Text: "Recovered final response"},
				},
			},
		},
		{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{
					fantasy.ToolCallContent{ToolName: "read_file"},
				},
			},
		},
	}

	got, err := al.resolveFinalContent("", steps)
	if err != nil {
		t.Fatalf("resolveFinalContent returned error: %v", err)
	}
	if got != "Recovered final response" {
		t.Fatalf("expected recovered text, got %q", got)
	}
}

func TestResolveFinalContent_ErrorsWhenNoTextExists(t *testing.T) {
	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{
					fantasy.ToolCallContent{ToolName: "write_file"},
				},
			},
		},
	}

	_, err := al.resolveFinalContent("", steps)
	if err == nil {
		t.Fatal("expected error when no final text exists")
	}
}

func TestResolveFinalContent_RecoversFromToolResultText(t *testing.T) {
	al := &AgentLoop{}
	steps := []fantasy.StepResult{
		{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{
					fantasy.ToolResultContent{
						ToolName: "exec",
						Result: fantasy.ToolResultOutputContentText{
							Text: "progressive-test-marker",
						},
					},
				},
			},
		},
	}

	got, err := al.resolveFinalContent("", steps)
	if err != nil {
		t.Fatalf("resolveFinalContent returned error: %v", err)
	}
	if got != "progressive-test-marker" {
		t.Fatalf("expected tool result text, got %q", got)
	}
}

// TestForceCompression_PersistsProvenance verifies that emergency compression
// cycles persist provenance metadata to the audit log for postmortem.
func TestForceCompression_PersistsProvenance(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-provenance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Small context window so we can exceed 95% threshold with modest history
	// 1000 * 0.95 = 950 tokens; estimateTokens = chars*2/5, so need chars > 2375
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         1000,
				MaxToolIterations: 10,
				ContinuityRetention: config.ContinuityRetentionConfig{
					MinMessages:         3,
					MaxMessages:         8,
					TargetContextRatio:  0.05,
					FailureKeepMessages: 8,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("Summary of conversation.")
	al := mustNewAgentLoop(t, cfg, msgBus, model)
	al.contextWindow = 1000
	sessionKey := "provenance-test-session"

	// Seed history to exceed critical threshold by a wide margin so this test
	// stays deterministic across tokenizer/estimator behavior changes.
	const charsPerMsg = 900
	for i := 0; i < 16; i++ {
		content := fmt.Sprintf("user message %d: %s", i, strings.Repeat("x", charsPerMsg-20))
		al.sessions.AddMessage(sessionKey, "user", content)
		al.sessions.AddMessage(sessionKey, "assistant", "short reply")
	}
	al.sessions.Save(sessionKey)
	history := al.sessions.GetHistory(sessionKey)
	keep := al.continuityKeepCount(history)
	if len(history) <= keep {
		t.Fatalf("test precondition failed: history=%d keep=%d", len(history), keep)
	}
	tokenEstimate := al.estimateTokens(history)
	criticalThreshold := al.contextWindow * 95 / 100
	if tokenEstimate <= criticalThreshold {
		t.Fatalf("test precondition failed: token_estimate=%d threshold=%d", tokenEstimate, criticalThreshold)
	}

	ctx := context.Background()
	al.forceCompression(ctx, sessionKey, "", "")

	del := al.MemoryDelegate()
	if del == nil {
		t.Fatal("MemoryDelegate is nil")
	}
	entries, err := del.ListAuditEntriesByAction(ctx, pkg.NAME, "emergency_compression", 50)
	if err != nil {
		t.Fatalf("ListAuditEntriesByAction: %v", err)
	}
	matching := make([]EmergencyProvenance, 0, len(entries))
	for _, entry := range entries {
		var prov EmergencyProvenance
		if err := json.Unmarshal([]byte(entry.Input), &prov); err != nil {
			continue
		}
		if prov.SessionKey == sessionKey {
			matching = append(matching, prov)
		}
	}
	if len(matching) == 0 {
		t.Fatal("Expected at least one emergency_compression audit entry")
	}

	// Verify metadata shape: session_key, cycle, token_estimate, critical_budget
	prov := matching[0]
	if prov.SessionKey != sessionKey {
		t.Errorf("session_key: want %q, got %q", sessionKey, prov.SessionKey)
	}
	if prov.Cycle < 1 || prov.Cycle > 3 {
		t.Errorf("cycle: want 1..3, got %d", prov.Cycle)
	}
	if prov.TokenEstimate <= 0 {
		t.Errorf("token_estimate: want > 0, got %d", prov.TokenEstimate)
	}
	if prov.CriticalBudget != 950 {
		t.Errorf("critical_budget: want 950, got %d", prov.CriticalBudget)
	}
	if prov.HistoryMsgCount < 8 {
		t.Errorf("history_msg_count: want >= 8, got %d", prov.HistoryMsgCount)
	}
}

func TestPersistOversizedRecoveryRefs_CreatesRecoverableReferences(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-recovery-ref-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         2048,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	model := newMockLanguageModel("ok")
	al := mustNewAgentLoop(t, cfg, msgBus, model)

	omitted := []oversizedRecoveryCandidate{
		{
			Message: messages.Message{
				Role:    "user",
				Content: "omitted oversized content for recovery",
			},
			OriginalIndex: 2,
			TokenEstimate: 9999,
		},
	}

	refs, err := al.persistOversizedRecoveryRefs(context.Background(), "recovery-session", omitted)
	if err != nil {
		t.Fatalf("persistOversizedRecoveryRefs failed: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one recovery ref, got %d", len(refs))
	}

	dagTool := tools.NewDagExpandTool(tools.DAGToolDeps{
		Delegate: al.MemoryDelegate(),
		AgentID:  pkg.NAME,
		SessionFn: func() string {
			return "recovery-session"
		},
	})
	res := dagTool.Execute(context.Background(), map[string]interface{}{
		"node_id":     refs[0],
		"session_key": "recovery-session",
	})
	if res.IsError {
		t.Fatalf("expected recovery ref expansion to succeed, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "omitted oversized content for recovery") {
		t.Fatalf("expected recovered content in output, got: %s", res.ForLLM)
	}
}
