#!/usr/bin/env bash
set -euo pipefail

# compare.sh - Build both branches and run eval comparison
# Usage: ./eval/scripts/compare.sh [--repeat N]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVAL_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_ROOT="$(dirname "$EVAL_DIR")"
REPEAT=${1:-3}
NPM_CMD="${EVAL_NPM_CMD:-npx}"
read -r -a NPM_CMD_ARR <<< "${NPM_CMD}"
export DEVCONTAINER_EXEC=""
TEMP_CONFIG="$(mktemp "${SCRIPT_DIR}/promptfoo-compare-XXXXXX.yaml")"

cleanup_compare_config() {
  rm -f "$TEMP_CONFIG"
}
trap cleanup_compare_config EXIT INT TERM

if [[ "${1:-}" == "--repeat" ]]; then
  REPEAT="${2:-3}"
fi

echo "=== DragonScale Eval Comparison ==="
echo "Repeat: ${REPEAT}x per test case"
echo ""

cd "$PROJECT_ROOT"

# 1. Build current branch eval-runner (instrumented wrapper)
echo "[1/4] Building eval-runner from current branch..."
make DEVCONTAINER_EXEC= eval-build 2>&1 | tail -1
cp "$EVAL_DIR/bin/eval-runner" "$EVAL_DIR/bin/eval-runner-branch"

# 2. Build main branch eval-runner
CURRENT_BRANCH=$(git branch --show-current)
STASH_RESULT=$(git stash 2>&1)

echo "[2/4] Building eval-runner from main branch..."
git checkout main 2>/dev/null
make DEVCONTAINER_EXEC= eval-build 2>&1 | tail -1
cp "$EVAL_DIR/bin/eval-runner" "$EVAL_DIR/bin/eval-runner-main"

# Restore working branch
git checkout "$CURRENT_BRANCH" 2>/dev/null
if [[ "$STASH_RESULT" != "No local changes to save" ]]; then
  git stash pop 2>/dev/null || true
fi

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

providers:
  - id: "exec:./bin/eval-runner"
    label: "branch"
    config:
      timeout: 120000
    env:
      DRAGONSCALE_EVAL_CONFIG: "${EVAL_CONFIG}"
      DRAGONSCALE_EVAL_BASE_CONFIG: "${EVAL_BASE_CONFIG}"
  - id: "exec:./bin/eval-runner-main"
    label: "main"
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
          const valid = trace.hasOwnProperty('output') && trace.hasOwnProperty('metrics');
          return { pass: valid, score: valid ? 1.0 : 0.0, reason: valid ? 'valid trace' : 'invalid trace' };
        } catch(e) {
          return { pass: false, score: 0, reason: 'not JSON: ' + e.message };
        }
    - type: javascript
      value: |
        const trace = JSON.parse(output);
        const dur = trace.metrics.total_duration_ms;
        const ok = dur < 60000;
        return { pass: ok, score: ok ? 1.0 : 0.0, reason: `${dur}ms` };

transform: "JSON.stringify({ prompt: vars.prompt })"
tests: "cases/*.yaml"
outputPath: "results/comparison.json"
YAML

# 4. Run comparison
echo "[3/4] Running eval comparison (${REPEAT}x)..."
"${NPM_CMD_ARR[@]}" promptfoo eval -c "$TEMP_CONFIG" --repeat "$REPEAT" --no-progress-bar

echo ""
echo "[4/4] Results saved to eval/results/comparison.json"
echo ""
echo "View results: cd eval && ${NPM_CMD} promptfoo view"
