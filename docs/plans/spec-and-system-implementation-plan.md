# Spec and System Issues Implementation Plan

## Scope

This plan addresses the spec and runtime issues cataloged in [issues.md](./issues.md).

Primary goal:

- bring the smoke spec, runtime behavior, and recovery semantics back into a single coherent contract

Secondary goals:

- eliminate silent or misleading success paths
- make review/comment handling deterministic and replay-safe
- prevent partial planning or adjustment application from being reported as complete
- expand smoke coverage so the documented contract matches the implemented system

Out of scope:

- unrelated feature work
- prompt-only fixes that duplicate deterministic runtime rules
- broad architecture changes that do not reduce one of the identified failure modes

## Guiding **Decisions**

Several issues are blocked on target-semantics decisions. The implementation should start by locking these down in code comments, tests, and the smoke spec.

Recommended decisions:

- Zero-selection approvals are invalid, not successful no-ops. If review selection filters out every proposed item, the review must stay open and the tick must return a deterministic operator-visible reason.
- Fresh human comments on a review take precedence over closing that review. Approved reviews may still apply selection directives from comments, but non-selection comments must be answered before the review is closed in the same tick.
- Pending planning reviews with both missing proposal payload and fresh comments should handle comments first, then redispatch if the review still lacks a proposal payload on the next tick.
- Targeted `loop --issue` exits on terminal `DONE`, not only on successful merge/close completion. The spec should say this explicitly.
- Final-round verification failure should hand off directly to `needs-review` without forcing another reviewer tick.
- Planning, task-planning, and adjustment application must be replay-safe and must not close their parent review or epic until all required side effects have either completed or the run has failed with resumable state.

These choices follow the repo principles in `AGENTS.md`: deterministic behavior over cleverness, GitHub as the audit surface, and fail-closed handling when state cannot be proven complete.

## Workstreams

### 1. Spec Baseline and Contract Decisions

Purpose:

- resolve pure smoke-spec drift first so later code changes are measured against an explicit target

Deliverables:

- update `docs/plans/smoke-testing-spec.md` with explicit scenarios for:
  - zero-selection approval on top-level planning, milestone task-planning, and adjustments
  - targeted loop exit on terminal `DONE`
  - approved review plus fresh human comments
  - pending review with both missing proposal payload and fresh comments
  - final-round verify failure going straight to `needs-review`
  - tick-global PR conversation sweep preempting non-implementation work
  - malformed reviewer output without a resumable reviewer thread
  - next-tick recovery after partial finalize failure
  - PR-backed `INIT` RESPOND preemption
  - first-round transient `DEVELOP` failure posting `develop-transient`
  - corrupt persisted implementation state failing closed
  - partial needs-review handoff after PR-ready succeeds
  - milestone-review create-then-assign partial success
  - INIT draft-PR creation rollback after branch push
  - bootstrap/planning-dispatch body-update success followed by assignment failure
- reconcile contradictory wording on top-level planning seeding
- mark which scenarios are spec-only clarifications versus scenarios that depend on runtime fixes landing first

Implementation notes:

- do not rewrite the spec around current bugs that violate core principles
- where runtime behavior is already correct and merely undocumented, codify the current behavior
- where runtime behavior is misleading or unsafe, spec the intended corrected behavior, then land code to match

### 2. Review and Comment Handling Semantics

Purpose:

- make human feedback handling precise, bot-attributed, and idempotent

Issues covered:

- mixed planning-comment intents collapsing into one action
- retry duplication after reply-side effects partially succeed
- review/comment handling suppressed by human-authored thumbs-up reactions
- approved reviews closing while fresh comments remain unanswered
- pending no-payload reviews redispatching before comment handling
- PR RESPOND missing GitHub review comments

Target design:

- treat each planning comment as an independent unit of work with its own processed marker
- distinguish selection comments from general discussion comments instead of forcing one aggregate action
- only bot-authored reactions or explicit bot markers count as processed
- unify PR conversation discovery across issue comments, review summaries, and inline review comments
- make RESPOND and planning-comment retries idempotent by persisting or deriving a durable processed state before replay can duplicate visible side effects

Implementation steps:

