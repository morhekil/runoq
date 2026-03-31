# Runtime Migration Plan: Bash Core To Go

## Status

Migration status: code/runtime migration is complete through the last known blocker on main (`86713bd` schema-retry hardening and `b947c1f` absolute last-message artifact-path fix). Runtime lifecycle smoke now reaches `REVIEW` and is currently blocked by Claude diff-reviewer `out_of_credits` rather than a runtime code failure. Sandbox smoke remains blocked by inaccessible configured repo access unless otherwise noted by smoke operators. Remaining work is smoke/ops-gated cleanup and confidence-cycle validation, not known migration code gaps.

Current landed fact set:

- foundation slices already in-repo: Go runtime skeleton (`cmd/runoq-runtime`, `internal/runtimecli`), explicit shell/runtime selection at `bin/runoq` with runtime now default and explicit shell fallback preserved, and first acceptance parity scenarios for `run --dry-run` plus `plan --dry-run`
- runtime-backed `state.sh` (`internal/runtimestate`), `report` (`internal/runtimereport`), and `verify.sh` (`internal/runtimeverify`) with shell/runtime parity coverage
- `0913f00` (`runtime: migrate gh-issue-queue behind runtime wrapper`): runtime-backed `gh-issue-queue.sh` list/next/set-status coverage behind the stable shell wrapper
- `136dea5` (`runtimeorchestrator: support queue dry-run`): runtime orchestrator queue dry-run slice with preserved selection, skipped-reason logging, and explicit `INIT` dry-run output
- `947607b` (`runtime: migrate issue-runner run slice`): first real runtime-backed `issue-runner.sh run <payload-json-file>` slice covering budget handling, malformed-payload recovery via `state.sh validate-payload`, single-round `review_ready`, and verification-driven round control within the stable payload-file contract
- `23c67ac` (`runtime: deepen issue-runner failure-path coverage`): deeper deterministic acceptance/unit coverage for issue-runner verification failure and escalation paths
- current slice: runtime orchestrator low-complexity `run --issue` composition now progresses past `INIT` through `CRITERIA`, `DEVELOP`, and the bounded success path `REVIEW -> DECIDE -> FINALIZE`, using the runtime `issue-runner.sh run <payload-json-file>` boundary for develop, preserving the existing deterministic `needs-review` handoff for non-`review_ready` outcomes, and applying the current low-complexity auto-merge decision table plus done-status/worktree cleanup on successful finalize
- current slice: runtime orchestrator non-low-complexity `CRITERIA` handling now progresses past the former not-implemented boundary by recording deterministic `CRITERIA` state and taking an explicit bounded handoff directly to `REVIEW -> DECIDE -> FINALIZE` with `needs-review`, without porting the broader iterative `DECIDE -> DEVELOP` loop or epic `INTEGRATE`
- current slice: state-transition contracts now explicitly allow `CRITERIA -> REVIEW` in both shell and runtime state engines so non-low-complexity deterministic handoff saves do not fail at the first review transition
- current slice: runtime orchestrator `REVIEW` after `review_ready` now follows the real diff-review boundary instead of inferred success, by invoking `diff-reviewer` via `runoq::claude_stream`, parsing verdict data from `review_log_path` with claude-output fallback, persisting deterministic `REVIEW` state, and then continuing through bounded `DECIDE -> FINALIZE`
- current slice: runtime orchestrator low-complexity `DECIDE -> DEVELOP` iterative loop is now bounded and runtime-backed: `DECIDE` emits `iterate` when verdict is `ITERATE` and rounds remain, the orchestrator re-enters `DEVELOP` with persisted checklist context (`review_checklist` carried into `previous_checklist`), and the loop remains bounded by `maxRounds` before deterministic `FINALIZE` handoff
- current slice: runtime orchestrator now owns epic `INTEGRATE` flow parity for queue-mode sweeps, including deterministic `integrate-pending` when children are incomplete, integration worktree create-or-reuse, `verify.sh integrate <worktree> <criteria_commit>` success and failure handling (`done` versus `needs-review` with `integrate_failures`), and runtime/shell acceptance parity for post-drain epic integration
- current slice: `scripts/orchestrator.sh` now defaults `run`-path routing to runtime when no implementation env override is set, while preserving explicit `RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell` fallback behavior with deterministic acceptance coverage for both paths
- current slice: helper wrappers used by the runtime run path now default to runtime when no implementation env override is set (`scripts/state.sh`, `scripts/verify.sh`, `scripts/gh-issue-queue.sh`), while preserving explicit `..._IMPLEMENTATION=shell` fallback behavior with deterministic acceptance coverage for default-routing plus shell-override paths
- current slice: remaining runtime-proven run-path wrappers now default to runtime when no implementation env override is set (`scripts/dispatch-safety.sh`, `scripts/worktree.sh`), while preserving explicit `..._IMPLEMENTATION=shell` fallback behavior with deterministic acceptance coverage for default-routing plus shell-override paths
- current slice: runtime orchestrator no longer forces `RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION=shell` or `RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell` for migrated helper calls, so wrapper defaults (runtime unless explicitly overridden) now flow through orchestrator-managed run paths while shell-owned components remain unchanged
- current slice: the top-level CLI wrapper `bin/runoq` now defaults to runtime when no implementation env override is set, while preserving explicit `RUNOQ_IMPLEMENTATION=shell` fallback behavior with deterministic acceptance coverage at the CLI boundary
- current slice: standalone `scripts/issue-runner.sh` now routes through the runtime wrapper path by default (with explicit `RUNOQ_ISSUE_RUNNER_IMPLEMENTATION=shell` fallback preserved), and `cmd/runoq-runtime` now dispatches `__issue_runner` for wrapper-boundary parity coverage
- current slice: runtime-default wrapper `go run` fallbacks now execute from `RUNOQ_ROOT` (not caller cwd) across CLI and migrated wrappers, with deterministic external-cwd regression coverage to preserve runtime-default behavior in smoke-managed target repos when `RUNOQ_RUNTIME_BIN` is unset
- current slice: `issue-runner.sh` now runs codex with split event/message artifacts (`--json` plus `-o`), persists round `thread_id`, performs bounded same-thread schema retries via `codex exec resume <thread_id> ...` on payload-schema failures, and `state.sh validate-payload` / `internal/runtimestate` now emit deterministic `payload_schema_valid` + `payload_schema_errors` metadata (with optional `thread_id`) to distinguish schema failures from ordinary normalization or verification mismatches
- current slice: orchestrator acceptance harness coverage now uses a contract-compatible fake Codex boundary for runtime issue-runner flows (`exec`, optional `resume`, `--json`, `-o`, and `thread.started` event emission), preventing stale harness behavior from masking runtime parity outcomes
- current slice: `issue-runner.sh` now resolves codex `-o` last-message targets to absolute paths (initial + schema-retry) before `runoq::captured_exec` changes into the sibling worktree, preserving repo-root log artifact writes and preventing malformed-payload retry loops caused by misplaced last-message files
- current slice: sandbox smoke marker commits now force-stage `.runoq/smoke/<run_id>.md` so smoke runs remain stable when target repositories ignore `.runoq/`, with deterministic live-smoke regression coverage for ignored-path staging

