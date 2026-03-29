# Execution And Maintenance Flows

This document describes the major runtime sequences in `runoq`: planning, execution, reconciliation, mention handling, and maintenance review.

For `runoq run`, the orchestrator and issue-runner are now shell scripts (`orchestrator.sh` and `issue-runner.sh`), not agents. The orchestrator drives phase transitions (INIT, CRITERIA, DEVELOP, REVIEW, DECIDE, FINALIZE, INTEGRATE), spawns agents for bounded reasoning tasks, and handles mention triage. The issue-runner drives codex rounds within the DEVELOP phase.

## `runoq plan`

`runoq plan <file>` is the plan-decomposition entrypoint. The CLI resolves context, then `scripts/plan.sh` invokes the `plan-decomposer` agent to produce an epic/task hierarchy. Each item receives an `estimated_complexity` and `complexity_rationale`. Issue creation is handled deterministically by `plan.sh` itself (not by the agent), using `gh-issue-queue.sh create`. Epics are created first, then tasks are created and linked as sub-issues via the GitHub sub-issues API.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as bin/runoq
  participant Plan as plan.sh
  participant Claude as plan-decomposer agent
  participant Queue as gh-issue-queue.sh
  participant GH as GitHub

  Operator->>CLI: runoq plan docs/plan.md
  CLI->>CLI: resolve TARGET_ROOT, REPO, absolute plan path
  CLI->>Plan: invoke plan.sh with repo and plan file
  Plan->>Claude: --agent plan-decomposer -- payload
  Claude->>Claude: read plan, decompose into epic/task hierarchy
  Claude-->>Plan: JSON with items[], each having complexity and rationale
  Plan->>Plan: validate JSON, extract items
  Plan-->>Operator: proposal with hierarchy, complexity, rationale, warnings
  alt operator confirms (or --auto-confirm)
    Plan->>Queue: create epic issues first
    Queue->>GH: create ready epic issues with runoq:meta blocks
    Plan->>Queue: create task issues with --parent-epic and --depends-on
    Queue->>GH: create ready task issues with runoq:meta blocks
    Queue->>GH: link tasks as sub-issues of parent epics via sub-issues API
    Plan-->>Operator: created queue summary with issue map
  else operator declines
    Plan-->>Operator: stop without GitHub mutation
  end
```

### Planning decision points

| Decision point | Current behavior |
| --- | --- |
| Plan granularity too broad, too narrow, or untestable | The agent must call that out in warnings before creation |
| User confirmation | No issues are created before explicit confirmation (unless `--auto-confirm`) |
| Issue creation path | `plan.sh` uses `gh-issue-queue.sh create` deterministically, not the agent |
| Epic/task linking | Tasks with a `parent_epic_key` are linked via the GitHub sub-issues API |
| Complexity rationale | Each task receives a `complexity_rationale` explaining the complexity estimate |

## `runoq run` Happy Path

The queue execution flow has two entry modes:

- `runoq run --issue N`: target a single issue directly
- `runoq run`: ask the queue for the next actionable ready issue

The sequence below shows the happy path for one issue after reconciliation succeeds.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as bin/runoq
  participant Auth as gh-auth.sh
  participant Run as run.sh
  participant Orch as orchestrator.sh
  participant Safety as dispatch-safety.sh
  participant Queue as gh-issue-queue.sh
  participant WT as worktree.sh
  participant PR as gh-pr-lifecycle.sh
  participant State as state.sh
  participant BarSet as bar-setter agent
  participant IssRun as issue-runner.sh
  participant Verify as verify.sh
  participant GH as GitHub
  participant FS as target repo and sibling worktree

  Operator->>CLI: runoq run [--issue N]
  CLI->>Auth: export-token
  Auth-->>CLI: GH_TOKEN
  CLI->>Run: invoke run flow
  Run->>Orch: dispatch issue
  Orch->>Safety: reconcile REPO
  Safety-->>Orch: reconciliation actions
  alt single-issue mode
    Orch->>Safety: eligibility REPO issue
  else queue mode
    Orch->>Queue: next REPO ready-label
    Queue-->>Orch: selected issue plus skipped reasons
    Orch->>Safety: eligibility REPO selected issue
  end
  Safety-->>Orch: allowed=true
  Orch->>Queue: set-status in-progress
  Queue->>GH: replace runoq:* issue label
  Orch->>WT: create issue worktree and branch
  WT->>FS: git worktree add from origin/main
  Orch->>PR: create draft PR
  PR->>GH: create draft PR from issue branch
  Orch->>State: save INIT breadcrumb
  alt estimated_complexity is medium or higher
    Orch->>State: save CRITERIA breadcrumb
    Orch->>BarSet: spawn with spec, worktree, branch
    BarSet->>FS: read spec, write acceptance tests, commit
    BarSet-->>Orch: criteria_commit, criteria_files, summary
    Orch->>PR: comment criteria summary
    Orch->>State: record criteria_commit in state
  end
  Orch->>State: save DEVELOP breadcrumb
  Orch->>IssRun: invoke with payload including criteria_commit
  IssRun->>FS: run codex dev loop in sibling worktree
  IssRun->>State: validate or reconstruct payload
  IssRun->>PR: comment codex payload
  IssRun->>Verify: round worktree branch base-sha payload
  Verify->>FS: inspect commits, diffs, pushed branch, test/build, criteria tamper check
  Verify-->>IssRun: ok=true
  IssRun-->>Orch: review_ready payload
  Orch->>State: save REVIEW, DECIDE, FINALIZE breadcrumbs
  Orch->>PR: comment orchestrator result and update summary
  alt PASS and complexity at or below maxComplexity and no caveats
    Orch->>PR: finalize auto-merge
    PR->>GH: ready PR and enable auto-merge
    Orch->>Queue: set-status done
    Queue->>GH: replace issue label with runoq:done
    Orch->>State: save DONE with outcome
    Orch->>WT: remove worktree
    WT->>FS: git worktree remove
  else anything else
    Orch->>PR: finalize needs-review
    Orch->>Queue: set-status needs-review
    Orch->>State: save FAILED with outcome
  end
```

