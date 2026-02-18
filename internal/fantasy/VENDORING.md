# Vendored: charm.land/fantasy v0.8.1

## What This Is

This directory contains a vendored copy of `charm.land/fantasy`, the Charmbracelet
Fantasy LLM agent framework. We vendor it to enable direct modifications for PicoClaw-specific
features (progressive disclosure, custom streaming hooks, tool call repair, etc.).

PicoClaw's `go.mod` contains a `replace` directive:

```
replace charm.land/fantasy v0.8.1 => ./internal/fantasy
```

This redirects all `charm.land/fantasy` imports to this local copy. No import paths
need to change in either PicoClaw code or the fantasy source itself.

## Automated Sync System

We maintain a two-layer automation system to keep this vendored copy in sync with upstream.

### Monitoring (CI)

A GitHub Actions workflow (`.github/workflows/fantasy-sync.yml`) runs weekly and:

1. Queries the Go module proxy for the latest `charm.land/fantasy` version
2. Compares it against the version in `.vendor-version`
3. If a new version exists, generates a diff summary and creates a GitHub issue
4. Issues are deduplicated — only one open issue per upstream version

### Local Sync (Makefile targets)

| Command | Description |
|---------|-------------|
| `make fantasy-check` | Print current vs latest version, exit 1 if behind |
| `make fantasy-diff` | Show diff against upstream without modifying anything |
| `make fantasy-diff FANTASY_VERSION=v0.9.0` | Diff against a specific version |
| `make fantasy-sync` | Full sync to latest: download, replace, re-apply patches, test |
| `make fantasy-sync FANTASY_VERSION=v0.9.0` | Sync to a specific version |
| `make fantasy-patch NAME=my-change` | Save local modifications as a numbered patch file |

All targets are thin wrappers around `scripts/sync-fantasy.sh`.

## Sync Workflow

When the CI creates an issue (or you notice a new version), follow these steps:

```bash
# 1. Preview what changed upstream
make fantasy-diff FANTASY_VERSION=vX.Y.Z

# 2. If the changes look good, run the full sync
make fantasy-sync FANTASY_VERSION=vX.Y.Z

# 3. Review the result
git diff

# 4. Run the full test suite
go test ./...

# 5. Commit
git add -A && git commit -m "build(fantasy): sync vendored SDK to vX.Y.Z"
```

The sync script will:
- Download the target version from the Go module cache
- Replace the vendored copy (preserving the `patches/` directory)
- Re-apply all local patches in order
- Update `.vendor-version`, `go.mod` replace directive, and this file
- Run `go build` and `go test` for validation

If any patch fails to apply, the script aborts with a clear error message showing
which patch conflicted. You'll need to resolve the conflict manually, then re-save
the patch with `make fantasy-patch`.

## Patch Management

Local modifications to the vendored SDK are tracked as numbered `.patch` files in the
`patches/` directory:

```
internal/fantasy/patches/
  0001-progressive-disclosure-hooks.patch
  0002-custom-streaming-callbacks.patch
  ...
```

### Creating a patch

Before making a local modification:

1. Make your changes to files in `internal/fantasy/`
2. Run `make fantasy-patch NAME=descriptive-name`
3. The script diffs your working tree against the pristine upstream version
4. A numbered patch file is generated in `patches/`

### How patches are re-applied during sync

During `make fantasy-sync`, the script:
1. Replaces the entire vendored directory with the new upstream version
2. Restores the `patches/` directory
3. Applies each `.patch` file in sequence using `git apply`
4. If a patch fails, the sync halts — fix the conflict and re-save the patch

### Resolving patch conflicts

If a patch fails during sync:
1. The script prints which patch conflicted
2. Manually apply the intent of the patch to the new upstream code
3. Delete the old patch: `rm internal/fantasy/patches/NNNN-name.patch`
4. Re-save: `make fantasy-patch NAME=name`
5. Re-run: `make fantasy-sync FANTASY_VERSION=vX.Y.Z`

## Files

| File | Purpose |
|------|---------|
| `.vendor-version` | Machine-readable current vendored version (`v0.8.1`) |
| `patches/` | Directory of local modification patches |
| `patches/.gitkeep` | Ensures the directory is tracked in git |
| `VENDORING.md` | This documentation file |

## Original License

Fantasy is licensed under the MIT License. See `LICENSE` in this directory.
