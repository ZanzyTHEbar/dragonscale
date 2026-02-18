#!/usr/bin/env bash
set -euo pipefail

# sync-fantasy.sh — Monitor and sync the vendored charm.land/fantasy SDK.
#
# Usage:
#   ./scripts/sync-fantasy.sh --check              # compare vendored vs latest
#   ./scripts/sync-fantasy.sh --diff  [VERSION]    # show diff without modifying
#   ./scripts/sync-fantasy.sh --sync  [VERSION]    # full sync + patch re-apply
#   ./scripts/sync-fantasy.sh --save-patch NAME    # capture working-tree diff as patch

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR_DIR="${REPO_ROOT}/internal/fantasy"
VERSION_FILE="${VENDOR_DIR}/.vendor-version"
PATCHES_DIR="${VENDOR_DIR}/patches"
VENDORING_MD="${VENDOR_DIR}/VENDORING.md"
GO_MOD="${REPO_ROOT}/go.mod"
MODULE="charm.land/fantasy"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { printf "${CYAN}[fantasy-sync]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[fantasy-sync]${NC} %s\n" "$*" >&2; }
err()  { printf "${RED}[fantasy-sync]${NC} %s\n" "$*" >&2; }
ok()   { printf "${GREEN}[fantasy-sync]${NC} %s\n" "$*"; }

current_version() {
    if [[ -f "$VERSION_FILE" ]]; then
        cat "$VERSION_FILE" | tr -d '[:space:]'
    else
        # Fall back to parsing VENDORING.md header
        grep -oP 'v[\d]+\.[\d]+\.[\d]+' "$VENDORING_MD" | head -1 || echo "unknown"
    fi
}

latest_version() {
    # Query the Go module proxy for the latest version.
    # go list -m -versions returns all tags; we pick the last one.
    local versions
    versions=$(go list -m -versions "${MODULE}" 2>/dev/null | awk '{$1=""; print}' | tr ' ' '\n' | grep -v '^\s*$' | tail -1)
    if [[ -z "$versions" ]]; then
        err "Could not query versions for ${MODULE}"
        exit 1
    fi
    echo "$versions"
}

download_version() {
    local ver="$1"
    log "Downloading ${MODULE}@${ver} ..." >&2
    go mod download "${MODULE}@${ver}" >/dev/null 2>&1

    local cache_dir
    cache_dir="$(go env GOMODCACHE)/${MODULE}@${ver}"
    if [[ ! -d "$cache_dir" ]]; then
        err "Downloaded module not found at ${cache_dir}"
        exit 1
    fi
    echo "$cache_dir"
}

# Extract version major component for breaking-change detection.
major_of() { echo "$1" | grep -oP '^v\K[0-9]+'; }

# ---------------------------------------------------------------------------
# --check
# ---------------------------------------------------------------------------
cmd_check() {
    local cur latest
    cur="$(current_version)"
    latest="$(latest_version)"

    printf "${BOLD}Vendored:${NC}  %s\n" "$cur"
    printf "${BOLD}Latest:${NC}    %s\n" "$latest"

    if [[ "$cur" == "$latest" ]]; then
        ok "Up to date."
        exit 0
    fi

    local cur_major latest_major
    cur_major="$(major_of "$cur")"
    latest_major="$(major_of "$latest")"
    if [[ "$cur_major" != "$latest_major" ]]; then
        warn "MAJOR version change (${cur_major} -> ${latest_major}) — expect breaking API changes."
    fi

    warn "Behind upstream by $(echo "$latest" | sed "s/$cur//") — run: make fantasy-sync VERSION=${latest}"
    exit 1
}

