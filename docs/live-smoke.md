# Live Smoke Tests

The live smoke suite validates `runoq` against real GitHub resources. It remains opt-in and credential-gated by design.

Interactive smoke commands log progress to stderr automatically. Set `RUNOQ_SMOKE_VERBOSE=1` to force those logs in non-interactive contexts, or `RUNOQ_SMOKE_VERBOSE=0` to silence them.

There are now four distinct live lanes:

- sandbox smoke: a narrow GitHub/App/auth probe
- lifecycle eval: a full end-to-end queue run fused with an LLM eval
- planning smoke: plan decomposition and epic/task issue creation (tick-based workflow)
- tick smoke: fixture-mode full tick lifecycle against real GitHub (deterministic agents)

They intentionally serve different purposes and should not be conflated.

All lanes use environment variables from `.env.smoke-sandbox` at the repo root. Source it before running any live smoke test.

For any lane that provisions or bootstraps a managed repo, `RUNOQ_SMOKE_INSTALLATION_ID` is required. This is the GitHub App installation ID, not the App ID. You can find it in the install URL after clicking **Install App** for your GitHub App, for example `https://github.com/settings/installations/<installation-id>` or `https://github.com/organizations/<org>/settings/installations/<installation-id>`.

## Lane Overview

### Sandbox smoke

This is the original narrow probe in `scripts/smoke-sandbox.sh preflight` and `scripts/smoke-sandbox.sh run`.

It validates:

- GitHub App auth via `scripts/gh-auth.sh`
- label provisioning via `scripts/setup.sh`
- queue issue creation via `scripts/gh-issue-queue.sh`
- draft PR creation via `scripts/gh-pr-lifecycle.sh`
- issue and PR comment attribution as `runoq[bot]`
- collaborator permission checks against a sandbox repo

It does not run the full `runoq run` workflow.

### Lifecycle eval

This is the new full-lifecycle lane in `scripts/smoke-lifecycle.sh preflight`, `scripts/smoke-lifecycle.sh run`, and `scripts/smoke-lifecycle.sh cleanup`.

It validates:

- disposable managed repo provisioning through `gh repo create`
- `runoq init` against a real GitHub repo
- deterministic queue seeding with an epic and dependent task issues
- epic/task hierarchy via the GitHub sub-issues API
- `runoq run` queue mode through the real orchestrator/developer flow
- worktree creation, PR lifecycle, verification, and finalization
- queue ordering across follow-up issues
- one-shot completion metrics suitable for LLM eval reporting

It is intentionally more expensive and less deterministic than the sandbox smoke probe.

### Planning smoke

This lane lives in `scripts/smoke-planning.sh preflight`, `scripts/smoke-planning.sh run`, and `scripts/smoke-planning.sh cleanup`.

It validates:

- plan decomposition via `scripts/plan.sh` against a fixture plan
- epic and task issue creation on a managed GitHub repo
- complexity rationale metadata in task issue bodies
- epic/task hierarchy via sub-issues API

It does not run the full `runoq run` workflow or exercise codex.

### Tick smoke

This lane lives in `scripts/smoke-tick.sh preflight`, `scripts/smoke-tick.sh run`, and `scripts/smoke-tick.sh cleanup`.

It validates the full `runoq tick` lifecycle with deterministic agent fixtures against a real GitHub repo:

- bootstrap: creates planning milestone + planning issue from plan file
- milestone decomposition via fixture agents
- comment handling: question, partial approval, rejection of proposal items
- milestone materialization after approval
- task decomposition and approval
- implementation dispatch delegation to orchestrator
- milestone review with proposed adjustments (partial approval/rejection)
- discovery milestone forced-pause (adjustment always created)
- project completion

This is the primary acceptance gate for the tick system. It uses real GitHub API but replaces LLM agents with fixture files via `RUNOQ_TEST_AGENT_FIXTURE_DIR`.

## Why Four Lanes Exist

Keep the tick smoke lane because it is better for:

- full tick state machine regression testing (every transition, deterministic)
- comment handling, partial approvals, and rejection flows
- adjustment and discovery milestone paths
- fast iteration (no LLM cost, reproducible)

Keep the narrow smoke lane because it is better for:

- auth regressions
- attribution checks
- label/setup validation
- permission and cleanup edge cases

Use the lifecycle eval when you want a higher-cost acceptance test that answers:

- can `runoq` run the real workflow end to end on GitHub?
- can the configured model stack complete a short dependent issue chain cleanly?
- does the result still look one-shotable after recent changes?

Use the planning smoke when you want to validate plan decomposition without running the full queue:

- does `runoq plan` decompose a plan into epics and tasks correctly?
- are issues created on GitHub with the right metadata and hierarchy?
- does complexity rationale appear in task issue bodies?

## Sandbox Smoke Setup

Set all of the following:

