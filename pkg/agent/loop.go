// DragonScale - Ultra-lightweight personal AI agent
// Inspired by and based on picoclaw: https://github.com/sipeed/picoclaw
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/channels"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	"github.com/ZanzyTHEbar/dragonscale/pkg/cortex"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/session"
	"github.com/ZanzyTHEbar/dragonscale/pkg/state"
	dragonsync "github.com/ZanzyTHEbar/dragonscale/pkg/sync"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

type AgentLoop struct {
	bus                     *bus.MessageBus
	languageModel           fantasy.LanguageModel
	workspace               string
	model                   string
	contextWindow           int // Maximum context window size in tokens
	maxIterations           int
	sessions                *session.SessionManager
	state                   *state.Manager
	contextBuilder          *ContextBuilder
	activeContextBuilder    *DefaultActiveContextBuilder
	tools                   *tools.ToolRegistry
	memoryStore             *memstore.MemoryStore           // 3-tier MemGPT memory (always initialized)
	memDelegate             memory.MemoryDelegate           // DB delegate (always initialized)
	obsManager              *observation.Manager            // Observational memory (always initialized)
	secureBus               *securebus.Bus                  // ITR SecureBus (always initialized)
	queries                 *memsqlc.Queries                // SQL query surface for runtime persistence
	kvDelegate              KVDelegate                      // KV adapter for offloaded tool results
	stateStore              *StateStore                     // Agent run state persistence
	offloadThresholdChars   int                             // Char threshold for tool result offloading (derived from token config)
	rlmEngine               rlmAnswerer                     // Recursive context reducer for oversized historical segments
	rlmDirectThresholdBytes int                             // Byte threshold before invoking RLM reduction
	conversationIDs         *boundedCache[string, ids.UUID] // Owner: agent_run.go — wrote by prepareRuntimeState, read in prepareRuntimeState/load path
	conversationMu          sync.Mutex                      // serializes conversation creation path
	identitySync            *dragonsync.IdentitySync        // File→DB sync for identity docs (nil if memory disabled)
	activeSessionKey        atomic.Value                    // Owner: agent_run.go — written in runAgentLoop, read by router/toolloop for context routing
	running                 atomic.Bool                     // Owner: loop.go — lifecycle gate controlled by Run/Stop only
	summarizing             sync.Map                        // Owner: summarizer.go — intended for async summarization lockout, currently gated by TODO path
	summarizeFailures       sync.Map                        // Owner: summarizer.go — write/read in forceCompression + summarizeSession error paths
	contextTreeCache        sync.Map                        // Owner: summarizer.go — sessionKey → contextTreeCacheEntry keyed by query and history size
	auditChan               chan *memory.AuditEntry         // Buffered channel for async audit logging; drained by background worker
	auditDone               chan struct{}                   // Closed when audit worker exits
	focusDirty              sync.Map                        // sessionKey → struct{}: set by focus tool callbacks, cleared after context reload
	ctxBlockCache           sync.Map                        // sessionKey → ctxBlockCacheEntry: cached focus + knowledge blocks
	cfg                     *config.Config                  // Stored for subagent factory access
	channelManager          *channels.Manager
	commandRegistry         []SlashCommand
	outputOverride          atomic.Value // Owner: command_handler.go — CLI output redirection target for internal messages
	toolResultSearch        fantasy.AgentTool
	cortex                  *cortex.Cortex
	inflight                sync.WaitGroup
	stopOnce                sync.Once
}

type outputTarget struct {
	Channel string
	ChatID  string
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey     string   // Session identifier for history/context
	Channel        string   // Target channel for tool execution
	ChatID         string   // Target chat ID for tool execution
	SenderID       string   // Originating sender identifier (for logging/audit)
	UserMessage    string   // User message content (may include prefix)
	EnableSummary  bool     // Whether to trigger summarization
	SendResponse   bool     // Whether to send response via bus
	NoHistory      bool     // If true, don't load session history (for heartbeat)
	Streaming      bool     // If true, stream token deltas to bus via OnTextDelta
	ConversationID ids.UUID // Conversation identifier for task tracking
	RunID          ids.UUID // Run identifier for task completion
}

// Option configures AgentLoop creation.
type Option func(*AgentLoop)

