# Queue Operations

This guide covers the day-to-day operator workflow for the `runoq` issue queue.

## Queue Labels And What They Mean

`runoq` uses labels as the visible queue state.

| Label | Meaning | Typical next step |
| --- | --- | --- |
| `runoq:ready` | Eligible for queue selection once dependencies and eligibility checks pass | run `runoq loop` or target it with `runoq tick --issue N` / `runoq loop --issue N` |
| `runoq:in-progress` | Actively being worked or awaiting reconciliation | inspect state, PR comments, and startup reconciliation output |
| `runoq:done` | Completed successfully | inspect PR finalization and reports |
| `runoq:blocked` | Manually or externally blocked | unblock prerequisites or adjust the issue |
| `runoq:needs-human-review` | Escalated to a person after verification, review, or reconciliation | inspect the PR and issue audit comments |
| `runoq:maintenance-review` | Tracking issue for maintenance review, not part of the normal implementation queue | triage maintenance findings separately |

The queue runner only selects from open issues labeled `runoq:ready`.

## How Selection Works

Queue selection is deterministic:

1. list open `runoq:ready` issues
2. derive metadata from labels and native GitHub APIs
3. sort by `priority`, then issue number
4. skip any issue whose `depends_on` items are not `runoq:done`
5. skip any issue that fails dispatch eligibility
6. choose the first remaining actionable issue

### Common blocked reasons

The runtime surfaces blocked or skipped reasons such as:

- `dependency #12 is not runoq:done`
- `missing dependency issue #404`
- `missing acceptance criteria`
- `existing open PR #88 already tracks this issue`
- `branch runoq/... has unresolved conflicts with origin/main`

Operators should treat these as queue hygiene signals, not as transient noise.

## Before Running The Queue

Check the queue surface:

- open issues labeled `runoq:ready`
- issue bodies include the metadata block and `## Acceptance Criteria`
- dependencies really reflect sequencing constraints
- old `runoq:in-progress` issues are understood before starting

There is no preview-only execution mode anymore. Reconciliation happens as part of implementation dispatch through `tick` or `loop`, so inspect issue labels, issue comments, PR comments, and `runoq report issue <n>` when you need to understand current queue state before continuing.

## Single-Issue Mode

Use single-issue mode when you want to target one queue item regardless of what else is ready.

```bash
runoq tick --issue 42
runoq loop --issue 42
```

Typical reasons:

- you are validating one specific issue end to end
- you want to retry a previously escalated or reconciled issue
- you do not want the runner to keep draining the queue afterward

`tick --issue N` advances that task by exactly one implementation transition. `loop --issue N` keeps invoking `tick --issue N` until the task reaches a terminal phase or the command is interrupted.

Single-issue implementation still performs the full phase sequence:

- startup reconciliation
- eligibility checks
- INIT: label transition to `runoq:in-progress`, worktree creation, draft PR creation
- DEVELOP: one bounded Codex dev round runs
- VERIFY: deterministic verification reruns from the pushed branch
- REVIEW: diff-reviewer evaluates the diff
- DECIDE: route to another DEVELOP round or FINALIZE
- FINALIZE: PR finalization, label transition, worktree cleanup

In `loop --issue N` mode it stops after that one issue reaches a terminal outcome.

## Queue Mode

Use queue mode when you want `runoq` to keep selecting the next actionable ready issue until there is nothing left to do or the circuit breaker halts execution.

```bash
runoq loop
```

Queue mode:

- reconciles interrupted runs first
- processes task issues in dependency-safe priority order
- resets the consecutive-failure counter after each clean completion
- stops when no actionable `runoq:ready` task issue remains
- runs milestone review after task queues drain and can create adjustment-review issues for follow-up planning

## Finalization Outcomes

An issue run ends in one of two broad outcomes:

### Clean completion

What happens:

- PR is marked ready and auto-merge is enabled
- issue label moves to `runoq:done`
- local state becomes terminal with `phase: "DONE"`
- sibling worktree is removed

This happens when the review verdict is `PASS`, caveats are empty, and `autoMerge.enabled` is `true`.

### Human-review escalation

What happens:

- PR is marked ready for review instead of auto-merge
- reviewer/assignee may be added from config
- issue label moves to `runoq:needs-human-review`
- local state becomes terminal with `phase: "FAILED"`
- issue and PR get escalation comments explaining why

Common triggers:

- non-`PASS` review verdict (`FAIL` or `ITERATE` at max rounds)
- caveats present in the dev-round result
- complexity exceeding the auto-merge threshold
- auto-merge disabled in config
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

- `runoq:bot:init` — orchestrator initialization
- `runoq:bot:review` — diff review verdict, score, and checklist per round
- `runoq:bot:finalize` — finalization decision table with complexity, verdict, and auto-merge status
- payload reconstruction comments when malformed output was patched or synthesized

### PR body

Look for:

- updated summary section
- updated areas-for-human-attention section
- whether the PR was left in review state or moved to auto-merge

### Local reports

Use:

```bash
runoq report summary
runoq report issue 42
runoq report cost
```

Use them to confirm:

- whether the issue reached `DONE` or `FAILED`
- the saved branch, PR number, and timestamps
- token totals and estimated cost over recent runs

## Operator Routine

For normal operation, this cadence works well:

1. Review open `runoq:ready`, `runoq:in-progress`, and `runoq:needs-human-review` issues on GitHub.
2. Use `runoq tick` when you want one deterministic transition and immediate operator feedback.
3. If the queue looks healthy and you want continuous progress, run `runoq loop`.
4. If one issue needs focused attention, run `runoq loop --issue N` or `runoq tick --issue N`.
5. After the run, inspect issue comments, PR comments, and `runoq report summary`.
6. If the queue halted or escalated repeatedly, fix the root cause before the next run.

## Related Docs

- [Operator workflow](./operator-workflow.md)
- [Recovery and troubleshooting guide](./recovery.md)
- [Execution and maintenance flows](../architecture/flows.md)
- [State and audit model](../reference/state-model.md)