1. Refactor planning-comment collection so the handler processes one comment at a time in deterministic order.
2. Introduce bot-identity checks for processed markers in both planning-review and PR-conversation paths.
3. Extend conversation discovery to include GitHub review APIs, not only issue comments.
4. Rework retry ordering so the system can detect "reply already posted, marker missing" and resume without duplicate replies or duplicated approval/change-request side effects.
5. Reorder review-precedence logic so fresh comments are handled before closing approved reviews or redispatching no-payload reviews.

Likely code areas:

- `comments/comments.go`
- `comments/handler.go`
- `internal/orchestrator/conversation.go`
- `internal/orchestrator/tick.go`
- `internal/orchestrator/phases.go`

### 3. Planning and Adjustment Materialization Safety

Purpose:

- remove misleading success reporting and make planning or adjustment application resumable

Issues covered:

- top-level planning apply non-atomic behavior
- milestone task-planning partial materialization
- adjustment apply non-atomic behavior
- adjustment apply closing milestone before planning advancement succeeds
- bootstrap/planning issue creation silently ignoring hierarchy/dependency mutation failures
- approved `modify` adjustment silently no-oping on missing target
- zero-selection approval being treated as success

Target design:

- validate the full requested batch before any externally visible closeout
- separate "materialization in progress" from "materialization completed"
- never close a planning review, adjustment review, milestone, or parent epic until all required issue creation, linking, editing, and seeding steps succeed
- persist enough deterministic apply state to resume safely on the next tick without duplicating artifacts
- fail loudly on missing modification targets or failed post-create mutations

Implementation steps:

1. Define explicit apply-state structs for top-level planning, milestone task-planning, and adjustment application.
2. Pre-validate each batch:
   - selection is non-empty
   - all referenced targets exist
   - every requested adjustment type is supported
   - dependency and hierarchy mutations are derivable before any closeout work begins
3. Teach issue creation helpers to return mutation failures instead of logging and continuing.
4. Persist progress for created issues, applied edits, and completed linkage so retries can resume idempotently.
5. Move review/epic closure and next-planning seeding to the final step after successful refresh and reconciliation.
6. Add operator-visible failure reasons when a partial external state exists and recovery will resume from persisted apply state.

Likely code areas:

- `internal/orchestrator/tick.go`
- `internal/issuequeue/app.go`
- any local state package that persists orchestration progress

Design constraint:

- GitHub issue creation is not truly atomic, so the implementation should aim for resumable idempotence rather than pretending external atomicity exists.

### 4. Recovery and Terminal-State Correctness

Purpose:

- make persisted state replay-safe, especially around `FINALIZE`, `RESPOND`, and corrupted recovery inputs

Issues covered:

- partial finalization being recovered as terminal success
- next-tick recovery after partial finalize failure
- corrupt persisted implementation state on unsupported phase or RESPOND resume target
- final-round verification failure skipping directly to terminal handoff without being documented
- malformed review output without a reviewer thread failing closed without repair coverage
- needs-review handoff partial success after PR-ready

Target design:

- only persist truly replay-safe terminal states as terminal
- treat `FINALIZE` as resumable in-progress state until PR update, PR finalization, and issue status mutation all succeed
- keep explicit hard-failure behavior for unsupported recovery states
- document and test direct `VERIFY -> DECIDE(finalize-needs-review)` on last round
- preserve fail-closed reviewer behavior when no same-thread repair path exists

Implementation steps:

1. Split current finalization flow into:
   - finalize-started
   - finalize-side-effects-complete
   - finalized-terminal
2. Re-enter finalization on recovery whenever a terminal proof is absent.
3. Audit all queue/tick callers that currently treat recovered `FINALIZE` as terminal and route them through the resumable path.
4. Keep corruption handling explicit and non-recovering for unknown phases or invalid RESPOND resume targets.
5. Add a small matrix of handoff/finalization partial-success tests so each externally visible step has a defined recovery rule.

Likely code areas:

- `internal/orchestrator/phases.go`
- `internal/orchestrator/queue.go`
- `internal/orchestrator/tick.go`

### 5. Dispatch, Reconciliation, and Queue Correctness

Purpose:

- stop dispatching from stale or misclassified state

Issues covered:

- reconciliation failures ignored before dispatch
- targeted epic issues misclassified as implementation tasks
- queue mode misreporting non-ready tasks as dependency-blocked
- tick-global PR conversation sweep not reflected in the documented dispatch contract

Target design:

