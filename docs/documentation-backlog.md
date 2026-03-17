# Documentation Backlog

This backlog captures the documentation work needed to make the current `agendev` runtime understandable, operable, and maintainable.

The existing documentation set is strong on product intent and implementation history, but thin on current architecture, operator workflows, runtime contracts, and recovery procedures. The tasks below focus on closing that gap.

## Task Metadata

Each task includes a small YAML block for tracking.

- `id`: Stable documentation task identifier.
- `state`: Always starts as `pending`.
- `priority`: Suggested execution priority (`p0`, `p1`, `p2`).
- `type`: Whether the work adds a new document or updates an existing one.
- `depends_on`: Task IDs that should land first when sequencing matters.

## Working Principles

- Look at current runtime behavior, not aspirational design when documenting how the system works.
- Treat scripts and JSON contracts as the source of truth for executable behavior.
- Keep PRD and backlog history documents separate from operator and reference documentation.
- Write for two audiences: operators using the tooling day to day, and engineers extending the runtime safely.
- Document failure, recovery, and edge cases, not just happy-path flows.

## Milestone 1: Entry Points And Core Orientation

### Task 1: Add repository README

---
id: D01
state: done
priority: p0
type: add
depends_on: []
---

Description:
- Create a top-level `README.md` that explains what `agendev` is, what problem it solves, and how the repo is organized.
- This should become the primary landing page for new contributors and operators.

Guidance:
- Include prerequisites such as `gh`, `jq`, `git`, `bats`, `shellcheck`, and the Claude CLI.
- Include a short install and setup path centered on `agendev init`.
- Summarize the main commands: `plan`, `run`, `report`, and `maintenance`.
- Link to deeper docs rather than duplicating all details in the README.
- Call out that the repo preserves the target working tree and uses sibling worktrees for execution.

Suggested sections:
- What `agendev` is
- How it works at a glance
- Prerequisites
- Quick start
- Command overview
- Documentation map
- Development and testing pointers

Acceptance guidance:
- A new reader should be able to understand the repo purpose in under five minutes.
- The README should link to the major architecture, reference, and operations docs created in this backlog.

### Task 2: Add quickstart operator guide

---
id: D02
state: done
priority: p0
type: add
depends_on:
  - D01
---

Description:
- Add `docs/operations/quickstart.md` as a day-one guide for someone trying to use `agendev` against a target repo.
- This document should cover the minimum viable operator workflow from setup through a successful run.

Guidance:
- Show the sequence `agendev init`, `agendev plan <file>`, `agendev run --issue N`, `agendev run`, `agendev report summary`, and `agendev maintenance`.
- Distinguish clearly between one-time setup and recurring operation.
- Include examples of what artifacts get created in GitHub and under `.agendev/`.
- Keep the guide procedural and operator-focused.

Suggested sections:
- Before you start
- Initial setup
- Creating queue issues from a plan
- Running a single issue
- Running the queue
- Inspecting outputs and reports
- Running maintenance review
- Where to go next

Acceptance guidance:
- A user with repo access and required tools should be able to follow the document end to end without reading source code first.

## Milestone 2: Architecture And System Flows

### Task 3: Add architecture overview

---
id: D03
state: pending
priority: p0
type: add
depends_on:
  - D01
---

Description:
- Add `docs/architecture/overview.md` describing the current runtime architecture as implemented.
- This should supersede the PRD as the primary architecture reference for the shipped system.

Guidance:
- Include C1, C2, and C3-style views or equivalent layered diagrams.
- Cover the roles of the human operator, target repository, Claude agents, skills, shell scripts, GitHub, and local state files.
- Explain the split between deterministic script logic and agent prompt logic.
- Clarify where the system stores recovery breadcrumbs versus where the audit trail lives.

Suggested sections:
- System context
- Container view
- Component view
- Boundaries and responsibilities
- Source-of-truth rules
- Architectural constraints and tradeoffs

Acceptance guidance:
- A maintainer should be able to identify which layer owns queue logic, PR lifecycle, auth, verification, maintenance review, and state handling.

### Task 4: Add execution and maintenance flow diagrams

---
id: D04
state: pending
priority: p1
type: add
depends_on:
  - D03
