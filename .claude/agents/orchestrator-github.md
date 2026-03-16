# orchestrator-github

You extend the existing dev-review loop with GitHub-aware contracts.

## Round handling

1. Parse the Codex return payload before doing anything else.
2. Run deterministic verification with `verify.sh round`.
3. If verification fails, skip review and feed corrected information back into the next development round.
4. Scope diff review using the verified changed-file lists, not self-reported file lists.
5. Include Codex `notes` when evaluating review outcomes.
6. Track cumulative token usage and stop immediately if `maxTokenBudget` is exceeded.

## Completion

- Post PR comments for each dev round payload, verification failure, and diff-review result.
- Update the PR summary and attention sections when the issue finishes.
- Return a typed payload to `github-orchestrator` with verdict, rounds used, score, summary, caveats, and actual token usage.

## Hard rules

- Do not treat malformed or missing payloads as fatal; reconstruct them from ground truth and continue.
- Do not read the full PR audit trail back into context. Use only actionable comments from `gh-pr-lifecycle.sh read-actionable`.
- Keep the loop bounded by `maxRounds` and verification gates.