Still pending: smoke-gated rollout completion and cleanup (confidence-cycle smoke lanes, fallback retirement timing, and final simplification steps in later milestones).

## Purpose

This document defines the migration plan for moving `runoq`'s deterministic orchestration core from Bash to Go while preserving the current operator workflow, GitHub audit model, sibling worktree model, and machine-readable contracts.

It also defines the verification model for the migration:

- deterministic tests for fast regression coverage
- parity acceptance tests for implementation-independent behavioral equivalence
- live smoke lanes for real GitHub and real LLM validation

This plan does not freeze low-level implementation details such as package shapes, function signatures, or internal type design.

## Why Migrate

The repository's shell-first design has been valuable and aligned with [ADR 0001](../adr/0001-shell-scripts-and-json-contracts.md). The recent architecture shift, however, moved the center of gravity from thin shell wrappers to a large stateful runtime with:

- multi-phase orchestration
- JSON-heavy contracts
- subprocess and stream management
- recovery and rollback flows
- structured capture logging
- cross-step state persistence
- real GitHub and real LLM workflow composition

That design still makes sense. The pressure now comes from the implementation language rather than the workflow architecture.

The migration goal is therefore:

- keep the workflow
- keep the contracts
- replace the core implementation substrate

## Goals

- Preserve the current CLI shape and user-facing workflow.
- Preserve documented JSON contracts unless explicitly versioned.
- Preserve GitHub as the audit and control surface.
- Preserve `.runoq/state/*.json` as resumability breadcrumbs.
- Preserve sibling worktrees as the execution model.
- Migrate incrementally by subsystem.
- Maintain shell compatibility wrappers during transition.
- Use parity acceptance tests and live smoke lanes as migration gates.