---

Description:
- Add `docs/architecture/flows.md` documenting the major runtime sequences and decision points.
- This should focus on dynamic behavior rather than static structure.

Guidance:
- Include sequence diagrams or step tables for:
- `agendev plan`
- `agendev run` happy path
- failure and escalation path
- startup reconciliation and resume behavior
- mention polling and authorization
- maintenance review and triage
- Show the handoffs between CLI, scripts, agents, GitHub, and local state.
- Make decision points explicit, especially around verification, needs-review escalation, and circuit breaker behavior.

Acceptance guidance:
- A reader should be able to trace how one issue moves from ready to done or needs-review without reading the scripts.

## Milestone 3: Command, Config, And Contract Reference

### Task 5: Add CLI reference

---
id: D05
state: pending
priority: p0
type: add
depends_on:
  - D01
---

Description:
- Add `docs/reference/cli.md` documenting the public CLI in [`bin/agendev`](/Users/Saruman/Projects/agendev/bin/agendev).
- This should be the main command reference for users of the tool.

Guidance:
- Document `init`, `plan`, `run`, `report`, and `maintenance`.
- Include flags, argument shapes, examples, expected side effects, and common errors.
- Explain when commands mutate GitHub state and when they do not.
- Include `run --issue N` versus queue mode and `run --dry-run`.

Suggested sections:
- Command synopsis
- Command reference
- Common examples
- Exit and failure behavior
- Related docs

Acceptance guidance:
- A user should not need to inspect the shell entrypoint to know how to invoke the tool correctly.

### Task 6: Add configuration and authentication reference

---
id: D06
state: pending
priority: p0
type: add
depends_on:
  - D01
---

Description:
- Add `docs/reference/config-auth.md` documenting configuration, environment variables, identity files, and GitHub App authentication behavior.
- This document should cover both setup and debugging of auth/config problems.

Guidance:
- Document the keys in [`config/agendev.json`](/Users/Saruman/Projects/agendev/config/agendev.json) and what consumes them.
- Document `.agendev/identity.json`, `GH_TOKEN`, `AGENDEV_APP_KEY`, `AGENDEV_FORCE_REFRESH_TOKEN`, `AGENDEV_CONFIG`, `AGENDEV_REPO`, `TARGET_ROOT`, and related runtime variables.
- Explain how `gh-auth.sh` prefers existing `GH_TOKEN` and when it mints a fresh installation token.
- Include minimum permission behavior and denial handling for mentions.

Acceptance guidance:
- A user encountering auth or config resolution failures should know where values come from and how to correct them.

### Task 7: Add script contract reference

---
id: D07
state: pending
priority: p1
type: add
depends_on:
  - D05
  - D06
---

Description:
- Add `docs/reference/script-contracts.md` as a contract-level reference for the public shell scripts in [`scripts/`](/Users/Saruman/Projects/agendev/scripts).
- This is primarily for maintainers and advanced operators.

Guidance:
- Cover the public entrypoints, not every internal helper.
- For each script, document purpose, arguments, JSON output, side effects, and primary callers.
- At minimum include:
- `gh-issue-queue.sh`
- `gh-pr-lifecycle.sh`
- `state.sh`
- `verify.sh`
- `worktree.sh`
- `dispatch-safety.sh`
- `report.sh`
- `mentions.sh`
- `maintenance.sh`
- `watchdog.sh`
- Note which outputs are machine-consumed contracts and therefore stability-sensitive.

Acceptance guidance:
- A maintainer should be able to add tests or extend behavior without re-deriving command contracts from the shell source.

### Task 8: Add target repository contract reference

---
id: D08
state: pending
priority: p1
type: add
depends_on:
  - D05
  - D06
---

Description:
- Add `docs/reference/target-repo-contract.md` documenting the assumptions `agendev` makes about the repository it operates on.
- This is the contract between the runtime repo and downstream project repos.

Guidance:
- Cover Git remote requirements, GitHub host assumptions, base branch assumptions, and sibling worktree behavior.
- Document the issue metadata block shape, PR template markers, and the role of `AGENTS.md`.
- Explain verification command expectations and default package.json bootstrapping done by `agendev init`.
- Clarify what is safe for target repos to customize and what is part of the runtime contract.

