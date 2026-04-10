# Architecture Overview

This document describes the current `runoq` runtime as implemented in the repository today. It is the primary architecture reference for the Go runtime with shell entrypoints.

## System Context

`runoq` sits between a human operator, a target GitHub repository, and Claude-based agents. The operator invokes the CLI from inside the target repository. The runtime resolves repository context, authenticates to GitHub, manages queue and PR state through the Go runtime (with shell entrypoints for auth, setup, and issue-runner), and delegates bounded reasoning work to agents and skills.

```mermaid
flowchart LR
  operator[Human operator]
  runtime[runoq runtime repo]
  target[Target repository checkout]
  worktrees[Sibling execution worktrees]
  github[GitHub issues, PRs, comments, labels]
  claude[Claude CLI agents and skills]
  state[.runoq/state/*.json]

  operator -->|runs runoq CLI| runtime
  runtime -->|resolves repo context| target
  runtime -->|creates/removes| worktrees
  runtime -->|reads/writes audit surface| github
  runtime -->|dispatches bounded tasks| claude
  runtime -->|writes recovery breadcrumbs| state
  worktrees -->|push branches to| github
  github -->|queue items, mentions, PR feedback| runtime
```

### External actors and systems

- Human operator: decides when to initialize a repo, confirm plan slicing, run the queue, inspect output, and triage maintenance findings.
- GitHub repository: hosts issues, PRs, labels, comments, collaborator permissions, and the long-lived operational audit trail.
- Claude CLI: runs the `milestone-decomposer`, `task-decomposer`, `plan-reviewer-*`, `milestone-reviewer`, `mention-responder`, `diff-reviewer`, and `maintenance-reviewer` agents.
- Target repository: provides the source tree, git remote, package scripts, `.gitignore`, and optional `tsconfig.json`.

## Subsystem View

At runtime the system is split into a small set of subsystems with strict roles.

```mermaid
flowchart TB
  subgraph operator_env[Operator workstation]
    cli[bin/runoq CLI]
    scripts[shell scripts and JSON config]
    local_state[.runoq state files]
    target_repo[target repo main checkout]
    worktree_repo[sibling worktree checkout]
  end

  subgraph agent_env[Claude runtime]
    agents[Agents and skills]
  end

  github[GitHub API and repo state]

  cli --> scripts
  scripts --> local_state
  scripts --> target_repo
  scripts --> worktree_repo
  scripts --> github
  scripts --> agents
  agents --> scripts
  worktree_repo --> github
  github --> scripts
```

### Subsystems

| Subsystem | Purpose | Primary implementation |
| --- | --- | --- |
| CLI entrypoint | Thin command router that resolves repo context and auth, then dispatches to scripts or Claude | `bin/runoq` |
| Go runtime | Owns queue logic, PR lifecycle, verification, maintenance operations, state, and recovery via Go packages dispatched through shell entrypoints | `internal/runtime*` packages, shell entrypoints in `scripts/*.sh`, `config/runoq.json` |
| Agent layer | Performs plan slicing, code review (diff-reviewer), mention response, and maintenance review around script contracts | `.claude/agents/*`, `.claude/skills/*` |
| GitHub control surface | Stores queue issues, PRs, labels, review comments, permissions, and audit comments | remote GitHub repo |
| Local breadcrumb state | Stores resumability state and processed-mention tracking | `.runoq/state/*.json` |
| Execution workspace | Holds the target repo main checkout plus sibling worktrees created per issue | target repo checkout and worktree siblings |

## Component View

The Go runtime is the architectural center of gravity. Shell entrypoints dispatch to Go packages in `internal/runtime*`. Prompted agents exist around the runtime, not inside it.

