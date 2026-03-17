# Execution And Maintenance Flows

This document describes the major runtime sequences in `agendev`: planning, execution, reconciliation, mention handling, and maintenance review.

For `agendev run`, one detail matters: outside fixture mode, [`scripts/run.sh`](../../scripts/run.sh) delegates to the `github-orchestrator` agent after argument parsing. The detailed execution sequence below is therefore an implementation-backed inference from two sources taken together:

- the full scripted flow in fixture mode inside `scripts/run.sh`
- the hard rules and dispatch steps in `.claude/agents/github-orchestrator.md`

## `agendev plan`

`agendev plan <file>` is the plan-slicing entrypoint. The shell CLI resolves context and auth, then hands the local file to the `plan-to-issues` skill.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as bin/agendev
  participant Auth as gh-auth.sh
  participant Claude as plan-to-issues skill
  participant Queue as gh-issue-queue.sh
  participant GH as GitHub

  Operator->>CLI: agendev plan docs/plan.md
  CLI->>CLI: resolve TARGET_ROOT, REPO, absolute plan path
  CLI->>Auth: export-token
  Auth-->>CLI: GH_TOKEN
  CLI->>Claude: --skill plan-to-issues -- <absolute plan path>
  Claude->>Claude: read plan and prepare proposed issue queue
  Claude-->>Operator: proposal, dependencies, granularity warnings
  alt operator confirms
    Claude->>Queue: create issue for each approved item
    Queue->>GH: create ready issues with agendev:meta blocks
    GH-->>Queue: issue URLs
    Queue-->>Claude: created issue references
    Claude-->>Operator: created queue summary and dependency graph
  else operator declines
    Claude-->>Operator: stop without GitHub mutation
  end
```

### Planning decision points

| Decision point | Current behavior |
| --- | --- |
| Plan granularity too broad, too narrow, or untestable | The skill must call that out before creation |
| User confirmation | No issues should be created before explicit confirmation |
| Issue creation path | The skill should use `gh-issue-queue.sh create`, not ad hoc `gh issue create` |

## `agendev run` Happy Path

The queue execution flow has two entry modes:

- `agendev run --issue N`: target a single issue directly
- `agendev run`: ask the queue for the next actionable ready issue

The sequence below shows the happy path for one issue after reconciliation succeeds.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as bin/agendev
  participant Auth as gh-auth.sh
  participant Run as run.sh / github-orchestrator
  participant Safety as dispatch-safety.sh
  participant Queue as gh-issue-queue.sh
  participant WT as worktree.sh
  participant PR as gh-pr-lifecycle.sh
  participant State as state.sh
  participant Verify as verify.sh
  participant GH as GitHub
  participant FS as target repo and sibling worktree

  Operator->>CLI: agendev run [--issue N]
  CLI->>Auth: export-token
  Auth-->>CLI: GH_TOKEN
  CLI->>Run: invoke run flow
  Run->>Safety: reconcile REPO
  Safety-->>Run: reconciliation actions
  alt single-issue mode
    Run->>Safety: eligibility REPO issue
  else queue mode
    Run->>Queue: next REPO ready-label
    Queue-->>Run: selected issue plus skipped reasons
    Run->>Safety: eligibility REPO selected issue
  end
  Safety-->>Run: allowed=true
  Run->>Queue: set-status in-progress
  Queue->>GH: replace agendev:* issue label
  Run->>WT: create issue worktree and branch
  WT->>FS: git worktree add from origin/main
  Run->>PR: create draft PR
  PR->>GH: create draft PR from issue branch
  Run->>State: save INIT breadcrumb
  Run->>PR: comment dispatch payload
  Run->>State: save DEVELOP breadcrumb
  Run->>FS: run dev loop in sibling worktree
  Run->>State: validate or reconstruct payload
  Run->>PR: comment codex payload
  Run->>Verify: round worktree branch base-sha payload
  Verify->>FS: inspect commits, diffs, pushed branch, test/build commands
  Verify-->>Run: ok=true
  Run->>State: save REVIEW, DECIDE, FINALIZE breadcrumbs
  Run->>PR: comment orchestrator result and update summary
  alt PASS and low complexity and no caveats
    Run->>PR: finalize auto-merge
    PR->>GH: ready PR and enable auto-merge
    Run->>Queue: set-status done
    Queue->>GH: replace issue label with agendev:done
    Run->>State: save DONE with outcome
    Run->>WT: remove worktree
    WT->>FS: git worktree remove
  else anything else
    Run->>PR: finalize needs-review
    Run->>Queue: set-status needs-review
    Run->>State: save FAILED with outcome
  end
```

