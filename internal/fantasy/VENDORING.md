# Vendored: charm.land/fantasy v0.8.1

## What This Is

This directory contains a vendored copy of `charm.land/fantasy` v0.8.1, the Charmbracelet
Fantasy LLM agent framework. We vendor it to enable direct modifications for PicoClaw-specific
features (progressive disclosure, custom streaming hooks, tool call repair, etc.).

## How It Works

PicoClaw's `go.mod` contains a `replace` directive:

```
replace charm.land/fantasy v0.8.1 => ./internal/fantasy
```

This redirects all `charm.land/fantasy` imports to this local copy. No import paths
need to change in either PicoClaw code or the fantasy source itself.

## Syncing Upstream Updates

To pull in a new upstream version:

1. Check the upstream version: `go list -m -versions charm.land/fantasy`
2. Download it: `go mod download charm.land/fantasy@vX.Y.Z`
3. Copy to vendor: `cp -r $(go env GOMODCACHE)/charm.land/fantasy@vX.Y.Z/* internal/fantasy/`
4. Fix permissions: `chmod -R u+w internal/fantasy/`
5. Re-apply local patches (see below)
6. Update the replace directive version in `go.mod` if needed
7. Run `go mod tidy && go test ./...`

## Local Patches

Document all local modifications here:

| Date | File | Description |
|------|------|-------------|
| (none yet) | — | Initial vendor, no patches applied |

## Original License

Fantasy is licensed under the MIT License. See `LICENSE` in this directory.
