# Test-First Implementation Plan

This document turns the PRD in `docs/prd.md` into a working backlog for implementation. It is organized to support a test-first build: deterministic code and contracts first, agent prompts after the shell/runtime layer is stable.

## Task Metadata

Each task includes a small YAML block for tracking.

- `id`: Stable task identifier.
- `status`: `todo`, `in_progress`, `blocked`, or `done`.
- `priority`: Initial delivery priority.
- `depends_on`: Task IDs that should land first.

## Working Principles

- Put deterministic behavior in scripts, not prompts.
- Write fixtures and contract tests before implementing orchestration behavior.
- Treat agents as thin dispatch layers over tested scripts.
- Prove the single-issue happy path before building queue-wide and maintenance features.
- Treat verification, recovery, and audit logging as core behavior, not hardening.

## Test Strategy

### Test layers

1. Unit tests
   - Parsing, validation, state transitions, repo detection, config handling.
2. Integration tests
   - Script and CLI flows with temp git repos and a fake `gh` command.
3. End-to-end local workflow tests
   - `agendev run --issue N` against fake GitHub responses.
4. Live smoke tests
   - Minimal checks against a sandbox GitHub repo with real app auth.

### Test harness requirements

- `bats`-based shell test runner.
- Temp directory and temp git repo helpers.
- Fixture loader utilities.
- `fake-gh` executable that returns fixture JSON and records invocations.
- Stable fixture sets for issue bodies, PR bodies, payloads, and GitHub API responses.

## Milestone Plan

### Milestone 1: Test Harness And Contracts

#### Task 1: Bootstrap shell test harness

---
id: T01
status: done
priority: high
depends_on: []
---

Acceptance criteria:
- `bats`-based test runner is wired into the repo.
- Test helpers exist for temp dirs, temp git repos, fixture loading, and command recording.
- A `fake-gh` executable can return fixture JSON and capture invocations.
- A smoke test proves the harness can run end to end.

Dependencies:
- None.

#### Task 2: Add fixture set for PRD contracts

---
id: T02
status: done
priority: high
depends_on:
  - T01
---

Acceptance criteria:
- Fixtures exist for valid and invalid issue metadata blocks.
- Fixtures exist for all inter-agent payload shapes from the PRD.
- Fixtures exist for audit comment formats with `agendev:event` and `agendev:payload:*` markers.
- Fixtures cover malformed JSON, missing fields, wrong types, and unknown fields.

Dependencies:
- Task 1.

#### Task 3: Codify payload validation rules

---
id: T03
status: done
priority: high
depends_on:
  - T02
---

Acceptance criteria:
- Tests define required fields, defaults, and patching behavior for Codex return payloads.
- Tests cover synthesis when no JSON block is returned.
- Tests cover normalization of invalid `status` values to `failed`.
- Expected outputs are fixture-driven and machine-verifiable.

Dependencies:
- Task 2.

### Milestone 2: Config, Repo Detection, And State

#### Task 4: Add centralized config and template files

---
id: T04
status: done
priority: high
depends_on:
  - T01
---

Acceptance criteria:
- `config/agendev.json` exists with labels, identity, auth, rounds, budget, verification, and stall settings from the PRD.
- `templates/issue-template.md` and `templates/pr-template.md` exist with required marker sections.
- Tests validate config shape and required keys.
- Template markers match the names used by scripts.

Dependencies:
- Task 1.

#### Task 5: Implement repo and environment resolution helpers

---
id: T05
status: done
priority: high
depends_on:
  - T01
---

Acceptance criteria:
- Logic resolves `AGENDEV_ROOT`, `TARGET_ROOT`, and `REPO` deterministically.
- Tests cover SSH and HTTPS GitHub remotes.
- Tests fail correctly for non-git directories, missing `origin`, and non-GitHub remotes.
- Errors are human-readable and actionable.

Dependencies:
- Task 1.

#### Task 6: Implement `state.sh` save/load/transition enforcement

---
id: T06
status: done
priority: high
depends_on:
  - T01
  - T02