## Non-Goals

- Do not redesign the product workflow during migration.
- Do not move deterministic behavior back into prompts.
- Do not replace GitHub with another control plane.
- Do not replace sibling worktrees with in-place execution.
- Do not attempt a big-bang rewrite.
- Do not lock in incidental implementation artifacts as public contract unless explicitly intended.

## Target Architecture

End-state ownership should look like this:

- Thin shell entrypoints remain in `bin/` and selected `scripts/`.
- The Go runtime owns deterministic orchestration logic.
- Agents remain thin and operate around stable contracts.
- GitHub operations remain observable and script-boundary friendly.
- Bats continues to provide deterministic contract coverage.
- A parity acceptance suite provides cross-implementation confidence.
- Live smoke lanes provide real-world validation against GitHub and real model stacks.

## Migration Scope

### Priority 1: move first

- `orchestrator`
- `issue-runner`
- `state`
- `dispatch-safety`
- shared capture/logging/process helpers

### Priority 2: move after the substrate is stable

- `gh-pr-lifecycle`
- `gh-issue-queue`
- `verify`
- `plan`

### Likely to stay shell-based longer

- `setup`
- `worktree`
- smoke harness scripts
- small operator wrappers and convenience scripts

## Contract Taxonomy

Before migration, every externally relevant behavior must be classified as either exact-match or normalized-match.

### Exact-match contract

These should match exactly across implementations unless explicitly versioned:

- CLI command shape
- stdout JSON fields and meanings
- exit-code behavior
- audit markers such as `<!-- runoq:event -->` and `<!-- runoq:payload:* -->`
- state transition semantics
- issue and PR label semantics
- GitHub-side effect meaning
- top-level log layout when intended as a contract
- expected artifact file presence when intended as a contract

### Normalized-match contract

These may be normalized before parity comparison:

- timestamps
- PIDs
- random uniqueness suffixes
- temp-derived paths
- non-semantic log directory suffixes
- non-semantic stderr wording

### Log naming rule

Exact log directory names should be treated as contract only if operators or automation are expected to rely on them directly.

Otherwise, parity should compare:

- stable directory structure
- stable semantic naming components
- discoverability
- artifact presence
- artifact content where relevant

while normalizing:

- timestamp fragments
- PID fragments
- random uniqueness tokens

## Verification Model

The migration uses four verification layers.

### 1. Native runtime tests

Purpose:

- fast feedback on Go internals
- focused coverage for state transitions, subprocess behavior, capture logic, and adapters

### 2. Existing Bats suites

Purpose:

- deterministic black-box regression coverage
- continuity with existing shell contracts and fixture-based behavior

### 3. Parity acceptance suite

Purpose:

- compare `legacy-shell` and `new-runtime` at the same behavioral boundary
- gate command cutovers without depending on implementation internals

### 4. Live smoke lanes

Purpose:

- validate the migrated system against real GitHub and real LLM workflows
- catch integration problems that deterministic fixtures cannot prove

Current smoke lanes are defined in [live-smoke.md](../live-smoke.md):

- sandbox smoke
- lifecycle eval
- planning smoke

## Parity Acceptance Suite

The acceptance suite is the main migration parity gate.

