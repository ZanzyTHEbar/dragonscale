# ADR 002: Unified Kernel Runtime

## Status

Accepted

## Context

The agent runtime previously allowed multiple execution variants:

- direct execution without SecureBus
- SecureBus-enabled execution
- offloading/state persistence in separate runtime paths used mostly in tests

This created behavior drift between environments and weakened long-horizon continuity guarantees.
For assistant-first workloads, kernel invariants must be enforced consistently.

## Decision

Adopt a single always-on kernel runtime path with no kernel feature toggles.

The runtime stack is:

1. SecureBus policy and secret handling
2. base tool execution
3. tool result offloading and indexing
4. run state persistence
5. leak scan and redaction
6. audit persistence

Additional decisions:

- SecureBus is initialized by default in `NewAgentLoop`.
- `assembleContext` always provides `WithToolRuntime(...)`; no nil SecureBus branch.
- `Bootstrap(...)` fails fast when SecureBus or unified runtime dependencies are missing.
- Tool result retrieval is exposed via `tool_result_search` in the same runtime surface.
- Session delegate bootstrap uses paginated DB scans and deterministic chronological replay.
- Normal background compaction is disabled; only emergency compression is triggered at hard budget.

## Consequences

### Positive

- Runtime behavior is deterministic across code paths.
- Offloading, state tracking, and security policy are always active together.
- Session continuity is improved under large histories.
- Prompt/tool-control guidance aligns with runtime capabilities.

### Negative

- Startup now has stricter dependency requirements.
- Runtime initialization complexity increases.
- Existing callers that expected optional SecureBus behavior may need adaptation.

## Implementation Notes

- Core wiring is in `pkg/agent/loop.go`, `pkg/agent/securebus_runtime.go`, and `pkg/runtime/bootstrap.go`.
- Session continuity changes are in `pkg/session/manager.go`.
- Subagent control-flow parity improvements are in `pkg/agent/toolloop.go` and `pkg/tools/subagent.go`.

