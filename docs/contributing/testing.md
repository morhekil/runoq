# Contributor Testing Guide

This guide explains how to validate changes to the `agendev` runtime safely and how to choose the right test layer for a shell change.

## Testing Principles

- Start with a failing deterministic test whenever the behavior can be expressed locally.
- Keep behavior in scripts and JSON contracts, then prove it with Bats.
- Use fake GitHub fixtures for almost all queue, PR, maintenance, auth, and reporting behavior.
- Reserve live smoke runs for credential, attribution, and permission edges that fixtures cannot prove locally.
- Run `shellcheck -x` on every shell script you touch.

## Repo Layout For Tests

Key paths:

- `test/*.bats`: Bats suites grouped by runtime area
- `test/test_helper.bash`: shared setup, tempdir, repo helpers, and fake-`gh` wiring
- `test/helpers/gh`: fake GitHub CLI used by deterministic integration tests
- `test/helpers/claude`: fake Claude CLI used by CLI and prompt wiring tests
- `test/fixtures/`: canned issues, comments, payloads, and plan examples
- `scripts/smoke-sandbox.sh`: sandbox-only real GitHub smoke runner
- `scripts/smoke-lifecycle.sh`: full lifecycle/eval live runner

## Choosing The Right Test Layer

### Contract and helper tests

Use focused suites when changing parsing, config, templates, or state semantics.

Examples:

- `test/foundation.bats`
- `test/payloads.bats`
- `test/state.bats`
- `test/fixtures.bats`

Choose this layer when:

- JSON shape or markers matter
- phase transitions change
- payload normalization changes
- config resolution changes

### Script integration tests with fake GitHub

This is the default layer for most runtime work.

Examples:

- `test/issue_queue.bats`
- `test/pr_lifecycle.bats`
- `test/dispatch_safety.bats`
- `test/maintenance.bats`
- `test/report.bats`
- `test/verify.bats`
- `test/worktree.bats`

Choose this layer when:

- a shell script talks to `gh`
- a script mutates labels, issues, PRs, or state files
- queue ordering, reconciliation, mention handling, or maintenance behavior changes

### CLI and end-to-end local workflow tests

Use these when the user-facing command shape or the full orchestration path changes.

Examples:

- `test/cli.bats`
- `test/run_integration.bats`

Choose this layer when:

- `bin/agendev` routing changes
- `run` behavior changes across multiple scripts
- reconciliation, circuit breaker, or escalation behavior changes end to end

### Live smoke tests

Use only when you need to validate behavior that fake fixtures cannot prove.

Examples:

- GitHub App auth against real GitHub
- comment attribution as `agendev[bot]`
- real collaborator permission checks
- sandbox cleanup behavior
- full end-to-end lifecycle/eval runs through `agendev init` and `agendev run`

Live smoke is opt-in and credential-gated by design.

Current lanes:

- sandbox smoke: narrow GitHub/App/auth validation
- lifecycle eval: managed disposable repo plus full queue execution and eval scoring

## The Bats Harness

`test/test_helper.bash` provides the main building blocks.

### Temp and repo helpers

- `TEST_TMPDIR`: fresh temp directory per test
- `make_git_repo <dir> [remote-url]`: create a local git repo with a seed commit
- `make_remote_backed_repo <remote-dir> <local-dir>`: create a local checkout backed by a bare remote
- `run_bash '<script>'`: convenience wrapper for `bash -lc`

### Fixture helpers

- `fixture_path <relative-path>`: resolve a file under `test/fixtures/`
- `load_fixture <relative-path>`: print a fixture file
- `write_fake_gh_scenario <path>`: write a fake-`gh` scenario definition
- `use_fake_gh <scenario> [state] [log] [capture-dir]`: point scripts at the fake GitHub CLI and capture calls

## Fake `gh` Fixtures

The fake GitHub CLI is the core of deterministic integration coverage.

### How scenarios work

Each scenario is a JSON array of match rules. Typical fields:

- `contains`: argv fragments that must match
- `stdout`: inline JSON or text to return
- `stdout_file`: file whose contents should be returned
- `stderr`
- `exit_code`

Typical pattern:

```bash
scenario="$TEST_TMPDIR/scenario.json"
write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo"],
    "stdout_file": "$(fixture_path "issues/list-ready.json")"
  }
]
EOF
use_fake_gh "$scenario"
```

### What to assert

Assert both:

- returned JSON or stdout
- recorded fake-`gh` invocations in `FAKE_GH_LOG` or `FAKE_GH_CAPTURE_DIR`

That combination proves both command shape and observable result.

## Fixtures And Contracts

The fixture tree is organized by contract type:

- `test/fixtures/issues/`: issue bodies and queue lists
- `test/fixtures/comments/`: PR and issue comments, permissions, audit samples
- `test/fixtures/payloads/`: dispatch and return payloads, malformed and missing cases
- `test/fixtures/plans/`: broad, narrow, and untestable plan examples

When adding a new deterministic behavior:

- prefer extending an existing fixture set first
- keep fixture names specific to the contract they prove
- add malformed or edge-case fixtures when the behavior needs recovery coverage

## Testing Workflow

### Focused first

Start with the smallest relevant suite.

Examples:

```bash
bats test/state.bats
bats test/dispatch_safety.bats
bats test/run_integration.bats
```

### Then adjacent regressions

After the focused test is green, run nearby suites that exercise the same contract boundary.

Examples:

- queue changes: `test/issue_queue.bats`, `test/run_integration.bats`, `test/dispatch_safety.bats`
- PR lifecycle changes: `test/pr_lifecycle.bats`, `test/run_integration.bats`
- maintenance changes: `test/maintenance.bats`, `test/mentions_integration.bats`
- auth/config changes: `test/auth.bats`, `test/cli.bats`, `test/live_smoke.bats` for preflight coverage

### ShellCheck

Run:

```bash
shellcheck -x scripts/*.sh scripts/lib/*.sh
```

Or target only the files you changed.

## When To Write Tests First

Default to test-first when:

- the behavior is deterministic
- a contract shape changes
- recovery or edge cases are involved
- the output is consumed by another script or by tests

You may need to start from implementation first only when the behavior genuinely depends on an agent or external system that cannot be expressed meaningfully in fixtures yet. Even then, add deterministic coverage for the shell boundary as soon as possible.

## Live Smoke Boundaries

Live smoke is intentionally outside normal local test runs.

Use it only when you are intentionally validating:

- GitHub App token minting against real GitHub
- sandbox repository label setup
- real issue and PR creation
- real collaborator permission checks
- cleanup of temporary sandbox artifacts
- full lifecycle/eval behavior in a disposable managed repo

Why it stays opt-in:

- it requires credentials
- it creates real GitHub resources
- it depends on a dedicated sandbox repo
- it should never make routine `bats` runs flaky or expensive

For setup and commands, see [docs/live-smoke.md](../live-smoke.md).

## A Practical Contributor Loop

1. Add or update a focused Bats test.
2. Make the script change.
3. Run the focused suite.
4. Run adjacent regression suites.
5. Run `shellcheck -x` on touched scripts.
6. Only if needed, run the live smoke preflight, sandbox flow, or lifecycle eval intentionally.

## Related Docs

- [Development guidelines](../development-guidelines.md)
- [Live smoke tests](../live-smoke.md)
- [Script contract reference](../reference/script-contracts.md)
