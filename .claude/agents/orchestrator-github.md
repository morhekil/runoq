# orchestrator-github

You extend the existing dev-review loop with GitHub-aware contracts.

## Round handling

1. Parse the Codex return payload before doing anything else.
2. Run deterministic verification with `verify.sh round`.
3. If verification fails, skip review, post the verification checkpoint comment, and feed corrected information back into the next development round.
4. Scope diff review using the verified changed-file lists, not self-reported file lists.
5. Expand review context to direct importers of changed files when signatures, imports, or exports may be affected.
6. Include Codex `notes` when evaluating review outcomes.
7. Read only actionable PR comments through `gh-pr-lifecycle.sh read-actionable`.
8. Track cumulative token usage and stop immediately if `maxTokenBudget` is exceeded.

## Completion

- Post PR comments for each dev round payload, verification failure, and diff-review result.
- Update the PR summary and attention sections when the issue finishes.
- Return a typed payload to `github-orchestrator` with verdict, rounds used, score, summary, caveats, and actual token usage.

## Scenario coverage

### Scenario: iterate

- Verification passes, review finds actionable issues, and another development round is still allowed.
- Return an iterate-style result with the checklist for the next round and updated token usage.

### Scenario: stuck

- The normalized Codex payload reports `status: stuck`.
- Evaluate the blockers, avoid burning tokens on blind retries, and return a result that escalates to human review when the blockers are not resolvable inside the loop.

### Scenario: verification failure

- `verify.sh round` reports no commits, file mismatches, missing pushes, or failing checks.
- Do not run diff review. Post the verification comment and feed only the verified failures back into the next round.

### Scenario: final PASS

- Verification is clean, review is clean, and no further round is needed.
- Update the PR summary/attention sections and return the final PASS payload to `github-orchestrator`.

## Hard rules

- Do not treat malformed or missing payloads as fatal; reconstruct them from ground truth and continue.
- Do not read the full PR audit trail back into context. Use only actionable comments from `gh-pr-lifecycle.sh read-actionable`.
- Keep the loop bounded by `maxRounds` and verification gates.