### Finalization decision table

| Condition | Outcome |
| --- | --- |
| Verification passes, verdict is `PASS`, complexity is `low`, and caveats are empty | Auto-merge PR, mark issue `done`, save terminal state, remove worktree |
| Verification fails | Post verification failure event, mark issue `needs-human-review`, preserve state |
| Verdict is not `PASS` | Mark `needs-human-review` |
| Verdict is `PASS` but caveats are present | Mark `needs-human-review` |
| Verdict is `PASS` but issue complexity is not `low` | Mark `needs-human-review` |

## Failure And Escalation Path

The runtime is designed to stop safely and leave breadcrumbs when the happy path breaks.

```mermaid
sequenceDiagram
  participant Run as run.sh / github-orchestrator
  participant State as state.sh
  participant PR as gh-pr-lifecycle.sh
  participant Queue as gh-issue-queue.sh
  participant Verify as verify.sh
  participant GH as GitHub

  alt dev command stalls or exits non-zero
    Run->>State: keep latest non-terminal breadcrumb
    Run->>PR: post agendev:event failure comment
    Run->>GH: post matching issue event
    Run-->>Run: exit with underlying dev status
  else payload missing or malformed
    Run->>State: validate-payload and synthesize or patch payload
    Run->>PR: post payload reconstruction event
    Run->>PR: post normalized payload comment
  else verification fails
    Run->>Verify: round
    Verify-->>Run: ok=false with failures[]
    Run->>PR: post verification failure event
    Run->>PR: finalize needs-review
    Run->>Queue: set-status needs-review
    Run->>GH: comment issue escalation
    Run->>State: save FAILED with outcome
  else orchestrator verdict or caveats block merge
    Run->>PR: finalize needs-review
    Run->>Queue: set-status needs-review
    Run->>State: save FAILED with outcome
  end
```

### Queue-level stop condition

In queue mode, each non-completed issue increments the consecutive failure counter. When the counter reaches `consecutiveFailureLimit`:

- `run.sh` posts a circuit-breaker event naming the failed issues
- queue processing stops
- the command returns a JSON result with `status: "halted"` and `failed_issues`

## Startup Reconciliation And Resume

Every `run` starts with reconciliation. This is where the runtime decides whether it can resume an interrupted run or must escalate.

```mermaid
sequenceDiagram
  participant Run as run.sh
  participant Safety as dispatch-safety.sh
  participant State as .agendev/state/*.json
  participant GH as GitHub

  Run->>Safety: reconcile REPO
  Safety->>State: scan *.json state files
  loop for each non-terminal issue state
    Safety->>GH: resolve open PR by saved pr_number or branch
    Safety->>GH: check whether branch exists on origin
    alt open PR exists and branch is pushed
      Safety->>GH: comment issue "Resuming"
      Safety->>GH: comment PR "Resuming"
      Safety-->>Run: action=resume
    else recoverability check fails
      Safety->>GH: set issue status needs-review
      Safety->>GH: comment issue "Marking for human review"
      Safety-->>Run: action=needs-review
    end
  end
  Safety->>GH: list agendev:in-progress issues
  alt issue has stale in-progress label with no active state
    Safety->>GH: reset issue to agendev:ready
    Safety->>GH: comment stale-label reset
    Safety-->>Run: action=reset-ready
  end
```

### Eligibility checks before dispatch

After reconciliation, `dispatch-safety.sh eligibility` can still reject an issue. It posts a skip comment and returns non-zero when any of these checks fail:

- acceptance criteria missing from the issue body
- any dependency is not labeled `agendev:done`
- an open PR already exists for the derived branch name
- the existing remote branch conflicts with `origin/main`

## Mention Polling And Authorization

Mention handling is used for maintenance triage and other bot-addressed comments. The control flow is intentionally permission-gated and deduplicated.

```mermaid
sequenceDiagram
  participant Poll as gh-pr-lifecycle.sh poll-mentions
  participant Mention as mentions.sh
  participant Perm as gh-pr-lifecycle.sh check-permission
  participant State as processed-mentions.json
  participant GH as GitHub

  Mention->>Poll: poll-mentions repo handle [--since]
  Poll->>GH: list open issues and PR-backed issues
  Poll->>GH: fetch comments per item
  Poll->>State: skip already-recorded comment IDs
  Poll-->>Mention: unprocessed @handle mentions
  loop each mention
    Mention->>Perm: check-permission repo author required-level
    alt permission allowed
      Mention->>State: record-mention comment_id
      Mention-->>Caller: action=process
    else permission denied and denyResponse=comment
      Mention->>GH: post agendev:event denial comment
      Mention->>State: record-mention comment_id
      Mention-->>Caller: action=deny
    else permission denied and denyResponse!=comment
      Mention->>State: record-mention comment_id
      Mention-->>Caller: action=ignore
    end
  end
```

### Authorization decision points

| Decision point | Current behavior |
| --- | --- |
| Comment already recorded in `processed-mentions.json` | Skip it entirely |
| Mention does not contain `@<handle>` or is an audit payload/event comment | Skip it |
| Collaborator permission below `authorization.minimumPermission` | Deny with comment or ignore silently based on `authorization.denyResponse` |
| Permission sufficient | Return `action: "process"` and let the caller apply domain-specific logic |

## Maintenance Review And Triage

Maintenance review is a staged workflow implemented by `maintenance.sh`. It is read-only until a human triages findings through comments.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as agendev maintenance
  participant Agent as maintenance-reviewer
  participant Maint as maintenance.sh
  participant GH as GitHub
  participant State as maintenance.json
  participant Queue as gh-issue-queue.sh

  Operator->>CLI: agendev maintenance
  CLI->>Agent: launch maintenance-reviewer
  Agent->>Maint: run repo findings-file
  alt no maintenance state yet
    Maint->>Maint: derive partitions from .gitignore and tsconfig.json
    Maint->>GH: create maintenance tracking issue
    Maint->>GH: comment one pending line per partition
    Maint->>State: save phase STARTED
  else existing maintenance state
    Maint->>State: load prior phase
  end
  alt phase STARTED
    Maint->>GH: post finding comments to tracking issue
    Maint->>State: save phase FINDINGS_POSTED with pending findings
  end
  alt phase FINDINGS_POSTED
    Maint->>GH: read tracking issue comments
    loop each new @agendev triage comment
      Maint->>GH: check collaborator permission
      alt approve or "file this"
        Maint->>Queue: create ready issue from finding
        Queue->>GH: create agendev:ready issue
        Maint->>GH: comment approval result
        Maint->>State: mark finding approved and record filed issue
      else skip or won't fix
        Maint->>GH: comment decline result
        Maint->>State: mark finding declined
      else unauthorized
        Maint->>GH: optional denial comment
        Maint->>State: record mention only
      end
    end
    opt all findings non-pending and recurring patterns exist
      Maint->>GH: post recurring patterns summary
    end
  end
  Maint->>GH: post final completion summary
  Maint->>State: save phase COMPLETED with summary
```

### Triage command interpretation

| Comment pattern | Effect |
| --- | --- |
| contains `approve` | file a queue issue and mark the finding approved |
| contains `file this` | same as approval |
| contains `lower priority` together with approval language | file the issue with priority override `3` |
| contains `skip` | mark finding declined |
| contains `won't fix` | mark finding declined |

### Maintenance resume behavior

`maintenance.sh run` resumes by phase:

- `STARTED`: post findings, then continue
- `FINDINGS_POSTED`: run triage, then complete
- `COMPLETED`: return saved summary without reposting the final completion comment
