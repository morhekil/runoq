# Spec and System Issues Ledger

All issue classes from the 2026-04-11 audit are closed. This file is kept as a resolved ledger so the implementation plan can point at a stable record without leaving stale "open issue" prose behind.

## Closed Spec Follow-Ups

- Closed 2026-04-11: zero-selection approvals are now explicitly invalid for top-level planning, milestone task-planning, and adjustment reviews. Runtime fails closed and the smoke spec documents the expected operator-visible behavior.
- Closed 2026-04-11: targeted `runoq loop --issue` semantics are documented in terms of terminal `DONE`, not only success completion.
- Closed 2026-04-11: approved planning and adjustment reviews now document fresh-comment precedence. Non-selection human comments are answered before apply/close continues.
- Closed 2026-04-11: pending planning reviews that both lack a proposal payload and have fresh comments now document the correct precedence: comment handling first, redispatch later if still needed.
- Closed 2026-04-11: the smoke spec now covers final-round verification failure routing directly to `finalize-needs-review` without an extra `REVIEW` tick.
- Closed 2026-04-11: the smoke spec now describes the tick-global PR conversation sweep as broader than implementation queue selection. It can preempt planning and milestone progression too.
- Closed 2026-04-11: reviewer-contract failure with no resumable reviewer thread is now explicitly documented as a fail-closed path with no repair attempt.
- Closed 2026-04-11: next-tick recovery after a partial finalize failure is now part of the smoke contract.
- Closed 2026-04-11: PR-backed `INIT` is now explicitly included in RESPOND preemption coverage.
- Closed 2026-04-11: first-round transient `DEVELOP` failures now explicitly require the durable `develop-transient` PR comment in smoke coverage.
- Closed 2026-04-11: corrupt persisted implementation state is now documented as a hard failure instead of a recoverable guess.
- Closed 2026-04-11: partial needs-review handoff after `pr ready` succeeds is now explicitly covered in the smoke spec.
- Closed 2026-04-11: milestone-review create-then-assign partial success is now explicitly covered in the smoke spec.
- Closed 2026-04-11: top-level planning seeding wording has been reconciled to "first newly created milestone", matching runtime behavior.
- Closed 2026-04-11: INIT rollback after branch push but before successful draft-PR creation is now explicitly covered in the smoke spec.
- Closed 2026-04-11: bootstrap/planning-dispatch proposal-body success followed by assignment failure is now explicitly covered in the smoke spec.
- Closed 2026-04-11: planning/task-planning partial-apply recovery is now described as resumable idempotence, not fake external atomicity.
- Closed 2026-04-11: mixed valid/invalid adjustment batches are now documented as fail-closed before earlier valid entries are allowed to mutate state.

## Closed Runtime Issues

- Closed 2026-04-11: planning comment handling is now per-comment and deterministic instead of collapsing mixed intents into one aggregate action.
- Closed 2026-04-11: planning-comment and PR-RESPOND retries now use durable bot markers so partial side effects do not cause duplicate visible replies on replay.
- Closed 2026-04-11: reconciliation failures now abort tick/run dispatch instead of being silently ignored.
- Closed 2026-04-11: PR RESPOND now sees issue-thread comments, review summaries, and inline review comments.
- Closed 2026-04-11: human thumbs-up reactions no longer count as processed markers for planning comments or PR conversations; only bot-authored markers do.
- Closed 2026-04-11: persisted `FINALIZE` state now resumes finalization instead of being treated as safely terminal.
- Closed 2026-04-11: queue mode now distinguishes "open but not ready" from true dependency/cycle blocking.
- Closed 2026-04-11: post-create hierarchy and dependency mutation failures now surface as hard errors to callers instead of being logged and ignored.
- Closed 2026-04-11: approved `modify` adjustments now fail closed when the target issue cannot be resolved.
- Closed 2026-04-11: approved top-level planning apply now persists resumable local apply-state so retries do not duplicate already-created milestones or seeded planning issues.
- Closed 2026-04-11: approved adjustment apply now refreshes and advances planning before closing the review and parent milestone.
- Closed 2026-04-11: targeted issue mode now validates the fetched issue type after dependency metadata is loaded, so epics/planning/adjustment issues are rejected correctly.
- Closed 2026-04-11: approved milestone task-planning now validates dependencies up front and persists resumable apply-state for created tasks and linked dependencies.
- Closed 2026-04-11: approved adjustment application now prevalidates supported adjustment shapes before applying the batch and persists resumable progress for completed edits.

## Reference

- Runtime changes landed across `comments`, `internal/orchestrator`, and `internal/issuequeue`.
- Spec alignment landed in [smoke-testing-spec.md](./smoke-testing-spec.md).
- Delivery tracking is recorded in [spec-and-system-implementation-plan.md](./spec-and-system-implementation-plan.md).
