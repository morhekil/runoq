# Architecture Overview

This document describes the current `runoq` runtime as implemented in the repository today. It is the primary architecture reference for the shipped shell/runtime layer.

## System Context

`runoq` sits between a human operator, a target GitHub repository, and Claude-based agents. The operator invokes the CLI from inside the target repository. The runtime resolves repository context, authenticates to GitHub, manages queue and PR state through deterministic scripts, and delegates bounded reasoning work to agents and skills.

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
- Claude CLI: runs the `plan-decomposer`, `bar-setter`, `mention-responder`, `diff-reviewer`, and `maintenance-reviewer` agents.
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
| Deterministic shell runtime | Owns queue logic, PR lifecycle, auth, verification, maintenance operations, and recovery | `scripts/*.sh`, `config/runoq.json` |
| Agent layer | Performs plan slicing, acceptance criteria authoring (bar-setter), code review (diff-reviewer), mention response, and maintenance review around script contracts | `.claude/agents/*`, `.claude/skills/*` |
| GitHub control surface | Stores queue issues, PRs, labels, review comments, permissions, and audit comments | remote GitHub repo |
| Local breadcrumb state | Stores resumability state and processed-mention tracking | `.runoq/state/*.json` |
| Execution workspace | Holds the target repo main checkout plus sibling worktrees created per issue | target repo checkout and worktree siblings |

## Component View

The deterministic shell runtime is the architectural center of gravity. Prompted agents exist around it, not inside it.

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
  issuerunner[issue-runner.sh]
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
  issuerunner --> verify
  issuerunner --> state
  issuerunner --> watchdog
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
| `scripts/verify.sh` | Ground-truth diff checks, branch push checks, test/build execution, payload consistency checks, criteria tamper check, epic integration verification | Final PR or issue decisions |
| `scripts/run.sh` | End-to-end issue execution flow, queue loop, circuit breaker, audit comment sequencing | Phase dispatch, criteria authoring, review reasoning |
| `scripts/orchestrator.sh` | Phase dispatch state machine (INIT, CRITERIA, DEVELOP, REVIEW, DECIDE, FINALIZE, INTEGRATE), mention triage via haiku classification, agent spawning, decision table | Implementation, review reasoning, criteria authoring |
| `scripts/issue-runner.sh` | Drive codex rounds, track token budget, call verify.sh, package payloads, pass criteria_commit to verification | Phase dispatch, PR lifecycle, queue decisions |
| `scripts/maintenance.sh` | Partition derivation, maintenance tracking issue lifecycle, findings storage, triage-to-issue filing | Code modification |
| `scripts/mentions.sh` | Mention polling, permission gating, deny comments, deduplication via state | Queue dispatch decisions |
| Claude skills and agents | Plan decomposition (plan-decomposer agent, produces epic/task hierarchy with complexity rationale), acceptance criteria authoring (bar-setter, opus), code review (diff-reviewer, opus), mention response (mention-responder, sonnet), maintenance review reasoning (maintenance-reviewer, opus) | Deterministic GitHub or filesystem contracts already defined in scripts |

## Boundaries And Responsibilities

### Deterministic layer vs prompt layer

The core architectural rule is that durable behavior belongs in shell scripts and JSON contracts, not in prompts.

- Scripts own queue ordering, label transitions, worktree paths, PR creation, verification gates, auth behavior, state transitions, mention authorization, maintenance triage side effects, phase dispatch (orchestrator.sh), and dev-round execution (issue-runner.sh).
- The orchestrator and issue-runner are shell scripts, not agents. Their work is deterministic dispatch and state machine transitions, not reasoning.
- Agents and skills are intentionally thin. They consume typed inputs, make bounded decisions, and are expected to call repository scripts instead of issuing ad hoc `gh` commands. Current agents: bar-setter (opus, criteria authoring), diff-reviewer (opus, code review), mention-responder (sonnet, PR question answering), maintenance-reviewer (opus, code health review).
- Mention triage uses a haiku structured-output call for classification (question, change-request, approval, irrelevant), not a full agent.

### Audit trail vs recovery breadcrumbs

`runoq` uses two different persistence models on purpose:

- GitHub issues, PRs, and comments are the operational audit trail. Audit markers such as `<!-- runoq:event -->` and `<!-- runoq:payload:* -->` make those comments machine-recognizable and human-readable.
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

- Shell-first runtime: easier to test and recover, but shell contracts must stay narrow and stable.
- GitHub as control plane: gives operators a visible audit trail, but couples runtime behavior to GitHub issue/PR semantics and permissions.
- Sibling worktrees: protect the target checkout, but require extra cleanup and branch reconciliation logic.
- Local breadcrumb state: enables resume and stale-run detection, but must never be confused with the system audit trail.
- Thin prompts: reduce hidden logic, but require more up-front script design whenever a behavior needs to be stable or testable.

## Ownership Summary

Use this table when deciding where a change belongs:

| Concern | Owning layer |
| --- | --- |
| Plan decomposition and issue creation | `plan.sh` (orchestrates decomposition and deterministic issue creation), `plan-decomposer` agent (produces epic/task hierarchy), `gh-issue-queue.sh` (creates issues and sub-issue links) |
| Queue logic and dependency ordering | `gh-issue-queue.sh`, `dispatch-safety.sh`, `run.sh` |
| Phase dispatch and decision table | `orchestrator.sh` |
| Dev-round execution and codex loop | `issue-runner.sh` |
| Acceptance criteria authoring | `bar-setter` agent (opus), invoked by `orchestrator.sh` during CRITERIA phase |
| PR lifecycle | `gh-pr-lifecycle.sh`, `orchestrator.sh` |
| Auth and token minting | `runoq::gh()` in `common.sh` (auto-mints app installation token globally on first call), `gh-auth.sh`, `.runoq/identity.json`, `GH_TOKEN` |
| Verification and criteria tamper check | `verify.sh` plus configured test/build commands |
| Epic integration verification | `verify.sh integrate` |
| Mention triage and response | `orchestrator.sh` (haiku classification), `mention-responder` agent (sonnet) |
| Maintenance review and triage | `maintenance.sh`, `mentions.sh`, `maintenance-reviewer` |
| State handling and payload reconstruction | `state.sh` |