---

Acceptance criteria:
- `state.sh` supports `save`, `load`, and transition validation.
- Atomic writes use temp file plus rename.
- Invalid transitions are rejected, including recovery from terminal `FAILED` or `DONE`.
- Tests cover happy-path transitions, invalid transitions, and corrupted state file handling.

Dependencies:
- Task 1.
- Task 2.

#### Task 7: Add processed mention state management

---
id: T07
status: done
priority: medium
depends_on:
  - T06
---

Acceptance criteria:
- State helpers can record and query processed GitHub comment IDs.
- Tests verify deduplication across polling cycles.
- Missing processed-mentions state initializes cleanly.
- Writes are atomic.

Dependencies:
- Task 6.

### Milestone 3: GitHub Script Layer

#### Task 8: Implement `gh-issue-queue.sh` list and metadata parsing

---
id: T08
status: done
priority: high
depends_on:
  - T01
  - T02
  - T04
---

Acceptance criteria:
- `list` returns structured JSON for queued issues.
- Metadata block parsing supports dependencies, priority, and estimated complexity.
- Tests cover absent metadata, malformed metadata, and mixed labels.
- Output is stable and suitable for downstream scripting.

Dependencies:
- Task 1.
- Task 2.
- Task 4.

#### Task 9: Implement `gh-issue-queue.sh` next with dependency resolution

---
id: T09
status: done
priority: high
depends_on:
  - T08
---

Acceptance criteria:
- `next` returns the next actionable issue only when all dependencies are `agendev:done`.
- Sorting is by explicit priority, then issue number.
- Tests cover blocked dependencies, missing dependency issues, and FIFO tie-breaking.
- Non-actionable issues are excluded with deterministic reasons available to callers.

Dependencies:
- Task 8.

#### Task 10: Implement `gh-issue-queue.sh` set-status and create

---
id: T10
status: done
priority: high
depends_on:
  - T08
---

Acceptance criteria:
- `set-status` removes old `agendev:*` labels and applies exactly one new state label.
- `create` creates issues with metadata block and ready label.
- Tests verify label transitions and created issue body structure.
- Unknown statuses fail cleanly.

Dependencies:
- Task 8.

#### Task 11: Implement `gh-pr-lifecycle.sh` create/comment/update-summary/finalize

---
id: T11
status: done
priority: high
depends_on:
  - T01
  - T02
  - T04
---

Acceptance criteria:
- `create` opens a draft PR linked to the issue.
- `comment` posts body content from file.
- `update-summary` replaces only marker-delimited sections in the PR body.
- `finalize` supports both auto-merge and needs-review flows.
- Tests verify exact `gh` calls and body mutation behavior.

Dependencies:
- Task 1.
- Task 2.
- Task 4.

#### Task 12: Implement `gh-pr-lifecycle.sh` line-comment/read-actionable/poll-mentions/check-permission

---
id: T12
status: done
priority: medium
depends_on:
  - T11
  - T07
---

Acceptance criteria:
- `line-comment` supports single-line and multi-line review comments.
- `read-actionable` returns only agent mentions and line-level comments, excluding audit comments.
- `poll-mentions` returns unprocessed mention candidates with context fields.
- `check-permission` enforces minimum permission using fixture-backed API responses.
- Tests cover both authorized and unauthorized users.

Dependencies:
- Task 11.
- Task 7.

### Milestone 4: Auth, Setup, And CLI

#### Task 13: Implement `gh-auth.sh` for GitHub App token minting

---
id: T13
status: done
priority: medium
depends_on:
  - T04
  - T05
---

Acceptance criteria:
- Script reads `.agendev/identity.json` and resolves private key path.
- Script can mint and export `GH_TOKEN` from a GitHub App installation.
- Tests cover missing identity, missing key, invalid key path, and refresh behavior.
- Failures suggest `agendev init` when appropriate.

Dependencies:
- Task 4.
- Task 5.

#### Task 14: Implement `setup.sh` for idempotent `agendev init`

