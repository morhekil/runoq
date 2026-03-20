# agendev

`agendev` is a deterministic shell/runtime layer for GitHub-backed agentic development orchestration.

It gives a human operator a repeatable way to turn a plan into GitHub issues, run one issue or a whole queue through an agent workflow, publish PR and audit updates back to GitHub, and keep enough local state to resume safely after interruptions. The runtime keeps the target repository's main working tree intact by doing execution work in sibling worktrees.

## What It Does

- Converts plan documents into queued GitHub issues through the `plan-to-issues` skill.
- Runs implementation work through shell scripts that manage queue selection, worktrees, verification, PR lifecycle, and resumability.
- Uses GitHub issues and PR comments as the main operator control surface and audit trail.
- Uses `.agendev/state/*.json` as local recovery breadcrumbs rather than the source of truth for history.
- Provides a separate maintenance review flow for filing follow-up work from read-only code review passes.

## How It Works

1. Run `agendev init` inside a target GitHub repository.
2. Create queued issues from a plan with `agendev plan <file>`.
3. Execute one issue with `agendev run --issue N` or let the runtime select the next ready issue with `agendev run`.
4. Inspect local state and token/cost summaries with `agendev report`.
5. Launch maintenance review with `agendev maintenance`.

The CLI is thin. Deterministic behavior lives in [`scripts/`](./scripts), JSON config, and test fixtures. Claude agents and skills are used as dispatch layers around those contracts.

## Prerequisites

- `bash`
- `git`
- `gh`
- `jq`
- `openssl`
- `bats`
- `shellcheck`
- Claude CLI, available on `PATH` as `claude` unless `AGENDEV_CLAUDE_BIN` is set
- A GitHub repository with an `origin` remote hosted on `github.com`
- A GitHub App private key at `$HOME/.agendev/app-key.pem` or a path supplied through `AGENDEV_APP_KEY`

## Quick Start

Run these commands from inside the target repository, not from this runtime repository:

```bash
git clone <target-repo>
cd <target-repo>

# Optional if /usr/local/bin is not writable.
export AGENDEV_SYMLINK_DIR="$HOME/.local/bin"

/path/to/agendev/bin/agendev init
/path/to/agendev/bin/agendev plan docs/plan.md
/path/to/agendev/bin/agendev run --issue 42
/path/to/agendev/bin/agendev report summary
```

`agendev init` creates `.agendev/identity.json`, `.agendev/state/`, any missing queue labels in GitHub, a default `package.json` when the target repo does not already have one, installs or refreshes agendev-managed `.claude/` agent and skill symlinks in the target repo, and creates a convenience `agendev` symlink.

## Command Overview

- `agendev init`: bootstraps identity, labels, state directories, a fallback `package.json`, and a local CLI symlink.
- `agendev plan <file>`: sends a plan document to the `plan-to-issues` skill to create queued GitHub issues.
- `agendev run [--issue N] [--dry-run]`: runs a single issue or the next ready queue item through the orchestrated implementation flow.
- `agendev report <summary|issue|cost>`: reports on saved run state and token usage.
- `agendev maintenance`: launches the maintenance reviewer agent for a read-only review pass.

## Repository Layout

- [`bin/`](./bin): user-facing CLI entrypoint.
- [`scripts/`](./scripts): deterministic runtime scripts and helpers.
- [`config/`](./config): runtime configuration and label/auth defaults.
- [`templates/`](./templates): issue and PR body templates.
- [`test/`](./test): Bats suites, helpers, and fake `gh` fixtures.
- [`docs/`](./docs): product, operations, architecture, and contributor documentation.
- [`.claude/`](./.claude): agent prompts and skills used by the runtime.

## Documentation Map

- [Architecture overview](./docs/architecture/overview.md)
- [Execution and maintenance flows](./docs/architecture/flows.md)
- [CLI reference](./docs/reference/cli.md)
- [Configuration and auth reference](./docs/reference/config-auth.md)
- [Script contract reference](./docs/reference/script-contracts.md)
- [State and audit model](./docs/reference/state-model.md)
- [Target repo contract](./docs/reference/target-repo-contract.md)
- [Quickstart](./docs/operations/quickstart.md)
- [Operator workflow](./docs/operations/operator-workflow.md)
- [Queue operations guide](./docs/operations/queue-operations.md)
- [Recovery and troubleshooting guide](./docs/operations/recovery.md)
- [Maintenance review guide](./docs/operations/maintenance-review.md)
- [Contributor testing guide](./docs/contributing/testing.md)
- [Agent and skill guide](./docs/contributing/agent-and-skill-guidelines.md)
- [Architecture decision records](./docs/adr/README.md)
- [Development guidelines](./docs/development-guidelines.md)
- [Live smoke tests](./docs/live-smoke.md)

More architecture, reference, and operator docs are tracked in the documentation backlog and should be preferred over the PRD once they land.

## Development And Testing

This repo expects deterministic behavior to live in scripts and JSON contracts, with Bats coverage for shell behavior changes.

- Run focused Bats suites first, then adjacent regressions.
- Run `shellcheck -x` on shell scripts you touch.
- Keep live GitHub validation opt-in through the sandbox smoke and lifecycle eval flows in [`docs/live-smoke.md`](./docs/live-smoke.md).
