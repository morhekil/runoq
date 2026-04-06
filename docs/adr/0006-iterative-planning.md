# ADR 0006: Iterative Milestone-Gated Planning

## Status

Accepted

## Context

The original planning flow in `runoq` decomposed an entire plan document into all epics and tasks in one pass and then created queue issues after a single confirmation step. That produced two persistent problems:

- the decomposition had no adversarial review step before humans saw it
- the full issue graph was committed up front, so later implementation learnings could not reshape the plan cleanly

At the same time, the execution side of `runoq` already had stronger deterministic orchestration, review gates, and GitHub-centered audit trails than the planning side.

## Decision

`runoq` now treats planning as an iterative, milestone-gated workflow coordinated by `runoq tick`.

Key decisions:

- GitHub issues are the planning control surface and system of record.
- Planning and adjustment work use explicit issue types (`planning`, `adjustment`) instead of terminal prompts.
- Proposals are posted as comments first; issue creation happens only after human approval.
- Every proposal is reviewed from two perspectives before publication: technical and product.
- Only the next milestone is decomposed into tasks. Later milestones remain coarse until earlier work produces concrete learnings.
- Discovery milestones always pause for human review when auto-advance is disabled.

## Consequences

Positive:

- planning gets the same deterministic auditability as execution
- humans review numbered proposals and comments directly on GitHub
- milestone reviews can modify the future plan without rewriting local state
- `runoq tick` becomes a single coordination command for operators and smoke tests

Tradeoffs:

- the planning workflow now spans more issue states and helper scripts
- smoke coverage must validate planning comments, approvals, and adjustment paths, not just issue creation
- the older `runoq plan` path remains as a deprecated compatibility flow during migration

## Implementation Notes

Core contracts introduced by this decision:

- `runoq.json` at the target repo root stores the committed plan path
- `plan-dispatch.sh` runs the decompose -> review -> iterate loop
- `tick.sh` advances exactly one planning/execution state transition per invocation
- `plan-comment-handler.sh` handles human comments on planning and adjustment issues
- `milestone-reviewer` can propose follow-up adjustments instead of assuming the original plan was correct
