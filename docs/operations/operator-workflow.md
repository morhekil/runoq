# Operator Workflow

This guide walks through the day-to-day operator workflow for using `runoq` against a target repository, from initialization through a successful run and a maintenance review launch.

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

## Creating Queue Issues From A Plan

Prepare a local plan document in the target repository, then run:

```bash
runoq plan docs/plan.md
```

The plan pipeline uses the `plan-decomposer` agent to break the document into an epic/task hierarchy. The decomposer proposes epics (grouping units) and tasks (implementable units), each with an estimated complexity (`low`, `medium`, or `high`) and a rationale for the complexity assessment. It then presents the proposal for interactive confirmation before creating anything in GitHub.

Additional flags:

```bash
runoq plan docs/plan.md --auto-confirm   # skip confirmation prompt
runoq plan docs/plan.md --dry-run        # show proposal without creating issues
```

After confirmation, the pipeline creates issues in two passes:

1. **Epics** — created first, labeled `runoq:ready`, with `type: epic` in the metadata block
2. **Tasks** — created in dependency order, linked to their parent epic via the GitHub sub-issues API, with `type: task` in the metadata block

Each issue includes:

- The `runoq:ready` label
- An `<!-- runoq:meta -->` block containing dependencies, priority, estimated complexity, complexity rationale, and type
- Acceptance criteria in the issue body

At this point GitHub becomes the queue surface. Operators can inspect issue titles, labels, and metadata without reading local state files.

## Running A Single Issue

Use single-issue mode when you want to drive one queue item explicitly:

```bash
runoq run --issue 42
```

During a successful run, `runoq` drives the issue through a deterministic phase sequence:

1. **INIT** — eligibility check, label transition to `runoq:in-progress`, worktree and draft PR creation
2. **CRITERIA** — for medium/high complexity issues, the `bar-setter` agent writes acceptance tests and specs in the worktree before development begins. Low-complexity issues skip this phase.
3. **DEVELOP** — the `issue-runner` script drives a Codex dev round in the worktree
4. **REVIEW** — the `diff-reviewer` agent evaluates the diff against the spec
5. **DECIDE** — the orchestrator routes to another DEVELOP round (if the review verdict is `ITERATE` and rounds remain), to FINALIZE, or to INTEGRATE for epics
6. **FINALIZE** — PR finalization, label transition, worktree cleanup

State is saved to `.runoq/state/42.json` after each phase so interrupted runs can be reconciled.

If the outcome is a clean pass and complexity is at or below the auto-merge threshold (currently `medium`), the issue is marked `runoq:done`, auto-merge is enabled on the PR, and the worktree is removed. Otherwise the run is escalated to `runoq:needs-human-review`.

## Running The Queue

Queue mode lets `runoq` select the next ready issue automatically:

```bash
runoq run
```

Queue selection is based on open issues labeled `runoq:ready`. The runtime skips issues whose dependencies are not yet labeled `runoq:done` and continues until there are no actionable items left or the consecutive-failure circuit breaker halts the queue.

After the task queue drains, the orchestrator performs an **epic sweep**: it checks all `runoq:ready` epics to see whether all of their child tasks are `runoq:done`. For each completed epic, it runs the **INTEGRATE** phase — which verifies acceptance criteria against the combined work of all child tasks — and marks the epic `runoq:done` if integration passes.

Use queue mode after the plan has been converted into issues and you want the runtime to keep draining ready work without naming each issue manually.

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
