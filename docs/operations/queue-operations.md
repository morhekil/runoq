# Queue Operations

This guide covers the day-to-day operator workflow for the `agendev` issue queue.

## Queue Labels And What They Mean

`agendev` uses labels as the visible queue state.

| Label | Meaning | Typical next step |
| --- | --- | --- |
| `agendev:ready` | Eligible for queue selection once dependencies and eligibility checks pass | run `agendev run` or target it with `agendev run --issue N` |
| `agendev:in-progress` | Actively being worked or awaiting reconciliation | inspect state, PR comments, and startup reconciliation output |
| `agendev:done` | Completed successfully | inspect PR finalization and reports |
| `agendev:blocked` | Manually or externally blocked | unblock prerequisites or adjust the issue |
| `agendev:needs-human-review` | Escalated to a person after verification, review, or reconciliation | inspect the PR and issue audit comments |
| `agendev:maintenance-review` | Tracking issue for maintenance review, not part of the normal implementation queue | triage maintenance findings separately |

The queue runner only selects from open issues labeled `agendev:ready`.

## How Selection Works

Queue selection is deterministic:

1. list open `agendev:ready` issues
2. parse the `agendev:meta` block from each issue body
3. sort by `priority`, then issue number
4. skip any issue whose `depends_on` items are not `agendev:done`
5. skip any issue that fails dispatch eligibility
6. choose the first remaining actionable issue

### Common blocked reasons

The runtime surfaces blocked or skipped reasons such as:

- `dependency #12 is not agendev:done`
- `missing dependency issue #404`
- `missing acceptance criteria`
- `existing open PR #88 already tracks this issue`
- `branch agendev/... has unresolved conflicts with origin/main`

Operators should treat these as queue hygiene signals, not as transient noise.

## Before Running The Queue

Check the queue surface:

- open issues labeled `agendev:ready`
- issue bodies include the metadata block and `## Acceptance Criteria`
- dependencies really reflect sequencing constraints
- old `agendev:in-progress` issues are understood before starting

If you suspect stale state, start with:

```bash
agendev run --dry-run
```

This performs reconciliation first, then shows queue state and selection without dispatching new work.

## Dry-Run Usage

### Queue preview

```bash
agendev run --dry-run
```

Use this to answer:

- what reconciliation actions would happen at startup?
- which issue would be selected next?
- which ready issues are being skipped, and why?

The returned JSON includes:

- `mode: "dry-run"`
- `reconciliation`
- `queue`
- `selection`

### Single-issue preview

```bash
agendev run --issue 42 --dry-run
```

Use this to confirm the runtime sees the targeted issue number and to inspect reconciliation output before dispatching manually.

## Single-Issue Mode

Use single-issue mode when you want to target one queue item regardless of what else is ready.

```bash
agendev run --issue 42
```

Typical reasons:

- you are validating one specific issue end to end
- you want to retry a previously escalated or reconciled issue
- you do not want the runner to keep draining the queue afterward

Single-issue mode still performs:

- startup reconciliation
- eligibility checks
- label transition to `agendev:in-progress`
- worktree creation
- PR creation and audit comments
- verification and finalization

It stops after that one issue.

## Queue Mode

Use queue mode when you want `agendev` to keep selecting the next actionable ready issue until there is nothing left to do or the circuit breaker halts execution.

```bash
agendev run
```

Queue mode:

- reconciles interrupted runs first
- processes issues in dependency-safe priority order
- resets the consecutive-failure counter after each clean completion
- stops when no actionable `agendev:ready` issue remains

## Finalization Outcomes

An issue run ends in one of two broad outcomes:

### Clean completion

What happens:

- PR is marked ready and auto-merge is enabled
- issue label moves to `agendev:done`
- local state becomes terminal with `phase: "DONE"`
- sibling worktree is removed

This only happens when verification passes, the orchestrator verdict is `PASS`, caveats are empty, and the issue complexity is `low`.

### Human-review escalation

What happens:

- PR is marked ready for review instead of auto-merge
- reviewer/assignee may be added from config
- issue label moves to `agendev:needs-human-review`
- local state becomes terminal with `phase: "FAILED"`
- issue and PR get escalation comments explaining why

Common triggers:

- verification failure
- non-`PASS` orchestrator verdict
- caveats in the orchestrator result
- medium or high complexity issue metadata
- unrecoverable interrupted state at reconciliation time

## Circuit Breaker Behavior

Queue mode tracks consecutive non-completed issues. When the count reaches `consecutiveFailureLimit`:

- the queue stops immediately
- the runtime posts a circuit-breaker event naming the failed issues
- the command returns JSON with `status: "halted"` and `failed_issues`

Operator response:

1. inspect the named issues and their PRs
2. decide whether the failures share a common root cause
3. fix the blocking condition before resuming queue mode

Do not just rerun blindly after a circuit-breaker halt.

## What To Inspect During And After A Run

### Issue comments

Look for:

- `Skipped: ...` eligibility failures
- reconciliation messages such as `Resuming` or `Marking for human review`
- escalation comments
- circuit-breaker comments

### PR comments

Look for:

- dispatch payload comment
- `agendev:payload:codex-return` comment for the dev round
- payload reconstruction comments when malformed output was patched or synthesized
- verification failure comments
- final orchestrator verdict comment

### PR body

Look for:

- updated summary section
- updated areas-for-human-attention section
- whether the PR was left in review state or moved to auto-merge

### Local reports

Use:

```bash
agendev report summary
agendev report issue 42
agendev report cost
```

Use them to confirm:

- whether the issue reached `DONE` or `FAILED`
- the saved branch, PR number, and timestamps
- token totals and estimated cost over recent runs

## Operator Routine

For normal operation, this cadence works well:

1. Run `agendev run --dry-run` to inspect reconciliation and selection.
2. If the queue looks healthy, run `agendev run`.
3. If one issue needs focused attention, run `agendev run --issue N`.
4. After the run, inspect issue comments, PR comments, and `agendev report summary`.
5. If the queue halted or escalated repeatedly, fix the root cause before the next run.

## Related Docs

- [Operator quickstart](./quickstart.md)
- [Execution and maintenance flows](../architecture/flows.md)
- [State and audit model](../reference/state-model.md)