Acceptance guidance:
- A downstream project maintainer should be able to tell whether their repo is compatible and what setup is required.

### Task 9: Add state and audit model reference

---
id: D09
state: pending
priority: p1
type: add
depends_on:
  - D07
---

Description:
- Add `docs/reference/state-model.md` documenting the structure and semantics of the local state files and audit comments.
- This should explain resumability and observability clearly.

Guidance:
- Cover `.agendev/state/<issue>.json`, `.agendev/state/maintenance.json`, and `processed-mentions.json`.
- Document the phase model and allowed transitions.
- Explain payload reconstruction, verification outputs, and when state is terminal.
- Explain GitHub audit markers such as `<!-- agendev:event -->` and `<!-- agendev:payload:* -->`.
- Make the distinction explicit: local state is for recovery breadcrumbs, GitHub comments/issues are the operational audit trail.

Acceptance guidance:
- A maintainer should be able to diagnose a stuck or interrupted run by reading the state and audit docs alone.

## Milestone 4: Operator Playbooks

### Task 10: Add queue operations guide

---
id: D10
state: pending
priority: p1
type: add
depends_on:
  - D02
  - D04
---

Description:
- Add `docs/operations/queue-operations.md` describing normal day-to-day operation of the issue queue.
- This should be a practical playbook rather than a command reference.

Guidance:
- Explain ready, in-progress, done, blocked, needs-human-review, and maintenance-review labels.
- Cover how issue selection works, including dependency ordering and blocked reasons.
- Explain dry-run usage, single-issue mode, queue mode, and what finalization outcomes mean.
- Document how and when the circuit breaker halts queue processing.
- Include how to inspect PR comments, issue comments, and reports after a run.

Acceptance guidance:
- An operator should be able to monitor and manage the queue confidently without reading code or tests.

### Task 11: Add recovery and troubleshooting guide

---
id: D11
state: pending
priority: p1
type: add
depends_on:
  - D09
  - D10
---

Description:
- Add `docs/operations/recovery.md` for failures, interruptions, and operational debugging.
- This should cover both automated recovery behavior and manual operator intervention.

Guidance:
- Include interrupted runs, stale labels, existing open PR conflicts, missing pushes, failed verification, malformed payload reconstruction, stalled dev commands, corrupted state files, and auth failures.
- Explain what `dispatch-safety.sh reconcile` does on startup.
- Document when the system resumes automatically and when it escalates to needs-review.
- Include concrete operator checks such as state files, worktree paths, and GitHub comments to inspect.

Acceptance guidance:
- A user encountering a broken run should be able to determine whether to resume, clean up, or escalate to human review.

### Task 12: Add maintenance review operations guide

---
id: D12
state: pending
priority: p1
type: add
depends_on:
  - D04
  - D09
---

Description:
- Add `docs/operations/maintenance-review.md` documenting how maintenance review works in practice.
- This should cover the workflow from launch through triage completion.

Guidance:
- Explain partition derivation from `.gitignore` and `tsconfig.json`.
- Document tracking issue creation, partition progress comments, findings format, recurring patterns, and final summary behavior.
- Explain the triage commands that approve, deny, or modify findings.
- Cover authorization reuse, processed mention handling, and how approved findings become queue issues.
- Emphasize that maintenance review is read-only until a human explicitly approves filing.

Acceptance guidance:
- An operator should be able to run and triage maintenance review safely, including understanding what the agent will and will not do.

## Milestone 5: Contributor And Maintainer Documentation

### Task 13: Add contributor testing guide

---
id: D13
state: pending
priority: p2
type: add
depends_on:
  - D07
---

Description:
- Add `docs/contributing/testing.md` covering how to work on the runtime and validate changes safely.
- This should replace ad hoc knowledge currently spread between tests and development notes.

Guidance:
- Cover repo layout, Bats suite organization, fake-`gh` fixtures, helper utilities, and live smoke boundaries.
- Include expectations for test-first changes when behavior is deterministic.
- Include `shellcheck -x` expectations and guidance for focused versus broader regression runs.
- Explain why live smoke tests remain opt-in and credential-gated.

