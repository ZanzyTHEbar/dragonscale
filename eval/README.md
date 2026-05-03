# DragonScale Eval Harness

End-to-end evaluation system for the DragonScale agent runtime.

## Quick Start

```bash
# Install promptfoo (one-time)
npm install -g promptfoo

# Build eval runner and run suite
make eval
```

`make eval` auto-builds `eval/bin/eval-runner` when it is missing.

To show richer promptfoo output with progress bars (when supported), run:

```bash
make eval DRAGONSCALE_PROMPTFOO_ARGS="--no-cache"
```

To keep compact/no-progress output (current default), run:

```bash
make eval DRAGONSCALE_PROMPTFOO_ARGS="--no-cache --no-progress-bar"
```

`make eval`, `make eval-test`, `make eval-compare`, and `make eval-fixtures` auto-run inside the devcontainer when the wrapper is enabled and Docker/devcontainer support is detected, keeping command execution aligned with the container build environment.

Set both `SKIP_DEVCONTAINER_WRAPPER=1` and `DEVCONTAINER_EXEC=` at the `make` invocation to force host execution for these targets.

```bash
# Force host execution instead of the devcontainer wrapper
SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval-test

# View results in browser
make eval-view

# Run Go-native component evals
make eval-test

# A/B comparison (current branch vs main)
make eval-compare
```

## CI Verification

PR CI now runs the Go-native eval suite as the low-flake smoke proof job.

- PR workflow job: `eval-proof`
- CI smoke command: `SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval-test`
- Eval-smoke artifact uploaded by `eval-proof`:
  - `eval/results/eval-test.log`
- Coverage artifacts uploaded separately by the `test` job:
  - `coverage.txt`
  - `coverage-summary.txt`

The full promptfoo harness remains a richer non-PR verification surface for now:

- Local/manual: `make eval`
- Local provider-backed proof equivalent to the GitHub workflow: `OPENROUTER_API_KEY=sk-or-v1-... SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval-proof-full`
- Local/manual comparison: `make eval-compare`

A standalone GitHub Actions workflow now exists for full eval proof runs without widening PR gating:

- Workflow: `eval-proof-full`
- Trigger: `workflow_dispatch`
- Command: `SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval`
- Workflow environment also pins Node with `actions/setup-node` and uses fixed promptfoo args.
- Fixed promptfoo args in workflow: `--no-cache --no-progress-bar -j 1`
- Threshold gate: `PROMPTFOO_PASS_RATE_THRESHOLD=100`, so promptfoo exits non-zero unless every eval assertion passes.
- Required secret: `DRAGONSCALE_EVAL_BASE_CONFIG` (the workflow materializes this secret into a temporary base-config file with restrictive permissions and points `DRAGONSCALE_EVAL_BASE_CONFIG` at that path during the run)
- Uploaded artifacts:
  - `eval/results/make-eval.log` (always uploaded for diagnostics)
  - `eval/results/latest.json` (uploaded whenever promptfoo produces it, including threshold failures)

This keeps the PR proof deterministic while preserving the larger promptfoo suite for deeper branch verification.

## Runtime Invariants

The current agent architecture has these always-on behaviors:

- Memory is always enabled.
- Meta tools are always registered (`tool_search`, `tool_call`).
- Eval runs against a single runtime profile via `eval/bin/eval-runner`.

## Architecture

```
eval/
├── cmd/eval-runner/     # Go binary that wraps dragonscale for promptfoo
├── cases/               # Golden dataset (YAML test cases)
│   ├── tool_calling.yaml
│   ├── token_efficiency.yaml
│   ├── multi_step.yaml
│   ├── edge_cases.yaml
│   ├── memory_ops.yaml
│   ├── meta_tools.yaml
│   ├── skills.yaml
│   ├── subagent.yaml
│   ├── reasoning.yaml
│   ├── assistant_proactive.yaml
│   ├── assistant_first_metrics.yaml
│   ├── procedural_long_context.yaml
│   └── error_recovery.yaml
├── go_evals/            # Go-native component tests (memory, tools)
├── scripts/             # CI/comparison scripts
│   └── compare.sh
├── bin/                 # Built eval runner binaries (gitignored)
├── results/             # Eval output JSON (gitignored)
└── promptfooconfig.yaml # Main eval configuration
```

## How It Works

1. **eval-runner** wraps dragonscale with an instrumented language model that captures every LLM call, tool invocation, token count, and timing.