```mermaid
flowchart LR
  cli[bin/runoq]
  common[common.sh and config]
  auth[gh-auth.sh]
  queue[gh-issue-queue.sh]
  safety[dispatch-safety.sh]
  worktree[worktree.sh]
  pr[gh-pr-lifecycle.sh]
  run[run.sh]
  orchestrator[orchestrator.sh]
  issuerunner[internal/issuerunner helper]
  verify[verify.sh]
  state[state.sh]
  maint[maintenance.sh]
  mentions[mentions.sh]
  watchdog[watchdog.sh]
  agents[Claude agents and skills]
  github[GitHub]
  fs[Local repo, worktrees, state]

  cli --> common
  cli --> auth
  cli --> run
  cli --> maint
  cli --> agents
  run --> orchestrator
  orchestrator --> queue
  orchestrator --> safety
  orchestrator --> worktree
  orchestrator --> pr
  orchestrator --> issuerunner
  orchestrator --> state
  orchestrator --> agents
  orchestrator --> verify
  issuerunner --> state
  run --> queue
  run --> safety
  maint --> state
  maint --> mentions
  maint --> queue
  maint --> pr
  agents --> queue
  agents --> pr
  queue --> github
  safety --> github
  pr --> github
  mentions --> github
  auth --> github
  worktree --> fs
  state --> fs
  verify --> fs
```

### Component responsibilities

| Component | Owns | Does not own |
| --- | --- | --- |
| `bin/runoq` | Public CLI shape, repo context export, auth bootstrap, command routing | Queue logic, verification, PR mutation details |
| `scripts/lib/common.sh` (`runoq::gh()`) | Global bot auth: auto-mints app installation token on first `gh` call, configures bot identity for worktrees | Queue state, issue or PR decisions |
| `scripts/gh-auth.sh` | Explicit token export for CLI bootstrap, `GH_TOKEN` reuse | Queue state, issue or PR decisions |
| `scripts/gh-issue-queue.sh` | Queue listing, metadata parsing, dependency ordering, label transitions, issue creation, epic/task sub-issue linking via GitHub sub-issues API, epic-status queries | PR lifecycle, verification, reconciliation |
| `scripts/dispatch-safety.sh` | Startup reconciliation, stale-label cleanup, eligibility checks, interrupted-run handling | PR creation, verification checks |
| `scripts/worktree.sh` | Branch naming, sibling worktree creation/removal | Queue selection, GitHub state |
| `scripts/gh-pr-lifecycle.sh` | Draft PR creation, audit comments, summary mutation, finalize actions, mention polling, permission checks | Queue ordering, local state transitions |
| `scripts/state.sh` | Atomic state writes, phase transition validation, payload extraction/normalization, processed-mention tracking | GitHub audit comments, verification commands |
| `scripts/verify.sh` | Ground-truth diff checks, branch push checks, test/build execution, payload consistency checks, criteria tamper check | Final PR or issue decisions |
| `scripts/run.sh` | End-to-end issue execution flow, queue loop, circuit breaker, audit comment sequencing | Phase dispatch, review reasoning |
| `scripts/orchestrator.sh` | Phase dispatch state machine (INIT, DEVELOP, VERIFY, REVIEW, DECIDE, FINALIZE), agent spawning, decision table | Implementation, review reasoning |
| `internal/issuerunner` (via orchestrator) | Execute one bounded codex develop round, capture artifacts, normalize payloads, track token budget for the round | Verification routing, PR lifecycle, queue decisions |
| `scripts/maintenance.sh` | Partition derivation, maintenance tracking issue lifecycle, findings storage, triage-to-issue filing | Code modification |
| `scripts/mentions.sh` | Mention polling, permission gating, deny comments, deduplication via state | Queue dispatch decisions |
| Claude skills and agents | Planning decomposition and review (`milestone-decomposer`, `task-decomposer`, `plan-reviewer-*`, `milestone-reviewer`), code review (diff-reviewer, opus), mention response (mention-responder, sonnet), maintenance review reasoning (maintenance-reviewer, opus) | Deterministic GitHub or filesystem contracts already defined in scripts |

## Boundaries And Responsibilities

### Deterministic layer vs prompt layer

The core architectural rule is that durable behavior belongs in Go packages and JSON contracts, not in prompts.