### Design

Each scenario should:

1. create the same target repo fixture
2. inject the same config and fake GitHub scenario
3. run the same user-facing command against both implementations
4. collect an observation bundle
5. normalize incidental differences
6. compare the normalized outcomes
7. optionally compare both to a golden contract expectation

### Observation bundle

Each scenario should capture:

- invoked command
- exit status
- stdout
- stderr
- fake `gh` call log
- created and updated comment bodies
- resulting issue and PR label state
- resulting `.runoq/state` files
- resulting branches and worktrees
- relevant `log/` artifacts

### What parity should assert

- command result and exit status
- stdout JSON contract
- semantic stderr category where relevant
- issue and PR mutation meaning
- recovery behavior
- audit marker presence and sequencing where intended
- final repo and worktree state
- state breadcrumb meaning

### What parity should not freeze by default

- exact temp paths
- exact timestamps
- exact PID-derived directory names
- non-semantic wording drift in diagnostics

### First acceptance scenarios

Build these before major cutovers:

1. `run --dry-run`
2. `run --issue` happy path
3. `run --issue` init failure rollback
4. `run --issue` no-commit verification escalation
5. queue dependency ordering
6. criteria and integrate flow
7. malformed payload recovery
8. mention authorization and deduplication
9. report commands
10. `plan --dry-run`

## Live Smoke Strategy

Live smoke is not a substitute for deterministic parity coverage. It is the reality-check layer that validates real GitHub and real model integration.

### Sandbox smoke

Best for:

- GitHub App auth
- attribution as `runoq[bot]`
- label provisioning
- issue and PR comments
- collaborator permission checks
- wrapper and environment propagation sanity

### Lifecycle eval

Best for:

- real `runoq run` end-to-end behavior
- orchestrator and issue-runner correctness
- PR lifecycle
- verification and finalization
- queue ordering
- one-shot completion quality
- integrated real LLM behavior

### Planning smoke

Best for:

- real `runoq plan` decomposition
- issue creation
- epic/task hierarchy
- complexity rationale propagation

### Smoke cadence

- Run baseline smoke before migration starts.
- Run the relevant smoke lane mid-milestone after major cutovers.
- Run the relevant smoke lane again before switching the default implementation.
- Run the relevant smoke lane again after the default switch.
- Run the relevant smoke lane one final time before deleting the shell fallback.

## Milestone Checklist Table