- `RUNOQ_SMOKE=1`
- `RUNOQ_SMOKE_REPO=<owner>/<sandbox-repo>`
- `RUNOQ_SMOKE_APP_ID=<github-app-id>`
- `RUNOQ_SMOKE_INSTALLATION_ID=<sandbox-installation-id>`
- `RUNOQ_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`
- `RUNOQ_SMOKE_PERMISSION_USER=<repo-collaborator-to-check>`
- `RUNOQ_SMOKE_PERMISSION_LEVEL=write`

Optional:

- `RUNOQ_SMOKE_RUN_ID=<stable-id>`

Commands:

```bash
scripts/smoke-sandbox.sh preflight
scripts/smoke-sandbox.sh run
```

## Lifecycle Eval Setup

Set all of the following:

- `RUNOQ_SMOKE=1`
- `RUNOQ_SMOKE_LIFECYCLE=1` when using the Bats wrapper
- `RUNOQ_SMOKE_REPO_OWNER=<owner-or-org-for-managed-repos>`
- `RUNOQ_SMOKE_APP_ID=<github-app-id>`
- `RUNOQ_SMOKE_INSTALLATION_ID=<github-app-installation-id>`
- `RUNOQ_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`

Optional:

- `RUNOQ_SMOKE_REPO_PREFIX=runoq-live-eval`
- `RUNOQ_SMOKE_REPO_VISIBILITY=private`
- `RUNOQ_SMOKE_RUN_ID=<stable-id>`
- `RUNOQ_SMOKE_MANIFEST_PATH=<path-to-managed-repo-manifest>`
- `RUNOQ_SMOKE_RUNS_DIR=<path-to-local-run-artifacts>`
- `RUNOQ_CLAUDE_BIN=<claude-cli>`
- `RUNOQ_SMOKE_CODEX_BIN=<codex-cli>`

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

## Tick Smoke Setup

Set all of the following:

- `RUNOQ_SMOKE=1`
- `RUNOQ_SMOKE_TICK=1` when using the Bats wrapper
- `RUNOQ_SMOKE_REPO_OWNER=<owner-or-org-for-managed-repos>`
- `RUNOQ_SMOKE_APP_ID=<github-app-id>`
- `RUNOQ_SMOKE_INSTALLATION_ID=<github-app-installation-id>`
- `RUNOQ_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`

Optional:

- `RUNOQ_SMOKE_REPO_PREFIX=runoq-live-eval`
- `RUNOQ_SMOKE_REPO_VISIBILITY=private`
- `RUNOQ_SMOKE_RUN_ID=<stable-id>`

Additional prerequisites:

- operator `gh` auth must be ready for repo creation, issue mutation, and cleanup
- `codex` and `claude` are NOT required (agents are replaced by fixtures)

Commands:

```bash
scripts/smoke-tick.sh preflight
scripts/smoke-tick.sh run
scripts/smoke-tick.sh cleanup --repo OWNER/REPO
```

## Planning Smoke Setup

Set all of the following:

- `RUNOQ_SMOKE=1`
- `RUNOQ_SMOKE_PLANNING=1` when using the Bats wrapper
- `RUNOQ_SMOKE_REPO_OWNER=<owner-or-org-for-managed-repos>`
- `RUNOQ_SMOKE_APP_ID=<github-app-id>`
- `RUNOQ_SMOKE_INSTALLATION_ID=<github-app-installation-id>`
- `RUNOQ_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`

Optional:

- `RUNOQ_SMOKE_REPO_PREFIX=runoq-live-eval`
- `RUNOQ_SMOKE_REPO_VISIBILITY=private`
- `RUNOQ_SMOKE_RUN_ID=<stable-id>`
- `RUNOQ_CLAUDE_BIN=<claude-cli>`

Additional prerequisites:

- operator `gh` auth must be ready for repo creation, issue mutation, and cleanup
- `claude` must be available on `PATH` unless overridden
- `codex` is not required for this lane

Commands:

```bash
scripts/smoke-planning.sh preflight
scripts/smoke-planning.sh run
scripts/smoke-planning.sh cleanup --repo OWNER/REPO
```

## Managed Repo Model

Both the lifecycle eval and planning smoke lanes provision disposable repos. The lifecycle eval provisions a disposable repo by:

1. copying a tiny seeded target repo from `test/fixtures/live_smoke_lifecycle_target/`
2. creating a real GitHub repo with `gh repo create --source ... --push`
3. enabling `main` as the base branch and enabling auto-merge
4. running `runoq init`
5. seeding a short dependent issue chain from `test/fixtures/live_smoke_lifecycle_issues.json`
6. running `runoq run` in queue mode

The seeded issue chain is intentionally small and predictable:

