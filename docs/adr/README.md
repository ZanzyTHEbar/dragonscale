# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for DragonScale.

ADRs document significant technical decisions — the context, the options considered, the chosen approach, and the trade-offs accepted. They create a historical record of how the system evolved and why.

## Format

Each ADR follows [Michael Nygard's template](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions):

- **Status**: Proposed | Accepted | Superseded | Deprecated
- **Context**: What forces are at play
- **Decision**: What we chose
- **Consequences**: What changes as a result

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [001](001-isolated-tool-runtime.md) | Isolated Tool Runtime (ITR) + DAG Task Executor + RLM Engine | Proposed |
| [002](002-unified-kernel-runtime.md) | Unified Kernel Runtime | Accepted |

## Development Requirements

- FlatBuffers (`flatc`) is required for schema-driven code generation workflows documented in ADR-001.
- sqlc is required for SQL query code generation in memory/runtime persistence layers.
- Canonical local checks:
  - `make flatc-check`
  - `make sqlc-check`
- Canonical containerized checks:
  - `make devcontainer-generate`
  - `make devcontainer-verify`