- reconciliation errors abort the tick before queue or targeted dispatch
- targeted issue mode validates the real fetched issue type before implementation dispatch
- queue reporting distinguishes:
  - dependency/cycle blocked
  - open but not ready
  - no dispatch candidate for another deterministic reason

Implementation steps:

1. Propagate `Reconcile()` failures to the top-level tick/run result.
2. Move targeted-issue type validation after dependency and type metadata are fully loaded.
3. Add explicit non-ready queue outcomes instead of collapsing them into `All tasks blocked`.
4. Align the smoke spec with the existing tick-global conversation-sweep preemption, or narrow the runtime if that broader preemption is not desired.

Likely code areas:

- `internal/orchestrator/tick.go`
- `internal/orchestrator/queue.go`
- `internal/orchestrator/depgraph.go`
- dispatch safety helpers

## Delivery Sequence

### Phase 0. Lock target semantics

Deliverables:

- a short decision record in this plan or an ADR for the contested semantics
- smoke-spec TODO list split into:
  - document existing correct behavior
  - document intended corrected behavior after code changes

Exit criteria:

- every issue in `docs/plans/issues.md` is tagged internally as `spec-only`, `runtime`, or `runtime+spec-followup`

### Phase 1. Comment and review correctness

Reason:

- these bugs can cause human feedback to be dropped or duplicated, and they influence planning-review precedence semantics

Deliverables:

- per-comment planning handling
- bot-only processed markers
- PR conversation discovery covering review comments
- idempotent retry handling for RESPOND and planning replies

### Phase 2. Dispatch and recovery safety

Reason:

- reconciliation, queue classification, targeted-issue routing, and finalize recovery affect global correctness across all flows

Deliverables:

- reconciliation failures abort dispatch
- targeted issue validation fixed
- non-ready queue outcome fixed
- resumable `FINALIZE` recovery model

### Phase 3. Planning and adjustment apply safety

Reason:

- these changes are broader and should build on the clarified comment/preemption semantics from earlier phases

Deliverables:

- surfaced post-create mutation failures
- resumable apply-state for planning/task-planning/adjustments
- missing-target `modify` failures
- review and milestone closure moved to confirmed-success boundaries

### Phase 4. Smoke spec alignment and coverage completion

Reason:

- land the full spec update once runtime behavior is fixed and validated

Deliverables:

- updated `docs/plans/smoke-testing-spec.md`
- fixture-smoke scenarios or deterministic integration coverage for every resolved issue class
- removal or closure of entries in `docs/plans/issues.md`

## TDD Plan

All runtime changes should be implemented test-first.

Test areas to add or expand:

- `comments`
  - one-comment-at-a-time handling
  - mixed-intent comment queues
  - bot-only processed marker recognition
  - idempotent retry after reply-post success and marker failure
- `internal/orchestrator`
  - approved review plus fresh comments
  - pending no-payload review plus fresh comments
  - PR review comments triggering RESPOND
  - reconcile failure aborting tick
  - targeted epic rejection in `--issue` mode
  - non-ready open tasks not reported as dependency-blocked
  - partial finalize recovery
  - partial needs-review handoff
  - partial milestone-review create-then-assign failure
  - zero-selection approval rejection
  - missing-target `modify` rejection
  - resumable planning/task-planning/adjustment apply
- `internal/issuequeue`
  - post-create hierarchy/dependency mutation failures surfacing to callers
- smoke/integration fixtures
  - issue comments versus PR review comments
  - mixed valid/invalid adjustment batches
  - partial materialization followed by recovery

## Validation

Minimum validation per phase:

```bash
go test ./comments ./internal/issuequeue ./internal/orchestrator
```

When queue/loop behavior changes:

```bash
go test ./internal/cli ./internal/orchestrator
```

Recommended final sweep:

```bash
go test ./...
```

If fixture-smoke coverage exists for the changed flows, run that suite before considering the phase complete.

## Completion Criteria

This plan is complete when:

- no issue in [issues.md](./issues.md) still describes an unresolved contradiction between spec and runtime
- planning, task-planning, adjustment, and finalize flows never report success without either completed side effects or resumable progress state
- comment/review handling is per-comment, bot-attributed, and idempotent
- targeted dispatch and queue summaries accurately describe operator-visible state
- the smoke spec reflects the implemented terminal-state and preemption rules
