# CLI Reference

This document describes the public `runoq` CLI implemented by [`bin/runoq`](../../bin/runoq).

## Command Synopsis

```text
runoq init
runoq plan <file>
runoq tick [--issue N]
runoq loop [--backoff N] [--max-wait-cycles N] [--issue N]
runoq report <summary|issue|cost> [...]
runoq maintenance
```

The CLI must be run from inside the target git repository. It resolves:

- `TARGET_ROOT` from the current git checkout
- `REPO` from the `origin` remote, unless `RUNOQ_REPO` is set
- `RUNOQ_CONFIG` from `config/runoq.json`, unless overridden

## Implementation Selection

`bin/runoq` is a runtime-only top-level entrypoint.

- Default behavior routes to the Go runtime shim (`RUNOQ_IMPLEMENTATION` unset).
- `RUNOQ_IMPLEMENTATION=runtime` is accepted as an explicit no-op compatibility setting.
- `RUNOQ_IMPLEMENTATION=shell` is no longer supported at the top-level CLI boundary.
- Optional test/operator override: `RUNOQ_RUNTIME_BIN=/abs/path/to/runoq-runtime` to use a prebuilt runtime binary instead of `go run`.
- Optional toolchain override when using `go run`: `RUNOQ_GO_BIN`.

The public CLI contract is unchanged; these env vars only control runtime dispatch mechanics.

## Mutation Summary

| Command | Local filesystem mutation | GitHub mutation |
| --- | --- | --- |
| `runoq init` | Yes | Yes |
| `runoq plan <file>` | No | Yes, but only after user confirmation in the plan pipeline |
| `runoq tick [--issue N]` | Yes when the tick dispatches implementation work; otherwise logs plus transient planning artifacts | Yes |
| `runoq loop [--backoff N] [--max-wait-cycles N] [--issue N]` | Yes | Yes |
| `runoq report ...` | No | No |
| `runoq maintenance` | Agent-dependent; expected to create local maintenance state | Yes |

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
This command is deprecated in favor of `runoq tick`.

```bash
runoq plan docs/plan.md
```

Arguments:

- `<file>`: required path to a local plan document; the CLI resolves it to an absolute path before invoking the pipeline

What it does:

- Resolves target repo context and GitHub auth
- Runs `scripts/plan.sh <repo> <absolute-path>`, which:
  1. Calls `milestone-decomposer` to break the plan into milestones, then `task-decomposer` for each milestone to produce the final epics and tasks with dependency ordering, complexity estimates, and complexity rationales
  2. Presents the proposed issue hierarchy to the operator for confirmation
  3. Creates GitHub issues deterministically via `gh-issue-queue.sh create` (epics first, then tasks with resolved dependency numbers)
- Supports `--auto-confirm` and `--dry-run` flags
- Prints a deprecation notice pointing operators to `runoq tick`
- Persists the decomposition invocations under `log/claude/milestone-decomposer-<timestamp>/` and `log/claude/task-decomposer-<timestamp>/` with `argv.txt`, `context.log`, `request.txt`, live `stdout.log`, `stderr.log`, `progress.log`, and `response.txt`

Common failures:

- Missing plan file
- Claude CLI not found
- Missing `.runoq/identity.json` or GitHub App key after auth bootstrap

### `runoq tick`

Advances the iterative planning and coordination workflow by exactly one step.

```bash
runoq tick
runoq tick --issue 42
```

What it does:

- Resolves target repo context and GitHub auth
- In normal mode, reads the committed `runoq.json` plan path and current GitHub issue state
- Executes one deterministic transition in the iterative planning state machine
- May bootstrap planning, answer planning comments, materialize approved milestone or task proposals, dispatch implementation, or advance milestone review
- With `--issue N`, skips queue selection and advances only that implementation task by one transition
- Prints a single-line status suitable for operators and notification hooks

Common statuses:

- `Proposal posted on #<n>`
- `Responded to comments on #<n>`
- `Applied approvals from #<n>`
- `Dispatched #<n>`
- `Awaiting human decision on #<n>`
- `Project complete`

Common failures:

- Missing committed `runoq.json` or configured `plan.file`
- Missing auth bootstrap inputs
- Proposal comment missing the expected `runoq:payload:*` marker
- Underlying planning or dispatch script failure

### `runoq loop [--backoff N] [--max-wait-cycles N] [--issue N]`

Repeats `runoq tick` until interrupted, all work is complete, or an optional wait limit is reached.

```bash
runoq loop
runoq loop --issue 42
runoq loop --backoff 15 --max-wait-cycles 4
```

Flags:

- `--issue N`: keep advancing exactly issue `N` instead of using queue selection
- `--backoff N`: seconds to wait before retrying after a waiting tick
- `--max-wait-cycles N`: stop after `N` consecutive waiting ticks instead of looping forever

Behavior:

- Resolves target repo context and GitHub auth
- Invokes `tick` repeatedly with the same runtime configuration
- In queue mode, keeps selecting the next actionable `runoq:ready` task when implementation dispatch is the next valid tick transition
- In `--issue N` mode, keeps advancing that one implementation task until it reaches a terminal phase or the command is interrupted
- Sleeps between waiting ticks according to `--backoff`
- Stops cleanly on terminal project completion, Ctrl-C, or the configured wait-cycle limit

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
- Persists the maintenance-reviewer invocation under `log/claude/maintenance-reviewer-<timestamp>/`

Expected side effects:

- Creates or resumes `.runoq/state/maintenance.json`
- Creates a GitHub tracking issue labeled `runoq:maintenance-review`
- Posts progress and finding comments to the tracking issue

Common failures:

- Claude CLI not found
- Missing auth bootstrap inputs
- Missing or invalid maintenance state on resume

## Internal: `tick-fmt` Subcommands

`scripts/tick-fmt.sh` (routed internally as `__tick_fmt`) provides pure formatting and parsing subcommands used by `tick.sh` and `plan-dispatch.sh`. All subcommands read from stdin and write to stdout.

| Subcommand | Input (stdin) | Output (stdout) |
|---|---|---|
| `format-proposal` | Proposal JSON | Markdown with `<!-- runoq:payload:plan-proposal -->` marker |
| `proposal-comment-body` | `{proposal, technical, product, warning}` JSON | Full review comment markdown |
| `milestone-body` | ProposalItem JSON | Milestone issue body markdown |
| `adjustment-review-body` | `{proposed_adjustments}` JSON | Adjustment review issue body |
| `parse-verdict` | Verdict text (VERDICT/SCORE/CHECKLIST) | ReviewScore JSON |
| `extract-json <marker>` | Text with marker-delimited code block | Extracted JSON string |
| `human-comment-selection` | Issue view JSON (`gh issue view --json`) | `{approved, rejected}` JSON |
| `select-items --selection JSON` | Proposal JSON | Filtered Proposal JSON |
| `merge-checklists <left> <right>` | (none — positional args) | Merged checklist text |

These are internal implementation details and are not part of the public CLI contract.

## Common Examples

Initialize a repo and create queue issues from a plan:

```bash
runoq init
runoq tick
runoq plan docs/plan.md
```

Inspect queue selection before dispatching:

```bash
runoq tick
```

Run one known issue:

```bash
runoq loop --issue 42
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
- `tick` or `loop` may exit with the underlying dev-process status when a development round stalls or crashes.
- JSON-producing commands are intended for machine consumption where practical: `report`, and many lower-level scripts emit structured JSON.

## Related Docs

- [README](../../README.md)
- [Operator workflow](../operations/operator-workflow.md)
- [Architecture overview](../architecture/overview.md)
- [Documentation backlog](../documentation-backlog.md)
