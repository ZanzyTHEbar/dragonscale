# Unified Kernel Execution Blueprint

This blueprint defines the always-on runtime model for DragonScale's assistant-first kernel.

## Core Invariants

- Single runtime path for tool execution.
- SecureBus enforcement is always active.
- Offloading + run-state persistence + tool-result retrieval are composed into that single path.
- Session continuity is lossless and deterministic.
- Normal compaction is disabled; compression is emergency-only and recursive.
- DAG snapshots are a persistent, lossless materialized view over immutable session history.
- Subagents run with main-loop parity and bounded delegation guardrails.
- Immutable history + active-context projection are formalized as explicit kernel contracts in `pkg/memory/kernel_contract.go`.

## Implemented State

- Unified runtime assembly is active in `pkg/agent/loop.go` and `pkg/agent/securebus_runtime.go`.
- Bootstrap fail-fast checks are enforced in `pkg/runtime/bootstrap.go`.
- Session projection pointers + integrity validation are active in `pkg/session/manager.go` and `pkg/session/projection_pointer.go`.
- Emergency compression provenance capture is active in `pkg/agent/loop.go`.
- DAG persistence and retrieval tools are active in:
  - `pkg/memory/dag/store.go`
  - `pkg/tools/dag.go`
  - `pkg/memory/sqlc/queries/dag.sql`
- Legacy session and DAG backfill passes are active at startup:
  - Session pointer backfill status: `migration:session_projection_backfill:v1`
  - DAG backfill status: `migration:dag_backfill:v1`
- Subagent delegation safety (scope/kept-work, depth/fanout, lineage audit) is active in `pkg/tools/subagent.go`.
- Hybrid retrieval routing across working-context, recall, archival, and DAG projections is active in `pkg/memory/store/memory_store.go`.
- Shadow-mode rollout, proof gates, auto-promotion, and fast rollback for retrieval augmentation are active in `pkg/memory/store/retrieval_policy.go`.
- Map operator runtime is active with FlatBuffers persistence and worker orchestration in:
  - `pkg/tools/map_runtime.go`
  - `pkg/tools/map_flatbuffer_codec.go`
  - `pkg/memory/migrations/012_map_operator_runs.go`
  - `pkg/memory/sqlc/queries/map_ops.sql`
- Map worker identity and dedupe flow resolve through deterministic keys (`map:{runID}:{itemIndex}`) in `pkg/tools/map_runtime.go`.
- Concurrency hardening coverage is active for subagent fanout/depth guardrails, retrieval-policy updates, and idempotent map-run reuse.

## Verification Gates

- `go test ./pkg/agent ./pkg/tools ./pkg/runtime ./pkg/session ./pkg/memory/dag ./eval/go_evals`
- `go test ./pkg/memory/store`
- `go test -race ./pkg/tools -run 'SubagentManager_ConcurrentSpawnRespectsFanout|LLMMap_IdempotencyReuse_Concurrent'`
- `go test -race ./pkg/memory/store -run 'Search_ConcurrentRetrievalPolicyUpdates'`
- Confirm no lints for touched files.
- Confirm backfill status keys are present in `agent_kv` after first boot.

## Remaining Work (Ordered)

- Integrate obligation heartbeat execution for proactive due checks.
- Keep JSONL strictly as an LLM boundary format; do not persist JSONL internally.
