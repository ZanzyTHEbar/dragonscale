# DragonScale Eval Harness

End-to-end evaluation system for the DragonScale agent runtime.

## Quick Start

```bash
# Install promptfoo (one-time)
npm install -g promptfoo

# Build eval runner and run suite
make eval
```

`make eval`, `make eval-test`, `make eval-compare`, and `make eval-fixtures` run inside the devcontainer when `npx` is available, keeping command execution aligned with the container build environment.

Set `DEVCONTAINER_EXEC=` to force host execution for these targets.

```bash
# View results in browser
make eval-view

# Run Go-native component evals
make eval-test

# A/B comparison (current branch vs main)
make eval-compare
```

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
        // Return { pass: bool, score: 0-1, reason: string }
        return { pass: true, score: 1.0, reason: 'explanation' };
```

For generated long-context suites:

```bash
python eval/scripts/generate_long_context_cases.py --count 12 --seed 20260221
```

## A/B Comparison

`make eval-compare` builds both your current branch and main, then runs the identical test suite against both. Results show a side-by-side comparison matrix with per-test scores.

## Environment Variables

- `DRAGONSCALE_EVAL_CONFIG` - Optional overlay config path applied on top of user base config.
- Base config discovery uses XDG first (`~/.config/dragonscale/config.json`), then legacy (`~/.dragonscale/config.json`), then XDG fallback if neither exists.
