# Chronos-LCM Harness: Complete Implementation Strategies Report

**Source:** Agent Transcript Analysis (ed005f0a-996c-4113-8699-98dfef2a9073)  
**Project:** DragonScale → Chronos-LCM  
**Analysis Date:** March 4, 2026

---

Deep Recursive Analysis Complete
I've conducted a thorough recursive search of the transcript and extracted 43 distinct implementation strategies, optimizations, techniques, and patterns. Here's the comprehensive summary:
📊 Summary Statistics
Category	Items Extracted
Architecture Decision Records (ADRs)	5 major architectural decisions
Context Engineering Strategies	3 patterns
Memory Management Optimizations	4 strategies
Scheduling & Cortex Patterns	3 patterns
Database & Query Optimizations	3 patterns
Caching Strategies	3 strategies
Security & Reliability Patterns	4 patterns
Proactive Agent Strategies	3 patterns
Action Mining & Few-Shot Techniques	3 patterns
Mathematical Formalisms	12 equations
TOTAL	43 strategies/patterns
🏗️ 5 Architecture Decision Records (ADRs)
ADR-001: LCM Immutable Store Pattern
Append-only immutable_messages table
TruncateHistory only removes from active view; originals remain forever
Schema with session_key, role, content, tool_call_id, token_estimate
ADR-002: Dual-Threshold Compaction Control
Overhead(C) = none if |C| < 0.7, async if 0.7 ≤ |C| < 0.9, blocking if |C| ≥ 0.9
3-Level Escalation: Normal → Aggressive bullet-point → Deterministic truncation
ADR-003: Cortex Autonomous Scheduler
Singleton goroutine ticking every 60s
TryLock guard per task
Tasks: decay, embedding_backfill, consolidation, bulletin, prioritize, drift
ADR-004: Memory Graph Edges + Centrality
Typed relations: related_to, updates, contradicts, caused_by
PageRank-style centrality: (in_degree + out_degree) / (total_nodes - 1)
ADR-005: Context Tree Query-Adaptive Scoring
S(node, query) = [α·s_sem + (1-α)·s_lex] × w_time × w_freq × w_type
Parameters: α=0.7, halfLife=6h, γ=0.8, τ=0.3, ε=0.05
🧠 12 Mathematical Formalisms Captured
Dual-Threshold Compaction Control
Exponential Decay: importance_new = max(importance_old × 0.95, 0.1)
Temporal Decay: w_time = e^(-λ·Δt) where λ = ln(2)/6
Context Tree Scoring (full multi-component formula)
Branch Scoring (bottom-up): S_branch = γ·max(S_children) + (1-γ)·S_self
Boltzmann Sampling: P(keep | S) = exp(S/T) / Σ exp(S_i/T)
Hysteresis Anti-Flicker: |S - S_prev| ≤ ε
Degree Centrality (for memory graph)
Drift Activity Score (5-state classification)
Action Chain Scoring: recency × success_rate × diversity
Priority Score: urgency × importance × feasibility
Vector Cosine Similarity
🔧 Key Optimization Strategies
Context Engineering:
Zero-Cost Continuity Short-Circuit (skip DAG if below 70% threshold)
Context Block Parallelization (errgroup for 4 concurrent DB reads)
Snapshot-on-Read Pattern (capture before summarization truncates)
Memory Management:
Exponential decay with floor protection (0.95 factor, 0.1 floor)
Soft Delete / Quarantine (30-day retention before hard purge)
In-Memory Vector Embedding Cache
N+1 Query Elimination via batch WHERE id IN (...)
Caching:
Retrieval Policy In-Memory Cache
Skills + Bootstrap File Cache (30s TTL with mtime invalidation)
Incremental DAG Compression Cache (append-only delta updates)
Security & Reliability:
SecureBus Error Sanitization (regex patterns → [REDACTED])
Circuit Breaker for KV Delegate
Subagent Session Isolation (unique suffix per invocation)
Scope-Reduction Guards (LCM invariant: kept_work required for nested agents)
🎯 Proactive Agent Strategies
Bulletin Task: Daily LLM briefings with 7 sections (summary, goals, tasks, decisions, facts, deadlines, gaps)
Prioritize Task: Auto-extract actionable items with scoring:
   Score = urgency × importance × feasibility
ADHD Fade Test: Gradual reminder spacing increase during compliance:
   interval = base × (1 + 0.2 × streak), max = 60min
📁 Full Report Location
The complete detailed report with:
All 43 strategies fully documented
Code implementation snippets
SQL schemas
Mathematical equations
Implementation checklist (Tiers 1-3)

## Executive Summary

This report documents every implementation strategy, optimization technique, architectural pattern, formalism, and algorithm discussed during the development of the Chronos-LCM Harness - a sophisticated AI agent system implementing Lossless Context Management (LCM) principles.

---

## Table of Contents

