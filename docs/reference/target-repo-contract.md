# Target Repository Contract

This document defines what `agendev` expects from the repository it operates on. It is the contract between this runtime repo and a downstream project repo.

## Compatibility Checklist

A target repository is compatible with `agendev` when all of the following are true:

- it is a git repository
- it has an `origin` remote
- `origin` resolves to `github.com` using SSH or HTTPS remote syntax
- the operator can run `agendev` from inside that repository checkout
- the repo can tolerate sibling worktrees created next to the main checkout
- queued issues use the `agendev:meta` block and include acceptance criteria
- verification commands configured in `config/agendev.json` can run successfully in the repo

## Git And Remote Assumptions

### Required remote shape

By default, `agendev` derives `REPO` from the `origin` remote and only accepts GitHub remotes in one of these forms:

- `git@github.com:owner/repo.git`
- `https://github.com/owner/repo.git`
- `ssh://git@github.com/owner/repo.git`

Anything else fails repo resolution unless `AGENDEV_REPO` overrides it.

In normal CLI usage, a usable `origin` remote is still required because repo context resolution consults `origin` before the override takes effect. Treat `AGENDEV_REPO` as a specialized override, not as a replacement for the remote contract.

### Base branch assumption

Sibling worktrees are created from `origin/main` unless `AGENDEV_BASE_REF` overrides it.

Implications for downstream repos:

- the remote should expose a usable `main` branch, or operators must deliberately override `AGENDEV_BASE_REF`
- existing issue branches are checked for conflicts against `origin/main`
- queue execution assumes the worktree base ref is fetchable from `origin`

## Working Tree And Worktree Behavior

`agendev` does not mutate the target repo’s main working tree during issue execution. Instead it:

- derives a branch name from the issue number and title
- creates a sibling worktree adjacent to the main checkout
- runs development and verification commands inside that sibling worktree
- removes the worktree after clean low-complexity completion

Downstream implications:

- the parent directory of the main checkout must allow sibling worktree directories
- tools or editors that watch the repo directory should not assume only one checkout exists
- local uncommitted changes in the main checkout do not become the execution base; `worktree.sh` uses `origin/main`

## Queue Issue Body Contract

### Required metadata block

Queue issues are expected to start with the metadata block used by [`templates/issue-template.md`](../../templates/issue-template.md):

```md
<!-- agendev:meta
depends_on: []
priority: 3
estimated_complexity: medium
-->
```

Field requirements:

- `depends_on`: JSON array of issue numbers
- `priority`: integer, lower means earlier queue selection
- `estimated_complexity`: string such as `low`, `medium`, or `high`

### Required acceptance criteria section

`dispatch-safety.sh eligibility` rejects an issue if the body does not contain:

```md
## Acceptance Criteria
```

The shell runtime does not inspect checklist items semantically, but it does require the section header to exist.

### Labels owned by the runtime

The runtime manages these labels:

- `agendev:ready`
- `agendev:in-progress`
- `agendev:done`
- `agendev:needs-human-review`
- `agendev:blocked`
- `agendev:maintenance-review`

Downstream repos should not repurpose these labels for unrelated workflows.

## PR Body Contract

PRs created by `agendev` use [`templates/pr-template.md`](../../templates/pr-template.md). Two marker-delimited regions are contract-sensitive:

- `<!-- agendev:summary:start -->` to `<!-- agendev:summary:end -->`
- `<!-- agendev:attention:start -->` to `<!-- agendev:attention:end -->`

`gh-pr-lifecycle.sh update-summary` rewrites only those sections. If those markers are missing, summary updates become unsafe.

The PR body also includes:

- `Closes #ISSUE_NUMBER`
- a review-rounds table section

Those are part of the expected operator-facing PR shape, even though the summary markers are the most automation-sensitive part.

## `AGENTS.md` In The Target Repo

The shell runtime does not parse `AGENTS.md`, but the prompt layer does. The `github-orchestrator` startup contract explicitly tells the agent to read `AGENTS.md` from the target repo context.

Downstream guidance:

- use `AGENTS.md` for repository-specific instructions, conventions, and constraints
- keep deterministic workflow rules out of `AGENTS.md`; those belong in scripts and runtime contracts
- do not assume `AGENTS.md` can override queue labels, state semantics, audit markers, or other shell-level invariants

In short: `AGENTS.md` is advisory to agents, not authoritative over the runtime.

## Verification Expectations

### What the runtime runs

`verify.sh` executes the configured commands:

- `verification.testCommand`
- `verification.buildCommand`

These commands run inside the sibling worktree.

### What the target repo must provide

A downstream repo should provide working commands for the configured verification steps. By default, the shipped config expects:

```json
{
  "verification": {
    "testCommand": "npm test",
    "buildCommand": "npm run build"
  }
}
```

That means the repo should normally have:

- a `package.json`
- a `test` script
- a `build` script

### Default bootstrapping during `agendev init`

If the target repo does not already have a `package.json`, `agendev init` creates a minimal placeholder:

```json
{
  "name": "agendev-target",
  "private": true,
  "scripts": {
    "test": "echo \"No tests configured\"",
    "build": "echo \"No build configured\""
  }
}
```

This makes the repo bootstrappable, not production-ready. Downstream maintainers should replace these placeholder scripts with real verification commands or supply a different runtime config.

## Maintenance Review Inputs

Maintenance review assumes the target repo provides enough structure to derive partitions.

Current inputs:

- `.gitignore`: used to derive exclusions
- `tsconfig.json`:
  - if `references` exists, maintenance uses referenced project paths
  - otherwise it uses top-level directories inferred from `include`

If neither file is present, maintenance can still run, but partition quality may be limited.

## Safe Customization Vs Runtime Contract

### Safe for downstream repos to customize

- `AGENTS.md` repository guidance
- source layout, as long as git and verification expectations still hold
- test/build tooling, if `verification.*` commands are updated accordingly
- whether the repo has TypeScript references or a single-project `tsconfig.json`
- issue content beyond the required metadata block and acceptance-criteria section

### Part of the runtime contract

- GitHub-hosted `origin` remote
- queue labels and their meanings
- issue metadata block shape
- `## Acceptance Criteria` section requirement
- PR summary and attention markers
- sibling worktree execution model
- `.agendev/` directory usage for identity and resumability state
- GitHub issues and PR comments as the audit/control surface

## Failure Signals That Usually Mean Contract Mismatch

- `Run agendev from inside a git repository.`
- `No 'origin' remote found. agendev requires a GitHub-hosted repo.`
- `Origin remote is not a GitHub URL: ...`
- `Skipped: missing acceptance criteria.`
- `Skipped: existing open PR #... already tracks this issue.`
- `verification.testCommand is not configured`
- verification failures because the repo lacks working test/build commands

## Related Docs

- [CLI reference](./cli.md)
- [Configuration and auth reference](./config-auth.md)
- [Script contract reference](./script-contracts.md)
- [Operator quickstart](../operations/quickstart.md)
