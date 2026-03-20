# Operator Workflow

This guide walks through the day-to-day operator workflow for using `agendev` against a target repository, from initialization through a successful run and a maintenance review launch.

## Before You Start

Complete the [quickstart](./quickstart.md) first. It covers system dependencies, GitHub App creation, and smoke test validation.

Once that is done, make sure you have:

- A target repository hosted on `github.com` with an `origin` remote
- The GitHub App installed on that repository
- A checkout of this runtime repository so you can invoke `bin/agendev`

Examples below assume:

```bash
export AGENDEV_RUNTIME=/path/to/agendev
cd /path/to/target-repo
```

If `/usr/local/bin` is not writable on your machine, set a writable symlink location before initialization:

```bash
export AGENDEV_SYMLINK_DIR="$HOME/.local/bin"
```

## Initial Setup

Run initialization from inside the target repository:

```bash
"$AGENDEV_RUNTIME/bin/agendev" init
```

`agendev init` performs one-time bootstrap work:

- Creates `.agendev/identity.json` with the GitHub App ID, installation ID, and private key path
- Creates `.agendev/state/` for resumability state files
- Ensures the managed `agendev:*` labels exist in GitHub
- Creates a minimal `package.json` only when the target repo does not already have one
- Copies or refreshes the agendev-managed Claude agents and skills inside the target repo's `.claude/` directories
- Creates an `agendev` symlink in `AGENDEV_SYMLINK_DIR` or `/usr/local/bin`

After this step you can usually call `agendev` directly if the symlink directory is on `PATH`.
The `.claude/` install is intentionally narrow: project-specific agents, skills, and settings can still live alongside the agendev-managed files, while `agendev init` refreshes only the managed filenames it owns.

## Creating Queue Issues From A Plan

Prepare a local plan document in the target repository, then run:

```bash
agendev plan docs/plan.md
```

The `plan-to-issues` skill reads the file, proposes a queue, and asks for explicit confirmation before creating anything in GitHub. After you confirm, it creates issues with:

- The `agendev:ready` label
- An `<!-- agendev:meta -->` block containing dependencies, priority, and estimated complexity
- Acceptance criteria in the issue body

At this point GitHub becomes the queue surface. Operators can inspect issue titles, labels, and metadata without reading local state files.

## Running A Single Issue

Use single-issue mode when you want to drive one queue item explicitly:

```bash
agendev run --issue 42
```

During a successful run, `agendev`:

- Verifies the issue is eligible for dispatch
- Moves the issue label from `agendev:ready` to `agendev:in-progress`
- Creates a sibling worktree next to the target repo
- Creates a draft PR for the issue branch
- Posts structured audit comments to the PR
- Writes `.agendev/state/42.json` so interrupted runs can be reconciled
- Verifies the resulting changes before finalization

If the outcome is a clean low-complexity pass, the issue is marked `agendev:done` and the worktree is removed. Otherwise the run is escalated to `agendev:needs-human-review`.

## Running The Queue

Queue mode lets `agendev` select the next ready issue automatically:

```bash
agendev run
```

Queue selection is based on open issues labeled `agendev:ready`. The runtime skips issues whose dependencies are not yet labeled `agendev:done` and continues until there are no actionable items left or the consecutive-failure circuit breaker halts the queue.

Use queue mode after the plan has been converted into issues and you want the runtime to keep draining ready work without naming each issue manually.

## Inspecting Outputs And Reports

Use the report commands from the target repository:

```bash
agendev report summary
agendev report issue 42
agendev report cost
```

What to inspect after a run:

- GitHub issue labels and issue comments for queue state and escalations
- The draft or finalized PR for audit comments and summary updates
- `.agendev/state/<issue>.json` for resumability state and the final outcome
- `.agendev/state/maintenance.json` after maintenance review starts

`report summary` aggregates local state files. `report issue <n>` prints the saved JSON for one issue. `report cost` estimates token cost from the configured per-million rates.

## Running Maintenance Review

Launch maintenance review from a clean target repository checkout:

```bash
agendev maintenance
```

This invokes the `maintenance-reviewer` agent. The runtime creates a tracking issue labeled `agendev:maintenance-review`, posts partition progress comments, and records local state in `.agendev/state/maintenance.json`. The review is read-only until a human triages findings and explicitly approves filing follow-up issues.

## Where To Go Next

- Use the [README](../../README.md) for the repo overview and prerequisite list
- Use [docs/live-smoke.md](../live-smoke.md) for sandboxed real-GitHub validation
- Use [docs/documentation-backlog.md](../documentation-backlog.md) to track the remaining operator, architecture, and reference docs
