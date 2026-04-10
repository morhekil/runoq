# Implementation Tick Alignment Follow-Up

## Status

Status: partially completed

## Purpose

This plan aligns the runtime with the intended PR-backed implementation workflow and the preferred agent invocation policy.

Two gaps drive this follow-up:

1. A fresh implementation dispatch currently runs past the intended `INIT` boundary instead of stopping after PR bootstrap.
2. The smoke draft had drifted toward cross-round thread-resume assumptions that are not the preferred design for deterministic GitHub-backed orchestration.

## Target Behavior

### Tick contract

Fresh happy path:

1. `INIT`
2. `DEVELOP`
3. `VERIFY`
4. `REVIEW`
5. `DECIDE`
6. `FINALIZE`

Iterate path:

1. `INIT`
2. `DEVELOP`
3. `VERIFY`
4. `REVIEW`
5. `DECIDE(iterate)`
6. `DEVELOP`
7. `VERIFY`
8. `REVIEW`
9. `DECIDE(finalize)`
10. `FINALIZE`

### Agent invocation policy

- `INIT` is a hard tick boundary on fresh dispatch.
- Cross-round implementer continuity must use explicit state such as `previous_checklist`, verification failures, and current branch state.
- Cross-round implementer thread resume is not part of the contract.
- Codex `resume` is allowed only for same-round schema-retry repair after malformed payload output.
- Reviewer invocations are fresh on every `REVIEW` tick.

## Workstreams

### A. Enforce the `INIT` boundary on fresh dispatch

Primary files:

- `internal/orchestrator/queue.go`
- `internal/orchestrator/app_test.go`
- `internal/orchestrator/tick_test.go`

Changes:

- Make fresh `RunIssue` return immediately after `phaseInit` on non-dry-run dispatch.
- Keep `resumeFromState("INIT")` responsible for the next tick's `DEVELOP` progression.
- Preserve current `runoq run --issue N` end-to-end CLI behavior by letting the CLI-only loop continue across boundaries.

Acceptance criteria:

- Fresh `tick` dispatch of a ready issue stops at `INIT`.
- The following tick resumes from PR state and reaches `DEVELOP`.
- Existing resume-from-`INIT` recovery still works.

### B. Lock in the invocation policy

Primary files:

- `internal/issuerunner/app.go`
- `internal/orchestrator/phases.go`
- `internal/issuerunner/app_test.go`
- `internal/orchestrator/app_test.go`

Changes:

- Keep fresh codex invocation per develop round.
- Keep same-round schema-retry resume only.
- Keep fresh reviewer invocation per review tick.
- Add tests that explicitly reject cross-round resume assumptions in the orchestrator contract.

Acceptance criteria:

- Round-2 develop uses fresh `codex exec` with explicit checklist carry-forward.
- Schema-retry path still uses `codex exec resume <thread_id>`.
- Review round 2 uses a fresh reviewer invocation.

### C. Align smoke expectations and fixture design

Primary files:

- `docs/plans/smoke-testing-spec.md`
- future fixture smoke harness files

Changes:

- Keep the happy-path smoke at 6 ticks and iterate smoke at 10 ticks.
- Validate explicit checklist carry-forward instead of cross-round thread reuse.
- Validate same-thread resume only in schema-retry scenarios.

Acceptance criteria:

- Smoke draft and harness expectations match the target runtime routing.
- No smoke scenario asserts reviewer cross-round resume.

## Test Plan

- Add a regression test proving fresh `RunIssue` stops at `INIT`.
- Add a regression test proving the next tick from `INIT` reaches `DEVELOP`.
- Keep the existing `DEVELOP -> VERIFY`, `VERIFY(fail) -> DECIDE`, and `REVIEW -> DECIDE` resume tests.
- Add or update tests covering:
  - iterate path with explicit `VERIFY` on every round
  - fresh round-2 codex invocation with `previous_checklist`
  - same-round schema retry using `resume`
  - fresh reviewer invocation on later rounds

## Risks

- The main risk is accidentally changing `runoq run --issue N` CLI behavior while fixing tick behavior.
- The second risk is over-coupling tests to exact subprocess argv instead of contract-level behavior.

## Rollout Notes

- Land the router change and tests first.
- Update or add fixture smoke only after the runtime boundary is corrected.
- Keep GitHub PR audit comments as the source of truth for resume behavior throughout the change.
