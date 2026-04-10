# Operator Workflow

This guide walks through the day-to-day operator workflow for using `runoq` against a target repository, from initialization through iterative planning, milestone execution, and a maintenance review launch.

## Before You Start

Complete the [quickstart](./quickstart.md) first. It covers system dependencies, GitHub App creation, and smoke test validation.

Once that is done, make sure you have:

- A target repository hosted on `github.com` with an `origin` remote
- The GitHub App installed on that repository
- A checkout of this runtime repository so you can invoke `bin/runoq`

Examples below assume:

```bash
export RUNOQ_RUNTIME=/path/to/runoq
cd /path/to/target-repo
```

If `/usr/local/bin` is not writable on your machine, set a writable symlink location before initialization:

```bash
export RUNOQ_SYMLINK_DIR="$HOME/.local/bin"
```

## Initial Setup

Run initialization from inside the target repository:

```bash
"$RUNOQ_RUNTIME/bin/runoq" init
```

`runoq init` performs one-time bootstrap work:

- Creates `.runoq/identity.json` with the GitHub App ID, installation ID, and private key path
- Creates `.runoq/state/` for resumability state files
- Ensures the managed `runoq:*` labels exist in GitHub
- Creates a minimal `package.json` only when the target repo does not already have one
- Installs or refreshes symlinks for the runoq-managed Claude agents and skills inside the target repo's `.claude/` directories
- Creates an `runoq` symlink in `RUNOQ_SYMLINK_DIR` or `/usr/local/bin`

After this step you can usually call `runoq` directly if the symlink directory is on `PATH`.
The `.claude/` install is intentionally narrow: project-specific agents, skills, and settings can still live alongside the runoq-managed files, while `runoq init` refreshes only the managed filenames it owns.

## Starting Iterative Planning

Prepare a local plan document in the target repository, commit or stage it in the target repository, then initialize `runoq` with the committed plan path:

```bash
"$RUNOQ_RUNTIME/scripts/setup.sh" --plan docs/plan.md
```

Once `runoq.json` exists at the repository root, the primary workflow is:

```bash
runoq tick
```

`runoq tick` advances the project by exactly one step:

1. Bootstraps the top-level planning epic and planning issue when the repo has no milestone epics yet
2. Posts proposal comments for planning issues through the decomposer plus technical/product review loop
3. Waits for human review on GitHub issues, or answers review comments when needed
4. Materializes approved milestone/task proposals into real GitHub issues
5. Dispatches implementation work once a milestone has task issues
6. Reviews completed milestones and creates adjustment issues when the plan should change

The operator control surface is GitHub:

- planning proposals arrive as comments on `type: planning` issues
- humans ask questions or request changes with normal issue comments
- approval is signaled by applying the configured `runoq:plan-approved` label
- milestone adjustments use `type: adjustment` issues and the same approval model

### Legacy full-plan mode

`runoq plan` still exists during the transition, but it is deprecated and should only be used when you explicitly want the older one-shot plan-to-issues flow.

```bash
runoq plan docs/plan.md --auto-confirm
runoq plan docs/plan.md --dry-run
```

## Running A Single Issue

Use single-issue mode when you want to drive one queue item explicitly:

```bash
runoq run --issue 42
```

During a successful run, `runoq` drives the issue through a deterministic phase sequence:

1. **INIT** — eligibility check, label transition to `runoq:in-progress`, worktree and draft PR creation
2. **DEVELOP** — one bounded Codex dev round runs in the worktree and posts its result to the PR
3. **VERIFY** — deterministic verification reruns from the pushed branch on a fresh worktree
4. **REVIEW** — the `diff-reviewer` agent evaluates the diff against the spec
5. **DECIDE** — the orchestrator routes to another DEVELOP round (if the review verdict is `ITERATE` and rounds remain) or to FINALIZE
6. **FINALIZE** — PR finalization, label transition, worktree cleanup

GitHub PR audit comments are the durable record for phase progression and resume.

If the outcome is a clean pass and complexity is at or below the auto-merge threshold (currently `medium`), the issue is marked `runoq:done`, auto-merge is enabled on the PR, and the worktree is removed. Otherwise the run is escalated to `runoq:needs-human-review`.

## Running The Queue

Queue mode lets `runoq` select the next ready issue automatically:

```bash
runoq run
```

Queue selection is based on open issues labeled `runoq:ready`. The runtime skips issues whose dependencies are not yet labeled `runoq:done` and continues until there are no actionable items left or the consecutive-failure circuit breaker halts the queue.

After the task queue drains, the orchestrator can run milestone review and open adjustment-review issues when follow-up planning is needed.

Use queue mode after `runoq tick` has already materialized task issues for the current milestone and you want the runtime to keep draining ready work without naming each issue manually.

## Inspecting Outputs And Reports

Use the report commands from the target repository:

```bash
runoq report summary
runoq report issue 42
runoq report cost
```

What to inspect after a run:

- GitHub issue labels and issue comments for queue state and escalations
- The draft or finalized PR for audit comments and summary updates
- `.runoq/state/<issue>.json` for resumability state and the final outcome
- `.runoq/state/maintenance.json` after maintenance review starts
- Epic status via `gh-issue-queue.sh epic-status` for parent/child completion tracking

`report summary` aggregates local state files. `report issue <n>` prints the saved JSON for one issue. `report cost` estimates token cost from the configured per-million rates.

## Running Maintenance Review

Launch maintenance review from a clean target repository checkout:

```bash
runoq maintenance
```

This invokes the `maintenance-reviewer` agent. The runtime creates a tracking issue labeled `runoq:maintenance-review`, posts partition progress comments, and records local state in `.runoq/state/maintenance.json`. The review is read-only until a human triages findings and explicitly approves filing follow-up issues.

## Where To Go Next

- Use the [README](../../README.md) for the repo overview and prerequisite list
- Use [docs/live-smoke.md](../live-smoke.md) for sandboxed real-GitHub validation
- Use [docs/adr/](../adr/README.md) for architectural decision records