# ---------------------------------------------------------------------------
# --diff [VERSION]
# ---------------------------------------------------------------------------
cmd_diff() {
    local target="${1:-$(latest_version)}"
    local cur
    cur="$(current_version)"

    log "Diffing vendored (${cur}) against upstream (${target}) ..."

    local upstream_dir
    upstream_dir="$(download_version "$target")"

    local tmp_upstream
    tmp_upstream="$(mktemp -d)"
    trap "rm -rf '${tmp_upstream}'" EXIT

    # Copy upstream to writable temp (module cache is read-only).
    cp -r "${upstream_dir}/." "${tmp_upstream}/"

    # Exclude non-source noise from the diff.
    local diff_opts=(
        --no-index
        --stat
        --diff-filter=ACDMRT
        --                        
        "${VENDOR_DIR}"
        "${tmp_upstream}"
    )

    echo ""
    printf "${BOLD}=== Upstream changes (${cur} -> ${target}) ===${NC}\n"
    git diff --no-index --stat -- "${VENDOR_DIR}" "${tmp_upstream}" 2>/dev/null || true
    echo ""
    printf "${BOLD}=== Full diff ===${NC}\n"
    git diff --no-index --no-color -- "${VENDOR_DIR}" "${tmp_upstream}" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# --sync [VERSION]
# ---------------------------------------------------------------------------
cmd_sync() {
    local target="${1:-$(latest_version)}"
    local cur
    cur="$(current_version)"

    if [[ "$cur" == "$target" ]]; then
        ok "Already at ${target}. Nothing to do."
        exit 0
    fi

    local cur_major target_major
    cur_major="$(major_of "$cur")"
    target_major="$(major_of "$target")"
    if [[ "$cur_major" != "$target_major" ]]; then
        warn "MAJOR version change (${cur_major} -> ${target_major}) — breaking API changes likely."
        read -r -p "Continue? [y/N] " confirm
        [[ "$confirm" =~ ^[Yy]$ ]] || { log "Aborted."; exit 0; }
    fi

    log "Syncing ${MODULE}: ${cur} -> ${target}"

    # 1. Download upstream
    local upstream_dir
    upstream_dir="$(download_version "$target")"

    # 2. Show what we're about to change
    log "Changes from upstream:"
    git diff --no-index --stat -- "${VENDOR_DIR}" "${upstream_dir}" 2>/dev/null || true
    echo ""

    # 3. Capture local modifications before overwriting.
    #    Compare current vendored copy against the CACHED version of the OLD upstream
    #    to see what local patches exist that aren't tracked yet.
    local old_upstream_dir
    old_upstream_dir="$(go env GOMODCACHE)/${MODULE}@${cur}" 2>/dev/null || true
    if [[ -d "$old_upstream_dir" ]]; then
        local untracked_diff
        untracked_diff="$(git diff --no-index --stat -- "$old_upstream_dir" "${VENDOR_DIR}" 2>/dev/null || true)"
        if [[ -n "$untracked_diff" ]]; then
            warn "Detected LOCAL modifications not captured as patches:"
            echo "$untracked_diff"
            warn "These will be LOST unless you save them first: make fantasy-patch NAME=<description>"
            read -r -p "Continue anyway? [y/N] " confirm
            [[ "$confirm" =~ ^[Yy]$ ]] || { log "Aborted. Save patches first."; exit 1; }
        fi
    fi

    # 4. Replace vendored copy
    log "Replacing vendored copy ..."
    # Preserve our patches directory and metadata.
    local tmp_patches
    tmp_patches="$(mktemp -d)"
    if [[ -d "$PATCHES_DIR" ]]; then
        cp -r "${PATCHES_DIR}" "${tmp_patches}/patches"
    fi
    local tmp_version=""
    [[ -f "$VERSION_FILE" ]] && tmp_version="$(cat "$VERSION_FILE")"

    rm -rf "${VENDOR_DIR}"
    cp -r "${upstream_dir}" "${VENDOR_DIR}"
    chmod -R u+w "${VENDOR_DIR}"

    # Restore patches directory and metadata.
    if [[ -d "${tmp_patches}/patches" ]]; then
        cp -r "${tmp_patches}/patches" "${PATCHES_DIR}"
    else
        mkdir -p "${PATCHES_DIR}"
    fi
    rm -rf "${tmp_patches}"

    # 5. Re-apply local patches
    local patch_count=0
    local patch_failed=0
    if compgen -G "${PATCHES_DIR}/*.patch" >/dev/null 2>&1; then
        log "Re-applying local patches ..."
        for patch_file in "${PATCHES_DIR}"/*.patch; do
            local pname
            pname="$(basename "$patch_file")"
            if git apply --directory="internal/fantasy" --check "$patch_file" 2>/dev/null; then
                git apply --directory="internal/fantasy" "$patch_file"
                ok "  Applied: ${pname}"
                patch_count=$((patch_count + 1))
            else
                err "  FAILED:  ${pname}"
                err "  Conflict — resolve manually. Reject file may be in internal/fantasy/"
                patch_failed=$((patch_failed + 1))
            fi
        done
    fi

    if [[ $patch_failed -gt 0 ]]; then
        err "${patch_failed} patch(es) failed to apply. Fix conflicts, then run:"
        err "  go build ./... && go test ./internal/fantasy/... ./pkg/fantasy/..."
        # Still update version file so re-running sync doesn't re-download.
        echo "${target}" > "${VERSION_FILE}"
        exit 1
    fi

    # 6. Update metadata
    echo "${target}" > "${VERSION_FILE}"

    # Update go.mod replace directive
    sed -i "s|replace ${MODULE} v[^ ]* => ./internal/fantasy|replace ${MODULE} ${target} => ./internal/fantasy|" "${GO_MOD}"

    # Update go.mod require version
    sed -i "s|${MODULE} v[^ ]*|${MODULE} ${target}|" "${GO_MOD}"

    # Update VENDORING.md header
    if [[ -f "$VENDORING_MD" ]]; then
        sed -i "s|${MODULE} v[0-9.]*|${MODULE} ${target}|g" "${VENDORING_MD}"
    fi

    # 7. Tidy and validate
    log "Running go mod tidy ..."
    (cd "${REPO_ROOT}" && go mod tidy)

    log "Building ..."
    (cd "${REPO_ROOT}" && go build ./...)

    log "Testing fantasy packages ..."
    (cd "${REPO_ROOT}" && go test ./internal/fantasy/... ./pkg/fantasy/... 2>&1) || {
        warn "Some tests failed — review output above."
    }

    echo ""
    ok "Sync complete: ${cur} -> ${target}"
    [[ $patch_count -gt 0 ]] && ok "${patch_count} local patch(es) re-applied."
    ok "Next steps:"
    ok "  1. Review changes:  git diff"
    ok "  2. Run full tests:  go test ./..."
    ok "  3. Commit:          git add -A && git commit -m 'build(fantasy): sync vendored SDK to ${target}'"
}

# ---------------------------------------------------------------------------
# --save-patch NAME
# ---------------------------------------------------------------------------
cmd_save_patch() {
    local name="${1:?Usage: sync-fantasy.sh --save-patch <name>}"
    local cur
    cur="$(current_version)"

    mkdir -p "${PATCHES_DIR}"

    # We diff the module-cache copy of the current version against our vendored copy.
    local pristine_dir
    pristine_dir="$(go env GOMODCACHE)/${MODULE}@${cur}"
    if [[ ! -d "$pristine_dir" ]]; then
        log "Downloading pristine ${cur} for comparison ..."
        go mod download "${MODULE}@${cur}" >/dev/null 2>&1
        pristine_dir="$(go env GOMODCACHE)/${MODULE}@${cur}"
    fi

    # Count existing patches to determine next sequence number.
    local next_num
    next_num=$(printf "%04d" "$(( $(ls -1 "${PATCHES_DIR}"/*.patch 2>/dev/null | wc -l) + 1 ))")

    local patch_file="${PATCHES_DIR}/${next_num}-${name}.patch"

    # Generate the diff, stripping the absolute paths to make it relative.
    git diff --no-index -- "${pristine_dir}" "${VENDOR_DIR}" 2>/dev/null \
        | sed "s|a${pristine_dir}/|a/|g; s|b${VENDOR_DIR}/|b/|g" \
        > "${patch_file}" || true

    if [[ ! -s "$patch_file" ]]; then
        rm -f "$patch_file"
        warn "No local modifications detected against pristine ${cur}."
        exit 0
    fi

    local stat
    stat="$(diffstat -s "$patch_file" 2>/dev/null || wc -l < "$patch_file")"
    ok "Saved patch: ${patch_file}"
    ok "Stats: ${stat}"
}

# ---------------------------------------------------------------------------
# Main dispatch
# ---------------------------------------------------------------------------
usage() {
    cat <<USAGE
Usage: $(basename "$0") <command> [args]

Commands:
  --check               Compare vendored vs latest upstream version
  --diff   [VERSION]    Show diff without modifying (default: latest)
  --sync   [VERSION]    Full sync: download, replace, re-apply patches, test
  --save-patch NAME     Capture local modifications as a numbered patch file

Environment:
  MODULE=${MODULE}
  VENDOR_DIR=${VENDOR_DIR}
USAGE
    exit 1
}

case "${1:-}" in
    --check)      cmd_check ;;
    --diff)       cmd_diff "${2:-}" ;;
    --sync)       cmd_sync "${2:-}" ;;
    --save-patch) cmd_save_patch "${2:-}" ;;
    *)            usage ;;
esac
