#!/usr/bin/env python3
"""Generate contamination-resistant procedural long-context eval cases.

The generator creates synthetic timeline prompts with a hidden anchor token
buried inside long context, then asks the model to retrieve that token.
"""

from __future__ import annotations

import argparse
import random
from pathlib import Path


def build_case(idx: int, rng: random.Random) -> tuple[str, str]:
    project = f"Project-{idx:02d}"
    token = f"VC-{rng.randint(100000, 999999)}"
    anchor_day = rng.randint(9, 42)
    total_days = rng.randint(55, 85)

    lines = [
        "You are reading a synthetic operations timeline.",
        "Most lines are noise; one line contains a verification code.",
    ]
    for day in range(1, total_days + 1):
        if day == anchor_day:
            lines.append(
                f"Day {day}: {project} verification code is {token}; keep this for final handoff."
            )
        else:
            phase = ["intake", "triage", "prep", "handoff", "review"][day % 5]
            lines.append(
                f"Day {day}: {project} {phase} log update {rng.randint(1000, 9999)}."
            )
    lines.append(
        f"Question: What is the verification code for {project}? Respond with only the code."
    )
    return "\n".join(lines), token


def render_yaml(count: int, seed: int) -> str:
    rng = random.Random(seed)
    out = [
        "# Procedurally generated long-context eval cases.",
        f"# seed: {seed}",
        "",
    ]
    for i in range(1, count + 1):
        prompt, token = build_case(i, rng)
        prompt_lines = ["      " + line for line in prompt.splitlines()]
        out.extend(
            [
                f'- description: "procedural long-context retrieval #{i:02d}"',
                "  vars:",
                "    prompt: |",
                *prompt_lines,
                "  assert:",
                "    - type: javascript",
                "      value: |",
                "        const trace = JSON.parse(output);",
                "        if (trace.error && !trace.output) return { pass: false, score: 0, reason: 'crashed: ' + trace.error };",
                "        const out = (trace.output || '').toLowerCase();",
                f"        const expected = '{token.lower()}';",
                "        const pass = out.includes(expected);",
                "        return { pass, score: pass ? 1.0 : 0.0, reason: pass ? 'retrieved correct anchor token' : `missing ${expected}` };",
                "",
            ]
        )
    return "\n".join(out).rstrip() + "\n"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--count", type=int, default=12, help="number of cases")
    parser.add_argument("--seed", type=int, default=20260221, help="rng seed")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("eval/cases/procedural_long_context.yaml"),
        help="output YAML path",
    )
    args = parser.parse_args()

    content = render_yaml(args.count, args.seed)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(content, encoding="utf-8")
    print(f"wrote {args.output} ({args.count} cases, seed={args.seed})")


if __name__ == "__main__":
    main()