1. [Architecture Decision Records (ADRs)](#1-architecture-decision-records-adrs)
2. [Context Engineering Strategies](#2-context-engineering-strategies)
3. [Memory Management Optimizations](#3-memory-management-optimizations)
4. [Scheduling & Cortex Patterns](#4-scheduling--cortex-patterns)
5. [Database & Query Optimizations](#5-database--query-optimizations)
6. [Caching Strategies](#6-caching-strategies)
7. [Security & Reliability Patterns](#7-security--reliability-patterns)
8. [Proactive Agent Strategies](#8-proactive-agent-strategies)
9. [Action Mining & Few-Shot Techniques](#9-action-mining--few-shot-techniques)
10. [Mathematical Formalisms & Equations](#10-mathematical-formalisms--equations)

---

## 1. Architecture Decision Records (ADRs)

### ADR-001: LCM Immutable Store Pattern

**Context:** SessionManager.TruncateHistory() permanently deletes original messages. DAG nodes reference index ranges (StartIdx/EndIdx) into session history, but once truncated, indices point to nothing.

**Decision:** Add append-only immutable_messages table. Every message persisted via sessions.AddMessage / sessions.AddFullMessage is written verbatim with stable UUID. TruncateHistory only removes from active session view; originals remain forever.

**Consequences:**
- Storage grows linearly (mitigated by periodic archival)
- dag_expand becomes lossless
- Full-text search over all history
- Emergency compression no longer loses data

**Schema:**
```sql
CREATE TABLE IF NOT EXISTS immutable_messages (
    id TEXT PRIMARY KEY,
    session_key TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_call_id TEXT,
    tool_calls TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    token_estimate INTEGER
);
CREATE INDEX idx_immutable_session ON immutable_messages(session_key, created_at);
CREATE INDEX idx_immutable_fts ON immutable_messages(content);
```

### ADR-002: Dual-Threshold Compaction Control Loop

**Context:** Current compaction is emergency-only (95% threshold, max 3 cycles, fallback = force-truncate). No soft/async compaction exists.

**Decision:** Implement LCM's control loop with:
- Soft threshold τ_soft = 70% of context window
- Hard threshold τ_hard = 90% of context window

**Formalism:**
```
Overhead(C) = 
    none       if |C| < τ_soft
    async      if τ_soft ≤ |C| < τ_hard
    blocking   if |C| ≥ τ_hard

Where |C| = estimated tokens of active context
```

**3-Level Escalation:**
1. **Level 1:** Normal LLM summarization ("preserve_details")
2. **Level 2:** Aggressive bullet-point summarization
3. **Level 3:** Deterministic truncation (NO LLM, guaranteed convergence)

**Consequences:**
- 80% of interactions have zero compaction overhead
- Guaranteed convergence via deterministic Level 3 fallback
- No more "drop messages" failure mode

### ADR-003: Cortex Autonomous Scheduler Pattern

**Context:** DragonScale is purely reactive - only processes inbound messages. Cannot initiate interactions or run background maintenance.

**Decision:** Add Cortex singleton goroutine started by AgentLoop.Run():
- Ticks every 60 seconds
- Each task has: name, interval, timeout, sync.Mutex TryLock guard
- Initial tasks: decay, embedding_backfill, consolidation, bulletin, prioritize, drift

**Architecture:**
```go
type CortexTask interface {
    Name() string
    Interval() time.Duration
    Timeout() time.Duration
    Execute(ctx context.Context) error
}
```

**Consequences:**
- Agent can proactively initiate (voice/push notifications)
- Memory maintains itself (decay, consolidation, pruning)
- Foundation for Inventory Guardian and proactive features

### ADR-004: Memory Graph Edges + Centrality

**Context:** Memory is flat - no relational structure. No way to express "X contradicts Y" or "A caused B".

**Decision:** Add memory_edges table with typed relations:
- `related_to`, `updates`, `contradicts`, `caused_by`, `result_of`, `part_of`
- Add memory_centrality table with PageRank-style degree centrality

**Schema:**
```sql
CREATE TABLE IF NOT EXISTS memory_edges (
    id TEXT PRIMARY KEY,
    memory_a_id TEXT NOT NULL REFERENCES recall_items(id),
    memory_b_id TEXT NOT NULL REFERENCES recall_items(id),
    relation TEXT NOT NULL,
    weight REAL DEFAULT 1.0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS memory_centrality (
    memory_id TEXT PRIMARY KEY REFERENCES recall_items(id),
    degree_centrality REAL DEFAULT 0.0,
    computed_at TIMESTAMP
);
```

**Centrality Formula:**
```
centrality(node) = (in_degree(node) + out_degree(node)) / (total_nodes - 1)
```

### ADR-005: Context Tree Query-Adaptive Scoring

**Context:** Current DAG compression selects detail level by token budget only. No per-query relevance scoring.

**Decision:** Replace SelectDAGLevel with per-node scoring function.

**Scoring Function:**
```
S(node, query) = [α·s_sem + (1-α)·s_lex] × w_time × w_freq × w_type

Where:
  s_sem = cosine(queryEmbed, nodeEmbed) ∈ [0,1]
  s_lex = Jaccard(queryTerms, nodeTerms) ∈ [0,1]
  w_time = e^(-λ·Δt), λ = ln(2)/halfLifeHours
  w_freq = 1 + log(1 + accessCount)
  w_type = prior from NodeType (tool=1.2, fact=1.1, hypothetical=0.8)
```

**TreeConfig Parameters:**
- α = 0.7 (semantic weight)
- halfLife = 6h → λ = ln(2)/6
- γ = 0.8 (branch child-inheritance weight)
- τ = 0.3 (pruning threshold)
- ε = 0.05 (hysteresis band)
- T = 0.2 (Boltzmann temperature)

**Pruning Strategy (Bottom-Up DFS):**
1. Score leaves with S(node, query)
2. For branches: S_branch = γ·max(S_children) + (1-γ)·S_self
3. Keep if: S ≥ τ OR on path to kept descendant OR is root
4. Hysteresis: keep if |S - S_prev| ≤ ε even if below τ

---

## 2. Context Engineering Strategies

### 2.1 Zero-Cost Continuity Short-Circuit

**Strategy:** Skip all DAG work if context fits comfortably below soft threshold.

**Implementation:**
```go
func applyDAGCompression(ctx context.Context, sessionKey string, history []messages.Message) []messages.Message {
    tokenEst := estimateTokens(history)
    softThreshold := contextWindow * 70 / 100
    
    if tokenEst < softThreshold {
        contextBuilder.SetDAGBlock("")
        return history  // Zero overhead
    }
    // ... existing DAG compression
}
```

**Impact:** Short conversations (<70% window) have zero DAG compression overhead.

### 2.2 Context Block Parallelization (errgroup)

**Strategy:** Parallelize independent context reads using errgroup.

**Implementation:**
```go
func refreshContextBlocks(ctx context.Context, opts processOptions) {
    var obsBlock, kb, focus string
    
    g, gCtx := errgroup.WithContext(ctx)
    g.Go(func() error { obsBlock = obsManager.LoadBlock(gCtx, opts.SessionKey); return nil })
    g.Go(func() error { kb = tools.LoadKnowledgeBlock(gCtx, al.memDelegate, opts.SessionKey); return nil })
    g.Go(func() error { 
        if fs, ok := tools.LoadFocusState(gCtx, al.memDelegate, opts.SessionKey); ok {
            focus = fs.FormatBlock()
        }
        return nil
    })
    g.Go(func() error {
        if al.identitySync != nil { _ = al.identitySync.CheckAndSync(gCtx) }
        return nil
    })
    
    _ = g.Wait()
    // Set blocks...
}
```

**Impact:** 4 sequential DB round trips → 1 parallel batch.

### 2.3 Snapshot-on-Read Pattern

**Strategy:** Capture tail snapshot BEFORE summarization can truncate history.

**Implementation:**
```go
func postProcess(ctx context.Context, opts processOptions, finalContent string, stepCount int) string {
    sessions.Save(opts.SessionKey)
    
    // Snapshot BEFORE summarization can truncate
    tail := sessionsToMessagePairs(opts.SessionKey)
    
    if opts.EnableSummary {
        maybeSummarize(ctx, opts.SessionKey, opts.Channel, opts.ChatID)
    }
    
    obsManager.MaybeObserveAsync(ctx, opts.SessionKey, tail)
    // ...
}
```

**Impact:** Fixes race condition between summarization and observation.

---

## 3. Memory Management Optimizations

### 3.1 Decay Task Formalism

**Strategy:** Exponential decay of memory importance with floor protection.

**Formalism:**
```
importance_new = importance_old × decayFactor

Where:
  decayFactor = 0.95 (configurable)
  floor = 0.1 (never decay below)
  exempt types: identity, preference
  batch size: 30 items per run
```

**SQL Implementation:**
```sql
UPDATE recall_items 
SET importance = MAX(importance * 0.95, 0.1), 
    updated_at = CURRENT_TIMESTAMP 
WHERE id IN (
    SELECT id FROM recall_items 
    WHERE importance > 0.1 
    ORDER BY updated_at ASC 
    LIMIT 30
)
```

### 3.2 Soft Delete / Quarantine Pattern

**Strategy:** Mark as deleted without hard removal; purge after retention period.

**Schema:**
```sql
ALTER TABLE recall_items ADD COLUMN suppressed_at TIMESTAMP;
ALTER TABLE archival_chunks ADD COLUMN suppressed_at TIMESTAMP;

-- Soft delete (sets timestamp)
UPDATE recall_items SET suppressed_at = CURRENT_TIMESTAMP WHERE id = ?;

-- Hard delete (prune task, after 30 days)
DELETE FROM recall_items 
WHERE suppressed_at < datetime('now', '-30 days') 
  AND NOT EXISTS (
      SELECT 1 FROM memory_edges 
      WHERE memory_a_id = id OR memory_b_id = id
  );
```

**Query Modifications:**
All read queries add: `WHERE suppressed_at IS NULL`

### 3.3 In-Memory Vector Embedding Cache

**Strategy:** Cache chunk embeddings in MemoryStore to avoid DB round trips on vector search.

**Implementation:**
```go
type MemoryStore struct {
    // ... existing fields ...
    vecCache struct {
        mu       sync.RWMutex
        items    []VectorSearchInput
        loaded   bool
    }
}

// Populate on first search, append on StoreArchival
func (m *MemoryStore) vectorSearchGoSide(ctx context.Context, queryVec Embedding, limit int) ([]SearchResult, error) {
    m.vecCache.mu.RLock()
    if m.vecCache.loaded {
        items := m.vecCache.items
        m.vecCache.mu.RUnlock()
        return m.computeSimilarity(queryVec, items, limit)
    }
    m.vecCache.mu.RUnlock()
    
    // Load from DB on first call
    // ... populate cache ...
}
```

### 3.4 N+1 Query Elimination (Batch Fetch)

**Strategy:** Replace individual queries with batch WHERE id IN (...) queries.

**Before (N+1):**
```go
for _, r := range merged {
    item, err := m.delegate.GetRecallItem(ctx, m.agentID, r.ID)
    // ... process ...
}
```

**After (Batch):**
```go
// Single query for all IDs
items, err := m.delegate.GetRecallItemsByIDs(ctx, m.agentID, ids)
for _, item := range items {
    createdAtMap[item.ID] = item.CreatedAt
}
```

**Impact:** 20-80 individual queries → 1 batch query per search.

---

## 4. Scheduling & Cortex Patterns

### 4.1 TryLock Guard Pattern

**Strategy:** Prevent concurrent task runs using sync.Mutex TryLock.

**Implementation:**
```go
type Cortex struct {
    tasks   []CortexTask
    locks   map[string]*sync.Mutex
    running atomic.Bool
}

func (c *Cortex) Start(ctx context.Context) {
    ticker := time.NewTicker(60 * time.Second)
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            for _, task := range c.tasks {
                if !c.tryLock(task.Name()) { continue }  // Skip if already running
                
                go func(t CortexTask) {
                    defer c.unlock(t.Name())
                    tCtx, cancel := context.WithTimeout(ctx, t.Timeout())
                    defer cancel()
                    
                    if err := t.Execute(tCtx); err != nil {
                        logger.WarnCF("cortex", "task failed", 
                            map[string]interface{}{"task": t.Name(), "error": err.Error()})
                    }
                }(task)
            }
        }
    }
}
```

### 4.2 Per-Task Interval Tracking

**Strategy:** Respect individual task intervals instead of running all tasks every tick.

**Implementation:**
```go
type Cortex struct {
    // ...
    lastRun map[string]time.Time
}

func (c *Cortex) shouldRun(task CortexTask) bool {
    last, ok := c.lastRun[task.Name()]
    if !ok {
        return true
    }
    return time.Since(last) >= task.Interval()
}

func (c *Cortex) Start(ctx context.Context) {
    // ...
    for _, task := range c.tasks {
        if !c.shouldRun(task) { continue }
        if !c.tryLock(task.Name()) { continue }
        
        c.lastRun[task.Name()] = time.Now()
        // ... execute ...
    }
}
```

### 4.3 Drift Detection Formalism

**Strategy:** Per-domain health tracking with activity scoring.

**Formalism:**
```
For each domain D:
  activity_score = count(memories modified in last 7 days) / count(all memories in D)
  
  state = match activity_score:
    > 0.5  → "overactive"
    > 0.2  → "active"
    > 0.05 → "drifting"
    > 0.0  → "neglected"
    = 0.0  → "cold"
```

**Implementation:**
```go
func (t *DriftTask) detectDrift(ctx context.Context) (map[string]DriftState, error) {
    domains, err := t.store.ListMemoryDomains(ctx, t.agentID)
    // ...
    
    for _, domain := range domains {
        recent := countRecentMemories(domain, 7*24*time.Hour)
        total := countTotalMemories(domain)
        
        score := float64(recent) / float64(total)
        state := classifyDriftState(score)
        // ...
    }
}
```

---

## 5. Database & Query Optimizations

### 5.1 sqlc Code Generation Pattern

**Pattern:** Use sqlc for type-safe SQL code generation:

1. **schema.sql** - Defines tables for sqlc to parse
2. **queries/*.sql** - Named queries with sqlc.arg() / sqlc.slice()
3. **sqlc generate** - Produces Go code in pkg/memory/sqlc/
4. **delegate/sqlite.go** - Wraps sqlc-generated Queries, converts between models

**Example Query File:**
```sql
-- queries/recall.sql
-- name: GetRecallItem :one
SELECT * FROM recall_items
WHERE id = ? AND agent_id = ? AND suppressed_at IS NULL;

-- name: ListRecallItems :many
SELECT * FROM recall_items
WHERE agent_id = ? AND session_key = ? AND suppressed_at IS NULL
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: DecayRecallImportanceBatch :exec
UPDATE recall_items 
SET importance = MAX(importance * sqlc.arg(decay_factor), sqlc.arg(floor)),
    updated_at = CURRENT_TIMESTAMP
WHERE id IN (
    SELECT id FROM recall_items
    WHERE agent_id = sqlc.arg(agent_id)
      AND importance > sqlc.arg(floor)
    ORDER BY updated_at ASC
    LIMIT sqlc.arg(batch_size)
);
```

### 5.2 Type Override Configuration

**Pattern:** Map SQL types to Go types in sqlc.yaml:

```yaml
overrides:
  - db_type: "BLOB"
    go_type:
      import: "github.com/ZanzyTHEbar/picoclaw/pkg/ids"
      type: "UUID"
    nullable: false
  - db_type: "REAL"
    go_type:
      import: "github.com/ZanzyTHEbar/picoclaw/pkg/memory"
      type: "Embedding"
    nullable: false
```

### 5.3 Bounded LRU Cache for Conversation IDs

**Strategy:** Replace unbounded sync.Map with size-limited LRU.

**Implementation:**
```go
type boundedCache struct {
    mu      sync.RWMutex
    items   map[string]cacheEntry
    maxSize int
}

type cacheEntry struct {
    value      ids.UUID
    lastAccess time.Time
}

func (c *boundedCache) Get(key string) (ids.UUID, bool) {
    c.mu.RLock()
    entry, ok := c.items[key]
    c.mu.RUnlock()
    
    if ok {
        c.mu.Lock()
        c.items[key] = cacheEntry{value: entry.value, lastAccess: time.Now()}
        c.mu.Unlock()
    }
    return entry.value, ok
}

func (c *boundedCache) Set(key string, value ids.UUID) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if len(c.items) >= c.maxSize {
        // Evict oldest half
        c.evictOldest(c.maxSize / 2)
    }
    
    c.items[key] = cacheEntry{value: value, lastAccess: time.Now()}
}
```

---

## 6. Caching Strategies

### 6.1 Retrieval Policy In-Memory Cache

**Strategy:** Cache policy state, gates, and metrics in MemoryStore.

**Before (5 DB calls per search):**
```go
m.retrievalPolicyMu.Lock()
state := m.loadRetrievalPolicyState(ctx)
gates := m.loadRetrievalPromotionGates(ctx)
metrics := m.loadRetrievalShadowMetrics(ctx)
// ... compute and update ...
state = m.updateRetrievalPolicy(ctx, state, gates, metrics, ...)
m.retrievalPolicyMu.Unlock()
```

**After (In-memory cache):**
```go
type MemoryStore struct {
    policyCache struct {
        state   retrievalPolicyState
        gates   retrievalPromotionGates
        metrics retrievalShadowMetrics
        loaded  bool
        dirty   bool
        mu      sync.RWMutex
    }
}

func (m *MemoryStore) Search(...) {
    m.policyCache.mu.RLock()
    if !m.policyCache.loaded {
        m.policyCache.mu.RUnlock()
        m.loadPolicyCache(ctx)
    } else {
        state := m.policyCache.state
        gates := m.policyCache.gates
        metrics := m.policyCache.metrics
        m.policyCache.mu.RUnlock()
        // Use cached values...
    }
}
```

### 6.2 Skills + Bootstrap File Cache (TTL)

**Strategy:** Cache filesystem scans and DB queries with mtime-based invalidation.

**Implementation:**
```go
type ContextBuilder struct {
    skillsCache struct {
        summary   string
        mtime     time.Time
        mu        sync.RWMutex
    }
    bootstrapCache struct {
        files     []BootstrapFile
        loadedAt  time.Time
        mu        sync.RWMutex
    }
}

const skillsCacheTTL = 30 * time.Second

func (cb *ContextBuilder) BuildSkillsSummary() string {
    cb.skillsCache.mu.RLock()
    cached := cb.skillsCache.summary
    mtime := cb.skillsCache.mtime
    cb.skillsCache.mu.RUnlock()
    
    // Check if cache is fresh
    if cached != "" && time.Since(mtime) < skillsCacheTTL {
        return cached
    }
    
    // Regenerate
    summary := cb.generateSkillsSummary()
    
    cb.skillsCache.mu.Lock()
    cb.skillsCache.summary = summary
    cb.skillsCache.mtime = time.Now()
    cb.skillsCache.mu.Unlock()
    
    return summary
}
```

### 6.3 Incremental DAG Compression Cache

**Strategy:** Cache the last DAG snapshot and append only new messages.

**Implementation:**
```go
type cachedDAG struct {
    snapshot    *dag.DAG
    msgCount    int
    rendered    string
    sessionKey  string
    persistFailed bool
}

func (al *AgentLoop) applyDAGCompression(ctx context.Context, sessionKey string, history []messages.Message) []messages.Message {
    // Check cache
    if cached, ok := al.dagCache[sessionKey]; ok {
        delta := len(history) - cached.msgCount
        if delta > 0 && delta <= 3 {
            // Incremental append
            newMsgs := history[cached.msgCount:]
            updatedDAG := al.dagCompressor.AppendAndRecompress(cached.snapshot, newMsgs)
            // ... use updatedDAG ...
            return
        }
    }
    
    // Full recompression
    dag := al.dagCompressor.Compress(history)
    // ... cache result ...
}
```

---

## 7. Security & Reliability Patterns

### 7.1 SecureBus Error Sanitization

**Strategy:** Sanitize policy errors before reaching LLM; preserve full details in audit only.

**Implementation:**
```go
func sanitizePolicyError(err string) string {
    patterns := []string{
        `recursion limit \d+ exceeded`,
        `blocked by SSRF filter: [\w.-]+`,
        `path .+ violates security policy`,
        `secret \w+ detected in output`,
    }
    
    sanitized := err
    for _, pattern := range patterns {
        re := regexp.MustCompile(pattern)
        sanitized = re.ReplaceAllString(sanitized, "[REDACTED: security policy violation]")
    }
    return sanitized
}

// In SecureBusRuntime:
if busResp.IsError {
    safeError := sanitizePolicyError(busResp.Error)
    return safeError, fmt.Errorf("policy rejection: %s", safeError)
}
```

### 7.2 Circuit Breaker for KV Delegate

**Strategy:** Wrap KV operations with circuit breaker for graceful degradation.

**Implementation:**
```go
type ResilientKV struct {
    inner    KVDelegate
    breaker  *CircuitBreaker
    fallback func(key string, value []byte)
}

func (r *ResilientKV) Put(ctx context.Context, key string, value []byte) error {
    if r.breaker.IsOpen() {
        r.fallback(key, value)  // Log to temp file
        return nil  // Degrade gracefully
    }
    
    err := r.inner.Put(ctx, key, value)
    r.breaker.Record(err)
    return err
}
```

### 7.3 Subagent Session Isolation

**Strategy:** Unique session key per subagent invocation to prevent state pollution.

**Implementation:**
```go
func (s *SubagentTool) Execute(ctx context.Context, params SubagentParams) (string, error) {
    // Generate unique suffix for this invocation
    uniqueID := generateShortUUID(8)
    sessionKey := fmt.Sprintf("%s::subagent::%s", params.BaseSession, uniqueID)
    
    // Spawn subagent with isolated session
    result, err := s.spawnSubagent(ctx, sessionKey, params)
    // ...
}
```

### 7.4 Scope-Reduction Guards (LCM Invariant)

**Strategy:** Prevent infinite delegation chains by requiring kept_work declaration.

**Formalism:**
```
When a sub-agent (not root) spawns a further sub-agent, it must declare:
1. delegated_scope — specific slice of work being handed off
2. kept_work — work the caller retains

If kept_work is empty → reject the call with "scope reduction violation"
Read-only exploration agents are exempt.
```

**Implementation:**
```go
func (s *SubagentTool) Spawn(ctx context.Context, params SubagentParams, depth int) error {
    // Root agent can spawn without restriction
    if depth == 0 {
        return s.doSpawn(ctx, params)
    }
    
    // Non-root must provide scope declaration
    if params.DelegatedScope == "" || params.KeptWork == "" {
        return fmt.Errorf("scope reduction violation: subagent at depth %d must declare delegated_scope and kept_work", depth)
    }
    
    // Log for audit trail
    logger.DebugCF("subagent", "scope delegation", map[string]interface{}{
        "depth": depth,
        "delegated": params.DelegatedScope,
        "kept": params.KeptWork,
    })
    
    return s.doSpawn(ctx, params)
}
```

---

## 8. Proactive Agent Strategies

### 8.1 Bulletin Task (Daily Briefing)

**Strategy:** Generate daily LLM briefings for system prompt injection.

**Sections:**
1. Executive Summary (current context)
2. Active Goals (priority ≥ 0.8, status = active)
3. Pending Tasks (priority ≥ 0.5, status = pending)
4. Recent Decisions (last 24h, category = decision)
5. Key Facts (centrality score ≥ 0.7)
6. Upcoming Deadlines (next 7 days)
7. Knowledge Gaps (questions from recent turns)

**Implementation:**
```go
func (t *BulletinTask) generateBulletin(ctx context.Context) (*Bulletin, error) {
    memories, _ := t.store.Search(ctx, t.agentID, "", 100)
    
    bulletin := &Bulletin{
        GeneratedAt: time.Now(),
        Sections: make(map[string]string),
    }
    
    // Gather by category
    goals := filterByCategory(memories, "goal")
    tasks := filterByCategory(memories, "task")
    decisions := filterRecent(memories, "decision", 24*time.Hour)
    
    // Synthesize via LLM
    bulletin.Sections["summary"] = t.synthesize("Create executive summary", memories)
    bulletin.Sections["goals"] = t.synthesize("List active goals", goals)
    // ...
    
    return bulletin, nil
}
```

### 8.2 Prioritize Task (Action Extraction)

**Strategy:** Auto-extract actionable items with micro-steps.

**Algorithm:**
```
Select top recall items where:
  - sector = 'procedural'
  - importance ≥ 0.5
  - status IN ('pending', 'blocked')
  
Score = urgency × importance × feasibility

Generate "today's 3 desk items" with:
  - Clear title
  - Micro-steps (2-5 minute chunks)
  - Estimated completion time
  - Dependencies
```

### 8.3 ADHD Fade Test Pattern

**Strategy:** Gradual reminder spacing increase during high compliance periods.

**Formalism:**
```
Base reminder interval: 15 minutes

During high compliance (3+ consecutive on-time responses):
  interval = base × (1 + fade_factor × streak)
  
Where fade_factor = 0.2 (20% increase per streak)
Max interval = 60 minutes

On missed response:
  reset to base interval
```

**Implementation:**
```go
type FadeTestConfig struct {
    BaseInterval  time.Duration  // 15 minutes
    FadeFactor    float64         // 0.2
    MaxInterval   time.Duration  // 60 minutes
}

func (t *BulletinTask) calculateReminderInterval(streak int) time.Duration {
    multiplier := 1.0 + (t.config.FadeFactor * float64(streak))
    interval := time.Duration(float64(t.config.BaseInterval) * multiplier)
    
    if interval > t.config.MaxInterval {
        return t.config.MaxInterval
    }
    return interval
}
```

---

## 9. Action Mining & Few-Shot Techniques

### 9.1 Action Mining Algorithm

**Strategy:** Mine successful tool call sequences from audit log.

**Algorithm:**
```
1. Query audit_entries for successful tool calls within sessions
2. Group by session, order by timestamp
3. Filter sequences where final step produced positive outcome
4. Score by: recency × success_rate × tool_diversity
5. Format as: "Past: tool_a(args) → tool_b(args) → result"
```

**Implementation:**
```go
func MineSuccessfulSequences(ctx context.Context, store AuditStore, window time.Duration) []ActionChain {
    entries, _ := store.QueryAudit(ctx, AuditQuery{
        Status:      "success",
        ToolCalls:   true,
        Since:       time.Now().Add(-window),
    })
    
    // Group by session
    sessions := groupBySession(entries)
    
    var chains []ActionChain
    for _, session := range sessions {
        chain := extractChain(session)
        if chain.HasPositiveOutcome() {
            chain.Score = calculateScore(chain)
            chains = append(chains, chain)
        }
    }
    
    // Sort by score
    sort.Slice(chains, func(i, j int) bool {
        return chains[i].Score > chains[j].Score
    })
    
    return chains[:10]  // Top 10
}
```

### 9.2 Intent Classification for Few-Shot Injection

**Strategy:** Classify user intent and inject relevant action replays.

**Intent Types:**
- `research` - Information gathering
- `file_research` - Code/document search
- `development` - Writing/editing code
- `git_workflow` - Version control operations
- `execution` - Running commands/tests

**Similarity Matching:**
```go
func FindRelevantChains(chains []ActionChain, query string, embedder Embedder) []ActionChain {
    queryEmbed, _ := embedder.Embed(query)
    
    var scored []ScoredChain
    for _, chain := range chains {
        chainEmbed, _ := embedder.Embed(chain.IntentDescription)
        similarity := cosineSimilarity(queryEmbed, chainEmbed)
        
        scored = append(scored, ScoredChain{
            Chain:      chain,
            Similarity: similarity,
        })
    }
    
    // Sort by similarity
    sort.Slice(scored, func(i, j int) bool {
        return scored[i].Similarity > scored[j].Similarity
    })
    
    return scored[:3]  // Top 3
}
```

### 9.3 Few-Shot Prompt Formatting

**Strategy:** Format successful sequences as few-shot examples.

**Format:**
```
You previously solved similar problems successfully:

Example 1 (Intent: {intent}):
  Past: {tool_a}({args_a}) → {tool_b}({args_b}) → {result}
  Outcome: {positive_outcome}

Example 2 (Intent: {intent}):
  Past: {tool_c}({args_c}) → {tool_d}({args_d}) → {tool_e}({args_e})
  Outcome: {positive_outcome}

Consider following similar patterns for the current task.
```

---

## 10. Mathematical Formalisms & Equations

### 10.1 Dual-Threshold Compaction Control

```
Overhead(C) = 
    none       if |C| < τ_soft
    async      if τ_soft ≤ |C| < τ_hard
    blocking   if |C| ≥ τ_hard

Where:
  |C| = estimated tokens of active context
  τ_soft = 0.7 × contextWindow
  τ_hard = 0.9 × contextWindow
```

### 10.2 Exponential Decay

```
importance_new = max(importance_old × decayFactor, floor)

Where:
  decayFactor = 0.95 (configurable)
  floor = 0.1 (never decay below)
  λ = -ln(decayFactor) per tick
```

### 10.3 Temporal Decay Weight

```
w_time = e^(-λ·Δt)

Where:
  λ = ln(2) / halfLifeHours = 0.1155 for 6h half-life
  Δt = hours since last access
```

### 10.4 Context Tree Scoring Function

```
S(node, query) = [α·s_sem + (1-α)·s_lex] × w_time × w_freq × w_type

Components:
  s_sem = cosine(queryEmbed, nodeEmbed) ∈ [0,1]
  s_lex = |queryTerms ∩ nodeTerms| / |queryTerms ∪ nodeTerms| ∈ [0,1]
  w_time = e^(-λ·Δt)
  w_freq = 1 + log(1 + accessCount)
  w_type = {tool: 1.2, fact: 1.1, hypothesis: 0.8}
  
Parameters:
  α = 0.7 (semantic weight)
  γ = 0.8 (branch inheritance)
  τ = 0.3 (pruning threshold)
  ε = 0.05 (hysteresis band)
```

### 10.5 Branch Scoring (Bottom-Up)

```
For leaf nodes:
  S_leaf = S(node, query)

For branch nodes:
  S_branch = γ·max(S_children) + (1-γ)·S_self
  
Where:
  γ = 0.8 (child inheritance weight)
```

### 10.6 Boltzmann Sampling for Exploration

```
P(keep | S) = exp(S/T) / Σ exp(S_i/T)

Where:
  T = 0.2 (temperature)
  S = node score
  
At low T: deterministic (keep highest scores)
At high T: more random exploration
```

### 10.7 Hysteresis (Anti-Flicker)

```
Keep node if:
  S ≥ τ
  OR is on path to kept descendant
  OR is root
  OR |S - S_prev| ≤ ε  (hysteresis band)

Where:
  ε = 0.05 prevents flicker between consecutive turns
```

### 10.8 Degree Centrality

```
centrality(node) = (in_degree(node) + out_degree(node)) / (total_nodes - 1)

Where:
  in_degree = count(memory_edges where memory_b_id = node.id)
  out_degree = count(memory_edges where memory_a_id = node.id)
```

### 10.9 Drift Activity Score

```
activity_score(D) = count(memories modified in last 7 days in D) / count(all memories in D)

state(D) = 
    "overactive"  if activity_score > 0.5
    "active"        if activity_score > 0.2
    "drifting"      if activity_score > 0.05
    "neglected"     if activity_score > 0.0
    "cold"          if activity_score = 0.0
```

### 10.10 Action Chain Scoring

```
Score(chain) = recency_weight × success_rate × diversity_factor

Where:
  recency_weight = e^(-λ·Δt), λ = ln(2)/30days
  success_rate = successes / (successes + failures)
  diversity_factor = 1 + (unique_tools / total_tools) × 0.5
```

### 10.11 Priority Score (Action Extraction)

```
Priority(item) = urgency × importance × feasibility

Where:
  urgency = 1 / (days_until_deadline + 1)
  importance = item.ImportanceScore ∈ [0,1]
  feasibility = estimate_feasibility(item) ∈ [0,1]
```

### 10.12 Vector Similarity (Cosine)

```
cosine_sim(A, B) = (A·B) / (||A|| × ||B||)

Where:
  A·B = Σ(A[i] × B[i])
  ||A|| = sqrt(Σ(A[i]²))
```

---

## Appendix: Implementation Checklist

### Tier 1 (Foundation) - P0
- [x] T1.1: Immutable message store
- [x] T1.2: Dual-threshold compaction control
- [x] T1.3: Zero-cost continuity short-circuit
- [x] T1.4: Cortex scheduler foundation

### Tier 2 (Enhancements) - P1
- [x] T2.1: Scope-reduction guards
- [x] T2.2: Memory graph edges + consolidation
- [x] T2.3: Type-aware large file summaries
- [x] T2.4: Soft delete / quarantine

### Tier 3 (Advanced) - P2
- [x] T3.1: Context tree with query-adaptive scoring
- [x] T3.2: Proactive tasks (bulletin, prioritize, drift)
- [x] T3.3: Action replay as few-shots

### Optimizations (Batches 1-11)
- [x] Batch 1: Race fixes + threshold wiring
- [x] Batch 2: Async summarization + parallel context
- [x] Batch 3: N+1 query elimination
- [x] Batch 4: In-memory caching layers
- [x] Batch 5-6: Write-behind batching
- [x] Batch 7-8: Security + correctness
- [x] Batch 9-11: Vector cache + hard caps

---

## Summary Statistics

| Category | Count |
|----------|-------|
| ADRs | 5 |
| Context Strategies | 3 |
| Memory Optimizations | 4 |
| Scheduling Patterns | 3 |
| Database Patterns | 3 |
| Caching Strategies | 3 |
| Security Patterns | 4 |
| Proactive Strategies | 3 |
| Few-Shot Techniques | 3 |
| Mathematical Formalisms | 12 |
| **Total Strategies/Patterns** | **43** |

---

*Report generated from comprehensive transcript analysis of the DragonScale → Chronos-LCM implementation.*