// WithChannelManager configures command routing for slash commands.
func WithChannelManager(cm *channels.Manager) Option {
	return func(al *AgentLoop) {
		al.channelManager = cm
	}
}

func NewAgentLoop(ctx context.Context, cfg *config.Config, msgBus *bus.MessageBus, model fantasy.LanguageModel, opts ...Option) (*AgentLoop, error) {
	sandbox := cfg.SandboxPath()
	os.MkdirAll(sandbox, 0755)

	workspace := sandbox
	restrict := cfg.RestrictToSandbox()

	toolsRegistry := createToolRegistry(workspace, restrict, cfg, msgBus)

	subagentManager := tools.NewSubagentManager(model, cfg.Agents.Defaults.Model, workspace, msgBus)
	subagentTools := createToolRegistry(workspace, restrict, cfg, msgBus)
	subagentManager.SetTools(subagentTools)

	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	subagentTool := tools.NewSubagentTool(subagentManager)
	toolsRegistry.Register(subagentTool)
	agenticMapTool := tools.NewAgenticMapTool(subagentManager)

	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)
	contextBuilder.SetContextWindow(cfg.Agents.Defaults.MaxTokens)

	sl := contextBuilder.SkillsLoader()
	toolsRegistry.Register(tools.NewSkillSearchTool(sl))
	toolsRegistry.Register(tools.NewSkillReadTool(sl))
	toolsRegistry.Register(tools.NewSkillTraverseTool(sl))

	// Initialize 3-tier MemGPT memory system (always enabled, fail-fast on error)
	memDBPath := cfg.DBPath()
	os.MkdirAll(filepath.Dir(memDBPath), 0755)

	del, err := delegate.NewFromConfig(cfg.Memory, memDBPath)
	if err != nil {
		return nil, fmt.Errorf("memory delegate init: %w", err)
	}
	if err := del.Init(ctx); err != nil {
		del.Close()
		return nil, fmt.Errorf("memory schema init: %w", err)
	}
	memDelegate := del
	queries := memDelegate.Queries()
	if queries == nil {
		del.Close()
		return nil, fmt.Errorf("memory delegate queries are not initialized")
	}
	kv := NewResilientKV(NewDelegateKV(memDelegate, pkg.NAME))
	stateStore := NewStateStore(queries)

	offloadThreshold := cfg.Memory.OffloadThresholdTokens
	if offloadThreshold <= 0 {
		// Default to ~3% of the context window, floored at 2048 tokens.
		// Small windows (4K-8K) get proportionally tighter thresholds;
		// large windows (128K+) avoid wasteful offloading of short results.
		offloadThreshold = max(cfg.Agents.Defaults.MaxTokens*3/100, 2048)
	}
	chunker := memstore.NewMarkdownChunker(memstore.DefaultMarkdownChunkerConfig())

	embedder, embErr := memstore.NewEmbedderFromConfig(cfg.Memory.Embedding, cfg.Providers)
	if embErr != nil {
		logger.WarnCF("agent", "Failed to create embedding provider, archival search will use FTS5 only",
			map[string]interface{}{"error": embErr.Error()})
	}

	ms := memstore.New(del, chunker, embedder, memstore.Config{
		ContextWindowTokens:    cfg.Agents.Defaults.MaxTokens,
		OffloadThresholdTokens: offloadThreshold,
	})
	contextBuilder.SetMemoryStore(ms)

	memTool := NewMemGPTTool(ms, pkg.NAME, "default")
	toolsRegistry.Register(memTool)
	toolsRegistry.Register(tools.NewObligationTool(memDelegate, pkg.NAME))

	toolsRegistry.Register(tools.NewKeywordSearchTool(ms, pkg.NAME))
	toolsRegistry.Register(tools.NewSemanticSearchTool(ms, pkg.NAME))
	toolsRegistry.Register(tools.NewChunkReadTool(ms, pkg.NAME))
	mapRuntime := tools.NewMapRuntime(queries, pkg.NAME, model, cfg.Agents.Defaults.Model, subagentManager)
	agenticMapTool.SetRuntime(mapRuntime)
	toolsRegistry.Register(agenticMapTool)
	llmMapTool := tools.NewLLMMapTool(model, cfg.Agents.Defaults.Model)
	llmMapTool.SetRuntime(mapRuntime)
	toolsRegistry.Register(llmMapTool)
	toolsRegistry.Register(tools.NewMapRunStatusTool(mapRuntime))
	toolsRegistry.Register(tools.NewMapRunReadTool(mapRuntime))

	// Register memory/search/skill tools on subagent registry so spawned
	// agents can search knowledge, offload results, and use skills.
	subagentMemTool := NewMemGPTTool(ms, pkg.NAME, "default")
	subagentTools.Register(subagentMemTool)
	subagentTools.Register(tools.NewObligationTool(memDelegate, pkg.NAME))
	subagentTools.Register(tools.NewKeywordSearchTool(ms, pkg.NAME))
	subagentTools.Register(tools.NewSemanticSearchTool(ms, pkg.NAME))
	subagentTools.Register(tools.NewChunkReadTool(ms, pkg.NAME))
	subagentTools.Register(tools.NewSkillSearchTool(sl))
	subagentTools.Register(tools.NewSkillReadTool(sl))
	subagentTools.RegisterMetaTools()
	for _, name := range []string{"memory", "read_file", "write_file", "list_dir", "exec"} {
		subagentTools.MarkGateway(name)
	}

	contextBuilder.SetDelegate(del)

	// Identity file sync (disk → DB)
	var idSync *dragonsync.IdentitySync
	identityDir, idErr := config.IdentityDir()
	if idErr != nil {
		logger.WarnCF("agent", "Could not resolve identity dir, identity sync disabled",
			map[string]interface{}{"error": idErr.Error()})
	} else {
		idSync = dragonsync.New(identityDir, pkg.NAME, memDelegate)
		idSync.OnChange = func() { contextBuilder.InvalidateBootstrapCache() }
		if syncErr := idSync.SyncAll(ctx); syncErr != nil {
			logger.WarnCF("agent", "Initial identity sync failed (non-fatal)",
				map[string]interface{}{"error": syncErr.Error()})
		} else {
			logger.InfoCF("agent", "Identity files synced to DB", nil)
		}
		if watchErr := idSync.Watch(ctx); watchErr != nil {
			logger.WarnCF("agent", "Identity file watcher failed to start, using mtime fallback",
				map[string]interface{}{"error": watchErr.Error()})
		}
	}

	// State manager (always delegate-backed)
	stateManager := state.NewManager(workspace, state.WithDelegate(memDelegate))

	// Session manager (always delegate-backed)
	sessionsDir := filepath.Join(workspace, "sessions")
	sessionsManager := session.NewSessionManager(sessionsDir, session.WithSessionDelegate(memDelegate, pkg.NAME))

	// One-shot DAG backfill for pre-existing session histories.
	backfillCtx, cancelBackfill := context.WithTimeout(ctx, 10*time.Second)
	defer cancelBackfill()
	if status, err := dag.BackfillMissingSessionDAGs(backfillCtx, memDelegate, queries, pkg.NAME, dag.DefaultBackfillOptions()); err != nil {
		logger.WarnCF("agent", "DAG backfill failed (non-fatal)",
			map[string]interface{}{"error": err.Error()})
	} else if status != nil {
		logger.InfoCF("agent", "DAG backfill pass completed",
			map[string]interface{}{
				"sessions_scanned":  status.SessionsScanned,
				"snapshots_created": status.SnapshotsCreated,
				"skipped_existing":  status.SkippedExisting,
				"failures":          status.Failures,
				"skipped":           status.Skipped,
			})
	}

	// Meta-tools for progressive disclosure (tool_search + tool_call).
	// tool_search returns full parameter schemas; discovered tools are
	// dynamically promoted to native callables via PrepareStep.
	toolsRegistry.RegisterMetaTools()

	// Gateway tools: always visible to the LLM. Keep this set minimal —
	// dynamic promotion handles everything else after tool_search.
	for _, name := range []string{
		"memory",
		"read_file",
		"write_file",
		"list_dir",
		"exec",
	} {
		toolsRegistry.MarkGateway(name)
	}

	// Observation manager
	callModelFn := func(ctx context.Context, prompt string) (string, error) {
		temp := 0.3
		maxTokens := int64(1024)
		resp, err := model.Generate(ctx, fantasy.Call{
			Prompt: fantasy.Prompt{
				fantasy.NewUserMessage(prompt),
			},
			Temperature:     &temp,
			MaxOutputTokens: &maxTokens,
		})
		if err != nil {
			return "", err
		}
		return resp.Content.Text(), nil
	}
	obsManager := observation.NewManager(memDelegate, pkg.NAME, callModelFn, observation.DefaultManagerConfig())
	rlmEngine, rlmThresholdBytes := newLiveRLMAnswerer(model)

	auditCh := make(chan *memory.AuditEntry, 256)
	auditDone := make(chan struct{})

	al := &AgentLoop{
		bus:                     msgBus,
		languageModel:           model,
		workspace:               workspace,
		model:                   cfg.Agents.Defaults.Model,
		contextWindow:           cfg.Agents.Defaults.MaxTokens,
		maxIterations:           cfg.Agents.Defaults.MaxToolIterations,
		sessions:                sessionsManager,
		state:                   stateManager,
		contextBuilder:          contextBuilder,
		tools:                   toolsRegistry,
		memoryStore:             ms,
		memDelegate:             memDelegate,
		obsManager:              obsManager,
		queries:                 queries,
		kvDelegate:              kv,
		stateStore:              stateStore,
		offloadThresholdChars:   offloadThreshold * 4,
		rlmEngine:               rlmEngine,
		rlmDirectThresholdBytes: rlmThresholdBytes,
		toolResultSearch:        NewToolResultSearchTool(queries, kv),
		conversationIDs:         newBoundedCache[string, ids.UUID](1024),
		identitySync:            idSync,
		summarizing:             sync.Map{},
		auditChan:               auditCh,
		auditDone:               auditDone,
		commandRegistry:         defaultSlashCommands(),
		cfg:                     cfg,
	}
	al.activeContextBuilder = NewDefaultActiveContextBuilder(pkg.NAME, contextBuilder, sessionsManager, memDelegate, ms, queries)

	go al.auditWorker(ctx, auditCh, auditDone)

	for _, apply := range opts {
		if apply != nil {
			apply(al)
		}
	}

	// Focus tools (start_focus / complete_focus)
	sessionKeyFn := func() string {
		if v := al.activeSessionKey.Load(); v != nil {
			return v.(string)
		}
		return ""
	}
	contextBuilder.SetSessionResolver(sessionKeyFn)
	memTool.SetSessionResolver(sessionKeyFn)
	subagentMemTool.SetSessionResolver(sessionKeyFn)
	focusInvalidate := func(sessionKey string) {
		if sessionKey != "" {
			al.focusDirty.Store(sessionKey, struct{}{})
		}
	}
	startFocus := tools.NewStartFocusTool(memDelegate, sessionsManager, sessionKeyFn)
	startFocus.OnChange = focusInvalidate
	toolsRegistry.Register(startFocus)
	completeFocus := tools.NewCompleteFocusTool(memDelegate, sessionsManager, sessionKeyFn)
	completeFocus.OnChange = focusInvalidate
	toolsRegistry.Register(completeFocus)
	toolsRegistry.Register(tools.NewFocusHistoryTool(memDelegate, sessionKeyFn))
	// Wire context into tool_search for focus-aware bias and unified discovery.
	if ts, ok := toolsRegistry.Get("tool_search"); ok {
		if tst, ok := ts.(*tools.ToolSearchTool); ok {
			tst.SetSkillsLoader(contextBuilder.SkillsLoader())
			tst.SetFocusContext(memDelegate, sessionKeyFn)
		}
	}

	// DAG tools: dag_expand, dag_describe, dag_grep (require delegate-backed session)
	dagDeps := tools.DAGToolDeps{
		Queries:   queries,
		Lister:    del,
		Delegate:  del,
		AgentID:   pkg.NAME,
		SessionFn: sessionKeyFn,
	}
	toolsRegistry.Register(tools.NewDagExpandTool(dagDeps))
	toolsRegistry.Register(tools.NewDagDescribeTool(dagDeps))
	toolsRegistry.Register(tools.NewDagGrepTool(dagDeps))

	// Unified runtime invariant: SecureBus is always enabled for tool execution.
	secretStore, err := initSecretStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create secret store: %w", err)
	}
	al.SetupSecureBus(secretStore, securebus.DefaultBusConfig())
	subagentManager.SetRunLoop(MakeUnifiedRunLoopFunc(al))

	// Initialize Cortex autonomous scheduler with foundation tasks.
	// DecayStore and BackfillStore are satisfied by LibSQLDelegate via duck typing.
	var decayStore cortex.DecayStore
	if ds, ok := al.memDelegate.(cortex.DecayStore); ok {
		decayStore = ds
	}
	var backfillStore cortex.BackfillStore
	if bs, ok := al.memDelegate.(cortex.BackfillStore); ok {
		backfillStore = bs
	}
	var embedFn func(ctx context.Context, text string) ([]float32, error)
	if al.memoryStore != nil && al.memoryStore.Embedder() != nil {
		embedder := al.memoryStore.Embedder()
		embedFn = func(ctx context.Context, text string) ([]float32, error) {
			return embedder.Embed(ctx, text)
		}
	}
	// ConsolidationStore for memory graph maintenance
	var consolidationStore cortex.ConsolidationStore
	if cs, ok := al.memDelegate.(cortex.ConsolidationStore); ok {
		consolidationStore = cs
	}
	// PruneStore for permanent deletion of quarantined items
	var pruneStore cortex.PruneStore
	if ps, ok := al.memDelegate.(cortex.PruneStore); ok {
		pruneStore = ps
	}
	// RLStore for reinforcement learning weight updates
	var rlStore cortex.RLStore
	if rs, ok := al.memDelegate.(cortex.RLStore); ok {
		rlStore = rs
	}
	// AuditAnalysisStore for audit log pattern detection
	var auditStore cortex.AuditAnalysisStore
	if aus, ok := al.memDelegate.(cortex.AuditAnalysisStore); ok {
		auditStore = aus
	}
	// DriftStore for domain health monitoring and trend tracking
	var driftStore cortex.DriftStore
	if ds, ok := al.memDelegate.(cortex.DriftMemorySource); ok {
		driftStore = cortex.NewMemoryDriftAdapter(ds)
	}
	cortexTasks := []cortex.Task{
		cortex.NewDecayTask(cortex.DefaultDecayConfig(), decayStore),
		cortex.NewBackfillTask(cortex.DefaultBackfillConfig(), backfillStore, embedFn),
		cortex.NewConsolidationTask(cortex.DefaultConsolidationConfig(), consolidationStore),
		cortex.NewPruneTask(cortex.DefaultPruneConfig(), pruneStore),
		cortex.NewRLTask(rlStore, pkg.NAME),
		cortex.NewAuditAnalysisTask(auditStore),
		cortex.NewDriftTask(cortex.DefaultDriftConfig(), driftStore),
	}
	al.cortex = cortex.New(cortexTasks, 60*time.Second)

	return al, nil
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)
	go al.cortex.Start(ctx)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			al.inflight.Add(1)
			roundTracker := tools.NewMessageSendTracker()
			roundCtx := tools.WithMessageSendTracker(ctx, roundTracker)
			response, err := func() (string, error) {
				defer al.inflight.Done()
				return al.processMessage(roundCtx, msg)
			}()
			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				// Check if the message tool already sent a response during this round.
				// If so, skip publishing to avoid duplicate messages to the user.
				alreadySent := roundTracker.Sent()

				if !alreadySent {
					outMsg := bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					}
					if constants.IsInternalChannel(msg.Channel) {
						if override, ok := al.getOutputTarget(); ok {
							outMsg.Channel = override.Channel
							if override.ChatID != "" {
								outMsg.ChatID = override.ChatID
							}
						}
					}
					al.bus.PublishOutbound(outMsg)
				}
			}
		}
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
	al.stopOnce.Do(func() {
		al.inflight.Wait()

		if al.secureBus != nil {
			al.secureBus.Close()
		}

		if al.auditChan != nil {
			close(al.auditChan)
			<-al.auditDone // wait for audit worker to drain
		}
		if al.sessions != nil {
			al.sessions.Close()
		}
		if al.identitySync != nil {
			al.identitySync.Close()
		}
		if al.memoryStore != nil {
			if err := al.memoryStore.Sync(); err != nil {
				logger.WarnCF("agent", "Failed to sync memory before shutdown",
					map[string]interface{}{"error": err.Error()})
			}
			al.memoryStore.Close()
		}
	})
}

