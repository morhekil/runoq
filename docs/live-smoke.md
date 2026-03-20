# Live Smoke Tests

The live smoke suite validates `agendev` against real GitHub resources. It remains opt-in and credential-gated by design.

Interactive smoke commands log progress to stderr automatically. Set `AGENDEV_SMOKE_VERBOSE=1` to force those logs in non-interactive contexts, or `AGENDEV_SMOKE_VERBOSE=0` to silence them.

There are now two distinct live lanes:

- sandbox smoke: a narrow GitHub/App/auth probe
- lifecycle eval: a full end-to-end queue run fused with an LLM eval

They intentionally serve different purposes and should not be conflated.

## Lane Overview

### Sandbox smoke

This is the original narrow probe in `scripts/smoke-sandbox.sh preflight` and `scripts/smoke-sandbox.sh run`.

It validates:

- GitHub App auth via `scripts/gh-auth.sh`
- label provisioning via `scripts/setup.sh`
- queue issue creation via `scripts/gh-issue-queue.sh`
- draft PR creation via `scripts/gh-pr-lifecycle.sh`
- issue and PR comment attribution as `agendev[bot]`
- collaborator permission checks against a sandbox repo

It does not run the full `agendev run` workflow.

### Lifecycle eval

This is the new full-lifecycle lane in `scripts/smoke-lifecycle.sh preflight`, `scripts/smoke-lifecycle.sh run`, and `scripts/smoke-lifecycle.sh cleanup`.

It validates:

- disposable managed repo provisioning through `gh repo create`
- `agendev init` against a real GitHub repo
- deterministic queue seeding with dependent issues
- `agendev run` queue mode through the real orchestrator/developer flow
- worktree creation, PR lifecycle, verification, and finalization
- queue ordering across follow-up issues
- one-shot completion metrics suitable for LLM eval reporting

It is intentionally more expensive and less deterministic than the sandbox smoke probe.

## Why Two Lanes Exist

Keep the narrow smoke lane because it is better for:

- auth regressions
- attribution checks
- label/setup validation
- permission and cleanup edge cases

Use the lifecycle eval when you want a higher-cost acceptance test that answers:

- can `agendev` run the real workflow end to end on GitHub?
- can the configured model stack complete a short dependent issue chain cleanly?
- does the result still look one-shotable after recent changes?

## Sandbox Smoke Setup

Set all of the following:

- `AGENDEV_SMOKE=1`
- `AGENDEV_SMOKE_REPO=<owner>/<sandbox-repo>`
- `AGENDEV_SMOKE_APP_ID=<github-app-id>`
- `AGENDEV_SMOKE_INSTALLATION_ID=<sandbox-installation-id>`
- `AGENDEV_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`
- `AGENDEV_SMOKE_PERMISSION_USER=<repo-collaborator-to-check>`
- `AGENDEV_SMOKE_PERMISSION_LEVEL=write`

Optional:

- `AGENDEV_SMOKE_RUN_ID=<stable-id>`

Commands:

```bash
scripts/smoke-sandbox.sh preflight
scripts/smoke-sandbox.sh run
```

## Lifecycle Eval Setup

Set all of the following:

- `AGENDEV_SMOKE=1`
- `AGENDEV_SMOKE_LIFECYCLE=1` when using the Bats wrapper
- `AGENDEV_SMOKE_REPO_OWNER=<owner-or-org-for-managed-repos>`
- `AGENDEV_SMOKE_APP_ID=<github-app-id>`
- `AGENDEV_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`

Optional:

- `AGENDEV_SMOKE_REPO_PREFIX=agendev-live-eval`
- `AGENDEV_SMOKE_REPO_VISIBILITY=private`
- `AGENDEV_SMOKE_RUN_ID=<stable-id>`
- `AGENDEV_SMOKE_MANIFEST_PATH=<path-to-managed-repo-manifest>`
- `AGENDEV_SMOKE_RUNS_DIR=<path-to-local-run-artifacts>`
- `AGENDEV_CLAUDE_BIN=<claude-cli>`
- `AGENDEV_SMOKE_CODEX_BIN=<codex-cli>`

Additional prerequisites for lifecycle eval:

- operator `gh` auth must be ready for repo creation, repo edit, issue/PR mutation, and cleanup
- the operator auth used for cleanup must have `delete_repo` scope
- the GitHub App must be installed so the managed repos are visible to the app
- `claude` and `codex` must both be available on `PATH` unless overridden
- `node` and `npm` must be available because the managed target repo uses `npm test` and `npm run build`

Commands:

```bash
scripts/smoke-lifecycle.sh preflight
scripts/smoke-lifecycle.sh run
scripts/smoke-lifecycle.sh cleanup --repo OWNER/REPO
```

## Managed Repo Model