- The Go runtime owns queue ordering, label transitions, worktree paths, PR creation, verification gates, state transitions, mention authorization, maintenance triage side effects, phase dispatch (orchestrator), and the develop-round helper logic.
- The orchestrator and issuerunner helper are deterministic dispatch, not agents. Their work is state machine transitions and payload normalization, not reasoning.
- Agents and skills are intentionally thin. They consume typed inputs, make bounded decisions, and are expected to call repository scripts instead of issuing ad hoc `gh` commands. Current agents: diff-reviewer (opus, code review), mention-responder (sonnet, PR question answering), maintenance-reviewer (opus, code health review).

### Audit trail vs recovery breadcrumbs

`runoq` uses two different persistence models on purpose:

- GitHub issues, PRs, and comments are the operational audit trail. Audit markers such as `<!-- runoq:bot -->` and `<!-- runoq:payload:* -->` make those comments machine-recognizable and human-readable.
- `.runoq/state/*.json` is a resumability mechanism. State files track the latest local phase, worktree, PR number, timestamps, payload normalization output, and mention deduplication, but they are not the long-term record of operator actions.

### Working tree safety

The target repository main checkout is preserved. Execution work happens in sibling worktrees named from the issue number and title, created from `origin/main` by `scripts/worktree.sh`. Successful low-complexity runs remove their worktrees after finalization.

## Source-Of-Truth Rules

- GitHub labels and issue metadata define queue eligibility and dependency ordering.
- GitHub PR and issue comments are the durable record of dispatch, verification, escalation, and maintenance activity.
- `config/runoq.json` defines labels, auth policy, reviewer defaults, branch/worktree prefixes, verification commands, and queue safety limits.
- `.runoq/identity.json` and `GH_TOKEN` determine which GitHub identity is used.
- `.runoq/state/*.json` and `processed-mentions.json` exist to recover and reconcile local execution, not to replace GitHub history.
- The target repository defines test/build commands indirectly through `config/runoq.json` and supplies the actual code and git remotes the runtime acts on.

## Architectural Constraints And Tradeoffs

- Go runtime with shell entrypoints: easier to test with Go unit tests, but shell entrypoints must stay narrow and stable.
- GitHub as control plane: gives operators a visible audit trail, but couples runtime behavior to GitHub issue/PR semantics and permissions.
- Sibling worktrees: protect the target checkout, but require extra cleanup and branch reconciliation logic.
- Local breadcrumb state: enables resume and stale-run detection, but must never be confused with the system audit trail.
- Thin prompts: reduce hidden logic, but require more up-front script design whenever a behavior needs to be stable or testable.

## Ownership Summary

Use this table when deciding where a change belongs:

| Concern | Owning layer |
| --- | --- |
| Plan decomposition and issue creation | `tick.sh` and `plan-dispatch.sh` for iterative planning, `plan.sh` for deprecated one-shot compatibility, `milestone-decomposer` and `task-decomposer` for decomposition, `plan-reviewer-*` and `milestone-reviewer` for gated review, `gh-issue-queue.sh` for milestone/task materialization |
| Queue logic and dependency ordering | `gh-issue-queue.sh`, `dispatch-safety.sh`, `run.sh` |
| Phase dispatch and decision table | `orchestrator.sh` |
| Dev-round execution and codex loop | `internal/issuerunner` via orchestrator |
| Acceptance criteria protection | `verify.sh` and integrate verification honor any legacy `criteria_commit` carried in issue state |
| PR lifecycle | `gh-pr-lifecycle.sh`, `orchestrator.sh` |
| Auth and token minting | `runoq::gh()` in `common.sh` (auto-mints app installation token globally on first call), `gh-auth.sh`, `.runoq/identity.json`, `GH_TOKEN` |
| Verification and criteria tamper check | `verify.sh` plus configured test/build commands |
| Epic integration verification | `verify.sh integrate` |
| Mention triage and response | `orchestrator.sh` (haiku classification), `mention-responder` agent (sonnet) |
| Maintenance review and triage | `maintenance.sh`, `mentions.sh`, `maintenance-reviewer` |
| State handling and payload reconstruction | `state.sh` |