2. **promptfoo** invokes `eval-runner` via `exec:` provider, sending prompts as JSON on stdin and parsing the structured trace JSON from stdout.

3. **Assertions** are JavaScript functions that inspect the trace to score:
   - Tool selection correctness
   - Output quality
   - Token efficiency
   - Latency thresholds
   - Error handling

## Trace Format

The eval runner emits JSON with this structure:

```json
{
  "output": "Agent response text",
  "steps": [
    {"index": 0, "type": "llm_call", "duration_ms": 1200},
    {"index": 1, "type": "tool_call", "tool": "read_file", "args": {...}, "result": "..."}
  ],
  "metrics": {
    "total_duration_ms": 3400,
    "step_count": 2,
    "tool_call_count": 1,
    "input_tokens": 450,
    "output_tokens": 120,
    "total_tokens": 570,
    "reasoning_tokens": 0,
    "cache_read_tokens": 0
  },
  "error": null,
  "session_key": "eval:1708300000000"
}
```

## Adding Test Cases

Create a new YAML file in `eval/cases/` following this pattern:

```yaml
- description: "what this tests"
  vars:
    prompt: "the prompt to send to dragonscale"
  assert:
    - type: javascript
      value: |
        const trace = JSON.parse(output);
        const usedTool = trace.metrics.tool_call_count > 0;
        return {
          pass: usedTool,
          score: usedTool ? 1.0 : 0.0,
          reason: usedTool ? 'tool call observed' : 'expected at least one tool call'
        };
```

For generated long-context suites:

```bash
python eval/scripts/generate_long_context_cases.py --count 12 --seed 20260221
```

## A/B Comparison

`make eval-compare` builds both your current branch and a base ref (default `origin/main`), then runs the identical test suite against both. Results show a side-by-side comparison matrix with per-test scores.

The comparison flow uses a temporary git worktree for the base ref, so the active checkout is never stashed or branch-switched underneath the user. Override the base ref with `DRAGONSCALE_EVAL_BASE_REF` (e.g. `DRAGONSCALE_EVAL_BASE_REF=origin/develop make eval-compare`).

## Environment Variables

- `DRAGONSCALE_EVAL_CONFIG` - Optional overlay config path applied on top of user base config, for example `DRAGONSCALE_EVAL_CONFIG=./configs/custom.json make eval`.
- `DRAGONSCALE_EVAL_BASE_CONFIG` - Optional explicit base config path for eval runs.
- `DRAGONSCALE_EVAL_BASE_REF` - Git ref to compare against in `make eval-compare` (default `origin/main`). The ref is fetched before building.
- `DRAGONSCALE_EVAL_HOST_HOME` - Optional path to a host-style home directory used for host-mounted config discovery (commonly `/host_home` when set by devcontainer via `.devcontainer/devcontainer.json`).
- Base config discovery order: `DRAGONSCALE_EVAL_BASE_CONFIG` (if set and valid), then `{DRAGONSCALE_EVAL_HOST_HOME}/.config/dragonscale/config.json` (if host home is set), then XDG at `~/.config/dragonscale/config.json`.
- Provider auth for local provider-backed eval:
  - Preferred DragonScale vars: `DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY`, `DRAGONSCALE_PROVIDERS_OPENAI_API_KEY`
  - Convenience aliases: `OPENROUTER_API_KEY`, `OPENAI_API_KEY`
  - Optional model/provider overrides: `DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER`, `DRAGONSCALE_AGENTS_DEFAULTS_MODEL`
- When no provider-backed base config is found, eval defaults to OpenRouter first (`openai/gpt-4o-mini`) when an OpenRouter key is present, then OpenAI (`gpt-4o-mini`) when an OpenAI key is present.
- `promptfooconfig.yaml` intentionally does not set provider-level `DRAGONSCALE_EVAL_CONFIG`; opsctl supplies the default/override so promptfoo does not overwrite caller-provided config before launching `eval-runner`.
- For direct promptfoo runs that bypass `make eval`/opsctl, set the overlay explicitly, for example `cd eval && DRAGONSCALE_EVAL_CONFIG=./configs/default.json npx promptfoo eval -c reverify-two-cases.yaml`.
- Do not commit provider config files containing API keys. CI should continue to use the `DRAGONSCALE_EVAL_BASE_CONFIG` secret-materialization workflow.
