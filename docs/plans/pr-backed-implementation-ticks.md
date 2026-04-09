# PR-Backed Implementation Tick Plan

## Status

Status: in progress

Completed so far:

- [x] `INIT` creates the draft PR immediately and posts initial audit state on the PR
- [x] `DECIDE` is a hard tick boundary and no longer chains into `FINALIZE` in the same tick
- [x] `VERIFY` exists as an explicit orchestrator phase boundary in the router
- [x] Resume tests updated for `DEVELOP -> VERIFY`, `VERIFY(fail) -> DECIDE`, and `REVIEW(pass) -> DECIDE`
- [x] `VERIFY` persists verifier inputs in PR state and re-runs deterministic verification from a fresh rehydrated branch worktree
- [x] `DEVELOP`, `VERIFY`, and `REVIEW` rehydrate disposable worktrees from the pushed branch instead of trusting prior local paths
- [x] `RESPOND` preempts every PR-backed phase when the PR has unprocessed non-audit comments
- [x] Orchestrator tests and full `go test ./...` are green after the boundary refactor

Still to do:

- [ ] Move verification execution itself out of the issue-runner loop so `VERIFY` is a truly separate tick on a fresh machine
- [ ] Remove `OPEN-PR` as a meaningful runtime phase and treat it as compatibility-only until old state/comments are gone
- [ ] Remove the issue-runner as a top-level orchestration concept and keep only reusable helpers
- [ ] Change phase comments from "next-state carriers" toward phase-result records and centralize routing logic fully in the orchestrator
- [ ] Update smoke specs and fixture smoke coverage to the new tick cadence

## Purpose

This plan tracks the refactor from the older mixed orchestration model toward a PR-backed, one-step-per-tick implementation workflow where a later tick can run on a different machine using GitHub as the durable control surface.

The core goals are:

- one durable audit surface: the PR
- one meaningful action per tick
- resume from GitHub, not from prior local filesystem state
- explicit phase boundaries
- no hidden internal orchestration loop deciding multiple downstream steps

## Target Tick Model

Target phases:

1. `INIT`
2. `DEVELOP`
3. `VERIFY`
4. `REVIEW`
5. `DECIDE`
6. `FINALIZE`
7. `RESPOND` as an interrupt, not a linear tail phase

Steady-state flow:

`INIT -> DEVELOP -> VERIFY -> REVIEW -> DECIDE -> DEVELOP/FINALIZE`

Interrupt flow:

- before any PR-backed phase, check for unprocessed PR comments
- if comments exist, run `RESPOND` only and stop

## Tick Responsibilities

### `INIT`

- mark issue `runoq:in-progress`
- create worktree and branch
- push the branch
- create the draft PR immediately
- post initial machine-readable audit state on the PR
- clean up local workspace when practical

### `DEVELOP`

- rehydrate a fresh local workspace from the PR branch
- run exactly one coding round
- commit and push if there are code changes
- mark PR ready if this is the first meaningful code push
- post `DEVELOP` result/state on the PR
- stop

### `VERIFY`

- rehydrate a fresh local workspace from the PR branch
- run deterministic verification only
- post verifier result on the PR
- stop

### `REVIEW`

- rehydrate a fresh local workspace from the PR branch
- run automated diff review only
- post review result on the PR
- stop

### `DECIDE`

- read latest phase results from the PR
- decide only the next route for the round
- post decision on the PR
- stop

### `FINALIZE`

- read `DECIDE` result
- perform final side effects only
- update issue labels/status
- enable auto-merge or hand off to `needs-review`
- post final result on the PR
- stop

### `RESPOND`

- read unprocessed human PR comments
- post replies
- mark comments processed
- do not advance implementation phases

## Routing Rules

The orchestrator should own all routing.

Current intended rules:

1. If no `INIT` result exists, run `INIT`.
2. If the current round has no `DEVELOP` result, run `DEVELOP`.
3. If the current round has no `VERIFY` result, run `VERIFY`.
4. If `VERIFY` failed, skip `REVIEW` and run `DECIDE`.
5. If `VERIFY` passed and `REVIEW` is missing, run `REVIEW`.
6. If required gates for the round are complete, run `DECIDE`.
7. If `DECIDE` says `iterate`, increment round and return to `DEVELOP`.
8. If `DECIDE` says terminal, run `FINALIZE`.

## Durable State Rules

After `INIT`, the PR is the durable execution record.

The next tick must be runnable on a different machine. A tick must not require:

- a previous `worktree` path
- a previous `log_dir`
- temporary payload files from a prior machine
- any local state not reconstructable from GitHub and the branch

Local workspace state is disposable. Each tick may create a worktree, use it, then remove it. Cleanup is hygiene, not correctness.

## Implementation Checklist

### A. Router and Boundaries

- [x] Add tests for `INIT` PR creation
- [x] Add tests for `DECIDE` tick boundary on `PASS`
- [x] Add tests for `VERIFY(fail) -> DECIDE`
- [x] Route resumed `DEVELOP` into `VERIFY`
- [x] Route pending PR comments into `RESPOND` before every PR-backed phase
- [ ] Remove legacy routing assumptions that still treat `OPEN-PR` as a primary phase

### B. Verification Extraction

- [ ] Split verification execution out of `issue-runner` so `DEVELOP` no longer performs verification internally
- [x] Persist whatever inputs `VERIFY` needs in a GitHub-recoverable form
- [x] Make `VERIFY` re-run deterministic verification on a fresh machine
- [x] Update tests to prove `VERIFY` is independent of prior local temp files

### C. Disposable Worktree Rehydration

- [x] Add a worktree helper that can materialize an existing pushed branch into a fresh disposable worktree
- [x] Use that helper from `DEVELOP`
- [x] Use that helper from `VERIFY`
- [x] Use that helper from `REVIEW`
- [ ] Best-effort cleanup the worktree after each tick

### D. Issue-Runner Reduction

- [ ] Replace the issue-runner round loop with reusable single-purpose helpers
- [ ] Keep codex invocation and payload/schema helpers only where they still add value
- [ ] Remove hidden multi-step orchestration from the issue-runner package

### E. Result Model Cleanup

- [ ] Move from phase comments carrying full next-state assumptions toward phase-result comments
- [ ] Keep routing policy centralized in the orchestrator
- [ ] Keep backward compatibility with existing state comments only as long as needed for resume

### F. Smoke and Docs

- [ ] Update fixture smoke expectations to the new cadence:
  `INIT -> DEVELOP -> VERIFY -> REVIEW -> DECIDE -> FINALIZE`
- [ ] Add iterate-path smoke coverage with the new verify boundary
- [ ] Add comment-response smoke proving `RESPOND` preempts normal progress
- [ ] Update operator-facing docs once the remaining slices are landed

## Notes

The current landed slice is intentionally partial.

It now establishes fresh-worktree execution for `DEVELOP`/`VERIFY`/`REVIEW` and makes `VERIFY` rerun deterministic verification from persisted state instead of trusting prior temp files.

The remaining architectural gap is that `issue-runner` still performs an internal verification loop to decide whether a `DEVELOP` round is review-ready. Fully removing that internal loop is still the next major slice.
