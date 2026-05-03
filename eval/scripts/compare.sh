#!/usr/bin/env bash
set -euo pipefail

# compare.sh - Build both branches and run eval comparison
# Usage: ./eval/scripts/compare.sh [--repeat N]
#
# Environment:
#   DRAGONSCALE_EVAL_BASE_REF   Local Git ref or fetchable <remote>/<branch>
#                               to compare against (default: origin/main).
#                               Remote branch refs are fetched before building.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVAL_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_ROOT="$(dirname "$EVAL_DIR")"
REPEAT=${1:-3}
NPM_CMD="${EVAL_NPM_CMD:-npx}"
read -r -a NPM_CMD_ARR <<< "${NPM_CMD}"
PROMPTFOO_ARGS="${DRAGONSCALE_PROMPTFOO_ARGS:---no-cache --no-progress-bar -j 1}"
read -r -a PROMPTFOO_ARGS_ARR <<< "${PROMPTFOO_ARGS}"
export DEVCONTAINER_EXEC=""
TEMP_CONFIG="$(mktemp "${SCRIPT_DIR}/promptfoo-compare-XXXXXX.yaml")"
TEMP_WORKTREE=""
BASE_REF="${DRAGONSCALE_EVAL_BASE_REF:-origin/main}"

cleanup_compare_config() {
  rm -f "$TEMP_CONFIG"
  if [[ -n "$TEMP_WORKTREE" && -d "$TEMP_WORKTREE" ]]; then
    git -C "$PROJECT_ROOT" worktree remove --force "$TEMP_WORKTREE" >/dev/null 2>&1 || rm -rf "$TEMP_WORKTREE"
  fi
}
trap cleanup_compare_config EXIT INT TERM

if [[ "${1:-}" == "--repeat" ]]; then
  REPEAT="${2:-3}"
fi

resolve_base_ref() {
  if [[ "$BASE_REF" == */* ]]; then
    local remote="${BASE_REF%%/*}"
    local remote_ref="${BASE_REF#*/}"
    if git -C "$PROJECT_ROOT" remote get-url "$remote" >/dev/null 2>&1 && [[ "$remote_ref" != *[\~^:{}]* ]]; then
      echo "Fetching ${remote}/${remote_ref}..."
      git -C "$PROJECT_ROOT" fetch --prune "$remote" "refs/heads/${remote_ref}:refs/remotes/${remote}/${remote_ref}" >/dev/null
      return
    fi
  fi

  if ! git -C "$PROJECT_ROOT" rev-parse --verify --quiet "${BASE_REF}^{commit}" >/dev/null; then
    echo "error: base ref ${BASE_REF} is not a known local ref; use <remote>/<branch> for fetchable branches or fetch it first" >&2
    exit 1
  fi
}

echo "=== DragonScale Eval Comparison ==="
echo "Repeat: ${REPEAT}x per test case"
echo ""

cd "$PROJECT_ROOT"

# 1. Build current branch eval-runner (instrumented wrapper)
echo "[1/4] Building eval-runner from current branch..."
make DEVCONTAINER_EXEC= eval-build 2>&1 | tail -1
cp "$EVAL_DIR/bin/eval-runner" "$EVAL_DIR/bin/eval-runner-branch"

# 2. Build base ref eval-runner in an isolated worktree
echo "[2/4] Building eval-runner from ${BASE_REF}..."
TEMP_WORKTREE="$(mktemp -d "${TMPDIR:-/tmp}/dragonscale-eval-base-XXXXXX")"
# Fetch configured remote refs deterministically, or verify local refs before building.
resolve_base_ref
git -C "$PROJECT_ROOT" worktree add --force --detach "$TEMP_WORKTREE" "$BASE_REF" >/dev/null
make -C "$TEMP_WORKTREE" DEVCONTAINER_EXEC= eval-build 2>&1 | tail -1
cp "$TEMP_WORKTREE/eval/bin/eval-runner" "$EVAL_DIR/bin/eval-runner-main"

# Put branch binary back as the default eval-runner
cp "$EVAL_DIR/bin/eval-runner-branch" "$EVAL_DIR/bin/eval-runner"

# 3. Update promptfoo config to enable comparison
cd "$EVAL_DIR"

# Resolve base config for promptfoo providers from explicit env if provided.
EVAL_BASE_CONFIG="${DRAGONSCALE_EVAL_BASE_CONFIG:-}"
EVAL_CONFIG="${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}"
if [ -z "$EVAL_BASE_CONFIG" ] && [ -n "${DRAGONSCALE_EVAL_DEBUG:-}" ]; then
  echo "warning: DRAGONSCALE_EVAL_BASE_CONFIG not set; default config will be resolved by runtime" >&2
fi

if [ -n "${DRAGONSCALE_EVAL_DEBUG:-}" ]; then
  echo "DRAGONSCALE_EVAL_BASE_CONFIG=${EVAL_BASE_CONFIG}"
fi

# Create a temp config with both providers
cat > "$TEMP_CONFIG" <<YAML
description: "DragonScale A/B comparison (branch vs main)"

maxConcurrency: 1

providers:
  - id: "exec:./bin/eval-runner"
    label: "branch"
    config:
      timeout: 120000
    env:
      DRAGONSCALE_EVAL_CONFIG: "${EVAL_CONFIG}"
      DRAGONSCALE_EVAL_BASE_CONFIG: "${EVAL_BASE_CONFIG}"
  - id: "exec:./bin/eval-runner-main"
    label: "${BASE_REF}"
    config:
      timeout: 120000
    env:
      DRAGONSCALE_EVAL_CONFIG: "${EVAL_CONFIG}"
      DRAGONSCALE_EVAL_BASE_CONFIG: "${EVAL_BASE_CONFIG}"

defaultTest:
  assert:
    - type: javascript
      value: |
        try {
          const trace = JSON.parse(output);
          const valid = typeof trace.output === 'string' && Array.isArray(trace.steps) && trace.metrics && typeof trace.metrics.total_duration_ms === 'number';
          return { pass: valid, score: valid ? 1.0 : 0.0, reason: valid ? 'valid trace' : 'invalid trace' };
        } catch(e) {
          return { pass: false, score: 0, reason: 'not JSON: ' + e.message };
        }
    - type: javascript
      value: |
        const trace = JSON.parse(output);
        const dur = trace.metrics.total_duration_ms;
        const score = dur < 30000 ? 1.0 : dur < 90000 ? 1.0 - (dur - 30000) / 60000 : 0.0;
        const pass = dur < 90000;
        return { pass, score, reason: `${dur}ms (score: ${score.toFixed(2)})` };

transform: "JSON.stringify({ prompt: vars.prompt })"
tests: "cases/*.yaml"
outputPath: "results/comparison.json"
YAML

# 4. Run comparison
echo "[3/4] Running eval comparison (${REPEAT}x)..."
"${NPM_CMD_ARR[@]}" promptfoo eval -c "$TEMP_CONFIG" --repeat "$REPEAT" "${PROMPTFOO_ARGS_ARR[@]}"

echo ""
echo "[4/4] Results saved to eval/results/comparison.json"
echo ""
echo "View results: cd eval && ${NPM_CMD} promptfoo view"
