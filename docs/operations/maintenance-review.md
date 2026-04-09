# Maintenance Review Operations

This guide explains how to run and triage `runoq` maintenance review safely.

The core rule is simple: maintenance review is read-only until a human explicitly approves filing work from a finding.

## What Maintenance Review Does

Maintenance review is a separate workflow from the normal implementation queue.

It:

- derives partitions from the target repo structure
- creates a tracking issue labeled `runoq:maintenance-review`
- posts one finding comment per finding
- waits for human triage comments
- creates queue issues only for findings a human approves

It does not modify source files or open PRs.

## Starting A Review

Launch it from a clean target repository checkout:

```bash
runoq maintenance
```

Behind the scenes, the maintenance reviewer agent uses `maintenance.sh` to start or resume the workflow.

## How Partitions Are Derived

`maintenance.sh derive-partitions` uses two repo inputs:

- `.gitignore`
- `tsconfig.json`

### Exclusions

The runtime combines:

- non-comment, non-empty `.gitignore` entries
- `tsconfig.json.exclude`

These are recorded as exclusions for the maintenance run.

### Partition mode

If `tsconfig.json.references` exists:

- maintenance uses `references` mode
- each referenced path becomes one partition

If `references` is absent:

- maintenance uses `single-project` mode
- it derives partitions from the top-level directories named in `include`

This means partition quality improves when the target repo’s `tsconfig.json` reflects real project structure.

## Tracking Issue Lifecycle

The first maintenance step creates a tracking issue titled like:

```text
Maintenance review 2026-03-17
```

The body includes:

- `<!-- runoq:bot -->`
- timestamp
- current branch
- current commit SHA
- partition names

After creation, the runtime posts one progress comment per partition, for example:

```text
Partition src reviewed. PERFECT-D score: pending. Findings: 0.
```

This gives operators a lightweight progress log before any findings are filed.

## Local Maintenance State

Maintenance review writes `.runoq/state/maintenance.json` and advances it through:

- `STARTED`
- `FINDINGS_POSTED`
- `COMPLETED`

It also reuses `.runoq/state/processed-mentions.json` so triage comments are only handled once.

## Findings Format

When findings are posted, each one becomes a tracking-issue comment with fields like:

- `Finding ID: F1`
- `Title: ...`
- `Dimension: ...`
- `Severity: ...`
- `Files: ...` when present
- `Grouped finding.` when `grouped: true`
- free-form description
- `Suggested fix: ...`
- `Note: this code is being modified in PR #...` when `inflight_pr` is present

The runtime also records:

- `recurring_patterns`
- finding `status`, initialized to `pending`
- finding `priority`, defaulting to `2` when not supplied

## Triage Commands

Triage happens through comments on the tracking issue.

### Approve and file work

Examples:

```text
@runoq approve F1
@runoq file this F1
```

Effect:

- creates a normal queue issue through `gh-issue-queue.sh create`
- comments on the tracking issue with the filed issue number
- marks the finding `approved`
- records `filed_issue` in maintenance state

### Approve with lower priority

Example:

```text
@runoq file this F3 but lower priority
```

Effect:

- files the issue
- overrides priority to `3`
- marks the finding `approved`

### Decline a finding

Examples:

```text
@runoq skip F2
@runoq won't fix F2
```

Effect:

- comments that the finding was declined
- marks the finding `declined`

## Authorization And Mention Handling

Triage comments are permission-gated.

Current behavior:

- required permission comes from `authorization.minimumPermission`
- the runtime checks collaborator permission before acting
- unauthorized comments are still recorded in `processed-mentions.json`
- if `authorization.denyResponse` is `comment`, the tracking issue receives a denial comment

This means:

- each triage comment is processed once
- repeating the same comment ID has no effect
- permission failures do not cause duplicate denial spam across polling cycles

## When Queue Issues Get Created

A queue issue is created only when all of the following are true:

- the comment contains approval language such as `approve` or `file this`
- the comment includes a recognizable finding ID
- the commenter meets the required permission level

Created issues:

- are labeled `runoq:ready`
- use the normal queue-issue metadata block
- default to `estimated_complexity: medium`
- include the finding description plus `Suggested fix: ...`

From that point on, the issue becomes part of the normal implementation queue.

## Recurring Patterns And Final Summary

After all findings leave `pending`, the runtime may post:

- a recurring-patterns summary comment
- a final completion summary comment

The final summary includes:

- partitions reviewed
- findings proposed
- approved count
- declined count
- issues created
- recurring patterns, when present

The maintenance state then moves to `COMPLETED`.

## Resume Behavior

Maintenance review is resumable by phase.

### `STARTED`

If the run stopped after creating the tracking issue but before posting findings:

- rerunning maintenance posts findings
- then continues to triage/completion

### `FINDINGS_POSTED`

If the run stopped after findings were posted:

- rerunning maintenance reuses the tracking issue
- processes new triage comments
- completes the summary

### `COMPLETED`

If the maintenance state is already complete:

- rerunning maintenance returns the saved summary
- it does not repost the final completion comment

## Operator Workflow

A safe operator loop looks like this:

1. Run `runoq maintenance`.
2. Open the tracking issue and read the partition comments and finding comments.
3. Approve only the findings you want filed as normal queue work.
4. Decline the findings you do not want filed.
5. Wait for the completion summary and confirm that all findings left `pending`.
6. Switch back to normal queue operations for any new `runoq:ready` issues created from approved findings.

## What To Inspect If Something Looks Wrong

- `.runoq/state/maintenance.json` for the current phase and finding statuses
- `.runoq/state/processed-mentions.json` for whether a triage comment was already handled
- tracking issue comments for approval, denial, recurring-pattern, and completion messages
- newly created queue issues when a finding was approved

## Related Docs

- [Operator workflow](./operator-workflow.md)
- [Recovery and troubleshooting guide](./recovery.md)
- [Execution and maintenance flows](../architecture/flows.md)
- [State and audit model](../reference/state-model.md)
