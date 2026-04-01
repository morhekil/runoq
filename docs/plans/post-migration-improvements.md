# Post-Migration Improvement Plan

## Status

Status: in-progress

Completed:
- A1: shared code extracted into internal/common (9 functions, 18 tests)
- A2: runtimeorchestrator split into 5 files (2000→84 lines in app.go)
- B1: ~3,100 lines dead shell code removed from 6 scripts
- C1-C5: docs updated; C6: post-migration architecture doc created

Deferred:
- A3, A4: type safety pass (map[string]any at serialization boundaries)
- B2: issue-runner Go migration
- B3: wrapper scripts have active callers in bats tests, not deletable
- A5, A6, B4: polish items

## Purpose

This plan defines improvements identified during a review of the completed Bash-to-Go runtime migration. It covers Go code quality, shell script cleanup, and documentation drift.

The review found:

- ~400 lines of duplicated Go code across 8 packages with no shared types
- `runtimeorchestrator/app.go` at 2,074 lines mixing four concerns
- ~2,000 lines of unreachable shell code below `exec` statements
- issue-runner still shell-owned despite migration marked complete
- documentation describing "shell runtime" as the center of gravity

## A. Go Runtime — Code Quality

### A1. Extract shared code into common packages

Priority: high

~400+ lines duplicated across 8 packages: `envLookup()`, `envSet()`, `runCommand()`, `commandRequest`, `commandExecutor`, `fail()`/`failf()`, `writeJSON()`.

Work:

- create `internal/common` with shared types (`CommandRequest`, `CommandExecutor`), env helpers, slug helpers (`branchSlug`, `branchName`), JSON writers, error helpers
- create `internal/gh` as a single package for GitHub interaction: JWT minting, app token lifecycle, and `gh` CLI wrapping; these are tightly coupled so one package with clear file separation (`auth.go`, `cli.go`, `config.go`) is cleaner than two packages with circular dependency risk
- update all 8 consuming packages to import from shared packages
- delete duplicated definitions

### A2. Split runtimeorchestrator and extract domain packages

Priority: high

`runtimeorchestrator/app.go` at 2,074 lines mixes orchestration, GitHub integration, git operations, JWT generation, and environment management.

Work:

- extract `internal/vcs` for git operations (`diff`, `log`, `checkout`, `add`, `commit`, `push`, rev-list) used across orchestrator, verify, worktree, and dispatch-safety
- extract `internal/gh` (shared with A1) for GitHub API calls and JWT/token logic currently duplicated between orchestrator and issue-queue
- split remaining orchestrator code into multiple files within the package (`phases.go`, `rounds.go`, etc.)
- target: no file over ~500 lines

### A3. Reduce large functions

Priority: medium

Functions exceeding 80-100 lines to decompose:

- `normalizePayload()` in runtimestate (111 lines)
- `reconcileStateFile()` in runtimedispatchsafety (83 lines)
- `runRound()` in runtimeorchestrator (93 lines)

### A4. Eliminate `any` usage, use typed structures and generics

Priority: medium

Several packages use `map[string]any` for JSON and `any` in function signatures where typed alternatives exist.

Work:

- replace `map[string]any` with typed structs for all JSON unmarshaling
- replace `any`-accepting utility functions (`numberOrZero(value any)`, `parseStringArray(value any)`) with Go generics or type-specific variants
- use `json.Number` or typed fields instead of `float64` casts from `any`
- audit for remaining `any` usage and eliminate unless genuinely needed at a serialization boundary

### A5. Use registry-based dispatch in main.go

Priority: low

Replace sequential if-elseif chain in `cmd/runoq-runtime/main.go` with a map-based dispatcher for cleaner subcommand routing.

### A6. Improve test patterns

Priority: low

- adopt table-driven tests where multiple cases follow the same structure
- add negative and edge-case tests for JSON parsing paths
- reduce reliance on real git repos in tests where mocks suffice

## B. Shell Scripts — Cleanup

### B1. Remove dead code below exec statements

Priority: high

Eight dispatcher scripts contain ~2,000+ lines of unreachable shell code after `exec` to the Go runtime. Delete everything after the `exec` line in:

- `dispatch-safety.sh`
- `gh-issue-queue.sh`
- `gh-pr-lifecycle.sh`
- `orchestrator.sh`
- `state.sh`
- `verify.sh`
- `worktree.sh`

`issue-runner.sh` is handled separately in B2.

### B2. Migrate issue-runner to Go

Priority: high

Complete the migration. Port round handling, budget tracking, payload recovery, verification feedback, and escalation paths from `issue-runner.sh` into a Go implementation under `internal/runtimeissuerunner`. Remove the shell implementation and `RUNOQ_ISSUE_RUNNER_IMPLEMENTATION` env var.

### B3. Remove trivial wrapper scripts

Priority: medium

Delete `state.sh`, `verify.sh`, `worktree.sh`. These are pure pass-throughs to the Go binary with no external callers. Update any internal references to call the Go binary directly.

### B4. Split smoke-common.sh

Priority: low

`lib/smoke-common.sh` (~400+ lines) covers auth, env, preflight, and execution. Split into focused modules (`smoke-auth.sh`, `smoke-env.sh`, `smoke-checks.sh`).

### B5. Remove test/helpers/orchestrator.sh

Priority: low

Vestigial test double. Verify no references remain, then delete.

## C. Documentation

### C1. Architecture overview: Go as center of gravity

Priority: high

`docs/architecture/overview.md` still describes "the deterministic shell runtime" as the architectural center of gravity. Update to reflect Go runtime as primary. Update ownership table to show Go dispatch.

### C2. Script contracts: remove transitional language

Priority: high

`docs/reference/script-contracts.md` refers to issue-runner as shell-owned "in the current migration slice." Remove transitional language. After B2 lands, update to reflect Go ownership.

### C3. README: reflect Go-primary architecture

Priority: medium

- "deterministic shell/runtime layer" should say "Go runtime with shell entrypoints"
- "behavior lives in `scripts/`" should say "behavior lives in `internal/runtime*`"
- add Go unit tests to testing section

### C4. Development guidelines: point to Go

Priority: medium

`docs/development-guidelines.md` says "keep orchestration rules in shell scripts." Update to reference Go code as the primary location.

### C5. Standardize runtime terminology

Priority: low

Replace "shell runtime" with "runtime" in ADR 0005, target-repo-contract, and agent/skill guidelines. Add Go to the list of places stable logic belongs.

### C6. Add post-migration architecture doc

Priority: medium

New document explaining:

- how shell entrypoints dispatch to Go
- what remained shell-owned and why (smoke harness, setup, auth, plan)
- where to make changes
- Go module layout guide

## Execution Order

| Phase | Items | Effect |
|-------|-------|--------|
| 1. Shared packages | A1, A2 | Foundation for all subsequent Go work |
| 2. Dead code and wrappers | B1, B3, B5 | Remove ~2,000+ lines of dead and trivial shell |
| 3. Issue-runner migration | B2 | Complete the Go migration |
| 4. Orchestrator decomposition | A3, A4 | Quality pass on largest files |
| 5. Documentation | C1-C6 | Align docs with reality |
| 6. Polish | A5, A6, B4 | Test and dispatch improvements |
