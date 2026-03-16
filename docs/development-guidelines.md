# Development Guidelines

This file captures the main lessons from the implementation work completed through backlog task `T34`.

## What this iteration proved

- Keep orchestration rules in shell scripts and JSON contracts. The more behavior that lives in prompts, the harder it is to recover, test, and review.
- Resume behavior needs explicit terminal-state handling. The maintenance runner only became safe after treating `COMPLETED` as idempotent instead of replaying its final summary.
- Deterministic fake-`gh` integration coverage should carry almost all of the load. Real GitHub checks are still necessary, but only for auth, attribution, and permission edges that fixtures cannot prove.
- Live smoke tests must be opt-in and credential-gated. They should never affect normal local `bats` runs.
- Diff review after green tests still finds real issues. Two bugs fixed during review were a duplicated maintenance completion comment and a smoke runner path that skipped GitHub App auth when `GH_TOKEN` was already present.

## Rules for future work

- Start with a failing Bats test whenever the behavior is deterministic enough to express locally.
- Keep script boundaries machine-readable. Prefer JSON output to prose whenever another script or test consumes the result.
- Preserve the target repo working tree. Use sibling worktrees or temporary clones for execution and smoke flows.
- Treat `.agendev/state/*.json` as resumability breadcrumbs. Write them atomically and make resume paths explicit.
- Reuse existing deterministic helpers before adding new prompt logic.
- Run `shellcheck -x` on every shell script you touch.
- After each backlog task, update the task status and implementation notes in [docs/backlog.md](/Users/Saruman/Projects/agendev/docs/backlog.md).
- Commit each completed task separately with a scoped message.

## Test guidance

- Prefer focused suites first, then run adjacent regression coverage before committing.
- For queue or maintenance behavior, default to fake-`gh` integration tests in `test/*.bats`.
- Keep live GitHub validation in [docs/live-smoke.md](/Users/Saruman/Projects/agendev/docs/live-smoke.md) and [scripts/live-smoke.sh](/Users/Saruman/Projects/agendev/scripts/live-smoke.sh).
- Any live smoke addition must stay minimal, sandbox-only, and clean up issues, PRs, and branches it creates.

## Maintenance-specific guidance

- Maintenance review stays read-only until a human explicitly approves a finding.
- Findings should be resumable, triageable, and safe to replay without duplicating side effects.
- Recurring-pattern summaries are useful only after all findings leave `pending`.