---
id: T14
status: done
priority: medium
depends_on:
  - T10
  - T13
---

Acceptance criteria:
- `agendev init` creates `.agendev/state/` if missing.
- It validates or creates `.agendev/identity.json`.
- It ensures required labels exist without duplicating them.
- It creates `package.json` only if missing.
- It ensures the CLI symlink points to `bin/agendev`.
- Tests verify idempotency across repeated runs.

Dependencies:
- Task 10.
- Task 13.

#### Task 15: Implement `bin/agendev` subcommand routing

---
id: T15
status: done
priority: high
depends_on:
  - T05
  - T13
  - T14
---

Acceptance criteria:
- `init`, `plan`, `run`, `report`, and `maintenance` route correctly.
- `run --issue N` and `run --dry-run` are passed through correctly.
- Environment variables are exported before invoking child commands.
- Tests cover usage output and all documented failure cases.

Dependencies:
- Task 5.
- Task 13.
- Task 14.

### Milestone 5: Worktree, Verification, And Safety

#### Task 16: Implement worktree lifecycle helpers

---
id: T16
status: done
priority: high
depends_on:
  - T04
  - T15
---

Acceptance criteria:
- Worktrees are created from `origin/main`, not local `main`.
- Branch names and worktree paths follow config.
- Cleanup removes worktrees on normal completion.
- Tests cover concurrent-safe creation assumptions and orphan preservation on failure.

Dependencies:
- Task 4.
- Task 15.

#### Task 17: Implement `watchdog.sh` stall detection

---
id: T17
status: done
priority: medium
depends_on:
  - T06
---

Acceptance criteria:
- Wrapped commands are terminated after configured inactivity timeout.
- Stall exit code is distinct.
- Stall marker data is written for recovery.
- Tests cover output-active commands, silent commands, and pass-through exit behavior.

Dependencies:
- Task 6.

#### Task 18: Implement payload extraction and validation workflow

---
id: T18
status: done
priority: high
depends_on:
  - T03
  - T06
---

Acceptance criteria:
- The last fenced JSON block is extracted from Codex output.
- Malformed or missing payloads are synthesized from ground truth.
- Missing required fields are patched according to PRD rules.
- Tests cover all documented failure modes.

Dependencies:
- Task 3.
- Task 6.

#### Task 19: Implement ground-truth verification helpers

---
id: T19
status: done
priority: high
depends_on:
  - T16
  - T18
---

Acceptance criteria:
- Verification checks commit existence, changed-file lists, remote push status, and configured test/build commands.
- Review is skipped when verification fails.
- Tests use temp repos to verify claimed vs actual commit/file mismatches.
- Misconfigured verification commands fail fast with clear errors.

Dependencies:
- Task 16.
- Task 18.

#### Task 20: Implement startup reconciliation and dispatch eligibility checks

---
id: T20
status: done
priority: high
depends_on:
  - T06
  - T09
  - T11
  - T16
---

Acceptance criteria:
- Reconciliation inspects orphaned state, stale labels, and interrupted runs.
- Eligibility checks enforce acceptance criteria presence, dependency completion, PR uniqueness, and branch conflict gating.
- Tests cover each skip/reset/resume path.
- Expected comments or actions are produced deterministically.

Dependencies:
- Task 6.
- Task 9.
- Task 11.
- Task 16.

#### Task 21: Implement reporting script

---
id: T21
status: done
priority: low
depends_on:
  - T06
  - T04
---

Acceptance criteria:
- `report summary`, `report issue`, and `report cost` operate on completed state files.
- Tests cover aggregation math, token summaries, and empty-state behavior.
- Output is stable enough for human inspection and scripting.
- Cost computation uses config-driven rates if configured.

Dependencies:
- Task 6.
- Task 4.

### Milestone 6: Skills And Agent MVP

#### Task 22: Add issue-queue skill

---
id: T22
status: done
priority: medium
depends_on:
  - T10
---