// auditWorker drains the audit channel, accumulating entries and flushing
// either when the batch reaches 32 entries or after 100ms of inactivity.
func (al *AgentLoop) auditWorker(_ context.Context, ch <-chan *memory.AuditEntry, done chan<- struct{}) {
	defer close(done)

	const maxBatch = 32
	const flushInterval = 100 * time.Millisecond

	buf := make([]*memory.AuditEntry, 0, maxBatch)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	// Use context.Background so flushes succeed even during shutdown
	// when the parent context may already be cancelled.
	flush := func() {
		if len(buf) == 0 {
			return
		}
		fCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := al.memDelegate.InsertAuditEntryBatch(fCtx, buf); err != nil {
			logger.WarnCF("agent", "Async audit batch insert failed",
				map[string]interface{}{"count": len(buf), "error": err.Error()})
		}
		cancel()
		buf = buf[:0]
	}

	for {
		select {
		case entry, ok := <-ch:
			if !ok {
				flush()
				return
			}
			buf = append(buf, entry)
			if len(buf) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

// SetSecureBus attaches a SecureBus to the agent loop. All tool
// calls are routed through the bus for capability enforcement, secret injection,
// leak scanning, and audit logging. Call before the first message is processed.
func (al *AgentLoop) SetSecureBus(b *securebus.Bus) {
	if al.secureBus != nil && al.secureBus != b {
		al.secureBus.Close()
	}
	al.secureBus = b
}

// SetupSecureBus creates a SecureBus wired to this loop's tool registry and
// attaches it so all subsequent tool calls are routed through it.
// ss may be nil — secret injection is then disabled but all other enforcement
// (policy, leak scanning, audit) remains active.
// The loop owns the installed bus: replacing it closes the previous bus, and
// Stop closes the current bus.
func (al *AgentLoop) SetupSecureBus(ss *security.SecretStore, cfg securebus.BusConfig) *securebus.Bus {
	if cfg.Policy.AllowedWorkspace == "" && al.cfg != nil && al.cfg.RestrictToSandbox() {
		cfg.Policy.AllowedWorkspace = al.cfg.SandboxPath()
	}
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		t, ok := al.tools.Get(name)
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(t), true
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		if sessionKey := toolSessionKeyFromContext(ctx); sessionKey != "" {
			ctx = tools.WithSessionKey(ctx, sessionKey)
		}
		channel, chatID := tools.ExecutionTargetFromContext(ctx)
		var asyncCallback tools.AsyncCallback
		if al.bus != nil && channel != "" && chatID != "" {
			asyncCallback = func(_ context.Context, result *tools.ToolResult) {
				if result == nil || result.ForUser == "" || result.Silent {
					return
				}
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: channel,
					ChatID:  chatID,
					Content: result.ForUser,
				})
			}
		}
		// Let tool_call re-enter SecureBus for target tools.
		if al.secureBus != nil {
			ctx = tools.WithSecureBusDispatcher(ctx, al.secureBus.Execute)
		}
		return al.tools.ExecuteWithContext(ctx, name, args, channel, chatID, asyncCallback)
	}
	auditSink := newSecureBusAuditSink(al.enqueueAuditEntry)
	b := securebus.New(cfg, ss, capLookup, executor, auditSink)
	al.SetSecureBus(b)
	return b
}

func (al *AgentLoop) getOutputTarget() (outputTarget, bool) {
	raw := al.outputOverride.Load()
	if raw == nil {
		return outputTarget{}, false
	}
	target, ok := raw.(outputTarget)
	if !ok || target.Channel == "" {
		return outputTarget{}, false
	}
	return target, true
}

// MemoryDelegate returns the active memory delegate (nil if memory system is disabled).
func (al *AgentLoop) MemoryDelegate() memory.MemoryDelegate {
	return al.memDelegate
}

// HasSecureBus reports whether SecureBus enforcement is active.
func (al *AgentLoop) HasSecureBus() bool {
	return al.secureBus != nil
}

// HasUnifiedRuntimeDeps reports whether unified runtime persistence dependencies
// are available.
func (al *AgentLoop) HasUnifiedRuntimeDeps() bool {
	return al.queries != nil && al.kvDelegate != nil && al.stateStore != nil
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Tools info
	toolNames := al.tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(toolNames),
		"names": toolNames,
	}

	// Skills info
	info["skills"] = al.contextBuilder.GetSkillsInfo()

	return info
}