- one epic defines the overall progress-tracking library scope
- task 1 implements the core formatter (no dependencies)
- task 2 adds overflow clamping (depends on task 1)
- task 3 adds a CLI wrapper (depends on task 2)

Tasks are linked to their parent epic via the GitHub sub-issues API. That gives end-to-end coverage of epic/task hierarchy plus a useful LLM eval signal without depending on highly variable reviewer-driven iteration.

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
- `lifecycle.seeded_tasks`
- `lifecycle.completed_issues`
- `lifecycle.all_tasks_done`
- `lifecycle.one_shot_completed`
- `lifecycle.one_shotable`
- `lifecycle.queue_order_ok`
- `lifecycle.open_prs`
- `lifecycle.merged_prs`
- `lifecycle.epics`
- `lifecycle.criteria_phases_run`
- `lifecycle.criteria_phases_skipped`
- `lifecycle.criteria_commits_recorded`
- `lifecycle.criteria_tamper_violations`
- `lifecycle.integration_gates_passed`
- `lifecycle.mentions`
- `lifecycle.issue_numbers`
- `lifecycle.issue_results`
- `lifecycle.report_summary`
- `lifecycle.prs`

This is meant to work as both:

- a hard acceptance signal
- an LLM eval summary for one-shot completion quality

## Managed Repo Tracking And Cleanup

Managed repos are tracked in a local manifest outside `.runoq/state/*.json`.

Default manifest path:

```text
.runoq/live-smoke/managed-repos.json
```

Default artifact root:

```text
.runoq/live-smoke/runs/<run_id>/
```

Why this stays outside `.runoq/state/`:

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
bats test/live_smoke.bats test/live_smoke_planning.bats
```

`test/live_smoke_sandbox.bats` is skipped unless sandbox smoke preflight is ready.

`test/live_smoke_lifecycle.bats` is skipped unless lifecycle preflight is ready and `RUNOQ_SMOKE_LIFECYCLE=1` is set.

`test/live_smoke_planning.bats` is skipped unless planning smoke preflight is ready and `RUNOQ_SMOKE_PLANNING=1` is set.

## Recommended Usage Pattern

For narrow GitHub validation:

1. Run `scripts/smoke-sandbox.sh preflight`.
2. Fix anything in `missing`.
3. Run `bats test/live_smoke.bats test/live_smoke_sandbox.bats`.

For plan decomposition validation:

1. Run `scripts/smoke-planning.sh preflight`.
2. Confirm operator `gh` auth and GitHub App access are both ready.
3. Run `bats test/live_smoke.bats test/live_smoke_planning.bats` or call `scripts/smoke-planning.sh run` directly.
4. Inspect the JSON summary and local artifacts under `.runoq/live-smoke/runs/<run_id>/`.
5. Delete managed repos intentionally with `scripts/smoke-planning.sh cleanup ...`.

For full lifecycle eval:

1. Run `scripts/smoke-lifecycle.sh preflight`.
2. Confirm operator `gh` auth and GitHub App access are both ready.
3. Run `bats test/live_smoke.bats test/live_smoke_lifecycle.bats` or call `scripts/smoke-lifecycle.sh run` directly.
4. Inspect the JSON summary and local artifacts under `.runoq/live-smoke/runs/<run_id>/`.
5. Delete managed repos intentionally with `scripts/smoke-lifecycle.sh cleanup ...`.

## Troubleshooting

### Lifecycle preflight fails

Checks:

- `RUNOQ_SMOKE=1`
- `RUNOQ_SMOKE_REPO_OWNER`
- `RUNOQ_SMOKE_APP_KEY`
- operator `gh` login is active
- `claude`, `codex`, `node`, and `npm` are available

### Repo creation succeeds but `runoq init` fails

Checks:

- the operator `gh` auth can access `/repos/<repo>/installation`
- the GitHub App is installed so the managed repo is visible to it
- the app key matches the installed app

### Lifecycle run fails before completing the queue

Inspect:

- `.runoq/live-smoke/runs/<run_id>/init.log`
- `.runoq/live-smoke/runs/<run_id>/run.log`
- `.runoq/live-smoke/runs/<run_id>/claude/<timestamp>/argv.txt`
- `.runoq/live-smoke/runs/<run_id>/claude/<timestamp>/context.log`
- `.runoq/live-smoke/runs/<run_id>/claude/<timestamp>/stdout.log`
- `.runoq/live-smoke/runs/<run_id>/claude/<timestamp>/stderr.log`
- `.runoq/live-smoke/runs/<run_id>/summary.json`
- copied state files under `.runoq/live-smoke/runs/<run_id>/state/`

### Cleanup fails

Checks:

- operator `gh` auth still exists
- the token has `delete_repo` scope
- the repo still exists and is owned by the expected account or org

## Related Docs

- [Contributor testing guide](./contributing/testing.md)
- [Configuration and auth reference](./reference/config-auth.md)
