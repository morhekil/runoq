# Recovery And Troubleshooting

This guide explains how to recover from interrupted runs, eligibility failures, verification failures, malformed payloads, stalled dev loops, and auth or state problems.

## Fast Triage

When a run behaves unexpectedly, check these in order:

1. `runoq report issue <n>`
2. the issue comments
3. the PR comments and PR body summary
4. `.runoq/state/<issue>.json`

Useful commands:

```bash
runoq tick --issue 42
runoq loop --issue 42
runoq report issue 42
runoq report summary
scripts/worktree.sh inspect 42
scripts/gh-auth.sh print-identity
```

## What Startup Reconciliation Does

Every implementation dispatch through `runoq tick --issue`, `runoq loop --issue`, or a queue-selecting implementation tick begins with `dispatch-safety.sh reconcile`.

Possible reconciliation outcomes:

| Outcome | Meaning | What the runtime does |
| --- | --- | --- |
| `resume` | non-terminal state still points to an open PR and pushed branch | comments on the issue and PR that the run is resuming |
| `needs-review` | interrupted state cannot be resumed safely | relabels the issue to `runoq:needs-human-review` and comments on the issue |
| `reset-ready` | GitHub still shows `runoq:in-progress`, but no active local state explains it | resets the label to `runoq:ready` and comments on the issue |

There is no preview-only reconciliation command. To observe reconciliation, inspect the issue and PR comments it writes during the next implementation tick.

## Interrupted Runs

### Recoverable interruption

Symptoms:

- `.runoq/state/<issue>.json` has a non-terminal phase like `INIT`, `DEVELOP`, `VERIFY`, `REVIEW`, or `DECIDE`
- the saved branch is still pushed to `origin`
- the PR still exists and is open
- issue or PR comments show `Detected interrupted run ... Resuming.`

What to do:

- confirm the branch and PR still reflect the intended work
- rerun `runoq tick --issue <n>` for one controlled transition, or `runoq loop --issue <n>` to keep advancing automatically
- otherwise let queue mode resume naturally on the next run

### Unrecoverable interruption

Symptoms:

- reconciliation comments say `Marking for human review`
- the issue is relabeled `runoq:needs-human-review`
- `.runoq/state/<issue>.json` is still non-terminal, but the branch or PR can no longer be resolved safely

Common causes:

- open PR was closed or deleted
- branch was not pushed or no longer exists on `origin`
- branch/PR metadata in state is stale beyond safe recovery

What to do:

- inspect the saved `branch` and `pr_number` in the state file
- inspect GitHub for a replacement PR or missing branch
- decide whether to reopen/recreate the PR manually or file follow-up work
- do not force the queue to continue on that issue until the human review is resolved

## Stale `runoq:in-progress` Labels

Symptoms:

- the next implementation tick resets the issue to `runoq:ready`
- issue comment says `Found stale runoq:in-progress label with no active run. Reset to runoq:ready.`

What it means:

- GitHub still thought the issue was active, but no non-terminal state file justified that label

What to do:

- confirm no one is actually working the issue in another checkout
- if the reset looks correct, allow queue processing to continue
- if the label was actually valid, inspect why the local state file was lost

## Eligibility Failures Before Dispatch

Eligibility failures happen before `runoq` mutates queue state for an issue. The runtime comments on the issue with `Skipped: ...`.

### Missing acceptance criteria

Signal:

- issue comment: `Skipped: missing acceptance criteria.`

Fix:

- add a `## Acceptance Criteria` section to the issue body
- rerun the queue or single-issue mode

### Existing open PR conflict

Signal:

- issue comment: `Skipped: existing open PR #... already tracks this issue.`

Fix:

- inspect the existing PR and decide whether it already owns the work
- close or merge the PR before retrying the issue
- avoid creating a second branch for the same issue

### Dependency not done

Signal:

- issue comment: `Skipped: dependency #... is not runoq:done.`

Fix:

- complete or relabel the dependency issue appropriately
- verify the dependency metadata block is correct

### Branch conflicts with `origin/main`

Signal:

- issue comment: `Skipped: branch runoq/... has unresolved conflicts with origin/main.`

Fix:

- inspect the existing branch and resolve or discard it
- if the branch belongs to stale work, clean up the PR/branch before retrying

## Milestone Review Follow-Up

After child tasks drain, tick runs milestone review and may open an adjustment-review issue instead of directly completing the epic.

Symptoms:

- terminal output reports `Milestone #<n> review complete. Adjustments proposed on #<m>`
- a new adjustment issue exists under the milestone

What to do:

- inspect the adjustment-review issue and decide whether to accept, reject, or revise the proposed follow-up work
- Check whether the criteria commit is still reachable
- Fix the underlying integration failures (often test/build failures across combined child work)
- Rerun `runoq loop` — the epic sweep will retry integration for epics whose children are all done

## Verification Failures

Verification failures escalate the issue to `runoq:needs-human-review`. The PR gets a verification comment and the issue gets an escalation comment.

