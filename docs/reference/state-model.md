# State And Audit Model

This document explains how `runoq` records local execution state and how that differs from the GitHub-side operational audit trail.

## Two Persistence Layers

`runoq` intentionally uses two different storage layers for different jobs:

- Local state under `.runoq/state/` is for resumability, reconciliation, and deduplication.
- GitHub issues, PRs, and comments are the operator-facing audit and control surface.

If you are debugging an interrupted run, read both. If you are reconstructing what happened historically, prefer GitHub comments over local state files.

## Local State Directory

Default location:

```text
<target-repo>/.runoq/state/
```

Override:

- `RUNOQ_STATE_DIR`

Current files of interest:

- `.runoq/state/<issue>.json`
- `.runoq/state/maintenance.json`
- `.runoq/state/processed-mentions.json`

All writes are intended to be atomic via temp file plus rename.

## Issue State Files: `.runoq/state/<issue>.json`

### Purpose

One file per issue stores the latest local execution breadcrumb for that issue. The runtime rewrites the file as the issue advances.

### Common fields

Fields injected by `state.sh save`:

- `issue`: numeric issue number
- `started_at`: first write timestamp for the run
- `updated_at`: last write timestamp

Fields commonly written by `run.sh` and `orchestrator.sh`:

- `phase`
- `round`
- `branch`
- `worktree`
- `pr_number`
- `criteria_commit` when bar-setter has authored acceptance criteria (medium+ complexity tasks)
- `complexity_rationale` free-text explanation of the complexity estimate (from issue metadata)
- `type` issue type (`epic` or `task`)
- `parent_epic` parent epic issue number (for tasks within an epic)
- `outcome` on terminal states
- `stall` when `watchdog.sh` times out

### Typical non-terminal shape

```json
{
  "issue": 42,
  "phase": "DEVELOP",
  "round": 1,
  "branch": "runoq/42-implement-queue",
  "worktree": "/path/to/../runoq-wt-42",
  "pr_number": 87,
  "started_at": "2026-03-17T00:00:00Z",
  "updated_at": "2026-03-17T00:05:00Z"
}
```

### Typical terminal shape

```json
{
  "issue": 42,
  "phase": "DONE",
  "round": 1,
  "branch": "runoq/42-implement-queue",
  "worktree": "/path/to/../runoq-wt-42",
  "pr_number": 87,
  "outcome": {
    "verdict": "PASS",
    "rounds_used": 1,
    "final_score": 42,
    "summary": "Completed successfully.",
    "caveats": [],
    "tokens_used": 0
  },
  "started_at": "2026-03-17T00:00:00Z",
  "updated_at": "2026-03-17T00:10:00Z"
}
```

`FAILED` uses the same general shape, with `phase: "FAILED"` and an `outcome` describing the escalation.

## Issue Phase Model

### Phases

- `INIT`
- `CRITERIA`
- `DEVELOP`
- `REVIEW`
- `DECIDE`
- `FINALIZE`
- `INTEGRATE`
- `DONE`
- `FAILED`

### Allowed transitions

`state.sh` enforces these transitions:

- `INIT -> CRITERIA`
- `INIT -> DEVELOP`
- `INIT -> FINALIZE`
- `INIT -> FAILED`
- `CRITERIA -> DEVELOP`
- `CRITERIA -> FAILED`
- `DEVELOP -> REVIEW`
- `DEVELOP -> FAILED`
- `REVIEW -> DECIDE`
- `REVIEW -> FAILED`
- `DECIDE -> DEVELOP`
- `DECIDE -> FINALIZE`
- `DECIDE -> FAILED`
- `FINALIZE -> DONE`
- `FINALIZE -> FAILED`
- `INTEGRATE -> DONE`
- `INTEGRATE -> FAILED`

`DONE` and `FAILED` are terminal. Any later transition is rejected.

### What terminal means

An issue state is terminal when:

- `phase` is `DONE`, or
- `phase` is `FAILED`

Reconciliation skips terminal states. Queue-level reporting treats them as completed historical records.

## Stall Markers

When `watchdog.sh` kills a silent child process, it augments the issue state with a `stall` object.

Example:

```json
{
  "stall": {
    "timed_out": true,
    "timeout_seconds": 600,
    "detected_at": "2026-03-17T00:07:00Z",
    "exit_code": 124,
    "command": "bash -lc ...",
    "last_phase": "DEVELOP",
    "last_round": 1
  }
}
```

This is a recovery breadcrumb, not the full postmortem. The matching operator-facing explanation is posted to GitHub comments.

## Payload Reconstruction And Verification In The State Model

### Payload normalization

`state.sh validate-payload` produces normalized JSON for Codex output with fields such as:

- `status`
- `commits_pushed`
- `commit_range`
- `files_changed`
- `files_added`
- `files_deleted`
- `tests_run`
- `tests_passed`
- `test_summary`
- `build_passed`
- `blockers`
- `notes`
- `payload_source`
- `patched_fields`
- `discrepancies`

`payload_source` values currently include:

- `patched`: a JSON payload existed but was normalized from ground truth
- `synthetic`: no usable payload existed, so a failure payload was synthesized

### Where that data lives

This normalized payload is part of the runtime model, but it is not persisted as a dedicated long-lived local state file. Instead:

- `run.sh` uses it to drive verification
- PR audit comments record the normalized payload and any reconstruction event
- issue state files continue to store the phase breadcrumb and final `outcome`

### Verification output

`verify.sh round` returns:

```json
{
  "ok": true,
  "review_allowed": true,
  "failures": [],
  "actual": {
    "commits_pushed": [],
    "files_changed": [],
    "files_added": [],
    "files_deleted": []
  }
}
```

That verification JSON is likewise transient unless copied into GitHub comments or a higher-level outcome.

## Maintenance State: `.runoq/state/maintenance.json`

### Purpose

This file tracks the resumable state of the maintenance-review workflow.

### Phases

- `STARTED`
- `FINDINGS_POSTED`
- `COMPLETED`

### Common shape by phase

When started:

```json
{
  "phase": "STARTED",
  "tracking_issue": 120,
  "partitions": [
    { "name": "src", "path": "src" }
  ]
}
```

After findings are posted:

```json
{
  "phase": "FINDINGS_POSTED",
  "tracking_issue": 120,
  "partitions": [
    { "name": "src", "path": "src" }
  ],
  "recurring_patterns": ["validation duplication"],
  "findings": [
    {
      "id": "F1",
      "title": "Approval finding",
      "description": "Document the approval path.",
      "suggested_fix": "Add missing documentation.",
      "status": "pending",
      "priority": 1
    }
  ]
}
```

After completion:

```json
{
  "phase": "COMPLETED",
  "tracking_issue": 120,
  "summary": {
    "partitions_reviewed": 1,
    "findings_proposed": 1,
    "approved": 1,
    "declined": 0,
    "issues_created": 1,
    "recurring_patterns": ["validation duplication"]
  }
}
```

### Finding state semantics

Each finding usually carries:

- `id`
- `title`
- `description`
- `suggested_fix`
- `status`
- `priority`
- optional `filed_issue`

Current status values written by the shell flow:

- `pending`
- `approved`
- `declined`

## Processed Mentions: `.runoq/state/processed-mentions.json`

Purpose:

- deduplicate comment handling across polling cycles
- prevent repeated denial or approval processing

Shape:

```json
[3001, 4001, 5002]
```

Semantics:

- each entry is a GitHub comment ID
- authorized and unauthorized mentions are both recorded
- absence means “not yet processed”

## GitHub Audit Markers

GitHub comments are the audit trail. The runtime uses machine-recognizable markers so scripts and humans can distinguish operational comments from free-form discussion.

### Comment markers

| Marker | Meaning | Typical location |
| --- | --- | --- |
| `<!-- runoq:event -->` | human-readable operational event | issue comments, PR comments, maintenance tracking issue comments |
| `<!-- runoq:event:<phase> -->` | phase-specific event posted by the orchestrator (e.g., `runoq:event:init`, `runoq:event:criteria`, `runoq:event:review`, `runoq:event:finalize`) | PR and issue comments |
| `<!-- runoq:event:verification-failure -->` | verification failure posted by `issue-runner` after a failed round | PR comment |
| `<!-- runoq:payload:codex-return -->` | normalized or reconstructed dev-round payload | PR comment |

### PR body markers

These are not comments, but they are still part of the audit/control surface:

- `<!-- runoq:summary:start -->` ... `<!-- runoq:summary:end -->`
- `<!-- runoq:attention:start -->` ... `<!-- runoq:attention:end -->`

`gh-pr-lifecycle.sh update-summary` depends on them to update the PR body in place.

## How Reconciliation Uses Local State

`dispatch-safety.sh reconcile` reads non-terminal issue state files to decide whether a run can resume.

Resume when:

- the saved or derived PR is still open, and
- the saved branch is still pushed to `origin`

Escalate to `needs-human-review` when:

- the PR cannot be resolved, or
- the branch is no longer pushed

Reset stale labels when:

- GitHub shows `runoq:in-progress`, but no active non-terminal state file explains it

This is why local state is a recovery breadcrumb: it enables the decision, but the resulting action is written back to GitHub comments and labels.

## Diagnosing A Stuck Or Interrupted Run

Check local state first:

- `.runoq/state/<issue>.json` for `phase`, `updated_at`, `branch`, `pr_number`, and any `stall`
- `.runoq/state/maintenance.json` for maintenance workflow phase
- `.runoq/state/processed-mentions.json` for duplicate-mention handling

Then check GitHub:

- issue comments for `Skipped: ...`, escalation events, and stale-label resets
- PR comments for dispatch payloads, normalized payloads, verification failures, and final verdicts
- PR body summary and attention sections for the latest operator summary

## Related Docs

- [Script contract reference](./script-contracts.md)
- [Execution and maintenance flows](../architecture/flows.md)
- [Configuration and auth reference](./config-auth.md)
