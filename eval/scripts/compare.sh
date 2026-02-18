#!/usr/bin/env bash
set -euo pipefail

# compare.sh - Build both branches and run eval comparison
# Usage: ./eval/scripts/compare.sh [--repeat N]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVAL_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_ROOT="$(dirname "$EVAL_DIR")"
REPEAT=${1:-3}

if [[ "${1:-}" == "--repeat" ]]; then
  REPEAT="${2:-3}"
fi

echo "=== PicoClaw Eval Comparison ==="
echo "Repeat: ${REPEAT}x per test case"
echo ""

cd "$PROJECT_ROOT"

# 1. Build current branch
echo "[1/4] Building current branch..."
make build 2>&1 | tail -1
cp build/picoclaw "$EVAL_DIR/bin/eval-runner"

# 2. Build main branch
CURRENT_BRANCH=$(git branch --show-current)
STASH_RESULT=$(git stash 2>&1)

echo "[2/4] Building main branch..."
git checkout main 2>/dev/null
make build 2>&1 | tail -1
cp build/picoclaw "$EVAL_DIR/bin/eval-runner-main"

# Restore
git checkout "$CURRENT_BRANCH" 2>/dev/null
if [[ "$STASH_RESULT" != "No local changes to save" ]]; then
  git stash pop 2>/dev/null || true
fi

# 3. Update promptfoo config to enable comparison
cd "$EVAL_DIR"

# Create a temp config with both providers
cat > promptfooconfig-compare.yaml <<'YAML'
description: "PicoClaw A/B comparison (branch vs main)"

providers:
  - id: "exec:./bin/eval-runner"
    label: "branch"
    config:
      timeout: 120000
  - id: "exec:./bin/eval-runner-main"
    label: "main"
    config:
      timeout: 120000

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
npx promptfoo eval -c promptfooconfig-compare.yaml --repeat "$REPEAT" --no-progress-bar

echo ""
echo "[4/4] Results saved to eval/results/comparison.json"
echo ""
echo "View results: cd eval && npx promptfoo view"
