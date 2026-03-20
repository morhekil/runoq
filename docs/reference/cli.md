# CLI Reference

This document describes the public `agendev` CLI implemented by [`bin/agendev`](../../bin/agendev).

## Command Synopsis

```text
agendev init
agendev plan <file>
agendev run [--issue N] [--dry-run]
agendev report <summary|issue|cost> [...]
agendev maintenance
```

The CLI must be run from inside the target git repository. It resolves:

- `TARGET_ROOT` from the current git checkout
- `REPO` from the `origin` remote, unless `AGENDEV_REPO` is set
- `AGENDEV_CONFIG` from `config/agendev.json`, unless overridden

## Mutation Summary

| Command | Local filesystem mutation | GitHub mutation |
| --- | --- | --- |
| `agendev init` | Yes | Yes |
| `agendev plan <file>` | No | Yes, but only after user confirmation inside the skill |
| `agendev run --issue N` | Yes | Yes |
| `agendev run` | Yes | Yes |
| `agendev run --dry-run` | No intended durable mutation beyond reconciliation side effects | Reconciliation comments or label cleanup may occur before the dry-run output |
| `agendev report ...` | No | No |
| `agendev maintenance` | Agent-dependent; expected to create local maintenance state | Yes |

`agendev run --dry-run` is not a pure no-op. The runtime performs startup reconciliation first, which can resume interrupted runs or reset stale `agendev:in-progress` labels before reporting queue state.

## Command Reference

### `agendev init`

Bootstraps the target repository for `agendev`.

```bash
agendev init
```

What it does:

- Creates `.agendev/identity.json` when missing or invalid
- Creates `.agendev/state/`
- Ensures the managed `agendev:*` labels exist in GitHub
- Creates a default `package.json` only when one does not already exist
- Copies or refreshes the agendev-managed `.claude/agents/*` and `.claude/skills/*/SKILL.md` files in the target repo without taking over the rest of the target repo's `.claude/`
- Creates an `agendev` symlink in `AGENDEV_SYMLINK_DIR` or `/usr/local/bin`

Important behavior:

- `init` does not use `gh-auth.sh`, because identity bootstrap happens here. It relies on the operator already being authenticated for `gh`.
- The default GitHub App key path is `$HOME/.agendev/app-key.pem`. Override it with `AGENDEV_APP_KEY`.

Common failures:

- Not inside a git repository
- No `origin` remote
- `origin` is not hosted on `github.com`
- GitHub App private key missing
- Managed `.claude` parent path exists as a non-directory
- Symlink destination exists as a non-symlink file

### `agendev plan <file>`

Sends a local plan document to the `plan-to-issues` skill.

```bash
agendev plan docs/plan.md
```

Arguments:

- `<file>`: required path to a local plan document; the CLI resolves it to an absolute path before invoking Claude

What it does:

- Resolves target repo context and GitHub auth
- Runs `claude --skill plan-to-issues --add-dir "$AGENDEV_ROOT" -- <absolute-path>`
- Lets the skill propose issue slicing, dependencies, and granularity warnings
- Creates GitHub issues only after explicit user confirmation

Common failures:

- Missing plan file
- Claude CLI not found
- Missing `.agendev/identity.json` or GitHub App key after auth bootstrap

### `agendev run [--issue N] [--dry-run]`

Runs the implementation workflow for a single issue or the next actionable queue item.

```bash
agendev run
agendev run --issue 42
agendev run --dry-run
agendev run --issue 42 --dry-run
```

Flags:

- `--issue N`: dispatch exactly issue `N` instead of selecting from the ready queue
- `--dry-run`: return reconciliation and queue-selection data without dispatching new work

Behavior:

- Resolves target repo context and GitHub auth
- Runs startup reconciliation through `dispatch-safety.sh reconcile`
- In queue mode, selects the next actionable `agendev:ready` issue by dependency and priority
- In execution mode, creates a sibling worktree, opens a draft PR, writes local state, verifies results, and finalizes with either `done` or `needs-human-review`

Dry-run output:

- Queue mode returns JSON with `mode`, `reconciliation`, `queue`, and `selection`
- Single-issue mode returns JSON with `mode`, `reconciliation`, and `issue`

Common failures:

- Not inside a git repository
- Claude CLI not found when the runtime falls through to agent mode
- Missing auth bootstrap inputs
- Eligibility failures such as missing acceptance criteria, blocked dependencies, or an existing open PR
- Agent stall or non-zero exit during development

### `agendev report <summary|issue|cost> [...]`

Reads saved local state and prints JSON reports.

```bash
agendev report summary
agendev report summary --last 10
agendev report issue 42
agendev report cost
agendev report cost --last 5
```

Subcommands:

- `summary [--last N]`: aggregates completed state files, including pass/fail counts and token totals
- `issue <issue-number>`: prints the saved JSON state for one issue
- `cost [--last N]`: estimates cost from token totals and configured token rates

Behavior:

- Reads `.agendev/state/*.json`
- Never mutates GitHub or the filesystem
- Returns zeroed JSON when the state directory is empty for `summary` and `cost`

Common failures:

- `issue <n>` when `.agendev/state/<n>.json` does not exist
- Invalid argument combinations

### `agendev maintenance`

Launches the maintenance reviewer agent.

```bash
agendev maintenance
```

What it does:

- Resolves target repo context and GitHub auth
- Runs `claude --agent maintenance-reviewer --add-dir "$AGENDEV_ROOT"`
- Hands control to the maintenance workflow, which uses deterministic scripts for partitioning, tracking issue management, findings posting, and triage

Expected side effects:

- Creates or resumes `.agendev/state/maintenance.json`
- Creates a GitHub tracking issue labeled `agendev:maintenance-review`
- Posts progress and finding comments to the tracking issue

Common failures:

- Claude CLI not found
- Missing auth bootstrap inputs
- Missing or invalid maintenance state on resume

## Common Examples

Initialize a repo and create queue issues from a plan:

```bash
agendev init
agendev plan docs/plan.md
```

Inspect queue selection before dispatching:

```bash
agendev run --dry-run
```

Run one known issue:

```bash
agendev run --issue 42
```

Inspect local outcomes after the run:

```bash
agendev report summary
agendev report issue 42
```

Launch maintenance review:

```bash
agendev maintenance
```

## Exit And Failure Behavior

- Unknown top-level subcommands print usage and exit non-zero.
- Command-specific argument validation failures print usage or a targeted error and exit non-zero.
- `agendev::die` failures are human-readable and include repo, auth, file-path, or config guidance where the scripts provide it.
- `run` may exit with the underlying dev-process status when a development round stalls or crashes.
- JSON-producing commands are intended for machine consumption where practical: `run --dry-run`, `report`, and many lower-level scripts emit structured JSON.

## Related Docs

- [README](../../README.md)
- [Operator workflow](../operations/operator-workflow.md)
- [Architecture overview](../architecture/overview.md)
- [Documentation backlog](../documentation-backlog.md)