| Milestone | Main Scope                                     | Deterministic Gate                   | Acceptance Gate                                                        | Inter-Work Smoke                                                           | Post-Work Smoke                                                  | Switch Default Gate                                     | Delete Fallback Gate                                                      |
| --------- | ---------------------------------------------- | ------------------------------------ | ---------------------------------------------------------------------- | -------------------------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------- |
| M0        | Contract freeze and baseline                   | Existing Bats unchanged              | Acceptance design approved                                             | Sandbox baseline; planning baseline if needed; one lifecycle baseline      | Baseline artifacts recorded                                      | N/A                                                     | N/A                                                                       |
| M1        | Acceptance harness foundation                  | New harness tests green              | Shell implementation passes first parity scenarios                     | Sandbox smoke optional if wrapper/test scaffolding touches runtime routing | Sandbox smoke if command routing changed                         | N/A                                                     | N/A                                                                       |
| M2        | Runtime skeleton and wrapper routing           | Build plus existing Bats green       | Acceptance runner can target both implementations                      | Sandbox smoke after wrapper routing                                        | Sandbox smoke stable with runtime present                        | Wrapper routing proven safe                             | N/A                                                                       |
| M3        | Shared infrastructure: state, capture, logging | State and logging tests green        | State and logging parity scenarios green                               | Sandbox smoke after substrate cutover                                      | Sandbox smoke plus one lifecycle eval                            | Shared helpers safe to use under runtime by default     | Old helper paths unused and undocumented                                  |
| M4        | Dispatch safety and recovery                   | Dispatch Bats green                  | Dry-run and recovery parity scenarios green                            | Lifecycle eval after recovery cutover                                      | Lifecycle eval stable; sandbox smoke if label/auth paths changed | Pre-dispatch path safe under runtime                    | Recovery shell path no longer needed                                      |
| M5        | Orchestrator state machine                     | Run integration tests green          | Happy-path and init-failure parity scenarios green                     | Lifecycle eval after partial cutovers                                      | Lifecycle eval plus sandbox smoke after full cutover             | Runtime orchestrator passes parity plus smoke           | Shell orchestrator fallback only removed after repeated smoke passes      |
| M6        | Issue-runner and verification orchestration    | Issue-runner and verify suites green | No-commit, malformed payload, and verification escalation parity green | Lifecycle eval after failure-path parity                                   | Lifecycle eval quality stable relative to baseline               | Runtime issue-runner passes parity plus lifecycle eval  | Shell issue-runner fallback removed after repeated lifecycle eval success |
| M7        | Queue, PR lifecycle, mentions, permissions     | Queue and PR Bats green              | Queue ordering and mention parity scenarios green                      | Sandbox smoke; planning smoke if issue creation semantics changed          | Sandbox smoke plus lifecycle eval                                | Runtime GitHub path passes parity plus smoke            | Shell GitHub lifecycle paths removed after repeated smoke success         |
| M8        | Planning flow                                  | Planning tests green                 | `plan --dry-run` parity green                                          | Planning smoke during cutover                                              | Planning smoke stable with runtime default                       | Runtime planning flow passes parity plus planning smoke | Shell planning fallback removed after repeated planning smoke success     |
| M9        | Cleanup and architecture simplification        | Full deterministic suite green       | Acceptance suite green on runtime-default repo                         | Full smoke set before removing final shell fallbacks                       | Full smoke set after cleanup                                     | Runtime is the default everywhere intended              | Old shell core paths removed only after final three-lane smoke pass       |

## Milestone Details

### M0: Contract Freeze And Baseline

Objective:

- define exactly what compatibility means
- record the real-world baseline before rewriting core behavior

Work:

- freeze command contracts from [script-contracts.md](../reference/script-contracts.md)
- classify fields as exact-match or normalized-match
- define log artifact contract boundaries
- document migration non-goals
- capture a shell-implementation baseline through live smoke

Deliverables:

- compatibility matrix
- acceptance taxonomy
- baseline smoke results and artifact storage convention

### M1: Acceptance Harness Foundation

Objective:

- establish the parity gate before major cutovers

Work:

- define acceptance scenario schema
- implement scenario runner
- implement adapters for shell and runtime
- implement normalization rules
- add the first high-value scenarios

Deliverables:

- acceptance test harness
- first parity scenarios
- shell implementation green against the parity suite

### M2: Runtime Skeleton And Wrapper Routing

Objective:

- introduce the Go runtime without changing behavior

Work:

- add the runtime skeleton
- add shell compatibility wrappers
- add command-level implementation selection
- ensure the acceptance suite can target both implementations cleanly

Deliverables:

- callable runtime
- wrapper routing
- no user-facing command change

### M3: Shared Infrastructure Migration

Objective:

- migrate the shell-mechanics-heavy substrate first

Work:

- port config and repo context helpers
- port state persistence and payload normalization
- port capture logging and subprocess execution
- preserve `.runoq/state/` and `log/` contracts

Deliverables:

- runtime-backed state and capture substrate
- thin shell wrappers over the new substrate

### M4: Dispatch Safety And Recovery

Objective:

- migrate startup reconciliation and eligibility checks

Work:

- port stale-run handling
- port reconciliation logic
- port eligibility decisions and skip reasons
- preserve recovery semantics

Deliverables:

- runtime-backed pre-dispatch safety layer

### M5: Orchestrator State Machine Migration

Objective:

- replace the main shell phase engine

Work:

- port phase transitions
- port init, criteria, develop, review, decide, finalize, and integrate flow
- preserve rollback, resume, and audit sequencing
- keep wrapper compatibility while parity is established

Deliverables:

- runtime-backed orchestrator

### M6: Issue-Runner And Verification Orchestration

Objective:

- migrate the development round loop and verification orchestration

Work:

- port round handling
- port budget tracking
- port malformed payload recovery
- port verification feedback and escalation paths
- preserve capture artifacts and PR-side effects

Deliverables:

- runtime-backed issue-runner path

### M7: Queue, PR Lifecycle, Mentions, Permissions

Objective:

- migrate GitHub-heavy deterministic logic behind a structured runtime boundary

Work:

- port queue discovery and ordering
- port label mutation and issue creation
- port PR lifecycle
- port mention polling and permission checks
- initially keep `gh` as the adapter under the runtime boundary unless direct API migration is separately justified

Deliverables:

- runtime-backed GitHub lifecycle boundary

### M8: Planning Flow Migration

Objective:

- migrate `plan` once shared agent and GitHub boundaries are stable

Work:

- port decomposition execution
- port proposal presentation and confirmation
- port issue creation ordering and hierarchy creation

Deliverables:

- runtime-backed planning flow

### M9: Cleanup And Architecture Simplification

Objective:

- remove obsolete shell core paths after parity is proven

Work:

- delete deprecated shell implementations
- keep only intentional shell wrappers and smoke helpers
- update ADRs and architecture documents
- formalize permanent shell vs runtime ownership boundaries

Deliverables:

- simplified runtime-centered architecture

## Rollout Rules

### Rule 1: cut over by command family, not by file count

Examples:

- state and capture substrate together
- orchestrator phases together
- queue and PR lifecycle together

### Rule 2: every default switch needs three green signals

Before switching a command family to runtime by default:

- deterministic tests must pass
- relevant parity acceptance scenarios must pass
- relevant live smoke lane must pass

### Rule 3: every shell fallback deletion needs a second confidence cycle

Before deleting the shell fallback for a command family:

- keep the runtime as default long enough to gather one more round of deterministic and smoke confidence
- rerun the relevant smoke lane under runtime-default conditions

### Rule 4: do not migrate two high-risk cores at once

Avoid overlapping major cutovers such as:

- orchestrator plus issue-runner plus queue lifecycle in one change
- state substrate plus full GitHub boundary in one jump

### Rule 5: add parity scenarios for every shell regression class discovered during migration

Any migration-era shell bug or runtime bug that changes contract-relevant behavior should become acceptance coverage if it is likely to recur across implementations.

## Suggested PR Breakdown

Recommended order:

1. document compatibility matrix and migration taxonomy
2. add acceptance harness
3. add first parity scenarios
4. add runtime skeleton and wrapper routing
5. port state subsystem
6. port capture and logging subsystem
7. port dispatch safety
8. port orchestrator
9. port issue-runner
10. port verify orchestration if still separate
11. port queue lifecycle
12. port PR lifecycle and mentions
13. port planning flow
14. remove deprecated shell core paths
15. update ADRs and architecture docs

## Governance During Migration

- New deterministic behavior should go into the runtime unless there is a strong reason to keep it in shell.
- Shell changes in migrated areas should be wrapper-only or urgent bugfix-only.
- Acceptance parity is required before changing the default implementation for a command family.
- Live smoke is required wherever the milestone says it is required, not only when something "feels risky."
- The shell implementation is not the oracle in raw form; the documented contract is.

## Completion Criteria

The migration is complete when all of the following are true:

- the runtime owns deterministic orchestration behavior for the intended command families
- acceptance parity is green across the runtime-default implementation
- the relevant Bats suites are green
- sandbox smoke passes
- lifecycle eval passes
- planning smoke passes
- obsolete shell core paths have been removed or explicitly retained with documented ownership
- architecture docs and ADRs reflect the new reality

## Immediate Next Steps

The recommended starting sequence is:

1. approve this migration plan and the contract taxonomy
2. document the compatibility matrix
3. run and store baseline smoke results for the current shell implementation
4. build the acceptance harness (implemented)
5. add the first parity scenarios (implemented for `run --dry-run` and `plan --dry-run`)
6. introduce the runtime skeleton (implemented)

That sequence creates the migration gates before large behavior changes begin.
