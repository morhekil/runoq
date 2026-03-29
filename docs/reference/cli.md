# CLI Reference

This document describes the public `runoq` CLI implemented by [`bin/runoq`](../../bin/runoq).

## Command Synopsis

```text
runoq init
runoq plan <file>
runoq run [--issue N] [--dry-run]
runoq report <summary|issue|cost> [...]
runoq maintenance
```

The CLI must be run from inside the target git repository. It resolves:

- `TARGET_ROOT` from the current git checkout
- `REPO` from the `origin` remote, unless `RUNOQ_REPO` is set
- `RUNOQ_CONFIG` from `config/runoq.json`, unless overridden

## Mutation Summary

| Command | Local filesystem mutation | GitHub mutation |
| --- | --- | --- |
| `runoq init` | Yes | Yes |
| `runoq plan <file>` | No | Yes, but only after user confirmation in the plan pipeline |
| `runoq run --issue N` | Yes | Yes |
| `runoq run` | Yes | Yes |
| `runoq run --dry-run` | No intended durable mutation beyond reconciliation side effects | Reconciliation comments or label cleanup may occur before the dry-run output |
| `runoq report ...` | No | No |
| `runoq maintenance` | Agent-dependent; expected to create local maintenance state | Yes |

`runoq run --dry-run` is not a pure no-op. The runtime performs startup reconciliation first, which can resume interrupted runs or reset stale `runoq:in-progress` labels before reporting queue state.

## Command Reference

### `runoq init`

Bootstraps the target repository for `runoq`.

```bash
runoq init
```

What it does:

- Creates `.runoq/identity.json` when missing or invalid
- Creates `.runoq/state/`
- Ensures the managed `runoq:*` labels exist in GitHub
- Creates a default `package.json` only when one does not already exist
- Installs or refreshes symlinks for the runoq-managed `.claude/agents/*` and `.claude/skills/*/SKILL.md` files in the target repo without taking over the rest of the target repo's `.claude/`
- Creates an `runoq` symlink in `RUNOQ_SYMLINK_DIR` or `/usr/local/bin`

Important behavior:

- `init` does not use `gh-auth.sh`, because identity bootstrap happens here. It relies on the operator already being authenticated for `gh`.
- The default GitHub App key path is `$HOME/.runoq/app-key.pem`. Override it with `RUNOQ_APP_KEY`.

Common failures:

- Not inside a git repository
- No `origin` remote
- `origin` is not hosted on `github.com`
- GitHub App private key missing
- Managed `.claude` parent path exists as a non-directory
- Symlink destination exists as a non-symlink file

### `runoq plan <file>`

Decomposes a plan document into GitHub issues using the plan decomposition pipeline (`scripts/plan.sh`).

```bash
runoq plan docs/plan.md
```

Arguments:

- `<file>`: required path to a local plan document; the CLI resolves it to an absolute path before invoking the pipeline

What it does:

- Resolves target repo context and GitHub auth
- Runs `scripts/plan.sh <repo> <absolute-path>`, which:
  1. Calls the `plan-decomposer` agent to decompose the plan into epics and tasks with dependency ordering, complexity estimates, and complexity rationales
  2. Presents the proposed issue hierarchy to the operator for confirmation
  3. Creates GitHub issues deterministically via `gh-issue-queue.sh create` (epics first, then tasks with resolved dependency numbers)
- Supports `--auto-confirm` and `--dry-run` flags

Common failures:

- Missing plan file
- Claude CLI not found
- Missing `.runoq/identity.json` or GitHub App key after auth bootstrap

### `runoq run [--issue N] [--dry-run]`

Runs the implementation workflow for a single issue or the next actionable queue item.

```bash
runoq run
runoq run --issue 42
runoq run --dry-run
runoq run --issue 42 --dry-run
```

Flags:

- `--issue N`: dispatch exactly issue `N` instead of selecting from the ready queue
- `--dry-run`: return reconciliation and queue-selection data without dispatching new work

Behavior:

- Resolves target repo context and GitHub auth
- Runs startup reconciliation through `dispatch-safety.sh reconcile`
- In queue mode, selects the next actionable `runoq:ready` issue by dependency and priority
- In execution mode, delegates to `orchestrator.sh run` which creates a sibling worktree, opens a draft PR, drives the phase state machine (INIT, CRITERIA, DEVELOP, REVIEW, DECIDE, FINALIZE), and finalizes with either `auto-merge` or `needs-human-review`

Dry-run output:

- Queue mode returns JSON with `mode`, `reconciliation`, `queue`, and `selection`
- Single-issue mode returns JSON with `mode`, `reconciliation`, and `issue`

Common failures:

- Not inside a git repository
- Claude CLI not found when the runtime falls through to agent mode
- Missing auth bootstrap inputs
- Eligibility failures such as missing acceptance criteria, blocked dependencies, or an existing open PR
- Agent stall or non-zero exit during development

### `runoq report <summary|issue|cost> [...]`

Reads saved local state and prints JSON reports.

```bash
runoq report summary
runoq report summary --last 10
runoq report issue 42
runoq report cost
runoq report cost --last 5
```

Subcommands:

- `summary [--last N]`: aggregates completed state files, including pass/fail counts and token totals
- `issue <issue-number>`: prints the saved JSON state for one issue
- `cost [--last N]`: estimates cost from token totals and configured token rates

Behavior:

- Reads `.runoq/state/*.json`
- Never mutates GitHub or the filesystem
- Returns zeroed JSON when the state directory is empty for `summary` and `cost`

Common failures:

- `issue <n>` when `.runoq/state/<n>.json` does not exist
- Invalid argument combinations

### `runoq maintenance`

Launches the maintenance reviewer agent.

```bash
runoq maintenance
```

What it does:

- Resolves target repo context and GitHub auth
- Runs `claude --agent maintenance-reviewer --add-dir "$RUNOQ_ROOT"`
- Hands control to the maintenance workflow, which uses deterministic scripts for partitioning, tracking issue management, findings posting, and triage

Expected side effects:

- Creates or resumes `.runoq/state/maintenance.json`
- Creates a GitHub tracking issue labeled `runoq:maintenance-review`
- Posts progress and finding comments to the tracking issue

Common failures:

- Claude CLI not found
- Missing auth bootstrap inputs
- Missing or invalid maintenance state on resume

## Common Examples

Initialize a repo and create queue issues from a plan:

```bash
runoq init
runoq plan docs/plan.md
```

Inspect queue selection before dispatching:

```bash
runoq run --dry-run
```

Run one known issue:

```bash
runoq run --issue 42
```

Inspect local outcomes after the run:

```bash
runoq report summary
runoq report issue 42
```

Launch maintenance review:

```bash
runoq maintenance
```

## Exit And Failure Behavior

- Unknown top-level subcommands print usage and exit non-zero.
- Command-specific argument validation failures print usage or a targeted error and exit non-zero.
- `runoq::die` failures are human-readable and include repo, auth, file-path, or config guidance where the scripts provide it.
- `run` may exit with the underlying dev-process status when a development round stalls or crashes.
- JSON-producing commands are intended for machine consumption where practical: `run --dry-run`, `report`, and many lower-level scripts emit structured JSON.

## Related Docs

- [README](../../README.md)
- [Operator workflow](../operations/operator-workflow.md)
- [Architecture overview](../architecture/overview.md)
- [Documentation backlog](../documentation-backlog.md)