Acceptance criteria:
- Skill instructions are thin and script-oriented.
- It documents available actions and expected JSON outputs.
- A fixture or golden test verifies the skill references the correct scripts and labels.
- It does not embed business logic duplicated from shell scripts.

Dependencies:
- Task 10.

#### Task 23: Add pr-lifecycle skill

---
id: T23
status: done
priority: medium
depends_on:
  - T12
---

Acceptance criteria:
- Skill covers create, post-review, update-summary, finalize, and actionable comment reads.
- It delegates behavior to `gh-pr-lifecycle.sh`.
- Golden tests verify required invariants are called out.
- It stays aligned with comment marker and PR template conventions.

Dependencies:
- Task 12.

#### Task 24: Add plan-to-issues skill

---
id: T24
status: done
priority: medium
depends_on:
  - T10
  - T04
---

Acceptance criteria:
- Skill reads a local plan, proposes issue slices, flags bad granularity, and creates issues only after confirmation.
- It reuses the issue template and issue-queue script.
- Tests or fixtures cover broad, narrow, and untestable issue slicing examples.
- Output includes dependency graph information.

Dependencies:
- Task 10.
- Task 4.

#### Task 25: Implement `github-orchestrator` agent prompt

---
id: T25
status: done
priority: high
depends_on:
  - T20
  - T22
  - T23
  - T15
---

Acceptance criteria:
- Prompt follows the dispatch loop, decision table, audit-trail rules, and circuit-breaker behavior from the PRD.
- It explicitly avoids source-code editing.
- Golden tests or prompt fixtures cover PASS, FAIL, blocked, dry-run, and budget-exhaustion scenarios.
- It relies on scripts and skills instead of embedding logic.

Dependencies:
- Task 20.
- Task 22.
- Task 23.
- Task 15.

#### Task 26: Implement `orchestrator-github` agent prompt

---
id: T26
status: done
priority: high
depends_on:
  - T18
  - T19
  - T23
---

Acceptance criteria:
- Prompt enforces payload parsing before any review work.
- It applies verification checkpoints and token-budget stop behavior.
- It scopes diff review using verified file lists and direct importers.
- Golden tests or fixtures cover iterate, stuck, verification-failure, and final PASS flows.

Dependencies:
- Task 18.
- Task 19.
- Task 23.

### Milestone 7: End-To-End Queue Execution

#### Task 27: Single-issue happy path integration

---
id: T27
status: done
priority: high
depends_on:
  - T15
  - T16
  - T19
  - T25
  - T26
---

Acceptance criteria:
- In a fake repo plus fake GitHub environment, `agendev run --issue N` creates branch, worktree, draft PR, state file, comments, finalization, and issue label updates.
- The flow uses payload logging and verification.
- The worktree is removed on successful completion.
- This becomes the main regression test.

Dependencies:
- Task 15.
- Task 16.
- Task 19.
- Task 25.
- Task 26.

#### Task 28: Failure and recovery integration suite

---
id: T28
status: done
priority: high
depends_on:
  - T17
  - T18
  - T19
  - T20
  - T27
---

Acceptance criteria:
- Covers no-commit payload, failed tests/build, missing push, malformed payload, stalled process, and agent crash.
- Covers startup reconciliation after interruption.
- Covers escalation to `needs-human-review`.
- Expected PR and issue comments are asserted.

Dependencies:
- Task 17.
- Task 18.
- Task 19.
- Task 20.
- Task 27.

Implementation notes:
- `test/run_integration.bats` now covers no-commit, failing verification commands, missing push, malformed payload reconstruction, watchdog stalls, crash preservation, and startup reconciliation before dispatch.

#### Task 29: Queue loop and circuit breaker integration

---
id: T29
status: todo
priority: medium
depends_on:
  - T09
  - T20
  - T25
  - T27
---

Acceptance criteria:
- Multi-issue execution respects dependencies and blocked issues.
- Consecutive failure limit halts the queue.
- Dry-run reports queue status without mutation.
- Final state and comments reflect circuit-breaker behavior.