### Finalization decision table

| Condition | Outcome |
| --- | --- |
| Verification passes, verdict is `PASS`, complexity is at or below `maxComplexity` (currently `medium`), and caveats are empty | Auto-merge PR, mark issue `done`, save terminal state, remove worktree |
| Verification fails | Post verification failure event, mark issue `needs-human-review`, preserve state |
| Criteria tamper check fails | Feed `criteria tampered: <files>` back as verification failure, iterate or escalate |
| Verdict is not `PASS` | Mark `needs-human-review` |
| Verdict is `PASS` but caveats are present | Mark `needs-human-review` |
| Verdict is `PASS` but issue complexity exceeds `maxComplexity` (currently `medium`) | Mark `needs-human-review` |

## Failure And Escalation Path

The runtime is designed to stop safely and leave breadcrumbs when the happy path breaks.

```mermaid
sequenceDiagram
  participant Run as run.sh
  participant Orch as orchestrator.sh
  participant IssRun as issue-runner.sh
  participant State as state.sh
  participant PR as gh-pr-lifecycle.sh
  participant Queue as gh-issue-queue.sh
  participant Verify as verify.sh
  participant GH as GitHub

  alt dev command stalls or exits non-zero
    Run->>State: keep latest non-terminal breadcrumb
    Run->>PR: post runoq:event failure comment
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
  participant State as .runoq/state/*.json
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
  Safety->>GH: list runoq:in-progress issues
  alt issue has stale in-progress label with no active state
    Safety->>GH: reset issue to runoq:ready
    Safety->>GH: comment stale-label reset
    Safety-->>Run: action=reset-ready
  end
```

### Eligibility checks before dispatch

After reconciliation, `dispatch-safety.sh eligibility` can still reject an issue. It posts a skip comment and returns non-zero when any of these checks fail:

- acceptance criteria missing from the issue body
- any dependency is not labeled `runoq:done`
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
      Mention->>GH: post runoq:event denial comment
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

## Epic Completion And Integration

When all child tasks of an epic reach `runoq:done`, the orchestrator triggers the INTEGRATE phase.

```mermaid
sequenceDiagram
  participant Orch as orchestrator.sh
  participant Queue as gh-issue-queue.sh
  participant WT as worktree.sh
  participant Verify as verify.sh
  participant State as state.sh
  participant GH as GitHub
  participant FS as target repo and sibling worktree

  Orch->>Queue: epic-status REPO epic-number
  Queue-->>Orch: all children runoq:done
  Orch->>WT: create integration worktree from main
  WT->>FS: git worktree add from origin/main (with all child PRs merged)
  Orch->>State: save INTEGRATE breadcrumb
  Orch->>Verify: integrate worktree criteria_commit
  Verify->>FS: confirm epic criteria files unchanged, run test suite
  Verify-->>Orch: ok=true/false, failures
  alt integration passes
    Orch->>Queue: set-status done for epic
    Queue->>GH: replace epic label with runoq:done
    Orch->>State: save DONE with outcome
    Orch->>WT: remove integration worktree
  else integration fails
    Orch->>Queue: create fix task under epic
    Queue->>GH: create runoq:ready fix issue with parent_epic
    Orch->>State: save FAILED with outcome
  end
```

### Integration decision table

| Condition | Outcome |
| --- | --- |
| All child tasks `runoq:done` and `verify.sh integrate` passes | Mark epic `done`, remove integration worktree |
| `verify.sh integrate` fails (criteria tampered or tests fail) | Create a fix task under the epic, back to queue |
| Not all children are `runoq:done` | Epic stays in current state, no integration attempted |

## Mention Triage And Response

The orchestrator handles mention triage using a haiku structured-output call for classification, then dispatches to the appropriate handler.

```mermaid
sequenceDiagram
  participant Orch as orchestrator.sh
  participant Poll as gh-pr-lifecycle.sh poll-mentions
  participant Haiku as haiku classification call
  participant Responder as mention-responder agent
  participant State as state.sh
  participant GH as GitHub

  Orch->>Poll: poll-mentions repo handle
  Poll-->>Orch: unprocessed mentions
  loop each mention
    Orch->>Haiku: classify mention text
    Haiku-->>Orch: question | change-request | approval | irrelevant
    alt question
      Orch->>Responder: spawn with PR context
      Responder->>GH: post reply with runoq:event marker
      Orch->>State: record-mention
    else change-request
      Orch->>Orch: extract checklist, feed into DEVELOP loop
      Orch->>State: record-mention
    else approval
      Orch->>Orch: handle label change or merge
      Orch->>State: record-mention
    else irrelevant
      Orch->>State: record-mention, no action
    end
  end
```

## Maintenance Review And Triage

Maintenance review is a staged workflow implemented by `maintenance.sh`. It is read-only until a human triages findings through comments.

```mermaid
sequenceDiagram
  actor Operator
  participant CLI as runoq maintenance
  participant Agent as maintenance-reviewer
  participant Maint as maintenance.sh
  participant GH as GitHub
  participant State as maintenance.json
  participant Queue as gh-issue-queue.sh

  Operator->>CLI: runoq maintenance
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
    loop each new @runoq triage comment
      Maint->>GH: check collaborator permission
      alt approve or "file this"
        Maint->>Queue: create ready issue from finding
        Queue->>GH: create runoq:ready issue
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
