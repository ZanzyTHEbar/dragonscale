# Vendored: charm.land/fantasy v0.22.0

## What This Is

This directory contains a vendored copy of `charm.land/fantasy`, the Charmbracelet
Fantasy LLM agent framework. DragonScale vendors Fantasy so it can track upstream
provider/runtime improvements while preserving a small local execution seam for
DragonScale-owned runtime concerns.

The project `go.mod` contains a `replace` directive:

```go
replace charm.land/fantasy v0.22.0 => ./internal/fantasy
```

This redirects all `charm.land/fantasy` imports to this local copy. No import paths
need to change in either project code or the Fantasy source itself.

## Local Patch Policy

Keep local modifications thin and app-specific.

Allowed local deltas:

- custom tool runtime injection and execution hooks
- step index propagation for persistence/offloading
- ReAct transition/step/tool-result observer hooks
- vendoring metadata and sync workflow support

Avoid carrying broad provider forks when upstream already supports the required
behavior.

## Automated Sync System

We maintain a two-layer automation system to keep this vendored copy in sync with
upstream.

### Monitoring

A GitHub Actions workflow can run `scripts/sync-fantasy.sh --check` to compare the
vendored version in `.vendor-version` against the latest module proxy version.

### Local Sync

| Command | Description |
|---------|-------------|
| `make fantasy-check` | Print current vs latest version, exit 1 if behind |
| `make fantasy-diff` | Show diff against upstream without modifying anything |
| `make fantasy-diff FANTASY_VERSION=vX.Y.Z` | Diff against a specific version |
| `make fantasy-sync` | Full sync to latest: download, replace, re-apply patches, test |
| `make fantasy-sync FANTASY_VERSION=vX.Y.Z` | Sync to a specific version |
| `make fantasy-patch NAME=my-change` | Save local modifications as a numbered patch file |

All targets are thin wrappers around `scripts/sync-fantasy.sh`.

## Sync Workflow

When a new upstream version is available:

```bash
# 1. Preview upstream changes
make fantasy-diff FANTASY_VERSION=vX.Y.Z

# 2. Save the current local source delta if it changed
make fantasy-patch NAME=dragonscale-runtime-seams

# 3. Sync and re-apply saved patches
make fantasy-sync FANTASY_VERSION=vX.Y.Z

# 4. Resolve conflicts, if any

# 5. Validate both modules
(cd internal/fantasy && go mod tidy && go test ./...)
go mod tidy
go build ./...
go test ./...
```

## Local Seam Summary

DragonScale's local seam adds these Fantasy APIs:

- `ToolRuntime`, `ParallelToolRuntime`, and `DAGToolRuntime`
- `WithToolRuntime`
- `WithStepIndex` and `StepIndexFromCtx`
- `ReActTransitionObserver`, `ReActStepObserver`, and `ReActToolResultObserver`

The app-level runtimes in `pkg/agent` depend on those APIs for SecureBus routing,
tool-result persistence, DAG-aware execution, and ReAct state recording.