The lifecycle eval provisions a disposable repo by:

1. copying a tiny seeded target repo from `test/fixtures/live_smoke_lifecycle_target/`
2. creating a real GitHub repo with `gh repo create --source ... --push`
3. enabling `main` as the base branch and enabling auto-merge
4. running `agendev init`
5. seeding a short dependent issue chain from `test/fixtures/live_smoke_lifecycle_issues.json`
6. running `agendev run` in queue mode

The seeded issue chain is intentionally small and predictable:

- issue 1 adds formatted output
- issue 2 revises the same area with a follow-up change
- issue 3 adds a thin CLI on top

That gives end-to-end coverage plus a useful LLM eval signal without depending on highly variable reviewer-driven iteration.

## Lifecycle Eval Output

`scripts/smoke-lifecycle.sh run` returns structured JSON with:

- `status`
- `repo`
- `run_id`
- `manifest_path`
- `artifacts_dir`
- `run_exit_code`
- `checks`
- `failures`
- `lifecycle.seeded_issues`
- `lifecycle.completed_issues`
- `lifecycle.all_issues_done`
- `lifecycle.one_shotable`
- `lifecycle.queue_order_ok`
- `lifecycle.issue_results`
- `lifecycle.report_summary`
- `lifecycle.prs`

This is meant to work as both:

- a hard acceptance signal
- an LLM eval summary for one-shot completion quality

## Managed Repo Tracking And Cleanup

Managed repos are tracked in a local manifest outside `.agendev/state/*.json`.

Default manifest path:

```text
.agendev/live-smoke/managed-repos.json
```

Default artifact root:

```text
.agendev/live-smoke/runs/<run_id>/
```

Why this stays outside `.agendev/state/`:

- issue state files are recovery breadcrumbs for target repos
- managed lifecycle repos are test infrastructure assets
- cleanup history is operational metadata, not queue resumability state

Cleanup is explicit:

```bash
scripts/smoke-lifecycle.sh cleanup --repo OWNER/REPO
scripts/smoke-lifecycle.sh cleanup --run-id <run_id>
scripts/smoke-lifecycle.sh cleanup --all
```

Successful cleanup marks the repo entry as deleted in the manifest. Failed cleanup keeps the repo active and records the last cleanup error.

## Bats Wrappers

Deterministic local guard coverage:

```bash
bats test/live_smoke.bats
```

Opt-in real GitHub runs:

```bash
bats test/live_smoke.bats test/live_smoke_sandbox.bats
bats test/live_smoke.bats test/live_smoke_lifecycle.bats
```

`test/live_smoke_sandbox.bats` is skipped unless sandbox smoke preflight is ready.

`test/live_smoke_lifecycle.bats` is skipped unless lifecycle preflight is ready and `AGENDEV_SMOKE_LIFECYCLE=1` is set.

## Recommended Usage Pattern

For narrow GitHub validation:

1. Run `scripts/smoke-sandbox.sh preflight`.
2. Fix anything in `missing`.
3. Run `bats test/live_smoke.bats test/live_smoke_sandbox.bats`.

For full lifecycle eval:

1. Run `scripts/smoke-lifecycle.sh preflight`.
2. Confirm operator `gh` auth and GitHub App access are both ready.
3. Run `bats test/live_smoke.bats test/live_smoke_lifecycle.bats` or call `scripts/smoke-lifecycle.sh run` directly.
4. Inspect the JSON summary and local artifacts under `.agendev/live-smoke/runs/<run_id>/`.
5. Delete managed repos intentionally with `scripts/smoke-lifecycle.sh cleanup ...`.

## Troubleshooting

### Lifecycle preflight fails

Checks:

- `AGENDEV_SMOKE=1`
- `AGENDEV_SMOKE_REPO_OWNER`
- `AGENDEV_SMOKE_APP_KEY`
- operator `gh` login is active
- `claude`, `codex`, `node`, and `npm` are available

### Repo creation succeeds but `agendev init` fails

Checks:

- the operator `gh` auth can access `/repos/<repo>/installation`
- the GitHub App is installed so the managed repo is visible to it
- the app key matches the installed app

### Lifecycle run fails before completing the queue

Inspect:

- `.agendev/live-smoke/runs/<run_id>/init.log`
- `.agendev/live-smoke/runs/<run_id>/run.log`
- `.agendev/live-smoke/runs/<run_id>/summary.json`
- copied state files under `.agendev/live-smoke/runs/<run_id>/state/`

### Cleanup fails

Checks:

- operator `gh` auth still exists
- the token has `delete_repo` scope
- the repo still exists and is owned by the expected account or org

## Related Docs

- [Contributor testing guide](./contributing/testing.md)
- [Configuration and auth reference](./reference/config-auth.md)