### No commits or missing push

Signals:

- PR comment mentioning `no new commits were created`
- PR or issue comment mentioning `branch tip is not pushed to origin`

What to inspect:

- local git history in the worktree
- whether `git push` succeeded from the worktree branch
- the `actual` and `failures` information from verification-related comments

Fix:

- ensure the dev loop creates a commit
- ensure the branch is pushed to `origin`
- rerun after the branch state is correct

### Test or build command failures

Signals:

- PR comment mentioning `test command failed`
- PR comment mentioning `build command failed`

What to inspect:

- configured `verification.testCommand`
- configured `verification.buildCommand`
- the worktree contents and package/tooling setup

Fix:

- fix the underlying test/build problem in the target repo
- or adjust runtime config if the wrong commands are configured

### Payload-reported failures

Signals:

- PR comment mentioning `payload reported failing tests`
- PR comment mentioning `payload reported failing build`

Fix:

- inspect the normalized payload comment
- determine whether the payload was correct or reconstructed
- if reconstruction occurred, also inspect the raw dev-round output path that produced it

## Malformed Or Missing Payloads

Symptoms:

- PR comment says `Codex payload required reconstruction. Source=...`
- PR comments include `payload_missing_or_malformed`
- the normalized payload comment shows `payload_source: "synthetic"` or `payload_source: "patched"`

What it means:

- the runtime could not trust the original fenced JSON payload
- `state.sh validate-payload` rebuilt or normalized it from git ground truth

What to do:

- inspect the PR’s `runoq:payload:codex-return` comment
- check whether the fallback payload caused verification to fail
- fix the agent/prompt/output issue before retrying if malformed payloads recur

This is often recoverable without touching local state files because the PR comments already preserve the reconstructed payload and the discrepancy markers.

## Stalled Dev Commands

Symptoms:

- command exits `124`
- issue comment says `Agent stalled after N seconds of inactivity. Process terminated. State preserved for resume.`
- `.runoq/state/<issue>.json` still shows a non-terminal phase and includes `stall`

What to inspect:

- `.stall.timeout_seconds`
- `.stall.command`
- `.stall.last_phase`
- `.stall.last_round`

Fix:

- inspect why the dev process stopped producing output
- rerun only after addressing the stall source
- use reconciliation to resume when the branch and PR are still intact

## Agent Crash Or Non-Zero Exit

Symptoms:

- run exits with a non-zero status from the dev command
- issue or PR comment says `Agent exited unexpectedly (exit code X). Last phase: ...`
- state remains in the last non-terminal phase

What to do:

- inspect the last saved `phase` and `round`
- check whether any partial branch updates were pushed
- rerun after the underlying dev command or environment problem is corrected

## Corrupted State Files

Symptoms:

- `runoq report issue <n>` or `scripts/state.sh load <n>` fails with `State file is corrupted for issue <n>`

What to do:

1. inspect GitHub comments and labels first, because they are the audit trail
2. if the issue is already terminal in GitHub terms, consider removing or archiving the bad local state file
3. if the issue was mid-run, inspect the PR and branch before deciding whether to recreate the breadcrumb manually or escalate to human review

Be conservative here. A corrupted state file means the runtime may not be able to reconcile safely.

## Auth And Config Failures

### `Run 'runoq init' first.`

Meaning:

- `.runoq/identity.json` is missing and no reusable `GH_TOKEN` was available

Fix:

- run `runoq init` from inside the target repo
- or export a valid `GH_TOKEN` if you intentionally bypass app-token minting

### `GitHub App private key not found`

Meaning:

- `RUNOQ_APP_KEY` or the path recorded in `.runoq/identity.json` does not exist

Fix:

- inspect `scripts/gh-auth.sh print-identity`
- correct `RUNOQ_APP_KEY` or repair the recorded `privateKeyPath`

### Repo resolution failures

Signals:

- `Run runoq from inside a git repository.`
- `No 'origin' remote found. runoq requires a GitHub-hosted repo.`
- `Origin remote is not a GitHub URL: ...`

Fix:

- run from the target repo checkout
- restore or correct the `origin` remote
- verify the repo really uses GitHub-hosted remotes

## When To Resume Vs Clean Up Vs Escalate

Resume when:

- reconciliation reports `resume`
- the branch is pushed
- the PR is still open
- the failure was transient and the current state is trustworthy

Clean up when:

- the problem is clearly stale local state or stale labels
- an old worktree or branch is blocking a fresh run
- you have confirmed no active work would be lost

Escalate to human review when:

- reconciliation already chose `needs-review`
- verification failed for reasons that need judgment, not simple retriggering
- state is corrupted and the correct continuation path is unclear
- repeated malformed payloads or repeated circuit-breaker halts point to a larger workflow problem

## Related Docs

- [Queue operations guide](./queue-operations.md)
- [State and audit model](../reference/state-model.md)
- [Configuration and auth reference](../reference/config-auth.md)
- [Execution and maintenance flows](../architecture/flows.md)
