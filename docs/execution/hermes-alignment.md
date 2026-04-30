# Hermes Alignment for DragonScale

## Status

Frozen for future work.

## Purpose

Capture the lasting strategic takeaways from the Hermes research pass so DragonScale can borrow the right ideas without weakening its kernel architecture.

This document is not an implementation spec.
Linear remains the source of truth for execution.
Memory Bank remains the source of truth for durable strategic context.

## Strategic Thesis

DragonScale should not become "Hermes in Go."

DragonScale already has the stronger context kernel:

- projection-first turn assembly
- immutable history with lossless references
- DAG-backed recovery and retrieval
- deterministic unified runtime invariants
- RLM reduction integrated into the hot path

Hermes is stronger in a different layer:

- runtime ergonomics
- prompt economics
- bounded always-on memory for stable preferences
- skill self-maintenance
- operator-facing product shell

The right synthesis is:

- keep DragonScale's kernel contracts
- borrow Hermes-grade ergonomics around the kernel

## What To Keep

- `ActiveContextProjection` as the canonical turn contract
- immutable history and lossless recovery references
- unified runtime path from `docs/adr/002-unified-kernel-runtime.md`
- emergency-safe context reduction and budgeted assembly
- clear separation between deterministic kernel behavior and agent autonomy

## What To Borrow

### 1. Prompt stability as an invariant

Adopt more explicit stable vs session-stable vs turn-volatile prompt segmentation.

Goal:

- increase provider-side prompt cache reuse
- reduce unnecessary prompt rebuilding
- preserve the projection kernel while making rendering cheaper and more legible

### 2. Tiny bounded profile memory

Add a small always-on profile layer for durable user preferences and agent operating constraints.

This should be:

- size capped
- prompt-native rather than retrieval-first
- separate from recall and archival memory

### 3. First-class session recall surface

Expose session-scoped history retrieval as a first-class product surface.

Goal:

- easy "what did we already do?" recall
- lineage-aware history lookup
- cleaner operator and agent access to prior work

### 4. Skill maintenance loop

Treat skills as living procedural memory rather than static markdown assets.

Goal:

- detect stale skills
- propose or apply fixes under policy
- track skill freshness and evidence of successful reuse

### 5. Explicit auxiliary model roles

Make model-role boundaries more explicit in config and runtime documentation.

Examples:

- primary task model
- compression model
- cheap utility model
- extraction model
- fallback model

### 6. Pressure signaling

Expose context and iteration pressure more clearly to both the agent and the operator.

Goal:

- encourage consolidation before failure
- make runtime state legible instead of implicit

### 7. Operator shell and inspection surfaces

Improve observability with explicit product-shell commands and inspections.

Examples:

- `doctor`
- `config check`
- `context inspect`
- `projection inspect`
- `session inspect`
- `retrieval policy status`

## What Not To Copy

- file-backed markdown memory as the primary durable memory system
- transcript-first compression as the primary context engine
- broad agent autonomy over core memory mechanics
- monolithic loop design that weakens kernel boundaries

## Current Overlap With Existing Work

Some Hermes-adjacent ideas already exist in the roadmap and backlog:

- stable-prefix context ideas overlap with Observational Memory
- retrieval surface work overlaps with Agentic Retrieval
- model-role ideas overlap partially with SubAgent Profiles and model routing
- CLI/inspection improvements overlap partially with the Cobra migration

The Hermes pass should therefore create:

- one parent strategy item
- a small number of missing child issues
- explicit references to overlapping work

It should not duplicate existing roadmap items.

## New Work That Appears Missing

The research pass identified these gaps as not yet cleanly represented:

1. bounded micro-profile memory
2. explicit session-search / history-search product surface
3. skill maintenance and self-patching loop
4. explicit auxiliary model-role architecture
5. context and iteration pressure signaling
6. operator diagnostics and inspection shell

## Decision Rule

Future Hermes-inspired work must satisfy both constraints:

1. It improves operator ergonomics, prompt economics, or bounded memory behavior.
2. It does not weaken the projection-first kernel, immutable history model, or unified runtime invariants.

If a proposed Hermes-inspired change conflicts with kernel determinism, DragonScale keeps the kernel and rejects the transplant.

## References

- `README.md`
- `ROADMAP.md`
- `docs/adr/002-unified-kernel-runtime.md`
- `docs/execution/unified-kernel-blueprint.md`
- [Hermes research](956e0452-1f29-4344-ad93-2cea783ae5a6)