Acceptance guidance:
- A new contributor should be able to add or update shell behavior and choose the right test layer without guessing.

### Task 14: Add agent and skill authoring guide

---
id: D14
state: pending
priority: p2
type: add
depends_on:
  - D03
  - D07
---

Description:
- Add `docs/contributing/agent-and-skill-guidelines.md` documenting how prompts, skills, and scripts should interact in this repo.
- This should codify the design discipline already implied by the runtime.

Guidance:
- Explain that deterministic behavior belongs in scripts and contracts, not prompt text.
- Document how agents should use scripts and skills rather than direct `gh` calls.
- Cover audit marker expectations, JSON payload discipline, and thin-prompt rules.
- Include examples of good versus bad boundaries, especially around queue logic, PR lifecycle, and maintenance review.

Acceptance guidance:
- A maintainer editing prompts or skills should know what logic must remain out of prompt space.

### Task 15: Add architecture decision records

---
id: D15
state: pending
priority: p2
type: add
depends_on:
  - D03
  - D09
---

Description:
- Add a small `docs/adr/` set capturing the core design decisions that define the repo.
- These should be short and durable, not long-form architecture docs.

Guidance:
- Initial ADR set should cover:
- shell scripts and JSON contracts as the deterministic boundary
- sibling worktrees instead of mutating the target working tree
- GitHub issues and PR comments as the primary audit/control surface
- local state files as resumability breadcrumbs rather than the audit trail
- Each ADR should include decision, context, consequences, and rejected alternatives.

Acceptance guidance:
- Future maintainers should be able to understand the key non-obvious design decisions without reverse-engineering old PRs or backlog notes.

## Milestone 6: Existing Documentation Updates

### Task 16: Expand or split development guidelines into durable contributor docs

---
id: D16
state: pending
priority: p2
type: update
depends_on:
  - D13
  - D14
---

Description:
- Update `docs/development-guidelines.md` so it complements the new contributor docs instead of carrying too much mixed-purpose material.

Guidance:
- Move durable contributor guidance into the new contributing documents where appropriate.
- Keep the lessons-learned content only if it still adds context that does not fit better elsewhere.
- Add links to the new testing and agent/skill guidance docs.

Acceptance guidance:
- The remaining document should have a clear purpose and should not duplicate the new contributor documentation unnecessarily.

### Task 17: Expand live smoke documentation

---
id: D17
state: pending
priority: p2
type: update
depends_on:
  - D02
  - D13
---

Description:
- Update `docs/live-smoke.md` to better document setup, scope, side effects, and failure handling for the sandbox smoke suite.

Guidance:
- Expand required environment explanation and ownership expectations for sandbox credentials.
- Document cleanup behavior and what resources the smoke suite creates.
- Clarify the difference between preflight, deterministic guard tests, and the actual sandbox run.
- Add troubleshooting guidance for auth failures, permission check failures, and cleanup issues.

Acceptance guidance:
- A maintainer should be able to run the smoke suite intentionally and understand the risk envelope before doing so.

## Suggested Execution Order

1. D01: Add repository README
2. D05: Add CLI reference
3. D06: Add configuration and authentication reference
4. D02: Add quickstart operator guide
5. D03: Add architecture overview
6. D04: Add execution and maintenance flow diagrams
7. D07: Add script contract reference
8. D08: Add target repository contract reference
9. D09: Add state and audit model reference
10. D10: Add queue operations guide
11. D11: Add recovery and troubleshooting guide
12. D12: Add maintenance review operations guide
13. D13: Add contributor testing guide
14. D14: Add agent and skill authoring guide
15. D16: Expand or split development guidelines into durable contributor docs
16. D17: Expand live smoke documentation
17. D15: Add architecture decision records

## Notes

- The highest-value early additions are the README, CLI/config reference, quickstart, and architecture docs.
- The most important operational gap is recovery and queue-management guidance for real-world failures.
- The most important engineering gap is a clear contract reference for scripts, state, and target-repo expectations.
- `docs/prd.md` and `docs/backlog.md` are treated as frozen historical artifacts and are intentionally excluded from this backlog.