Dependencies:
- Task 9.
- Task 20.
- Task 25.
- Task 27.

#### Task 30: Mention polling and authorization integration

---
id: T30
status: todo
priority: medium
depends_on:
  - T07
  - T12
  - T25
---

Acceptance criteria:
- Authorized mentions are processed once.
- Unauthorized mentions are denied or ignored per config.
- PR and issue mention contexts are distinguished.
- Processed comment IDs prevent duplicate actions.

Dependencies:
- Task 7.
- Task 12.
- Task 25.

### Milestone 8: Maintenance Review

#### Task 31: Implement `maintenance-reviewer` agent and partition derivation

---
id: T31
status: todo
priority: low
depends_on:
  - T15
  - T23
---

Acceptance criteria:
- Partitions are derived from `.gitignore`, `tsconfig` exclude/include, and project references as specified.
- Tracking issue creation and partition progress reporting are implemented.
- Tests cover single-project and referenced-project layouts.
- The agent remains read-only.

Dependencies:
- Task 15.
- Task 23.

#### Task 32: Implement maintenance triage loop

---
id: T32
status: todo
priority: low
depends_on:
  - T12
  - T31
---

Acceptance criteria:
- Approved findings create new `agendev:ready` issues.
- Denied and modified findings update tracking issue state correctly.
- Tests cover grouping, deduplication with in-flight PRs, and recurring-pattern summary behavior.
- Mention authorization rules are reused.

Dependencies:
- Task 12.
- Task 31.

#### Task 33: Maintenance end-to-end integration

---
id: T33
status: todo
priority: low
depends_on:
  - T31
  - T32
---

Acceptance criteria:
- A full maintenance run creates a tracking issue, posts findings, processes triage mentions, and updates the final summary.
- No source files or PRs are modified by the maintenance flow.
- State allows resume after interruption.
- Findings can be turned into queue issues only after approval.

Dependencies:
- Task 31.
- Task 32.

## Live Smoke Tests

### Task 34: Real GitHub sandbox smoke suite

---
id: T34
status: todo
priority: low
depends_on:
  - T14
  - T15
  - T27
---

Acceptance criteria:
- Runs only with explicit credentials and sandbox repo configuration.
- Verifies GitHub App auth, label creation, issue creation, PR creation, comment attribution as `agendev[bot]`, and mention permission checks.
- Failures are isolated from normal local test runs.
- Scope stays minimal and non-destructive.

Dependencies:
- Task 14.
- Task 15.
- Task 27.

## Suggested MVP Cut

Build to this point before adding mentions, reporting, or maintenance review:

1. Task 1: Bootstrap shell test harness
2. Task 2: Add fixture set for PRD contracts
3. Task 4: Add centralized config and template files
4. Task 6: Implement `state.sh`
5. Task 8: Implement `gh-issue-queue.sh` list and metadata parsing
6. Task 9: Implement `gh-issue-queue.sh` next with dependency resolution
7. Task 10: Implement `gh-issue-queue.sh` set-status and create
8. Task 11: Implement `gh-pr-lifecycle.sh` create/comment/update-summary/finalize
9. Task 15: Implement `bin/agendev` subcommand routing
10. Task 16: Implement worktree lifecycle helpers
11. Task 18: Implement payload extraction and validation workflow
12. Task 19: Implement ground-truth verification helpers
13. Task 20: Implement startup reconciliation and dispatch eligibility checks
14. Task 22: Add issue-queue skill
15. Task 23: Add pr-lifecycle skill
16. Task 25: Implement `github-orchestrator` agent prompt
17. Task 26: Implement `orchestrator-github` agent prompt
18. Task 27: Single-issue happy path integration

## Implementation Notes

- The highest-value early tests are the payload contract tests, state transition tests, and the single-issue happy path integration test.
- Keep prompt files thin. If a rule appears in more than one prompt, it likely belongs in a script.
- Do not start maintenance review until the single-issue flow is proven and stable.
- Treat fixture quality as part of the product. Most future regressions should be reproducible from saved fixtures and temp repos.
